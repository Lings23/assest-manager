"""Compare bound-parameter EXPLAIN evidence; never substitute it for HTTP p95."""
import argparse
from collections import Counter
import json
from pathlib import Path


def load(directory):
    items = json.loads((directory / "plans.json").read_text(encoding="utf-8-sig"))
    result = {item["name"]: item for item in items}
    if len(result) != len(items):
        raise ValueError("Duplicate plan names")
    return result


def flatten(item):
    explained = item["plan"][0]
    root = explained["Plan"]
    nodes = []

    def walk(node):
        nodes.append(node)
        for child in node.get("Plans", []):
            walk(child)

    walk(root)
    visited = sum((node.get("Actual Rows", 0) + node.get("Rows Removed by Filter", 0)
                   + node.get("Rows Removed by Index Recheck", 0)) * node.get("Actual Loops", 1)
                  for node in nodes if node.get("Relation Name"))
    return {
        "execution_ms": explained["Execution Time"],
        "planning_ms": explained.get("Planning Time"),
        "returned_rows": root.get("Actual Rows"),
        "relation_tuples_visited": visited,
        # Parent buffer totals include children. Do not sum them a second time.
        "shared_hit_blocks": root.get("Shared Hit Blocks", 0),
        "shared_read_blocks": root.get("Shared Read Blocks", 0),
        "temp_read_blocks": root.get("Temp Read Blocks", 0),
        "temp_written_blocks": root.get("Temp Written Blocks", 0),
        "node_types": dict(Counter(node["Node Type"] for node in nodes)),
        "indexes": sorted({node["Index Name"] for node in nodes if "Index Name" in node}),
        "sorts": [{"method": node.get("Sort Method"), "space_kib": node.get("Sort Space Used"),
                   "space_type": node.get("Sort Space Type"), "loops": node.get("Actual Loops")}
                  for node in nodes if "Sort Method" in node],
        "heap_fetches": sum(node.get("Heap Fetches", 0) for node in nodes),
    }


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--before-dir", required=True, type=Path)
    parser.add_argument("--after-dir", required=True, type=Path)
    parser.add_argument("--output-stem", required=True, type=Path)
    args = parser.parse_args()
    before, after = load(args.before_dir), load(args.after_dir)
    if set(before) != set(after):
        raise ValueError("The matrices contain different queries; no automatic comparison")
    rows = []
    for name in sorted(before):
        old, new = before[name], after[name]
        comparable = old.get("arguments") == new.get("arguments")
        for key in ("visible_rows", "matching_rows"):
            if old.get(key) != new.get(key):
                comparable = False
        if not comparable:
            raise ValueError(f"Parameters or population differ: {name}")
        x, y = flatten(old), flatten(new)
        rows.append({"name": name, "same_parameters_and_population": comparable,
                     "same_sql_shape": " ".join(old["sql"].split()) == " ".join(new["sql"].split()),
                     "before": x, "after": y,
                     "server_time_change_percent": ((y["execution_ms"] / x["execution_ms"] - 1) * 100)
                     if x["execution_ms"] else None,
                     "visible_rows": old.get("visible_rows"), "matching_rows": old.get("matching_rows")})
    report = {"queries": len(rows), "before": str(args.before_dir.resolve()),
              "after": str(args.after_dir.resolve()),
              "definitions": ["Single sequential EXPLAIN executions, not latency percentiles or concurrent HTTP measurements.",
                              "All bound parameters, visible and matching row counts must match.",
                              "Root buffer totals are inclusive and are not summed over descendants.",
                              "Relation tuple visits sum relation scan rows, rejected rows and loops; index/CTE nodes are not counted twice.",
                              "An unchanged SQL shape can still have an index-driven plan change; repeated timing also has noise."],
              "rows": rows}
    output = args.output_stem.with_suffix(".json").resolve()
    markdown = args.output_stem.with_suffix(".md").resolve()
    sources = {(args.before_dir / "plans.json").resolve(), (args.after_dir / "plans.json").resolve()}
    if output in sources or markdown in sources:
        raise ValueError("Output would overwrite source evidence")
    output.write_text(json.dumps(report, ensure_ascii=False, indent=2), encoding="utf-8")
    lines = ["# SQL计划对照", "", "顺序诊断的服务器时间，不是HTTP p95；完整矩阵及节点/Buffer/排序/索引见同名JSON。", "",
             "| 查询 | 原SQL ms | 新SQL ms | 原访问行次 | 新访问行次 | 新索引 |", "| --- | --- | --- | --- | --- | --- |"]
    for row in sorted(rows, key=lambda item: item["before"]["execution_ms"], reverse=True)[:40]:
        x, y = row["before"], row["after"]
        lines.append(f"| {row['name']} | {x['execution_ms']:.3f} | {y['execution_ms']:.3f} | {x['relation_tuples_visited']:.0f} | {y['relation_tuples_visited']:.0f} | {', '.join(y['indexes'])} |")
    markdown.write_text("\n".join(lines) + "\n", encoding="utf-8")
    print(f"Compared {len(rows)} matching bound-parameter queries")


if __name__ == "__main__":
    main()
