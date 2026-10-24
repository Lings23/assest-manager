"""Plot only archived k6 observations; no interpolation creates new load points."""
import argparse
import json
import os
from pathlib import Path


def main():
    parser=argparse.ArgumentParser()
    parser.add_argument('--results-dir',type=Path,required=True)
    parser.add_argument('--protocol-title',default='Local Docker Compose · frozen D1 · original mixed workload')
    args=parser.parse_args()
    root=args.results_dir.resolve()
    out=root/'plots'
    out.mkdir(parents=True,exist_ok=True)
    os.environ.setdefault('MPLCONFIGDIR',str(out/'.mplconfig'))
    import matplotlib
    matplotlib.use('Agg')
    import matplotlib.pyplot as plt
    report=json.loads((root/'capacity-summary.json').read_text(encoding='utf-8-sig'))
    steps=sorted((r for r in report['runs'] if '-step-' in r['run_id']),key=lambda r:r['target_rps'])
    if not steps:
        raise RuntimeError('No actual staircase measurements')
    fig,axes=plt.subplots(2,2,figsize=(14,9),layout='constrained')
    palette={'list':'#2762a8','detail':'#16887b','search':'#d37319','versions':'#8f62aa','create':'#bf4262','update':'#777777'}
    for operation,color in palette.items():
        valid=[r for r in steps if r['operations'][operation]['p95_evidence_sufficient']]
        axes[0,0].plot([r['actual_rps'] for r in valid],[r['operations'][operation]['duration_ms']['p(95)'] for r in valid],marker='o',label=operation,color=color)
    axes[0,0].set(xlabel='Actual steady business RPS',ylabel='Steady p95 latency (ms)',yscale='log',title='D1 throughput vs latency (observed points)')
    axes[0,0].legend(ncol=2)
    target=[r['target_rps'] for r in steps]
    actual=[r['actual_rps'] for r in steps]
    axes[0,1].plot(target,actual,'o-',color='#2762a8',label='observed')
    axes[0,1].plot([0,max(target)],[0,max(target)],'--',color='#999999',label='target')
    axes[0,1].set(xlabel='Target RPS',ylabel='Actual steady RPS',title='Arrival-rate delivery')
    axes[0,1].legend()
    errors=[100*(r['attempts']-r['successes'])/r['attempts'] if r['attempts'] else None for r in steps]
    axes[1,0].plot(target,errors,'o-',color='#bf4262')
    axes[1,0].set(xlabel='Target RPS',ylabel='Unexpected business errors (%)',title='All steady requests, including failures')
    axes[1,0].set_ylim(0,max(.1,max((value for value in errors if value is not None),default=0)*1.2))
    axes[1,1].plot(target,[r['dropped_iterations'] for r in steps],'o-',color='#d37319')
    axes[1,1].set(xlabel='Target RPS',ylabel='Dropped iterations',title='Client arrivals not executed')
    for ax in axes.flat:
        ax.grid(True,alpha=.25)
    for r in steps:
        if not r['record']['load_point_passed']:
            axes[0,1].scatter([r['target_rps']],[r['actual_rps']],marker='x',s=140,color='#bf2121',zorder=5)
            axes[0,1].annotate('threshold failure',(r['target_rps'],r['actual_rps']),xytext=(-100,15),textcoords='offset points',color='#bf2121')
    fig.suptitle(args.protocol_title,fontsize=16)
    fig.savefig(out/'throughput-latency.png',dpi=150)
    fig.savefig(out/'throughput-latency.svg')
    plt.close(fig)
    repeats=[r for r in report['runs'] if '-repeat-' in r['run_id']]
    if repeats:
        fig,ax=plt.subplots(figsize=(12,5),layout='constrained')
        labels=[f"{r['target_rps']} RPS / round {r['run_id'].rsplit('-r',1)[-1]}" for r in repeats]
        for operation,color in palette.items():
            ax.plot(labels,[r['operations'][operation]['duration_ms']['p(95)'] if r['operations'][operation]['p95_evidence_sufficient'] else None for r in repeats],marker='o',label=operation,color=color)
        ax.set(yscale='log',ylabel='Steady p95 latency (ms)',title='Independent 600-second candidate repetitions')
        ax.tick_params(axis='x',rotation=0)
        ax.grid(True,alpha=.25);ax.legend(ncol=3)
        fig.savefig(out/'candidate-repetitions.png',dpi=150)
        fig.savefig(out/'candidate-repetitions.svg')
        plt.close(fig)
    diagnostic=root/'client-capacity-diagnostic.json'
    if diagnostic.exists():
        data=json.loads(diagnostic.read_text(encoding='utf-8-sig'))
        variants=[data['original_client'],data['diagnostic_client']]
        labels=[f"{v['preallocated_vus']} VUs per scenario" for v in variants]
        fig,axes=plt.subplots(1,2,figsize=(11,4.5),layout='constrained')
        axes[0].bar(labels,[v['dropped_iterations'] for v in variants],color=['#2762a8','#d37319'])
        axes[0].set(ylabel='Dropped iterations',title='Same target: 240 RPS')
        axes[1].bar(labels,[v['auth_requests']['login'] for v in variants],color=['#2762a8','#d37319'])
        axes[1].set(ylabel='Real login requests',title='More VUs also add initial sessions')
        fig.suptitle('Separate client diagnostic; both complete load points failed')
        fig.savefig(out/'client-startup-diagnostic.png',dpi=150)
        fig.savefig(out/'client-startup-diagnostic.svg')
        plt.close(fig)
    (out/'provenance.json').write_text(json.dumps({'source':'../capacity-summary.json','staircase_run_ids':[r['run_id'] for r in steps],'candidate_run_ids':[r['run_id'] for r in repeats],'notes':'No fitted curve or extrapolated maximum. Lines only connect actual measured points. Red cross means original load criteria failed.'},indent=2),encoding='utf-8')
    print('Saved real-measurement PNG/SVG charts and provenance.')


if __name__=='__main__':main()
