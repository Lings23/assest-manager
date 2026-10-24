"""Actual local Gateway imports, including the internal JSON byte-limit edges.

No passwords, bearer/refresh tokens or execution tokens are written to evidence.
The normalized-byte estimate is checked separately against Go's real encoder in
the application tests; this driver never substitutes simulated task results.
"""
import argparse
import base64
import csv
import hashlib
import http.cookiejar
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


def compact(value):
    return json.dumps(value, ensure_ascii=False, separators=(",", ":")).encode()


def go_json(value):
    # Go encoding/json's default HTML escaping. These fixtures have no floats.
    return compact(value).replace(b"<", b"\\u003c").replace(b">", b"\\u003e").replace(b"&", b"\\u0026").replace("\u2028".encode(), b"\\u2028").replace("\u2029".encode(), b"\\u2029")


def read_env(path):
    values = {}
    for line in Path(path).read_text(encoding="utf-8-sig").splitlines():
        if line and not line.startswith("#") and "=" in line:
            key, value = line.split("=", 1)
            values[key.strip()] = value.strip().strip("\"'")
    if values.get("POSTGRES_DB") != "asset_perf" or values.get("POSTGRES_PORT") != "59432":
        raise ValueError("Only the isolated optimization database is permitted")
    return values


class Session:
    def __init__(self, values, log):
        self.opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
        self.token = ""
        self.log = log
        self.refresh_at = float("inf")
        status, data = self.request("/auth/login", {"username": values.get("PERF_ADMIN_USERNAME", values.get("IAM_BOOTSTRAP_ADMIN_USERNAME", "admin")), "password": values.get("PERF_ADMIN_PASSWORD", values["IAM_BOOTSTRAP_ADMIN_PASSWORD"])})
        if status != 200 or not data.get("access_token") or data["user"].get("must_change_password"):
            raise RuntimeError("Fixture login failed; credentials withheld")
        self.token = data["access_token"]
        self.user_id = data["user"]["user_id"]
        self.refresh_at = time.monotonic() + max(10, data.get("expires_in", 900) - 60)

    def request(self, path, body=None, *, no_refresh=False):
        if not no_refresh and self.token and time.monotonic() >= self.refresh_at:
            status, refreshed = self.request("/auth/refresh", {}, no_refresh=True)
            if status != 200 or not refreshed.get("access_token"):
                raise RuntimeError("Session refresh failed")
            self.token = refreshed["access_token"]
            self.refresh_at = time.monotonic() + max(10, refreshed.get("expires_in", 900) - 60)
        data = compact(body) if body is not None else None
        headers = {"Content-Type": "application/json"}
        if self.token:
            headers["Authorization"] = "Bearer " + self.token
        req = urllib.request.Request(BASE + path, data=data, headers=headers)
        start = time.perf_counter()
        status, received = 0, b""
        try:
            with self.opener.open(req, timeout=120) as response:
                status, received = response.status, response.read()
        except urllib.error.HTTPError as error:
            status, received = error.code, error.read()
        finally:
            with self.log.open("a", encoding="utf-8") as stream:
                stream.write(json.dumps({"utc_epoch": time.time(), "path": path, "method": req.get_method(), "status": status, "duration_ms": (time.perf_counter() - start) * 1000, "sent_bytes": len(data or b""), "received_bytes": len(received)}) + "\n")
        return status, json.loads(received) if received else {}


def source_rows(args, session):
    marker = "W" + hashlib.sha256(args.run_id.encode()).hexdigest()[:12]
    if args.case in ("ordinary", "near", "oversize"):
        columns = ["department_name", "department_code", "remarks"]
        row = {"department_name": marker, "department_code": "WIDTH", "remarks": "ordinary"}
        if args.case != "ordinary":
            request = {"task_id": "0" * 36, "owner_id": session.user_id, "department_id": "", "rows": [{"key": "0" * 36 + ":2", "fields": {**row, "remarks": ""}}]}
            target = args.max_body_bytes + (128 if args.case == "oversize" else -128)
            row["remarks"] = "x" * (target - len(go_json(request)))
        return "responsible-department", columns, [row] * args.rows
    fixture = ROOT / "data/perf/20261016-local-baseline/seed-d1/seed-system-info-00-p000.csv"
    with fixture.open(encoding="utf-8-sig", newline="") as stream:
        reader = csv.DictReader(stream)
        template = list(reader)[1]
        columns = reader.fieldnames
    # Preserve schema-valid enum/date/boolean values. Shorten only ordinary text
    # and add legitimate text with HTML characters, whose JSON expansion is part
    # of the exact internal wire-size problem. External 100k CSV stays <100MiB.
    row = {key: ("wide-data" if len(value.encode()) > 12 else value) for key, value in template.items()}
    row["remarks"] = "w" * 500 + "<>&" * 24
    return "system-info", columns, [{**row, "system_name": marker + "-" + str(i)} for i in range(args.rows)]


