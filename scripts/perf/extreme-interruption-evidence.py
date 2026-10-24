"""Resource evidence for an interrupted client; never synthesizes business RPS."""
import argparse
import json
from datetime import datetime
from pathlib import Path


def read(path):
    return json.loads(path.read_text(encoding='utf-8-sig'))


def stamp(value):
    return datetime.fromisoformat(value.replace('Z', '+00:00')).timestamp()


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--results-dir', required=True, type=Path)
    args = parser.parse_args()
    root = args.results_dir.resolve()
    state, abort = read(root/'state.json'), read(root/'telemetry/abort.json')
    assert state['status'] == 'interrupted' and state['final_restore']
    events = [json.loads(line) for line in (root/'execution.jsonl').read_text(encoding='utf-8-sig').splitlines()]
    start = next(e['utc'] for e in events if e['kind'] == 'api_started')
    a, b = stamp(start), stamp(abort['utc'])
    def frames(name):
        return [f for line in (root/'telemetry'/name).read_text(encoding='utf-8-sig').splitlines() if line.strip() for f in [json.loads(line)] if a <= stamp(f['utc']) <= b]
    host = frames('host-resources.jsonl')
    lowest = min(host, key=lambda r:r['free_memory_ratio'])
    samples = [(f['utc'], p) for f in frames('load-processes.jsonl') for p in f['processes'] if p['name'] == 'k6']
    peak = max(samples, key=lambda item:item[1]['rss_bytes'])
    report = dict(status='resource-interrupted', valid_complete_throughput_point=False,
                  successful_rps=None, business_error_rate=None,
                  reason='No complete steady summary or successful per-point SQL audit; do not estimate RPS from partial counters.',
                  planned_target_rps=640, planned_vus_per_scenario=512, planned_independent_sessions=1024,
                  api_started_utc=start, abort=abort, host_samples=len(host),
                  minimum_observed_host_free_ratio=lowest['free_memory_ratio'], minimum_free_sample_utc=lowest['utc'],
                  k6_peak_observed_rss_bytes=peak[1]['rss_bytes'], k6_peak_rss_utc=peak[0],
                  final_restore=state['final_restore'],
                  application_physical_limit_identified=False)
    (root/'interruption-evidence.json').write_text(json.dumps(report, ensure_ascii=False, indent=2), encoding='utf-8')
    print(json.dumps(report, ensure_ascii=False))


if __name__ == '__main__':
    main()
