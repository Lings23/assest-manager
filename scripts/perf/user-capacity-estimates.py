"""Reproduce explicitly theoretical user estimates from persisted steady HTTP means.

No network calls, simulated benchmark samples, or percentile averaging. A user
cycle consists of m sequential HTTP calls plus Z seconds of human think time.
The mean HTTP time is borrowed from the measured 320 RPS workload; changing the
traffic mix, data scope, fanout or user/session population requires another test.
"""
import argparse
import json
from pathlib import Path


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--results-dir', type=Path, required=True)
    parser.add_argument('--output', type=Path, required=True)
    args = parser.parse_args()
    acceptance = json.loads((args.results_dir / 'acceptance.json').read_text(encoding='utf-8-sig'))
    certificate = acceptance['certificate']
    assert acceptance['status'] == 'completed' and certificate['rounds'] == 3
    verified = certificate['rps']
    runs = []
    total_count = 0
    total_duration_ms = 0
    for run_id in certificate['run_ids']:
        path = args.results_dir / 'api' / f'{run_id}-summary.json'
        summary = json.loads(path.read_text(encoding='utf-8-sig'))
        assert summary['load_point_passed'] and summary['dropped_iterations'] == 0
        assert summary['preauthentication']['window_logins'] == 0
        assert summary['preauthentication']['window_refreshes'] == 0
        count = sum(op['duration_ms']['count'] for op in summary['operations'].values())
        duration_ms = sum(op['duration_ms']['count'] * op['duration_ms']['avg'] for op in summary['operations'].values())
        assert count == sum(op['successes'] for op in summary['operations'].values())
        mean = duration_ms / count
        runs.append({'source': str(path), 'requests': count, 'mean_http_ms': mean,
                     'actual_rps': summary['actual_steady_rps'],
                     'mean_requests_inflight_little': summary['actual_steady_rps'] * mean / 1000})
        total_count += count
        total_duration_ms += duration_ms
    assert total_count == certificate['requests'] == certificate['successes']
    mean_seconds = total_duration_ms / total_count / 1000
    result = {
        'definition': 'Count-weighted arithmetic mean of actual steady HTTP request duration. Little L=lambda*W applies to this measured HTTP boundary and stable mean. It is neither maximum concurrent connections nor a tested user count.',
        'user_model': 'N=(lambda_http/m)*(Z+m*R): m sequential HTTP requests/action, Z think seconds/action, R borrowed measured mean HTTP seconds. Assumes the same request mix and sufficiently stable latency; does not model login bursts, browser rendering, upload size or parallel fanout.',
        'runs': runs, 'aggregate_requests': total_count,
        'aggregate_mean_http_ms': mean_seconds * 1000,
        'rate_budget_rps': {'verified': verified, 'illustrative_70_percent': verified * 0.7},
        'planning_headroom_is_not_a_relaxed_test_threshold': True,
        'user_estimates': []}
    for m in (1, 2, 3):
        for think in (1, 2, 5, 10, 30):
            result['user_estimates'].append({
                'http_requests_per_action': m, 'think_seconds': think,
                'sequential_action_http_seconds_assumed': m * mean_seconds,
                'users_at_verified_rate': verified * (think + m * mean_seconds) / m,
                'users_at_70_percent_budget': verified * 0.7 * (think + m * mean_seconds) / m,
                'measured_users': False})
    boundary_path = args.results_dir / 'api/preauth-step-480-mixed-480-r1-summary.json'
    if boundary_path.exists():
        boundary = json.loads(boundary_path.read_text(encoding='utf-8-sig'))
        n = sum(op['attempts'] for op in boundary['operations'].values())
        successful = sum(op['successes'] for op in boundary['operations'].values())
        duration = sum(op['duration_ms']['count'] * op['duration_ms']['avg'] for op in boundary['operations'].values()) / n / 1000
        drops = boundary['dropped_iterations']
        actual = boundary['actual_steady_rps']
        result['near_boundary_480'] = {
            'source': str(boundary_path), 'target_rps': boundary['target_rps'],
            'actual_rps': actual, 'steady_seconds': boundary['steady_seconds'],
            'issued_requests': n, 'successful_issued_requests': successful,
            'issued_request_success_fraction': successful / n,
            'dropped_iterations': drops, 'reconstructed_arrival_opportunities': n + drops,
            'dropped_fraction_of_reconstructed_opportunities': drops / (n + drops),
            'load_point_passed': boundary['load_point_passed'],
            'three_round_stable_certificate': False,
            'mean_http_ms': duration * 1000,
            'http_request_time_per_wall_second': actual * duration,
            'occupancy_note': 'Descriptive completed-cohort request-time / 180s, approximately 12.55; nonstationary late-window queueing and boundary spillovers prevent treating it as a stable concurrency certificate.',
            'operations': boundary['operations'], 'user_estimates': []}
        for m in (1, 2, 3):
            for think in (1, 2, 5, 10, 30):
                result['near_boundary_480']['user_estimates'].append({
                    'http_requests_per_action': m, 'think_seconds': think,
                    'users_at_nominal_480': 480 * (think + m * duration) / m,
                    'users_at_observed_rate': actual * (think + m * duration) / m,
                    'measured_users': False, 'stable_capacity_claim': False})
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(json.dumps(result, ensure_ascii=False, indent=2) + '\n', encoding='utf-8')
    print(json.dumps({'requests': total_count, 'mean_http_ms': mean_seconds * 1000,
                      'verified_rps': verified, 'output': str(args.output)}))


if __name__ == '__main__':
    main()