def verify_database(args, values, task_id):
    if not re.fullmatch(r"[0-9a-f-]{36}", task_id):
        raise ValueError("Invalid task UUID")
    command = ["docker", "compose", "--env-file", str(args.env_file), "-p", "asset-amqp-opt", "exec", "-T", "postgres", "psql", "-X", "-q", "--csv", "-v", "ON_ERROR_STOP=1", "-U", values["POSTGRES_USER"], "-d", values["POSTGRES_DB"], "-v", "task_ids_json=" + json.dumps([task_id])]
    result = subprocess.run(command, input=(ROOT / "scripts/perf/verify.sql").read_text(encoding="utf-8"), text=True, capture_output=True, timeout=90, check=True, cwd=ROOT)
    (args.out_dir / "database.csv").write_text(result.stdout, encoding="utf-8")
    rows = list(csv.DictReader(io.StringIO(result.stdout)))
    if len(rows) != 1 or rows[0]["task_id"] != task_id:
        raise RuntimeError("Missing exact task SQL evidence")
    return rows[0]


def task_observation(args, values, task_id):
    command = ["docker", "compose", "--env-file", str(args.env_file), "-p", "asset-amqp-opt", "exec", "-T", "postgres", "psql", "-X", "-q", "-At", "-v", "ON_ERROR_STOP=1", "-U", values["POSTGRES_USER"], "-d", values["POSTGRES_DB"], "-v", "wanted=" + task_id]
    sql = """SELECT json_build_object('state',state,'attempt',attempt,'dispatch_version',dispatch_version,
      'token_hash',md5(execution_token::text),'lease_until',lease_until,
      'committed',(SELECT count(*) FROM task_report.import_rows WHERE task_id=t.id AND committed_at IS NOT NULL),
      'asset_keys',(SELECT count(*) FROM asset.import_idempotency WHERE task_id=t.id),
      'event',(SELECT json_build_object('event_id',event_id,'payload',payload) FROM task_report.outbox WHERE aggregate_id=t.id AND topic='task.requested' ORDER BY dispatch_version DESC LIMIT 1))
      FROM task_report.tasks t WHERE id=:'wanted'::uuid;"""
    raw = subprocess.run(command, input=sql, text=True, capture_output=True, timeout=30, check=True, cwd=ROOT)
    return json.loads(raw.stdout)


