"""Offline S7 acceptance: preserve absolute, relative and backend task evidence."""
import argparse
import importlib.util
import json
from pathlib import Path
import re
import statistics
from datetime import datetime, timedelta, timezone


def read(path):
    return json.loads(Path(path).read_text(encoding='utf-8-sig'))


def instance_window(metrics, observations):
    # Never subtract counters from alternating replicas of the same service.
    instances = {}
    for _, frame in observations:
        instances.setdefault(frame['service'], set()).add(frame.get('instance'))
    return metrics.service_window([(t, {**frame, 'service': frame['service'] + ('@' + str(frame.get('instance')) if len(instances[frame['service']]) > 1 else '')}) for t, frame in observations])


def task_window(observations, start, end):
    instances = {}
    for time, frame in observations:
        if not start <= time <= end or frame['exit_code'] != 0:
            continue
        values = {}
        for line in frame['text'].splitlines():
            match = re.fullmatch(r'(taskcenter_\w+) ([\d.eE+\-]+)', line)
            if match:
                values[match[1]] = float(match[2])
        instances.setdefault(frame['instance'], []).append(values)
    result = {}
    for instance, rows in instances.items():
        names = set().union(*(set(row) for row in rows))
        result[instance] = {'samples': len(rows), 'metrics': {}}
        for name in sorted(names):
            present = [row[name] for row in rows if name in row]
            item = {'minimum': min(present), 'maximum': max(present), 'first': present[0], 'last': present[-1]}
            if name.endswith('_total'):
                item['delta'] = present[-1] - present[0] if present[-1] >= present[0] else None
                item['counter_reset'] = present[-1] < present[0]
            result[instance]['metrics'][name] = item
    return result


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--results-dir', type=Path, required=True)
    parser.add_argument('--baseline-dir', type=Path)
    args = parser.parse_args()
    root = args.results_dir.resolve()
    spec = importlib.util.spec_from_file_location('capacity_summary', Path(__file__).with_name('capacity-summary.py'))
    metrics = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(metrics)
    service_frames = metrics.frames(root / 'telemetry/service-metrics.jsonl')
    database_frames = metrics.frames(root / 'telemetry/database-details.jsonl')
    task_frames = metrics.frames(root / 'telemetry/task-metrics.jsonl')
    process_frames = metrics.frames(root / 'telemetry/load-processes.jsonl')
    result = {'definitions': [
        'Read persisted real measurements only. A completed suite verifies tasks/SQL/absolute API gates; the 30% relative reference is a separate verdict.',
        'S7 uses original 48 RPS own-scope traffic and the original frozen S7 snapshot, not the D1 mixed-scope 320 RPS capacity dataset.',
        'Each operation p95 requires >=100 samples; p99 >=1000. No average of percentiles or pooled run-level percentile is calculated.',
        'HTTP durations enclose nested IAM/SQL work. Queue/acquire wait sums are concurrent totals and must not be added to enclosing spans.',
        'Telemetry whole-case windows include setup; task/trigger windows include co-located CSV parsing, validation and polling. Samples may miss short peaks.',
        'Only a configuration with three complete five-case repetitions can certify the full S7 matrix. One-repeat configurations are exploratory comparisons.'
    ], 'suites': []}
    baseline = args.baseline_dir or root.parent / '20261016-local-baseline/interference/s7-20261017'
    if (baseline / 'comparisons.json').exists():
        comparisons = read(baseline / 'comparisons.json')
        reference = {'source': str(baseline), 'relative_comparisons': sum(len(c['operations']) for c in comparisons),
            'relative_violations': sum(v['within_30_percent'] is not True for c in comparisons for v in c['operations'].values()),
            'task_groups': [], 'comparison_limit': 'Same nominal S7 dataset, accounts, traffic and windows; P0/P1/P2 code/indexes and enhanced observation differ. Historical changes are not attributable to a new P3 application change.'}
        for point in ('import', 'export', 'both'):
            for kind in ('import', 'export'):
                paths = sorted(baseline.glob(f'{point}-r*/{kind}/*.manifest.json'))
                tasks = [t for path in paths for t in read(path)['tasks']]
                if tasks:
                    reference['task_groups'].append({'point': point, 'kind': kind, 'tasks': len(tasks),
                        'median_create_to_complete_ms': statistics.median(t['timing']['create_to_complete_ms'] for t in tasks),
                        'sources': [str(p) for p in paths]})
        result['historical_reference'] = reference
    for state_path in sorted((root / 'interference').glob('*/state.json')):
        state = read(state_path)
        suite = {'source': str(state_path), 'run_id': state['run_id'], 'status': state['status'],
                 'plan': state['plan'], 'preparation': state['preparation'], 'cases': [], 'relative_violations': []}
        for case in state['cases']:
            item = {'case_id': case['case_id'], 'point': case['point'], 'round': case['round'],
                    'status': case['status'], 'overlap': case.get('overlap'),
                    'execution_evidence': case.get('execution_evidence', []), 'tasks': [], 'file_clients': [], 'background_batches': []}
            if case.get('audit'):
                item['audit'] = read(case['audit'])
            for child in case.get('children', []):
                if child.get('api_summary') and Path(child['api_summary']).exists():
                    summary = read(child['api_summary'])
                    public = {key: summary[key] for key in ('run_id', 'target_rps', 'actual_steady_rps', 'steady_seconds', 'load_point_passed', 'dropped_iterations', 'auth_requests', 'operations', 'windows')}
                    public['source'] = child['api_summary']
                    item['online' if summary['mode'] == 'mixed' else 'statistics'] = public
                if child.get('job_manifest') and Path(child['job_manifest']).exists():
                    manifest = read(child['job_manifest'])
                    item['background_batches'].append({'source': child['job_manifest'], 'kind': manifest['mode'], 'batches': manifest['batches']})
                    for task in manifest['tasks']:
                        item['tasks'].append({key: task.get(key) for key in ('kind', 'task_id', 'state', 'passed', 'attempt', 'rows', 'timing', 'utc_create_started', 'utc_completion_observed', 'utc_download_completed', 'server_result', 'download')})
                    if child.get('finished_utc'):
                        a, b = metrics.stamp(child['started_utc']), metrics.stamp(child['finished_utc'])
                        observed = [(time, p) for time, frame in process_frames if a <= time <= b for p in frame.get('processes', []) if int(p['id']) == int(child['pid'])]
                        client = {'name': child['name'], 'pid': child['pid'], 'elapsed_seconds': b-a,
                                  'process_samples': len(observed), 'cpu_note': 'Only observed CPU-counter intervals; unobserved edges are not extrapolated. This is the file-client pwsh process, separate from k6 and Worker.'}
                        if observed:
                            client['peak_observed_rss_bytes'] = max(p['rss_bytes'] for _, p in observed)
                            client['unobserved_leading_seconds'] = observed[0][0]-a
                            client['unobserved_trailing_seconds'] = b-observed[-1][0]
                        if len(observed) >= 2:
                            dt = observed[-1][0]-observed[0][0]
                            cpu = observed[-1][1]['cpu_seconds']-observed[0][1]['cpu_seconds']
                            client['observed_cpu_seconds'] = cpu if cpu >= 0 else None
                            client['mean_cpu_percent_one_core_in_observed_span'] = cpu/dt*100 if cpu >= 0 and dt > 0 else None
                        if len(manifest['tasks']) == 1:
                            task = manifest['tasks'][0]
                            done = task.get('utc_download_completed') or task.get('utc_completion_observed')
                            client['before_task_create_seconds'] = metrics.stamp(task['utc_create_started'])-a
                            client['after_task_completion_or_download_seconds'] = b-metrics.stamp(done)
                        item['file_clients'].append(client)
            log_path = state_path.parent / f"{case['point']}-r{case['round']}" / 'task-worker-events.jsonl'
            task_ids = {t['task_id'] for t in item['tasks']}
            corrected = []
            if log_path.exists():
                for line in log_path.read_text(encoding='utf-8-sig').splitlines():
                    if not line.strip().startswith('{'):
                        continue
                    entry = json.loads(line)
                    if entry.get('msg') != 'task succeeded' or entry.get('task_id') not in task_ids:
                        continue
                    completed = datetime.fromisoformat(entry['time'].replace('Z', '+00:00')).astimezone(timezone.utc)
                    started = completed - timedelta(milliseconds=entry['duration_ms'])
                    corrected.append({'task_id': entry['task_id'], 'worker_id': entry['worker_id'],
                        'duration_ms': entry['duration_ms'], 'completed_log_utc': completed.isoformat(),
                        'estimated_started_utc': started.isoformat(), 'source': str(log_path),
                        'note': 'UTC parsed directly from original log string; duration_ms is rounded. No queue-time reconstruction.'})
            if corrected and len(corrected) == len(task_ids):
                item['orchestrator_execution_evidence'] = item['execution_evidence']
                item['execution_evidence'] = corrected
                mismatches = []
                for original in item['orchestrator_execution_evidence']:
                    canonical = next(row for row in corrected if row['task_id'] == original['task_id'])
                    difference = metrics.stamp(original['completed_log_utc'])-metrics.stamp(canonical['completed_log_utc'])
                    if abs(difference) > .001:
                        mismatches.append({'task_id': original['task_id'], 'original_minus_log_seconds': difference})
                item['orchestrator_time_parse_mismatches'] = mismatches
                events = []
                for row in corrected:
                    events += [(metrics.stamp(row['estimated_started_utc']), 1), (metrics.stamp(row['completed_log_utc']), -1)]
                active = peak = 0
                for _, change in sorted(events):
                    active += change
                    peak = max(peak, active)
                item['execution_concurrency'] = {'distinct_worker_ids': len({e['worker_id'] for e in corrected}),
                    'peak_overlapping_log_intervals': peak,
                    'note': 'Estimated from actual UTC log completion minus rounded worker duration; simultaneous interval evidence, not a queue-time or sustained-capacity estimate.'}
                if case['point'] == 'both':
                    overlap = max(0, (min(metrics.stamp(e['completed_log_utc']) for e in corrected)-max(metrics.stamp(e['estimated_started_utc']) for e in corrected))*1000)
                    item['original_orchestrator_overlap'] = item['overlap']
                    item['overlap'] = {**item['overlap'], 'estimated_worker_interval_overlap_ms': overlap,
                        'execution_overlap_confirmed': bool(item['overlap']['two_running_observed_utc']) or overlap > 0,
                        'timestamp_source': 'Raw log UTC strings; original orchestrator values retained separately.'}
            if case.get('finished_utc'):
                start, end = metrics.stamp(case['started_utc']), metrics.stamp(case['finished_utc'])
                item['whole_case_telemetry'] = instance_window(metrics, [(t, f) for t, f in service_frames if start <= t <= end])
                item['whole_case_database_activity'] = metrics.database_window(database_frames, start, end)
                item['whole_case_task_metrics'] = task_window(task_frames, start, end)
                if case.get('background_trigger_utc') and item['tasks']:
                    a = metrics.stamp(case['background_trigger_utc'])
                    b = max(metrics.stamp(task.get('utc_download_completed') or task['utc_completion_observed']) for task in item['tasks'])
                    item['background_window_utc'] = [case['background_trigger_utc'], b]
                    item['background_telemetry'] = instance_window(metrics, [(t, f) for t, f in service_frames if a <= t <= b])
                    item['background_database_activity'] = metrics.database_window(database_frames, a, b)
            suite['cases'].append(item)
        comparisons = read(state['comparisons']) if state.get('comparisons') else []
        if isinstance(comparisons, dict):
            comparisons = [comparisons]  # PowerShell serializes a single result as an object.
        count = 0
        for comparison in comparisons:
            for operation, values in comparison['operations'].items():
                count += 1
                if values['within_30_percent'] is not True:
                    suite['relative_violations'].append({'round': comparison['round'], 'point': comparison['point'], 'operation': operation, **values})
        suite['relative_comparison_count'] = count
        suite['relative_violation_count'] = len(suite['relative_violations'])
        suite['full_s7_matrix_completed'] = (state['status'] == 'completed' and state['plan']['repetitions'] == 3 and len(suite['cases']) == 15 and count == 72)
        suite['all_cases_absolute_and_sql_passed'] = bool(suite['cases']) and all(c['status'] == 'passed' and c.get('audit', {}).get('passed') and c.get('online', {}).get('load_point_passed') for c in suite['cases'])
        suite['relative_reference_passed'] = bool(count) and not suite['relative_violations']
        for kind in ('import', 'export'):
            tasks = [t for c in suite['cases'] for t in c['tasks'] if t['kind'] == kind]
            if tasks:
                durations = [t['timing']['create_to_complete_ms'] for t in tasks]
                suite[f'{kind}_summary'] = {'tasks': len(tasks), 'all_succeeded': all(t['state'] == 'succeeded' and t['passed'] and t['attempt'] == 1 for t in tasks),
                    'median_create_to_complete_ms': statistics.median(durations), 'minimum_create_to_complete_ms': min(durations), 'maximum_create_to_complete_ms': max(durations),
                    'timing_note': 'Individual task lifecycle includes preflight/confirmation or queue/polling; worker log intervals separately estimate execution. Small task sample counts do not support p95.'}
        result['suites'].append(suite)
    (root / 'p3-summary.json').write_text(json.dumps(result, ensure_ascii=False, indent=2) + '\n', encoding='utf-8')
    print(json.dumps([{'run': s['run_id'], 'status': s['status'], 'cases': len(s['cases']), 'relative_failures': s['relative_violation_count'], 'relative_comparisons': s['relative_comparison_count'], 'full_matrix': s['full_s7_matrix_completed']} for s in result['suites']]))


if __name__ == '__main__':
    main()
