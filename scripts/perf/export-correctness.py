"""Full CSV contents and download ownership through the actual Gateway/MinIO.

Uses a frozen own-scope reporter and compares every exported row/field with the
same authorized public List view. No concurrent mutation is allowed in this run.
"""
import argparse
import csv
from decimal import Decimal
import hashlib
import io
import json
from pathlib import Path
import re
import subprocess
import time
import urllib.error
import urllib.request


ROOT = Path(__file__).resolve().parents[2]
BASE = "http://127.0.0.1:59080/api/v1"


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--manifest", type=Path, required=True)
    parser.add_argument("--out-dir", type=Path, required=True)
    parser.add_argument("--env-file", type=Path, default=ROOT / ".env.optimization")
    args = parser.parse_args()
    args.out_dir = args.out_dir.resolve()
    if not args.out_dir.is_relative_to(ROOT / "data/perf") or "optimization-" not in str(args.out_dir):
        raise ValueError("Use a new optimization evidence directory")
    args.out_dir.mkdir(parents=True, exist_ok=False)
    manifest = json.loads(args.manifest.read_text(encoding="utf-8-sig"))
    if manifest["dataset"] != "D1" or manifest["base_url"].rstrip("/") != BASE:
        raise ValueError("Only the isolated D1 Gateway is allowed")
    credentials = json.loads(Path(manifest["credentials_file"]).read_text(encoding="utf-8-sig"))
    passwords = {u["username"]: u["password"] for u in credentials["users"]}
    reporters = sorted((u for u in manifest["users"] if u["data_scope"] == "own" and "reporter" in u["roles"]), key=lambda u: u["username"])
    owner, foreign = reporters[1], reporters[0]
    result = {"passed": False, "owner_id": owner["id"], "foreign_id": foreign["id"], "types": [], "started_epoch": time.time()}

    def request(method, path, token="", body=None, *, raw=False):
        headers = {"Content-Type": "application/json"}
        if token:
            headers["Authorization"] = "Bearer " + token
        req = urllib.request.Request(BASE + path, method=method, headers=headers, data=json.dumps(body, ensure_ascii=False).encode() if body is not None else None)
        started = time.perf_counter()
        status, data = 0, b""
        try:
            with urllib.request.urlopen(req, timeout=90) as response:
                status, data = response.status, response.read()
        except urllib.error.HTTPError as error:
            status, data = error.code, error.read()
        finally:
            with (args.out_dir / "requests.jsonl").open("a", encoding="utf-8") as stream:
                stream.write(json.dumps({"epoch": time.time(), "method": method, "path": path, "status": status, "elapsed_ms": (time.perf_counter() - started) * 1000, "bytes": len(data)}) + "\n")
        return status, data if raw else json.loads(data) if data else {}

    def expect(response, status, action):
        if response[0] != status:
            raise RuntimeError(f"{action}: HTTP {response[0]}, expected {status}")
        return response[1]

    def same(value, text, kind):
        if value is None:
            return text == ""
        if value == "***":
            return text == "***"
        if kind in ("number", "integer"):
            return Decimal(str(value)) == Decimal(text)
        if kind == "boolean":
            return text == str(value).lower()
        return str(value).replace("\r\n", "\n") == text.replace("\r\n", "\n")

    try:
        tokens = {}
        for account in (owner, foreign):
            data = expect(request("POST", "/auth/login", body={"username": account["username"], "password": passwords[account["username"]]}), 200, "fixture login")
            if data["user"]["user_id"] != account["id"] or data["user"].get("must_change_password"):
                raise RuntimeError("Fixture identity mismatch")
            tokens[account["id"]] = data["access_token"]
        token = tokens[owner["id"]]
        for kind in manifest["asset_types"]:
            schema = expect(request("GET", f"/asset-types/{kind}/schema", token), 200, "schema")["schema"]
            exported = [f for f in schema["Fields"] if f["Export"]]
            wanted = {}
            offset = 0
            while True:
                page = expect(request("GET", f"/assets/{kind}?limit=100&offset={offset}", token), 200, "frozen authorized list")["items"] or []
                for record in page:
                    if record["owner_id"] != owner["id"] or record.get("deleted_at") or record["id"] in wanted:
                        raise RuntimeError("Public own-scope List contains duplicate, deleted or foreign data")
                    wanted[record["id"]] = record
                offset += len(page)
                if len(page) < 100:
                    break
            expected_count = int(manifest["per_user_counts"][owner["id"]][kind])
            if len(wanted) != expected_count:
                raise RuntimeError("Public source differs from frozen D1 counts")
            key = "export-correct-" + kind + "-" + str(time.time_ns())
            body = {"asset_type": kind, "idempotency_key": key}
            task = expect(request("POST", "/export-jobs", token, body), 202, "create export")["task"]
            replay = expect(request("POST", "/export-jobs", token, body), 200, "idempotent create")
            if replay["created"] or replay["task"]["id"] != task["id"]:
                raise RuntimeError("Export idempotency changed")
            deadline = time.monotonic() + 180
            while time.monotonic() < deadline:
                task = expect(request("GET", "/tasks/" + task["id"], token), 200, "poll export")["task"]
                if task["state"] in ("succeeded", "dead_letter", "cancelled"):
                    break
                time.sleep(0.25)
            if task["state"] != "succeeded" or task["attempt"] != 1 or task["result"]["rows"] != expected_count:
                raise RuntimeError("Export did not finish with exact authorized row count")
            path = "/export-jobs/" + task["id"] + "/download"
            expect(request("GET", path, raw=True), 401, "anonymous download denied")
            expect(request("GET", path, tokens[foreign["id"]], raw=True), 404, "foreign owner download hidden")
            content = expect(request("GET", path, token, raw=True), 200, "owner download")
            (args.out_dir / (kind + ".csv")).write_bytes(content)
            reader = csv.DictReader(io.StringIO(content.decode("utf-8-sig"), newline=""))
            columns = ["id", "owner_id", "department_id"] + [f["Name"] for f in exported]
            if reader.fieldnames != columns:
                raise RuntimeError("Export columns/order changed")
            seen = set()
            compared = 0
            for row in reader:
                record = wanted.get(row["id"])
                if record is None or row["id"] in seen or row["owner_id"] != owner["id"] or row["department_id"] != (record.get("department_id") or ""):
                    raise RuntimeError("CSV includes duplicate, missing or foreign rows")
                seen.add(row["id"])
                for field in exported:
                    # The existing non-sensitive export contract removes these
                    # values, while public List masks them as "***". Require
                    # an empty CSV cell, never accept leaked source contents.
                    valid = (row[field["Name"]] == "") if field.get("Sensitive") else same(record["fields"].get(field["Name"]), row[field["Name"]], field["Type"])
                    if not valid:
                        raise RuntimeError("CSV value differs from the authorized public view")
                    compared += 1
            if seen != set(wanted):
                raise RuntimeError("CSV omitted visible rows")
            result["types"].append({"type": kind, "task_id": task["id"], "rows": len(seen), "compared_fields": compared, "bytes": len(content), "sha256": hashlib.sha256(content).hexdigest(), "anonymous_status": 401, "foreign_status": 404, "idempotent_create_passed": True, "passed": True})
        values = {}
        for line in args.env_file.read_text(encoding="utf-8-sig").splitlines():
            if line and not line.startswith("#") and "=" in line:
                key, value = line.split("=", 1)
                values[key] = value.strip().strip("\"'")
        ids = [row["task_id"] for row in result["types"]]
        if values.get("POSTGRES_DB") != "asset_perf" or values.get("POSTGRES_PORT") != "59432" or any(not re.fullmatch(r"[0-9a-f-]{36}", task_id) for task_id in ids):
            raise RuntimeError("SQL configuration/identity guard failed")
        command = ["docker", "compose", "--env-file", str(args.env_file), "-p", "asset-amqp-opt", "exec", "-T", "postgres", "psql", "-X", "-q", "--csv", "-v", "ON_ERROR_STOP=1", "-U", values["POSTGRES_USER"], "-d", values["POSTGRES_DB"], "-v", "task_ids_json=" + json.dumps(ids)]
        raw = subprocess.run(command, input=(ROOT / "scripts/perf/verify.sql").read_text(encoding="utf-8"), text=True, capture_output=True, check=True, timeout=90, cwd=ROOT)
        (args.out_dir / "database.csv").write_text(raw.stdout, encoding="utf-8")
        rows = list(csv.DictReader(io.StringIO(raw.stdout)))
        if len(rows) != 7 or {row["task_id"] for row in rows} != set(ids):
            raise RuntimeError("Missing exact export SQL records")
        for row in rows:
            if row["kind"] != "export" or row["state"] != "succeeded" or int(row["attempt"]) != 1 or int(row["same_key_tasks"]) != 1:
                raise RuntimeError("Export task/attempt/idempotency SQL mismatch")
            for field in ("import_keys", "parsed_rows", "committed_rows", "active_assets", "import_versions", "unpublished_events", "dead_letters"):
                if int(row[field]):
                    raise RuntimeError("Export changed assets or left dispatch/dead-letter records")
        result["sql_passed"] = True
        result["passed"] = True
    finally:
        result["finished_epoch"] = time.time()
        (args.out_dir / "result.json").write_text(json.dumps(result, ensure_ascii=False, indent=2), encoding="utf-8")
    print("Seven-type full CSV comparison and download ownership passed")


if __name__ == "__main__":
    main()
