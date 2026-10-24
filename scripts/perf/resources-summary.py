"""Offline resource observations by completed test window; no service requests."""
import argparse
import bisect
import fnmatch
import json
import re
import statistics
from datetime import datetime, timezone
from pathlib import Path


def timestamp(value):
    return datetime.fromisoformat(value.replace("Z", "+00:00")).timestamp()


def utc(value):
    return datetime.fromtimestamp(value, timezone.utc).isoformat()


def memory_bytes(value):
    match = re.match(r"\s*([\d.]+)\s*([a-zA-Z]+)", value or "")
    units = {"b": 1, "kb": 1000, "kib": 1024, "mb": 10**6,
             "mib": 2**20, "gb": 10**9, "gib": 2**30, "tib": 2**40}
    if not match or match[2].lower() not in units:
        return None
    return float(match[1]) * units[match[2].lower()]


def coverage(frames, start, end):
    times = [item[0] for item in frames]
    return {"samples": len(times), "first_utc": utc(times[0]) if times else None,
            "last_utc": utc(times[-1]) if times else None,
            "largest_internal_gap_seconds": max((b-a for a, b in zip(times, times[1:])), default=None),
            "unobserved_leading_seconds": times[0]-start if times else end-start,
            "unobserved_trailing_seconds": end-times[-1] if times else end-start,
            "no_samples": not times}


def extreme(values, minimum=False):
    present = [value for value in values if value is not None]
    return (min(present) if minimum else max(present)) if present else None


