"""Final evidence audit; completed work and failed relative goals stay separate."""
import json
from pathlib import Path


def read(path):
    return json.loads(Path(path).read_text(encoding='utf-8-sig'))


def main():
    root = Path(__file__).resolve().parents[2]
    out = root / 'data/perf/optimization-p3'
    report = read(out / 'p3-summary.json')
    matrix = next(s for s in report['suites'] if s['run_id'] == 'p3-20261018-single2')
    assert matrix['full_s7_matrix_completed'] and matrix['all_cases_absolute_and_sql_passed']
    assert matrix['relative_comparison_count'] == 72
    online = [case['online'] for case in matrix['cases']]
    attempts = sum(sum(op['attempts'] for op in r['operations'].values()) for r in online)
    successes = sum(sum(op['successes'] for op in r['operations'].values()) for r in online)
    drops = sum(r['dropped_iterations'] for r in online)
    assert attempts == successes and attempts > 200000 and drops == 0
    assert matrix['import_summary']['all_succeeded'] and matrix['export_summary']['all_succeeded']
    assert matrix['import_summary']['tasks'] == matrix['export_summary']['tasks'] == 6
    controller = read(out / 'p3-four-controller.json')
    assert controller['status'] == 'completed' and len(controller['runs']) == 2
    scaling = []
    for workers in (1, 2):
        suite = next(s for s in report['suites'] if s['run_id'] == f'p3-four-w{workers}')
        assert suite['status'] == 'completed' and suite['all_cases_absolute_and_sql_passed']
        case = next(c for c in suite['cases'] if c['point'] == 'import')
        assert len(case['tasks']) == 4 and all(t['state'] == 'succeeded' and t['attempt'] == 1 and t['passed'] for t in case['tasks'])
        assert case['audit']['passed'] and case['audit']['expected_C_import_assets'] == 400000
        if workers == 2:
            assert case['execution_concurrency']['distinct_worker_ids'] >= 2
        scaling.append({'workers': workers, 'slots_per_worker': 2, 'tasks': 4,
            'concurrency': case['execution_concurrency'], 'batches': case['background_batches'],
            'relative_violations': suite['relative_violation_count'], 'relative_comparisons': suite['relative_comparison_count']})
    final = sorted(out.glob('final-validation-*'))[-1]
    original = read(final / 'state.json')
    if original['status'] == 'failed':
        assert original['error'] == 'Final isolated broker still has queued or unacked messages'
        recovered = read(final / 'acceptance-recovery.json')
        assert recovered['status'] == 'completed'
    else:
        assert original['status'] == 'completed'
    assert all(original['steps'][key] for key in ('functional', 'exports', 'assets_and_permissions', 'wide', 'worker_fault'))
    assert read(final / 'permissions.json')['passed']
    assert read(final / 'assets/result.json')['passed'] and read(final / 'exports/result.json')['passed']
    assert read(final / 'worker-fault/result.json')['passed'] and read(final / 'wide-1000/result.json')['passed']
    assert '--- FAIL:' not in (final / 'functional.log').read_text(encoding='utf-8-sig')
    expected = {'responsible_total': 40001, 'responsible_active': 38010, 'versions': 145002, 'active_tasks': 0, 'unpublished_events': 0}
    assert read(out / 'post-scaling-quiescence.json') == expected
    assert read(out / 'post-scaling-permissions.json')['passed']
    assert read(out / 'post-scaling-source-proof.json')['passed']
    runtime = read(out / 'final-single-runtime.json')
    assert runtime['passed'] and runtime['actual_workers'] == 1 and runtime['slots_per_worker'] == 2
    queues = read(out / 'post-scaling-queues.json')
    if isinstance(queues, dict):
        queues = [queues]
    assert all(q['unacked'] == 0 and q['delta'] == 0 for q in queues)
    capacity = read(root / 'data/perf/optimization-p2/preauthenticated-20261018/acceptance.json')
    assert capacity['highest_verified_stable_rps'] == 320
    model = read(out / 'single-instance-user-estimates.json')
    boundary = model['near_boundary_480']
    assert boundary['issued_requests'] == boundary['successful_issued_requests'] == 85801
    assert boundary['dropped_iterations'] == 600 and not boundary['load_point_passed']
    result = {'status': 'completed', 'meaning': 'Authorized experiments, functional/fault checks, final dataset/runtime and evidence audit completed; this does not mean every performance target passed.',
        'main_s7': {'cases': 15, 'requests': attempts, 'successes': successes, 'drops': drops,
                    'relative_violations': matrix['relative_violation_count'], 'relative_comparisons': 72,
                    'relative_reference_passed': matrix['relative_reference_passed']},
        'four_task_diagnostics': scaling, 'functional_evidence': str(final),
        'historical_gate_failure': str(final / 'state.json') if original['status'] == 'failed' else None,
        'recovered_verification': str(final / 'acceptance-recovery.json') if original['status'] == 'failed' else None,
        'highest_verified_stable_rps': 320, 'short_boundary_actual_rps': boundary['actual_rps'],
        '480_stable_certificate': False, 'user_counts_measured': False,
        'final_single_instance_worker': runtime, 'final_d1': expected, 'final_queues': queues,
        'all_performance_goals_passed': matrix['relative_reference_passed'],
        'limitations': ['D2 not executed', 'No 320 RPS plus background-task stable certificate', 'Four-task configuration diagnostics are one round each', 'Some short P3 contention causes remain unproven', 'Known preexisting cross-scope delete/restore and sensitive version audit issues remain separate', 'No production or sustained user-count capacity certification']}
    (out / 'acceptance.json').write_text(json.dumps(result, ensure_ascii=False, indent=2) + '\n', encoding='utf-8')
    print(json.dumps({'status': result['status'], 'relative_violations': matrix['relative_violation_count'], 'stable_rps': 320}))


if __name__ == '__main__':
    main()
