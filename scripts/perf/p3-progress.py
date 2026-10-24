"""Compact, read-only progress; partial relative counts are never certification."""
import json
from pathlib import Path

root = Path(__file__).resolve().parents[2] / 'data/perf/optimization-p3'
for path in sorted((root / 'interference').glob('*/state.json')):
    state = json.loads(path.read_text(encoding='utf-8-sig'))
    complete = [case for case in state['cases'] if case['status'] == 'passed']
    comparisons = failures = 0
    for case in complete:
        if case['point'] == 'control':
            continue
        controls = [c for c in complete if c['round'] == case['round'] and c['point'] == 'control']
        if len(controls) != 1:
            continue
        summaries = []
        for source in (controls[0], case):
            child = next(c for c in source['children'] if c['name'].endswith('-online'))
            summaries.append(json.loads(Path(child['api_summary']).read_text(encoding='utf-8-sig')))
        for op, reference in summaries[0]['operations'].items():
            actual = summaries[1]['operations'][op]
            if reference['successes'] >= 100 and actual['successes'] >= 100:
                comparisons += 1
                failures += actual['duration_ms']['p(95)'] > reference['duration_ms']['p(95)'] * 1.3
    print(json.dumps({'run': state['run_id'], 'status': state['status'], 'complete_cases': len(complete),
        'current': state['cases'][-1]['case_id'] if state['cases'] else 'preparation',
        'partial_relative_failures': failures, 'partial_comparisons': comparisons, 'error': state.get('error')}))
