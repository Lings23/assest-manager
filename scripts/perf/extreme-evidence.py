"""Read-only audit of the extreme protocol. Never fabricates missing observations."""
import argparse
import csv
import hashlib
import json
from pathlib import Path


def read(path):
    return json.loads(path.read_text(encoding="utf-8-sig"))


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--results-dir", type=Path, required=True)
    args = parser.parse_args()
    root = args.results_dir.resolve()
    state = read(root / "state.json")
    rows = []
    for path in sorted((root / "api").glob("*-runs.json")):
        records = read(path)
        for record in records if isinstance(records, list) else [records]:
            s = read(Path(record["summary_path"]))
            audit = read(Path(record["write_audit"]))
            attempts = sum(op["attempts"] for op in s["operations"].values())
            successes = sum(op["successes"] for op in s["operations"].values())
            expected = s["target_rps"] * s["steady_seconds"]
            row = dict(run_id=s["run_id"], target_rps=s["target_rps"],
                       vus_per_scenario=s["max_vus_per_scenario"], steady_seconds=s["steady_seconds"],
                       attempts=attempts, successes=successes, failures=attempts-successes,
                       actual_rps=attempts/s["steady_seconds"], successful_rps=successes/s["steady_seconds"],
                       issued_error_rate=(attempts-successes)/attempts if attempts else None,
                       issued_fraction_of_target=attempts/expected,
                       dropped_iterations_all_scenarios=s["dropped_iterations"],
                       original_gate_passed=record["load_point_passed"], baseline_eligible=record["baseline_eligible"],
                       exit_code=record["exit_code"], sql_write_audit_passed=audit["passed"],
                       preauthentication=s["preauthentication"], operations=s["operations"],
                       steady_windows=[w for w in s["windows"] if w["phase"] == "steady"],
                       source=str(path.relative_to(root)))
            row['scope_request_counts'] = {scope: sum(o['attempts'] for o in values.values()) for scope, values in s['breakdowns']['scope'].items()}
            row['asset_type_request_counts'] = {kind: sum(o['attempts'] for o in values.values()) for kind, values in s['breakdowns']['asset_type'].items()}
            row["pure_business_protocol_passed"] = (
                s["preauthentication"]["sessions"] == 2*s["max_vus_per_scenario"]
                and s["preauthentication"]["window_logins"] == 0
                and s["preauthentication"]["window_refreshes"] == 0)
            rows.append(row)
    rows.sort(key=lambda r: (r["vus_per_scenario"], r["target_rps"], r["steady_seconds"], r["run_id"]))
    certifications = []
    for item in state.get("certification", []) or []:
        selected = [r for r in rows if f"extreme-repeat-{item['rps']}-" in r["run_id"]]
        qualified = len(selected) == 3 and all(
            r["steady_seconds"] == 600 and r["original_gate_passed"] and r["baseline_eligible"]
            and r["dropped_iterations_all_scenarios"] == 0
            and r["sql_write_audit_passed"] and r["pure_business_protocol_passed"]
            for r in selected)
        if qualified != item["passed"]:
            raise ValueError("Controller and independent certification audit disagree")
        certifications.append(dict(rps=item["rps"], passed=qualified, rounds=len(selected)))
    peak = max(rows, key=lambda r: r["successful_rps"], default=None)
    report = dict(
        definitions=[
            "Successful RPS is validated successful requests in the steady-start cohort divided by scheduled steady seconds; includes bounded drain, not an exact completion-timestamp stream.",
            "Dropped iterations are k6 total across warmup and steady. Do not divide this total by steady-only arrivals or call it a business HTTP failure.",
            "Original latency, error, arrival, dropped and correctness gates are unchanged. Extreme safety stops do not certify SLA.",
            "Short-point peak is observed on this shared host, not a physical server limit or sustained certificate.",
            "p95 requires >=100 successes and p99 >=1000 per endpoint; no cross-round percentile averaging.",
        ], controller_status=state["status"], stop_reason=state.get("stop_reason"),
        certified_points=certifications, prior_independent_stable_rps=320,
        highest_new_verified_stable_rps=max((c["rps"] for c in certifications if c["passed"]), default=None),
        highest_observed_successful_rps=peak["successful_rps"] if peak else None,
        peak_run_id=peak["run_id"] if peak else None,
        all_completed_point_sql_audits_passed=bool(rows) and all(r["sql_write_audit_passed"] for r in rows),
        all_completed_point_auth_protocol_passed=bool(rows) and all(r["pure_business_protocol_passed"] for r in rows),
        interrupted_runs=[dict(source=str(p.relative_to(root)), record=read(p)) for p in (root/"api").glob("*-interrupted.json")],
        runs=rows)
    (root/"extreme-evidence.json").write_text(json.dumps(report, ensure_ascii=False, indent=2), encoding="utf-8")
    with (root/"extreme-points.csv").open("w", encoding="utf-8-sig", newline="") as out:
        fields = ["run_id", "target_rps", "vus_per_scenario", "steady_seconds", "attempts", "successes", "failures", "actual_rps", "successful_rps", "issued_error_rate", "issued_fraction_of_target", "dropped_iterations_all_scenarios", "original_gate_passed", "sql_write_audit_passed", "pure_business_protocol_passed"]
        writer = csv.DictWriter(out, fields, extrasaction="ignore")
        writer.writeheader(); writer.writerows(rows)
    lines = ["| run | VU/场景 | 目标/成功 RPS | 成功/请求 | drops（含预热） | 原门槛 |", "| --- | ---: | --- | --- | ---: | --- |"]
    for r in rows:
        lines.append(f"| {r['run_id']} | {r['vus_per_scenario']} | {r['target_rps']}/{r['successful_rps']:.3f} | {r['successes']}/{r['attempts']} | {r['dropped_iterations_all_scenarios']} | {'通过' if r['original_gate_passed'] else '未通过'} |")
    (root/"extreme-points.md").write_text("\n".join(lines)+"\n", encoding="utf-8")
    sources = [root/"state.json", *sorted((root/"api").glob("*-runs.json")), *sorted((root/"api").glob("*-summary.json")), *sorted((root/"api").glob("*-write-audit.json"))]
    (root/"extreme-evidence-provenance.json").write_text(json.dumps({str(p.relative_to(root)): hashlib.sha256(p.read_bytes()).hexdigest() for p in sources}, indent=2), encoding="utf-8")
    print(f"Audited {len(rows)} completed load points; independent certificates={certifications}")


if __name__ == "__main__":
    main()
