"""Offline P1 acceptance digest. Only persisted real measurements are read."""
import json
from pathlib import Path
import statistics


ROOT = Path(__file__).resolve().parents[2]


def read(path):
    return json.loads(path.read_text(encoding="utf-8-sig"))


def main():
    sql = ROOT / "data/perf/optimization-p1-sql"
    imports = ROOT / "data/perf/optimization-p1-import"
    index = read(sql / "indexes-state.json")
    result = {
        "definitions": [
            "No new benchmarks are executed by this offline script.",
            "Three import samples give descriptive medians, not reliable p95.",
            "Stage HTTP results use identical workload parameters; sampled host contention can differ.",
            "P2/P3 and a new maximum stable RPS are outside this acceptance scope.",
        ],
        "sql_matrix_status": index["status"],
        "sql_acceptance": read(sql / "acceptance-state.json"),
        "index_write_cost": [],
        "index_space": [],
        "http_results": read(sql / "observed-results.json"),
    }
    for profile in ("none", "scoped", "search"):
        cases = [x for x in index["writes"] if x["profile"] == profile]
        samples = [x["tasks"][0]["timing"]["e2e_ms"] for x in cases]
        result["index_write_cost"].append({
            "profile": profile, "e2e_ms": samples,
            "median_e2e_ms": statistics.median(samples),
            "source_sha256": sorted({x["tasks"][0]["source"]["sha256"] for x in cases}),
            "all_sql_passed": all(x["tasks"][0]["db_verified"] for x in cases),
        })
    for profile in ("scoped", "search"):
        log = (sql / f"checkpoint-{profile}.log").read_text(encoding="utf-8-sig")
        checkpoint = Path(log.strip().split("Checkpoint saved: ")[-1])
        indexes = read(checkpoint / "postgres.json")["asset_indexes"]
        result["index_space"].append({
            "profile": profile, "checkpoint": str(checkpoint),
            "total_asset_index_bytes": sum(i["bytes"] for i in indexes),
            "added_index_bytes": sum(i["bytes"] for i in indexes if "_live_" in i["indexname"]),
        })
    if (imports / "state.json").exists():
        state = read(imports / "state.json")
        result["import_matrix_status"] = state["status"]
        result["import_cases"] = state["cases"]
        result["import_writes"] = state["writes"]
    if (imports / "acceptance-state.json").exists():
        result["import_acceptance"] = read(imports / "acceptance-state.json")
    (sql / "p1-acceptance-summary.json").write_text(
        json.dumps(result, ensure_ascii=False, indent=2), encoding="utf-8"
    )
    print("Archived offline P1 summary; statuses retained without inferring success.")


if __name__ == "__main__":
    main()
