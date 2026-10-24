"""Offline certificate for the independent preauthenticated P2 protocol."""
import argparse
import hashlib
import json
from pathlib import Path


def read(path):
    return json.loads(path.read_text(encoding='utf-8-sig'))


def main():
    parser=argparse.ArgumentParser()
    parser.add_argument('--results-dir',required=True,type=Path)
    args=parser.parse_args();root=args.results_dir.resolve()
    state=read(root/'state.json')
    if state['status']!='completed':raise RuntimeError('Do not certify an unfinished controller')
    repo=Path(__file__).resolve().parents[2]
    for name in ('api.js','api-preauth.js'):
        actual=hashlib.sha256((repo/'scripts/perf'/name).read_bytes()).hexdigest().upper()
        if actual!=state['script_hashes'][name]:raise RuntimeError(f'Business script changed: {name}')
    runs=[]
    for path in sorted((root/'api').glob('*-runs.json')):
        records=read(path)
        if not isinstance(records,list):records=[records]
        for record in records:
            summary=read(Path(record['summary_path']))
            if summary['target_rps']==160:raise RuntimeError('User explicitly excluded160RPS repetition')
            expected=summary['max_vus_per_scenario']*2
            auth=summary['preauthentication']
            if auth['sessions']!=expected or auth['window_logins']!=0 or auth['window_refreshes']!=0:
                raise RuntimeError('Preauthentication was not established; do not call this pure business capacity')
            if not read(Path(record['write_audit']))['passed']:raise RuntimeError('SQL reconciliation failed')
            operations=summary['operations']
            failed=[name+':'+rule for name,value in summary['metrics'].items() for rule,detail in (value.get('thresholds') or {}).items() if not detail['ok']]
            runs.append({'run_id':record['run_id'],'target_rps':summary['target_rps'],'actual_rps':summary['actual_steady_rps'],'steady_seconds':summary['steady_seconds'],'requests':sum(o['attempts'] for o in operations.values()),'successes':sum(o['successes'] for o in operations.values()),'drops':summary['dropped_iterations'],'passed':record['load_point_passed'],'baseline_eligible':record['baseline_eligible'],'preauthentication':auth,'failed_thresholds':failed,'summary':record['summary_path'],'operations':operations})
    highest=state['highest_verified_stable_rps']
    certificate=None
    if highest is not None:
        candidates=[c for c in state['certification'] if c['rps']==highest and c['passed']]
        if len(candidates)!=1 or len(candidates[0]['records'])!=3:raise RuntimeError('Three independent passed repetitions required')
        ids={r['run_id'] for r in candidates[0]['records']}
        selected=[r for r in runs if r['run_id'] in ids]
        if len(selected)!=3 or not all(r['passed'] and r['baseline_eligible'] and r['drops']==0 and r['steady_seconds']==600 and all(o['p95_evidence_sufficient'] and o['p99_evidence_sufficient'] for o in r['operations'].values()) for r in selected):raise RuntimeError('Invalid candidate evidence')
        certificate={'rps':highest,'rounds':3,'requests':sum(r['requests'] for r in selected),'successes':sum(r['successes'] for r in selected),'drops':sum(r['drops'] for r in selected),'run_ids':sorted(ids),'per_round_steady_seconds':600}
    if state['first_failed_target'] is not None:
        if any(r['target_rps']>state['first_failed_target'] for r in runs):raise RuntimeError('Escalation after first failure')
    result={'status':'completed','protocol':'preauthenticated independent VU sessions; real IAM authorization and SQL retained','highest_verified_stable_rps':highest,'certificate':certificate,'first_failed_complete_target_rps':state['first_failed_target'],'application_maximum_claimed':False,'160_rps_executed':False,'resource_policy':read(root/'resource-policy.json') if (root/'resource-policy.json').exists() else None,'resource_policy_handoff':read(root/'resource-policy-handoff.json') if (root/'resource-policy-handoff.json').exists() else None,'prior_resumed_harness_error':state.get('error'),'runs':runs}
    (root/'acceptance.json').write_text(json.dumps(result,ensure_ascii=False,indent=2),encoding='utf-8')
    print(json.dumps({k:result[k] for k in ('status','highest_verified_stable_rps','certificate','first_failed_complete_target_rps','160_rps_executed')},ensure_ascii=False))


if __name__=='__main__':main()