def crash_worker(args, values, observation, phase_dir):
    # This deliberately kills exactly one local optimization worker after a
    # partial durable commit. No production or unrelated container is eligible.
    container = "asset-amqp-opt-task-worker-1"
    labels = subprocess.run(["docker", "inspect", container, "--format", '{{index .Config.Labels "com.docker.compose.project"}},{{index .Config.Labels "com.docker.compose.service"}}'], text=True, capture_output=True, check=True, timeout=30).stdout.strip()
    if labels != "asset-amqp-opt,task-worker":
        raise RuntimeError("Worker identity guard failed")
    running = subprocess.run(["docker", "ps", "-q", "--filter", "label=com.docker.compose.project=asset-amqp-opt", "--filter", "label=com.docker.compose.service=task-worker"], text=True, capture_output=True, check=True, timeout=30).stdout.split()
    if len(running) != 1 or not 0 < observation["committed"] < args.rows:
        raise RuntimeError("Fault needs one worker and a genuinely partial import")
    maintenance = phase_dir / "telemetry/maintenance.flag"
    maintenance.parent.mkdir(parents=True, exist_ok=True)
    maintenance.write_text("Intentional SIGKILL and restart of the isolated worker", encoding="utf-8")
    record = {"killed_epoch": time.time(), "committed_before_kill": observation["committed"], "asset_keys_before_kill": observation["asset_keys"], "attempt_before_kill": observation["attempt"], "dispatch_before_kill": observation["dispatch_version"], "lease_until_before_kill": observation["lease_until"], "duplicate_deliveries_routed": 0}
    try:
        killed = subprocess.run(["docker", "kill", "--signal", "KILL", container], text=True, capture_output=True, check=True, timeout=30)
        (args.out_dir / "kill.log").write_text(killed.stdout + killed.stderr, encoding="utf-8")
        event = observation["event"]
        basic = base64.b64encode((values["RABBITMQ_USER"] + ":" + values["RABBITMQ_PASSWORD"]).encode()).decode()
        for _ in range(args.duplicate_deliveries):
            body = {"properties": {"delivery_mode": 2, "content_type": "application/json", "message_id": event["event_id"], "headers": {"event_id": event["event_id"]}}, "routing_key": "task.requested", "payload": compact(event["payload"]).decode(), "payload_encoding": "string"}
            request = urllib.request.Request("http://127.0.0.1:" + values["RABBITMQ_MANAGEMENT_PORT"] + "/api/exchanges/%2F/asset.tasks/publish", data=compact(body), headers={"Content-Type": "application/json", "Authorization": "Basic " + basic})
            with urllib.request.urlopen(request, timeout=15) as response:
                if response.status != 200 or not json.load(response).get("routed"):
                    raise RuntimeError("Duplicate fault delivery was not routed")
            record["duplicate_deliveries_routed"] += 1
        restarted = subprocess.run(["docker", "compose", "--env-file", str(args.env_file), "-p", "asset-amqp-opt", "up", "--no-build", "-d", "--wait", "task-worker"], text=True, capture_output=True, check=True, timeout=150, cwd=ROOT)
        (args.out_dir / "restart.log").write_text(restarted.stdout + restarted.stderr, encoding="utf-8")
        record["healthy_again_epoch"] = time.time()
    finally:
        (args.out_dir / "fault.json").write_text(json.dumps(record, indent=2), encoding="utf-8")
        maintenance.unlink(missing_ok=True)
    return record


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--env-file", type=Path, default=ROOT / ".env.optimization")
    parser.add_argument("--out-dir", type=Path, required=True)
    parser.add_argument("--run-id", required=True)
    parser.add_argument("--case", choices=["ordinary", "wide", "near", "oversize"], required=True)
    parser.add_argument("--rows", type=int, required=True)
    parser.add_argument("--max-body-bytes", type=int, default=1048576)
    parser.add_argument("--expect", choices=["succeeded", "dead_letter"], default="succeeded")
    parser.add_argument("--wait-seconds", type=int, default=1800)
    parser.add_argument("--fault-worker", action="store_true")
    parser.add_argument("--duplicate-deliveries", type=int, default=0)
    args = parser.parse_args()
    args.out_dir = args.out_dir.resolve()
    allowed = next((path.resolve() for path in (ROOT / "data/perf/optimization-p1-import", ROOT / "data/perf/optimization-p2", ROOT / "data/perf/optimization-p3") if args.out_dir.is_relative_to(path.resolve())), None)
    if allowed is None or not re.fullmatch(r"[A-Za-z0-9_.-]{1,80}", args.run_id):
        raise ValueError("Use a new P1-import/P2/P3 evidence directory and safe run ID")
    if args.fault_worker and allowed.name not in ("optimization-p1-import", "optimization-p3"):
        raise ValueError("Process fault injection is restricted to P1-import/P3 acceptance")
    if args.rows < 1 or args.rows > 100000 or (args.case in ("near", "oversize") and args.rows != 1):
        raise ValueError("Invalid row count")
    if args.fault_worker and (args.case != "ordinary" or args.rows < 10000 or args.expect != "succeeded"):
        raise ValueError("Controlled worker crash requires a normal >=10k-row success case")
    if not 0 <= args.duplicate_deliveries <= 5 or (args.duplicate_deliveries and not args.fault_worker):
        raise ValueError("Duplicate delivery injection needs the controlled fault case")
    args.out_dir.mkdir(parents=True, exist_ok=False)
    result = {"run_id": args.run_id, "case": args.case, "rows": args.rows, "expected_state": args.expect, "passed": False, "started_epoch": time.time()}
    try:
        values = read_env(args.env_file)
        abort_file = allowed / "telemetry/abort.json"
        if abort_file.exists():
            raise RuntimeError("Resource collector stop condition prevents another import")
        session = Session(values, args.out_dir / "requests.jsonl")
        kind, columns, rows = source_rows(args, session)
        csv_path = args.out_dir / "source.csv"
        with csv_path.open("w", encoding="utf-8", newline="") as stream:
            writer = csv.DictWriter(stream, fieldnames=columns, lineterminator="\n")
            writer.writeheader()
            writer.writerows(rows)
        result["source_bytes"] = csv_path.stat().st_size
        result["source_sha256"] = hashlib.sha256(csv_path.read_bytes()).hexdigest()
        payload = {"asset_type": kind, "idempotency_key": args.run_id, "csv": csv_path.read_text(encoding="utf-8")}
        result["external_json_bytes"] = len(compact(payload))
        if result["external_json_bytes"] > 100 << 20:
            raise ValueError("External JSON exceeds unchanged Gateway/Task 100MiB limit")
        started = time.perf_counter()
        status, body = session.request("/import-jobs", payload)
        if status != 202:
            raise RuntimeError(f"Task creation returned {status}")
        task = body["task"]
        task_id = task["id"]
        result["task_id"] = task_id
        if task["state"] != "awaiting_confirmation" or task["result"].get("valid") != args.rows or task["result"].get("error_count") != 0:
            raise RuntimeError("CSV preflight did not accept every row")
        result["create_preflight_ms"] = (time.perf_counter() - started) * 1000
        status, _ = session.request(f"/import-jobs/{task_id}/confirm", {})
        if status != 202:
            raise RuntimeError(f"Confirmation returned {status}")
        confirmed = time.perf_counter()
        deadline = confirmed + args.wait_seconds
        fault = None
        while time.perf_counter() < deadline:
            if abort_file.exists():
                raise RuntimeError("Resource collector requested pressure stop; inspect task before resuming")
            status, body = session.request(f"/tasks/{task_id}")
            if status != 200:
                raise RuntimeError(f"Task poll returned {status}")
            task = body["task"]
            if args.fault_worker and fault is None and task["state"] == "running":
                observed = task_observation(args, values, task_id)
                if 0 < observed["committed"] < args.rows:
                    fault = crash_worker(args, values, observed, allowed)
                    result["fault"] = fault
            if task["state"] in ("succeeded", "dead_letter", "cancelled"):
                break
            time.sleep(0.25)
        result.update({"create_to_complete_ms": (time.perf_counter() - started) * 1000, "confirm_to_complete_ms": (time.perf_counter() - confirmed) * 1000, "state": task["state"], "attempt": task["attempt"], "task_result": task.get("result"), "last_error": task.get("last_error")})
        if task["state"] != args.expect:
            raise RuntimeError("Unexpected terminal state or timeout")
        if args.fault_worker and (fault is None or task["attempt"] < 2):
            raise RuntimeError("A real partial-commit crash/recovery was not observed")
        db = verify_database(args, values, task_id)
        if args.expect == "succeeded":
            fields = ["import_keys", "parsed_rows", "committed_rows", "active_assets", "import_versions", "reported_committed"]
            if not args.fault_worker:
                fields.append("reported_created")
            for field in fields:
                if int(db[field]) != args.rows:
                    raise RuntimeError(f"SQL reconciliation failed: {field}")
            if (not args.fault_worker and int(db["reported_skipped"])) or int(db["dead_letters"]) or int(db["same_key_tasks"]) != 1:
                raise RuntimeError("Unexpected retry/duplicate task or dead letter")
            if args.fault_worker and (int(db["reported_skipped"]) < fault["committed_before_kill"] or int(db["reported_created"]) + int(db["reported_skipped"]) != args.rows):
                raise RuntimeError("Recovered current-attempt counters differ from durable row deduplication")
            result["rows_per_second_e2e"] = args.rows / (result["create_to_complete_ms"] / 1000)
        else:
            if int(db["active_assets"]) or int(db["import_keys"]) or int(db["import_versions"]) or int(db["dead_letters"]) != 1:
                raise RuntimeError("Oversize first-row SQL outcome differs")
            if args.case == "oversize" and (task["attempt"] != 1 or "bytes" not in (task.get("last_error") or "")):
                raise RuntimeError("Oversize row needs a clear permanent byte-limit error at attempt one")
        result["passed"] = True
    finally:
        result["finished_epoch"] = time.time()
        (args.out_dir / "result.json").write_text(json.dumps(result, ensure_ascii=False, indent=2), encoding="utf-8")
    print(f"Import {args.run_id}: {result['state']}; rows={args.rows}; SQL passed")


if __name__ == "__main__":
    main()
