"""Read-only D1 reporting permission checks through the actual Gateway.

Credentials/tokens stay in memory; output contains only fixture account IDs,
scopes, counts and status codes. Not a performance test.
"""
import argparse
import json
from pathlib import Path
import time
import urllib.error
import urllib.request


def read_json(path):
    return json.loads(Path(path).read_text(encoding="utf-8-sig"))


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--manifest", required=True)
    parser.add_argument("--output", required=True)
    args = parser.parse_args()
    manifest = read_json(args.manifest)
    base = manifest["base_url"].rstrip("/")
    if base != "http://127.0.0.1:59080/api/v1" or manifest["dataset"] != "D1":
        raise ValueError("Only the isolated local D1 Gateway is allowed")
    credentials = read_json(manifest["credentials_file"])
    users = {u["username"]: u for u in manifest["users"]}
    accounts = list(credentials["users"])
    if credentials.get("admin") and not any(a["username"] == credentials["admin"]["username"] for a in accounts):
        accounts.append(credentials["admin"])
    result = {"cases": [], "passed": False, "permission_checks": 0}

    def request(path, token=None, body=None):
        headers = {"Content-Type": "application/json"}
        if token:
            headers["Authorization"] = "Bearer " + token
        req = urllib.request.Request(base + path, headers=headers,
                                     data=json.dumps(body).encode() if body is not None else None)
        try:
            with urllib.request.urlopen(req, timeout=60) as response:
                return response.status, json.load(response)
        except urllib.error.HTTPError as error:
            return error.code, {}

    try:
        status, _ = request("/reporting/asset-counts")
        result["unauthenticated_status"] = status
        assert status == 401
        status, _ = request("/reporting/asset-counts", token="invalid")
        result["invalid_token_status"] = status
        assert status == 401
        for credential in accounts:
            account = {**users.get(credential["username"], {}), **credential}
            status, session = request("/auth/login", body={"username": credential["username"], "password": credential["password"]})
            assert status == 200, "Fixture authentication failed; credentials withheld"
            user_id = session["user"]["user_id"]
            expected = manifest["per_user_counts"].get(user_id, manifest["per_user_counts"].get(credential["username"]))
            assert expected is not None, "Missing independent fixture counts"
            started = time.perf_counter()
            status, response = request("/reporting/asset-counts", token=session["access_token"])
            counts = {item["asset_type"]: item["total"] for item in response.get("items", [])}
            correct = status == 200 and len(counts) == 7 and all(counts.get(kind) == int(expected.get(kind, 0)) for kind in manifest["asset_types"])
            result["cases"].append({"account_id": user_id, "scope": account.get("data_scope", "global"), "roles": account.get("roles", []), "status": status, "counts": counts, "passed": correct, "elapsed_ms": (time.perf_counter() - started) * 1000})
            result["permission_checks"] += 7
            assert correct, "Authorized counts differ from frozen fixture"
        result["passed"] = True
    finally:
        Path(args.output).write_text(json.dumps(result, ensure_ascii=False, indent=2), encoding="utf-8")
    print(f"Reporting permission checks passed: {len(result['cases'])} accounts / {result['permission_checks']} counts")


if __name__ == "__main__":
    main()