def memory_bins(resources, processes, start, end, width=300):
    """Offsets follow the wrapper clock, not an inferred k6 scenario clock."""
    bins = []
    left = start
    while left < end:
        right = min(left+width, end)
        containers, clients = {}, {}
        for time, frame in resources:
            if left <= time < right:
                for stat in frame.get("stats", []):
                    value = memory_bytes(stat.get("MemUsage"))
                    if value is not None:
                        key = (stat["Name"], stat["ID"])
                        containers.setdefault(key, []).append(value)
        for time, frame in processes:
            if left <= time < right:
                for proc in frame.get("processes", []):
                    if proc.get("name") == "k6" and proc.get("rss_bytes") is not None:
                        clients.setdefault(proc["id"], []).append(proc["rss_bytes"])
        bins.append({"offset_seconds": left-start, "duration_seconds": right-left,
            "containers": [{"name": key[0], "container_id": key[1], "samples": len(values),
                "minimum_docker_memory_bytes": min(values), "median_docker_memory_bytes": statistics.median(values),
                "maximum_docker_memory_bytes": max(values)} for key, values in containers.items()],
            "k6_processes": [{"pid": key, "samples": len(values), "minimum_rss_bytes": min(values),
                "median_rss_bytes": statistics.median(values), "maximum_rss_bytes": max(values)} for key, values in clients.items()]})
        left = right
    return bins


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--results-dir", required=True, type=Path)
    parser.add_argument("--output-json", required=True, type=Path)
    parser.add_argument("--window-pattern", default="*")
    args = parser.parse_args()
    root, output = args.results_dir.resolve(), args.output_json.resolve()
    inputs, errors, windows = set(), [], {}

    def read(path):
        path = path.resolve()
        inputs.add(path)
        try:
            return json.loads(path.read_text(encoding="utf-8-sig"))
        except (OSError, ValueError):
            errors.append({"file": str(path), "error": "JSON unavailable or invalid"})
            return None

    def add_window(key, start, end, metadata):
        if not start or not end or not fnmatch.fnmatchcase(key, args.window_pattern):
            return
        a, b = timestamp(start), timestamp(end)
        if b < a:
            errors.append({"window": key, "error": "End precedes start"})
            return
        windows[key] = {"id": key, "start_utc": utc(a), "end_utc": utc(b),
                        "duration_seconds": b-a, "metadata": metadata, "bounds": (a, b)}

    for path in root.glob("api/*-runs.json"):
        records = read(path)
        for record in records if isinstance(records, list) else ([records] if records else []):
            add_window("api:"+record["run_id"], record.get("started_utc"), record.get("finished_utc"),
                       {"source": str(path), "kind": "API whole run, including setup/warmup",
                        "final_load_point_passed": record.get("load_point_passed"), "exit_code": record.get("exit_code")})
    for path in root.glob("jobs/matrix*/execution-*.json"):
        state = read(path)
        for record in (state or {}).get("records", []):
            if "id" in record:
                add_window("file:"+path.parent.name+":"+record["id"], record.get("started_utc"), record.get("finished_utc"),
                           {"source": str(path), "kind": "Whole file point including restore/verification", "state": record.get("state")})
    for path in root.rglob("*.manifest.json"):
        manifest = read(path)
        for task in (manifest or {}).get("tasks", []):
            end = task.get("utc_download_completed") if task.get("kind") == "export" else task.get("utc_completion_observed")
            add_window("task:"+str(path.relative_to(root))+":"+task["label"], task.get("utc_create_started"), end,
                       {"source": str(path), "kind": task.get("kind"), "passed": task.get("passed"),
                        "nominal_rows": task.get("rows"), "download_rows": (task.get("download") or {}).get("rows")})
    # Real wide-import and content/fault acceptance drivers use epoch bounds.
    # These windows include client preparation and SQL reconciliation. Never
    # replace a missing short-task sample with an adjacent restore observation.
    for path in root.rglob("result.json"):
        record = read(path)
        if not record or not record.get("started_epoch") or not record.get("finished_epoch"):
            continue
        add_window("acceptance:"+str(path.relative_to(root)),
                   utc(record["started_epoch"]), utc(record["finished_epoch"]),
                   {"source": str(path), "kind": "Acceptance client including preparation and SQL verification",
                    "passed": record.get("passed"), "nominal_rows": record.get("rows"),
                    "expected_state": record.get("expected_state"), "observed_state": record.get("state")})
    streams = {}
    for name in ("resources", "host-resources", "load-processes", "rabbitmq"):
        path = root / "telemetry" / (name+".jsonl")
        inputs.add(path.resolve())
        frames = []
        try:
            with path.open(encoding="utf-8-sig") as handle:
                for number, line in enumerate(handle, 1):
                    try:
                        value = json.loads(line)
                        frames.append((timestamp(value["utc"]), value))
                    except (ValueError, KeyError, TypeError):
                        errors.append({"file": str(path), "line": number, "error": "Invalid frame"})
        except OSError:
            errors.append({"file": str(path), "error": "Missing stream"})
        frames.sort(key=lambda item: item[0])
        streams[name] = ([frame[0] for frame in frames], frames)
    if output in inputs:
        raise SystemExit("Output would overwrite a source file")
    for window in windows.values():
        start, end = window.pop("bounds")
        selected = {}
        window["coverage"] = {}
        for name, (times, frames) in streams.items():
            subset = frames[bisect.bisect_left(times, start):bisect.bisect_right(times, end)]
            selected[name] = subset
            window["coverage"][name] = coverage(subset, start, end)
        containers = {}
        for time, frame in selected["resources"]:
            for stat in frame.get("stats", []):
                key = stat["Name"]+":"+stat["ID"]
                item = containers.setdefault(key, {"name": stat["Name"], "container_id": stat["ID"], "samples": 0,
                                                   "peak_docker_memory_bytes": None, "peak_cpu_percent_one_core": None})
                item["samples"] += 1
                item["peak_docker_memory_bytes"] = extreme([item["peak_docker_memory_bytes"], memory_bytes(stat.get("MemUsage"))])
                try:
                    cpu = float(stat["CPUPerc"].rstrip("%"))
                except (KeyError, ValueError):
                    cpu = None
                item["peak_cpu_percent_one_core"] = extreme([item["peak_cpu_percent_one_core"], cpu])
        window["containers"] = list(containers.values())
        hosts = [frame for _, frame in selected["host-resources"]]
        window["host"] = {"minimum_free_memory_kib": extreme([h.get("free_memory_kib") for h in hosts], True),
                          "minimum_free_memory_ratio": extreme([h.get("free_memory_ratio") for h in hosts], True),
                          "peak_cpu_percent_all_cores": extreme([h.get("cpu_percent") for h in hosts]),
                          "cpu_samples": sum(h.get("cpu_percent") is not None for h in hosts)}
        processes = {}
        for time, frame in selected["load-processes"]:
            for proc in frame.get("processes", []):
                if proc.get("name") == "k6":
                    processes.setdefault(proc["id"], []).append((time, proc))
        window["k6_processes"] = []
        for pid, observations in processes.items():
            cpu_values = [p.get("cpu_seconds") for _, p in observations]
            valid = len(observations) >= 2 and all(v is not None for v in cpu_values)
            reset = valid and any(b < a for a, b in zip(cpu_values, cpu_values[1:]))
            delta = cpu_values[-1]-cpu_values[0] if valid and not reset else None
            span = observations[-1][0]-observations[0][0]
            window["k6_processes"].append({"pid": pid, "samples": len(observations),
                "peak_rss_bytes": extreme([p.get("rss_bytes") for _, p in observations]),
                "observed_cpu_delta_seconds": delta, "counter_reset_observed": bool(reset),
                "observed_span_seconds": span,
                "mean_cpu_percent_one_core_observed_span": 100*delta/span if delta is not None and span > 0 else None})
        queues = {}
        for time, frame in selected["rabbitmq"]:
            for queue in frame.get("queues", []):
                key = queue.get("vhost", "")+":"+queue["name"]
                item = queues.setdefault(key, {"name": queue["name"], "vhost": queue.get("vhost"), "samples": 0,
                                              "peak_ready": None, "peak_unacked": None, "peak_consumers": None})
                item["samples"] += 1
                for target, field in (("peak_ready", "messages_ready"), ("peak_unacked", "messages_unacknowledged"), ("peak_consumers", "consumers")):
                    item[target] = extreme([item[target], queue.get(field)])
        window["queues"] = list(queues.values())
        if end-start >= 600:
            window["memory_bins_300s"] = memory_bins(selected["resources"], selected["load-processes"], start, end)
    global_coverage = {name: coverage(frames, frames[0][0], frames[-1][0]) if frames else {"samples": 0, "no_samples": True}
                       for name, (_, frames) in streams.items()}
    result = {"schema_version": 1, "generated_utc": datetime.now(timezone.utc).isoformat(),
              "results_dir": str(root), "window_pattern": args.window_pattern,
              "definitions": {"memory": "Docker MemUsage sample maximum is not process RSS or an instantaneous peak. Byte conversions retain the precision limits of rounded Docker unit strings.",
                  "timestamps": "Frame utc is the collector tick; individual component reads follow it and are not simultaneous or exact task-boundary timestamps.",
                  "memory_bins": "Five-minute offsets begin at the wrapper start, including setup/warmup. Last bins may be partial; these are resource observations, not latency percentiles or proof about heap leaks.",
                  "cpu": "Docker and k6 percentages use one logical CPU=100%; host uses all CPUs=100%.",
                  "coverage": "Missing observations stay null/empty; short operations may have no sample. Edge gaps do not establish service inactivity.",
                  "k6_cpu": "Delta only between observed counter samples; unobserved edges are not extrapolated. PID reuse without a counter decrease cannot be detected.",
                  "windows": "Whole API windows include setup and warmup; file points include restore/validation. Task windows end at completion/download before CSV validation."},
              "global_coverage": global_coverage, "input_errors": errors, "windows": list(windows.values())}
    output.parent.mkdir(parents=True, exist_ok=True)
    output.write_text(json.dumps(result, ensure_ascii=False, indent=2), encoding="utf-8")
    print(json.dumps({"windows": len(windows), "input_errors": len(errors), "output": str(output)}))


if __name__ == "__main__":
    main()
