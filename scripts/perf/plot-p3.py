"""Standalone figures from archived P3 measurements and labelled user models."""
import argparse
import hashlib
import json
import os
from pathlib import Path


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--results-dir', type=Path, required=True)
    args = parser.parse_args()
    root = args.results_dir.resolve()
    out = root / 'plots'
    out.mkdir(exist_ok=True)
    os.environ.setdefault('MPLCONFIGDIR', str(out / '.mplconfig'))
    import matplotlib
    matplotlib.use('Agg')
    import matplotlib.pyplot as plt
    import numpy as np
    from matplotlib.colors import TwoSlopeNorm
    report = json.loads((root / 'p3-summary.json').read_text(encoding='utf-8-sig'))
    operations = ['list', 'detail', 'search', 'versions', 'create', 'update']
    for suite in report['suites']:
        state = json.loads(Path(suite['source']).read_text(encoding='utf-8-sig'))
        if not state.get('comparisons'):
            continue
        comparisons = json.loads(Path(state['comparisons']).read_text(encoding='utf-8-sig'))
        if isinstance(comparisons, dict):
            comparisons = [comparisons]
        comparisons.sort(key=lambda c: (c['round'], ['statistics', 'import', 'export', 'both'].index(c['point'])))
        values = np.array([[c['operations'][op]['p95_growth_fraction'] * 100 for c in comparisons] for op in operations])
        fig, ax = plt.subplots(figsize=(max(9, len(comparisons) * .86), 5.2), layout='constrained')
        norm = TwoSlopeNorm(vmin=min(-30, float(values.min())), vcenter=0, vmax=max(60, float(values.max())))
        display = ax.imshow(values, cmap='RdYlGn_r', norm=norm, aspect='auto')
        ax.set_xticks(range(len(comparisons)), [f"r{c['round']}\n{c['point']}" for c in comparisons])
        ax.set_yticks(range(len(operations)), operations)
        for row in range(len(operations)):
            for col in range(len(comparisons)):
                ax.text(col, row, f'{values[row,col]:+.0f}%', ha='center', va='center', fontsize=9,
                        fontweight='bold' if values[row,col] > 30 else 'normal')
        ax.set_title(f"{suite['run_id']} | p95 vs same-round online control\n{suite['relative_violation_count']}/{suite['relative_comparison_count']} exceed +30%; absolute gates and task throughput reported separately")
        fig.colorbar(display, ax=ax, label='Relative p95 change (%)')
        fig.savefig(out / f"{suite['run_id']}-relative-p95.png", dpi=160)
        fig.savefig(out / f"{suite['run_id']}-relative-p95.svg")
        plt.close(fig)
    model = json.loads((root / 'single-instance-user-estimates.json').read_text(encoding='utf-8-sig'))
    fig, axes = plt.subplots(1, 2, figsize=(12, 5), layout='constrained')
    think = np.linspace(.5, 10, 60)
    for m, ax in zip((1, 2), axes):
        for rate, latency, label, style, color in (
            (320, model['aggregate_mean_http_ms'] / 1000, '320 RPS: certified input', '-', '#2762a8'),
            (224, model['aggregate_mean_http_ms'] / 1000, '224 RPS: illustrative budget', '-', '#16887b'),
            (model['near_boundary_480']['actual_rps'], model['near_boundary_480']['mean_http_ms'] / 1000,
             '476.67 RPS: short boundary, NOT certified', '--', '#b96119')):
            ax.plot(think, rate * (think + m * latency) / m, style, color=color, label=label)
        ax.set(xlabel='Mean think time between actions (seconds)', ylabel='Theoretical active interactive users',
               title=f'{m} sequential business HTTP request(s) / action', ylim=(0, None))
        ax.grid(alpha=.2)
        ax.legend(fontsize=8)
    fig.suptitle('MODEL, not a user-count load test | same measured request mix assumed\nN = (HTTP RPS / requests per action) x (think time + action HTTP time)')
    fig.savefig(out / 'theoretical-active-users.png', dpi=160)
    fig.savefig(out / 'theoretical-active-users.svg')
    plt.close(fig)
    sources = [root / 'p3-summary.json', root / 'single-instance-user-estimates.json']
    (out / 'provenance.json').write_text(json.dumps({'sources': [{'path': str(p), 'sha256': hashlib.sha256(p.read_bytes()).hexdigest()} for p in sources], 'notes': 'Heatmaps show actual comparisons only; active-user curves are explicitly theoretical models. No fitted performance limit.'}, indent=2), encoding='utf-8')
    print('P3 PNG/SVG figures and model provenance saved.')


if __name__ == '__main__':
    main()
