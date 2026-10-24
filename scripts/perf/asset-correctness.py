"""Real Gateway seven-type CRUD, IAM scope, versions and concurrency regression.

Run outside measurement windows and restore D1 afterwards. Only fake test assets
are created; authentication material stays in memory and is never logged.
"""
import argparse
from concurrent.futures import ThreadPoolExecutor
import json
from pathlib import Path
import re
import subprocess
import threading
import time
import urllib.error
import urllib.request


ROOT = Path(__file__).resolve().parents[2]
BASE = "http://127.0.0.1:59080/api/v1"


def read_json(path):
    return json.loads(Path(path).read_text(encoding="utf-8-sig"))


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--manifest", type=Path, required=True)
    parser.add_argument("--env-file", type=Path, default=ROOT / ".env.optimization")
    parser.add_argument("--out-dir", type=Path, required=True)
    args = parser.parse_args()
    args.out_dir = args.out_dir.resolve()
    if not args.out_dir.is_relative_to(ROOT / "data/perf") or "optimization-" not in str(args.out_dir):
        raise ValueError("Evidence must be in a new optimization directory")
    args.out_dir.mkdir(parents=True, exist_ok=False)
    manifest = read_json(args.manifest)
    if manifest["dataset"] != "D1" or manifest["base_url"].rstrip("/") != BASE:
        raise ValueError("Only the isolated D1 Gateway is allowed")
    credentials = read_json(manifest["credentials_file"])
    metadata = {u["username"]: u for u in manifest["users"]}
    result = {"passed": False, "types": [], "permission_checks": 0, "started_epoch": time.time()}
    lock = threading.Lock()

    def request(method, path, token="", body=None):
        headers = {"Content-Type": "application/json"}
        if token:
            headers["Authorization"] = "Bearer " + token
        data = json.dumps(body, ensure_ascii=False).encode() if body is not None else None
        req = urllib.request.Request(BASE + path, method=method, headers=headers, data=data)
        start = time.perf_counter()
        status, raw = 0, b""
        try:
            with urllib.request.urlopen(req, timeout=60) as response:
                status, raw = response.status, response.read()
        except urllib.error.HTTPError as error:
            status, raw = error.code, error.read()
        finally:
            with lock, (args.out_dir / "requests.jsonl").open("a", encoding="utf-8") as stream:
                stream.write(json.dumps({"epoch": time.time(), "method": method, "path": path, "status": status, "elapsed_ms": (time.perf_counter() - start) * 1000}) + "\n")
        return status, json.loads(raw) if raw else {}

    def expect(status, response, expected, action):
        if status != expected:
            raise RuntimeError(f"{action}: HTTP {status}, expected {expected}; see sanitized request log")
        return response

    try:
        tokens, principals = {}, {}
        accounts = list(credentials["users"]) + [credentials["admin"]]
        for account in accounts:
            if account["username"] in tokens:
                continue
            status, data = request("POST", "/auth/login", body={"username": account["username"], "password": account["password"]})
            expect(status, data, 200, "fixture login")
            if data["user"].get("must_change_password"):
                raise RuntimeError("Fixture requires password initialization")
            expected = metadata.get(account["username"])
            if expected and data["user"]["user_id"] != expected["id"]:
                raise RuntimeError("Fixture identity mismatch")
            tokens[account["username"]] = data["access_token"]
            principals[account["username"]] = data["user"]
        admin_name = credentials["admin"]["username"]
        admin = tokens[admin_name]
        admin_id = principals[admin_name]["user_id"]
        root_department = next(d["id"] for d in manifest["departments"] if not d.get("parent_id"))

        def can_read(account):
            scope = account["data_scope"]
            if scope == "global":
                return True
            if scope == "own":
                return account["id"] == admin_id
            visible = {account["department_id"]}
            if scope == "descendants":
                while True:
                    added = {d["id"] for d in manifest["departments"] if d.get("parent_id") in visible}
                    if added <= visible:
                        break
                    visible |= added
            return root_department in visible

        created_records = []
        for kind in manifest["asset_types"]:
            sample = manifest["samples"][kind][0]
            sample_id = sample["id"]
            data = expect(*request("GET", f"/assets/{kind}/{sample_id}", admin), 200, "seed detail")
            schema = expect(*request("GET", f"/asset-types/{kind}/schema", admin), 200, "schema")["schema"]
            field = next(f for f in schema["Fields"] if f["Type"] == "string" and not f.get("Enum") and not f.get("Required") and not f.get("RequiredWhen"))
            field_name = field["Name"]
            marker = "E2E-" + str(time.time_ns())
            limit = field.get("MaxLength") or 200
            fields = {**data["asset"]["fields"], field_name: marker[:limit]}
            created = expect(*request("POST", f"/assets/{kind}", admin, {"department_id": root_department, "fields": fields}), 201, "create")["asset"]
            asset_id = created["id"]
            if created["version"] != 1 or created["owner_id"] != admin_id or created["department_id"] != root_department:
                raise RuntimeError("Create identity/version mismatch")
            created_records.append({"asset_type": kind, "id": asset_id})
            item = {"type": kind, "id": asset_id, "mutable_field": field_name, "passed": False, "scope_checks": []}
            result["types"].append(item)
            for account in manifest["users"]:
                expected_status = 200 if can_read(account) else 404
                status, detail = request("GET", f"/assets/{kind}/{asset_id}", tokens[account["username"]])
                expect(status, detail, expected_status, "IAM-scoped detail")
                item["scope_checks"].append({"account_id": account["id"], "scope": account["data_scope"], "expected_status": expected_status, "actual_status": status})
                result["permission_checks"] += 1
            updated = expect(*request("PATCH", f"/assets/{kind}/{asset_id}", admin, {"version": 1, "fields": {field_name: (marker + "-u")[:limit]}}), 200, "update")["asset"]
            if updated["version"] != 2:
                raise RuntimeError("Update did not increment version")
            expect(*request("PATCH", f"/assets/{kind}/{asset_id}", admin, {"version": 1, "fields": {field_name: "stale"[:limit]}}), 409, "stale optimistic lock")
            with ThreadPoolExecutor(max_workers=2) as executor:
                updates = list(executor.map(lambda suffix: request("PATCH", f"/assets/{kind}/{asset_id}", admin, {"version": 2, "fields": {field_name: (marker + suffix)[:limit]}}), ["-left", "-right"]))
            if sorted(status for status, _ in updates) != [200, 409]:
                raise RuntimeError("Concurrent writes did not produce one winner and one conflict")
            detail = expect(*request("GET", f"/assets/{kind}/{asset_id}", admin), 200, "concurrent detail")["asset"]
            if detail["version"] != 3:
                raise RuntimeError("Concurrent write created extra versions")
            expect(*request("DELETE", f"/assets/{kind}/{asset_id}?version=3", admin), 204, "soft delete")
            expect(*request("GET", f"/assets/{kind}/{asset_id}", admin), 404, "deleted record hidden")
            deleted = expect(*request("GET", f"/assets/{kind}/{asset_id}?include_deleted=true", admin), 200, "include deleted")["asset"]
            if not deleted.get("deleted_at") or deleted["version"] != 4:
                raise RuntimeError("Soft-delete history/version mismatch")
            restored = expect(*request("POST", f"/assets/{kind}/{asset_id}/restore", admin, {"version": 4}), 200, "restore")["asset"]
            if restored.get("deleted_at") or restored["version"] != 5:
                raise RuntimeError("Restore semantics changed")
            versions = expect(*request("GET", f"/assets/{kind}/{asset_id}/versions", admin), 200, "history")["versions"]
            if [v["version"] for v in versions] != [5, 4, 3, 2, 1] or any(v["snapshot"]["id"] != asset_id for v in versions):
                raise RuntimeError("Version history lost/duplicated a successful operation")
            listed = expect(*request("GET", f"/assets/{kind}?limit=20&offset=0", admin), 200, "restored list")["items"]
            if sum(record["id"] == asset_id for record in listed) != 1:
                raise RuntimeError("Restored record missing/duplicated in current ordered list")
            expect(*request("DELETE", f"/assets/{kind}/{asset_id}?version=5", admin), 204, "final soft-delete cleanup")
            item.update({"passed": True, "final_version": 6, "concurrent_statuses": [status for status, _ in updates]})
        values = {}
        for line in args.env_file.read_text(encoding="utf-8-sig").splitlines():
            if line and not line.startswith("#") and "=" in line:
                key, value = line.split("=", 1)
                values[key] = value.strip().strip("\"'")
        if values.get("POSTGRES_DB") != "asset_perf" or values.get("POSTGRES_PORT") != "59432" or any(not re.fullmatch(r"[0-9a-f-]{36}", row["id"]) for row in created_records):
            raise RuntimeError("SQL identity/configuration guard failed")
        sql = """WITH wanted AS (SELECT * FROM jsonb_to_recordset(:'records'::jsonb) AS r(asset_type text,id text))
          SELECT json_build_object('records',count(*),'valid_histories',count(*) FILTER(WHERE versions=6 AND distinct_versions=6 AND min_version=1 AND max_version=6))
          FROM (SELECT w.id,count(v.*) versions,count(DISTINCT v.version) distinct_versions,min(v.version) min_version,max(v.version) max_version FROM wanted w LEFT JOIN asset.asset_versions v ON v.asset_id=w.id AND v.asset_type=w.asset_type GROUP BY w.id) verified;"""
        command = ["docker", "compose", "--env-file", str(args.env_file), "-p", "asset-amqp-opt", "exec", "-T", "postgres", "psql", "-X", "-q", "-At", "-v", "ON_ERROR_STOP=1", "-U", values["POSTGRES_USER"], "-d", values["POSTGRES_DB"], "-v", "records=" + json.dumps(created_records)]
        raw = subprocess.run(command, input=sql, text=True, capture_output=True, check=True, timeout=60, cwd=ROOT)
        result["sql"] = json.loads(raw.stdout)
        if result["sql"] != {"records": 7, "valid_histories": 7}:
            raise RuntimeError("SQL version reconciliation failed")
        result["passed"] = True
    finally:
        result["finished_epoch"] = time.time()
        (args.out_dir / "result.json").write_text(json.dumps(result, ensure_ascii=False, indent=2), encoding="utf-8")
    print(f"Seven-type CRUD/concurrency/history passed; scope checks={result['permission_checks']}")


if __name__ == "__main__":
    main()
