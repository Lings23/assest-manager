"""Plot persisted extreme-protocol points without fitting a system maximum."""
import argparse
import json
import os
from pathlib import Path


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--results-dir', required=True, type=Path)
    parser.add_argument('--diagnostic-dir', type=Path)
    args = parser.parse_args()
    root = args.results_dir.resolve()
    report = json.loads((root/'extreme-evidence.json').read_text(encoding='utf-8-sig'))
    out = root/'plots'
    out.mkdir(exist_ok=True)
    os.environ.setdefault('MPLCONFIGDIR', str(out/'.mplconfig'))
    import matplotlib
    matplotlib.use('Agg')
    import matplotlib.pyplot as plt
    runs = report['runs']
    points = sorted((r for r in runs if '-step-' in r['run_id'] or '-refine-' in r['run_id']), key=lambda r:r['target_rps'])
    if not points:
        raise RuntimeError('No measured staircase points')
    fig, axes = plt.subplots(2, 2, figsize=(14, 9), layout='constrained')
    targets = [r['target_rps'] for r in points]
    axes[0, 0].plot(targets, [r['successful_rps'] for r in points], 'o-', label='Successful business cohort RPS')
    axes[0, 0].plot(targets, [r['actual_rps'] for r in points], 's--', label='Issued business cohort RPS')
    axes[0, 0].plot([0, max(targets)], [0, max(targets)], ':', color='gray', label='Target')
    diagnostic = []
    if args.diagnostic_dir:
        diagnostic = json.loads((args.diagnostic_dir/'extreme-evidence.json').read_text(encoding='utf-8-sig'))['runs']
        if not diagnostic:
            axes[0, 0].text(.04, .86, '512-VU diagnostic: memory safety stop; no valid point', transform=axes[0, 0].transAxes, fontsize=8, color='#8d4299')
        for r in diagnostic:
            axes[0, 0].scatter(r['target_rps'], r['successful_rps'], marker='*', s=180, color='#8d4299', label='Separate 512-VU diagnostic', zorder=5)
    for r in points:
        if not r['original_gate_passed']:
            axes[0, 0].scatter(r['target_rps'], r['successful_rps'], color='red', marker='x', s=100, zorder=4)
    axes[0, 0].set(xlabel='Offered target RPS', ylabel='Steady cohort RPS', title='Red cross: original certification gate failed')
    axes[0, 0].legend(fontsize=8)
    for op in ['list', 'detail', 'search', 'versions', 'create', 'update']:
        axes[0, 1].plot(targets, [r['operations'][op]['duration_ms']['p(95)'] if r['operations'][op]['p95_evidence_sufficient'] else None for r in points], 'o-', label=op)
    axes[0, 1].set(xlabel='Offered target RPS', ylabel='p95 (ms, all responses)', yscale='log', title='Latency vs offered load, no fitted limit')
    axes[0, 1].legend(ncol=2, fontsize=8)
    axes[1, 0].plot(targets, [100*r['issued_error_rate'] for r in points], 'o-', color='#bd334e', label='Issued business errors (%)')
    axes[1, 0].plot(targets, [100*max(0, 1-r['issued_fraction_of_target']) for r in points], 's-', color='#cf7b1b', label='Steady arrival shortfall (%)')
    axes[1, 0].set(xlabel='Offered target RPS', ylabel='Percent', title='Failed HTTP requests and unissued arrivals differ')
    axes[1, 0].legend(fontsize=8)
    calibration = [r for r in runs if r['target_rps'] == 480 and r['steady_seconds'] == 180 and ('-control128-' in r['run_id'] or '-step-' in r['run_id'])]
    calibration.sort(key=lambda r:r['vus_per_scenario'])
    labels = [f"{r['vus_per_scenario']} VUs / scenario" for r in calibration]
    axes[1, 1].bar(labels, [r['dropped_iterations_all_scenarios'] for r in calibration], color=['#2762a8', '#d37319'][:len(labels)])
    axes[1, 1].set(ylabel='Dropped iterations (warmup + steady)', title='480 RPS: client-capacity calibration')
    for ax in axes.flat:
        ax.grid(alpha=.25)
    fig.suptitle('Single service instances · frozen D1 · preauthenticated mixed workload', fontsize=14)
    fig.savefig(out/'extreme-throughput.png', dpi=150)
    fig.savefig(out/'extreme-throughput.svg')
    plt.close(fig)
    repeats = [r for r in runs if '-repeat-' in r['run_id']]
    if repeats:
        fig, axes = plt.subplots(1, 2, figsize=(14, 4.8), layout='constrained')
        for r in repeats:
            w = r['steady_windows']
            minutes = [x['index']+1 for x in w]
            axes[0].plot(minutes, [x['actual_rps'] for x in w], 'o-', label=f"{r['target_rps']} target")
            for operation, style in [('versions', 'o-'), ('search', 's--')]:
                axes[1].plot(minutes, [x['operations'][operation]['p(95)'] for x in w], style, label=f"{r['target_rps']} / {operation}")
        axes[0].set(xlabel='Steady minute', ylabel='Observed business cohort RPS', title='600-second candidates: late arrival loss')
        axes[1].axhline(500, color='gray', linestyle=':', label='Versions full-window p95 gate: 500 ms')
        axes[1].set(xlabel='Steady minute', ylabel='Per-minute p95 (ms)', title='Per-minute observations; certification uses full window')
        for ax in axes:
            ax.grid(alpha=.25);ax.legend(fontsize=8)
        fig.savefig(out/'long-run-degradation.png', dpi=150)
        fig.savefig(out/'long-run-degradation.svg')
        plt.close(fig)
    (out/'extreme-provenance.json').write_text(json.dumps(dict(source='../extreme-evidence.json', diagnostic_source=str(args.diagnostic_dir) if args.diagnostic_dir else None, run_ids=[r['run_id'] for r in points+calibration+diagnostic+repeats], notes='Only observed points; connecting lines are not interpolation evidence. Certification and saturation research are separate. No averaged percentiles. Purple star, if present, is a separate client configuration. Minute p95 is descriptive; full-window thresholds determine certification.'), indent=2), encoding='utf-8')
    print('Saved extreme throughput PNG/SVG and provenance')


if __name__ == '__main__':
    main()
