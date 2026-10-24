"""Offline capacity and telemetry evidence; never executes a load or SQL query."""
import argparse
import csv
from collections import Counter, defaultdict
from datetime import datetime, timezone
import json
import hashlib
import math
from pathlib import Path
import re


def read(path):
    return json.loads(path.read_text(encoding='utf-8-sig'))


def stamp(value):
    return datetime.fromisoformat(value.replace('Z','+00:00')).timestamp()


def frames(path):
    if not path.exists():
        return []
    result=[]
    with path.open(encoding='utf-8-sig') as stream:
        for line in stream:
            item=json.loads(line)
            result.append((stamp(item['utc']),item))
    return result


def samples(text):
    result={}
    for line in text.splitlines():
        if not line.startswith('service_'):
            continue
        match=re.fullmatch(r'(\w+)(\{.*\})? ([+\-\w.]+)',line)
        if match:
            labels=tuple(sorted(re.findall(r'(\w+)="((?:\\.|[^"\\])*)"',match[2] or '')))
            result[(match[1],labels)]=float(match[3])
    return result


def quantile(buckets,q):
    values=sorted((float(k),v) for k,v in buckets.items())
    if not values or values[-1][1]<=0:
        return None
    target=values[-1][1]*q
    lower,count=0.0,0.0
    for upper,current in values:
        if current>=target:
            if not math.isfinite(upper):
                return None
            return 1000*(lower+(upper-lower)*(target-count)/(current-count)) if current>count else 1000*upper
        lower,count=upper,current
    return None


def service_window(observations):
    by_service=defaultdict(list)
    failures=[]
    for time,frame in observations:
        if frame['exit_code']!=0:
            failures.append({'utc':frame['utc'],'service':frame['service'],'exit_code':frame['exit_code']})
            continue
        by_service[frame['service']].append((time,samples(frame['text'])))
    result={}
    for service,rows in by_service.items():
        first,last=rows[0][1],rows[-1][1]
        counters=[]
        late_series=[]
        histograms=defaultdict(dict)
        for key,end in last.items():
            name,labels=key
            if key not in first:
                late_series.append({'metric':name,'labels':dict(labels),'observed_cumulative_end':end})
                continue
            delta=end-first[key]
            if delta<0:
                counters.append({'metric':name,'labels':dict(labels),'counter_reset':True})
                continue
            if name.endswith('_bucket'):
                group=(name[:-7],tuple((k,v) for k,v in labels if k!='le'))
                histograms[group][dict(labels)['le']]=delta
            elif name.endswith(('_total','_sum','_count')):
                counters.append({'metric':name,'labels':dict(labels),'delta':delta})
        gauge_names={'service_db_pool_max_open_connections','service_db_pool_open_connections','service_db_pool_in_use_connections','service_db_pool_idle_connections','service_go_goroutines','service_go_heap_alloc_bytes','service_go_heap_inuse_bytes','service_process_resident_memory_bytes'}
        peaks={name:max((value for _,data in rows for (n,_),value in data.items() if n==name),default=None) for name in gauge_names}
        result[service]={'samples':len(rows),'span_seconds':rows[-1][0]-rows[0][0], 'gauges_peak':peaks,'counter_deltas':counters,'series_first_seen_after_first_scrape':late_series,'histogram_estimates':[{'metric':name,'labels':dict(labels),'count':buckets.get('+Inf',0),'p50_ms':quantile(buckets,.5),'p95_ms':quantile(buckets,.95) if buckets.get('+Inf',0)>=100 else None,'p99_ms':quantile(buckets,.99) if buckets.get('+Inf',0)>=1000 else None} for (name,labels),buckets in histograms.items()]}
    return {'services':result,'scrape_failures':failures}


def database_window(observations,start,end):
    rows=[];errors=[];events=Counter()
    for time,frame in observations:
        if not start<=time<=end:continue
        if frame['exit_code']!=0:
            errors.append(frame['utc']);continue
        data=json.loads(frame['text']);rows.append(data)
        for activity in data.get('activity') or []:
            if activity.get('state')=='active':events[str(activity.get('wait_event_type'))+':'+str(activity.get('wait_event'))]+=1
    return {'valid_samples':len(rows),'active_peak':max(((r.get('summary') or {}).get('active') or 0 for r in rows),default=None),'active_lock_waiting_peak':max(((r.get('summary') or {}).get('active_lock_waiting') or 0 for r in rows),default=None),'active_wait_event_samples':dict(events),'scrape_error_times':errors}


def main():
    parser=argparse.ArgumentParser()
    parser.add_argument('--results-dir',type=Path,required=True)
    parser.add_argument('--sql-plans-dir',type=Path)
    args=parser.parse_args()
    root=args.results_dir.resolve()
    dictionary=defaultdict(list)
    plans=(args.sql_plans_dir or root.parent/'optimization-p1-sql/sql-analyzed-search').resolve()
    for path in plans.glob('*.json'):
        data=read(path)
        if 'sql' in data:
            dictionary[hashlib.md5(data['sql'].encode()).hexdigest()].append({'name':data.get('name'),'source':str(path)})
    services=frames(root/'telemetry/service-metrics.jsonl')
    details=frames(root/'telemetry/database-details.jsonl')
    result={'definitions':['Only persisted real k6/telemetry is read; no benchmark or database calls.','API latency is exact k6 steady summary; server histograms are bucket-interpolated estimates, never added to enclosing spans.','Whole windows include setup/warmup and preserve late failure evidence. Diagnostic interiors use each run interior_method; preauth mixed bounds use wrapper excess over scheduled duration to trim setup conservatively. These are not exact phase timestamps.','Counter deltas use only keys observed at both edges; absent series remain absent, resets are flagged.','P95 requires >=100 samples; P99 >=1000. Unknown histogram overflow quantiles remain null.'],'runs':[]}
    for path in sorted((root/'api').glob('*-runs.json')):
        records=read(path)
        if not isinstance(records,list):records=[records]
        for record in records:
            summary=read(Path(record['summary_path']))
            start,end=stamp(record['started_utc']),stamp(record['finished_utc'])
            bounds=(start+90,end-10)
            interior_method='Original diagnostic heuristic: wrapper+90s to wrapper end-10s'
            if summary.get('preauthentication') and summary['mode']=='mixed':
                # The wrapper encloses setup, the full scheduled scenarios and
                # teardown/audit. Its excess over scheduled wall time bounds
                # setup from above. Trim both edges conservatively, without
                # pretending to know exact phase timestamps or profiling setup
                # as business. Empty short-smoke interiors remain empty.
                scheduled=summary['warmup_seconds']+summary['steady_seconds']
                excess=max(0,end-start-scheduled)
                bounds=(start+excess+summary['warmup_seconds']+5,start+scheduled-5)
                interior_method='Preauth mixed: wrapper excess bounds setup; start+excess+warmup+5s to start+scheduled-5s; wrapper must enclose complete scheduled scenarios'
            server=service_window([(t,f) for t,f in services if bounds[0]<=t<=bounds[1]])
            events=Counter();lock_peak=active_peak=0;invalid=[];settings=[];db_samples=0
            for t,frame in details:
                if not bounds[0]<=t<=bounds[1]:continue
                if frame['exit_code']!=0:invalid.append(frame['utc']);continue
                db_samples+=1
                data=json.loads(frame['text']);counts=data.get('summary') or {}
                lock_peak=max(lock_peak,counts.get('active_lock_waiting') or 0);active_peak=max(active_peak,counts.get('active') or 0)
                settings.append(data.get('settings'))
                for activity in data.get('activity') or []:
                    if activity.get('state')=='active':events[str(activity.get('wait_event_type'))+':'+str(activity.get('wait_event'))]+=1
            result['runs'].append({'run_id':record['run_id'],'record':record,'target_rps':summary['target_rps'],'actual_rps':summary['actual_steady_rps'],'attempts':sum(x['attempts'] for x in summary['operations'].values()),'successes':sum(x['successes'] for x in summary['operations'].values()),'dropped_iterations':summary['dropped_iterations'],'operations':summary['operations'],'auth_requests':summary['auth_requests'],'client_configuration':{'preallocated_vus':summary['preallocated_vus'],'max_vus_per_scenario':summary['max_vus_per_scenario'],'fixture_creates':summary['write_effects']['fixture_creates']},'minute_windows':summary['windows'],'scope_breakdown':summary['breakdowns']['scope'],'interior_bounds_utc':[datetime.fromtimestamp(t,timezone.utc).isoformat() for t in bounds],'telemetry':server,'database_activity':{'active_peak':active_peak,'active_lock_waiting_peak':lock_peak,'active_wait_event_samples':dict(events),'scrape_error_times':invalid,'settings_observed':settings[:1]}})
            result['runs'][-1]['authentication_protocol']=summary.get('authentication_protocol','original lazy per-VU login')
            result['runs'][-1]['preauthentication']=summary.get('preauthentication')
            result['runs'][-1]['interior_method']=interior_method
            result['runs'][-1]['whole_window_telemetry']=service_window([(t,f) for t,f in services if start<=t<=end])
            result['runs'][-1]['whole_window_database_activity']=database_window(details,start,end)
            result['runs'][-1]['database_activity']['valid_samples']=db_samples
            if not db_samples:
                result['runs'][-1]['database_activity']['active_peak']=None
                result['runs'][-1]['database_activity']['active_lock_waiting_peak']=None
    result['interrupted_runs']=[{'source':str(path),'record':read(path)} for path in (root/'api').glob('*-interrupted.json')]
    result['slow_sql_observations']=[]
    for path in sorted((root/'slow-sql').glob('*.jsonl')):
        groups=defaultdict(list)
        for line in path.read_text(encoding='utf-8-sig').splitlines():
            if line.strip():
                item=json.loads(line)
                groups[(item['operation'],item['query_fingerprint'])].append(item['duration_ms'])
        result['slow_sql_observations'].append({'source':str(path),'groups':[{'operation':operation,'query_fingerprint':fingerprint,'count':len(values),'min_ms':min(values),'max_ms':max(values)} for (operation,fingerprint),values in groups.items()]})
    (root/'capacity-summary.json').write_text(json.dumps(result,ensure_ascii=False,indent=2),encoding='utf-8')
    (root/'sql-fingerprint-dictionary.json').write_text(json.dumps(dictionary,ensure_ascii=False,indent=2),encoding='utf-8')
    with (root/'capacity-operations.csv').open('w',encoding='utf-8-sig',newline='') as stream:
        writer=csv.writer(stream)
        writer.writerow(['run_id','target_rps','actual_rps','preallocated_vus','max_vus_per_scenario','operation','attempts','successes','error_rate','p50_ms','qualified_p95_ms','qualified_p99_ms','raw_p95_ms','raw_p99_ms','dropped_iterations','load_point_passed'])
        for run in result['runs']:
            for name,op in run['operations'].items():
                duration=op.get('duration_ms') or {}
                writer.writerow([run['run_id'],run['target_rps'],run['actual_rps'],run['client_configuration']['preallocated_vus'],run['client_configuration']['max_vus_per_scenario'],name,op['attempts'],op['successes'],op.get('errors'),duration.get('med'),duration.get('p(95)') if op['p95_evidence_sufficient'] else None,duration.get('p(99)') if op['p99_evidence_sufficient'] else None,duration.get('p(95)'),duration.get('p(99)'),run['dropped_iterations'],run['record']['load_point_passed']])
    rows=['# 实际吞吐与延迟','', 'p95/p99按最低成功样本数决定是否展示。资源与服务器估计见同名JSON；所有失败/中断另存原始记录。','', '| run | 目标/实际RPS | 成功/请求 | drops | 列表p95 | 搜索p95 | 结果 |','| --- | --- | --- | --- | --- | --- | --- |']
    for run in result['runs']:
        def latency(op):
            item=run['operations'][op]
            return f"{item['duration_ms']['p(95)']:.2f}" if item['p95_evidence_sufficient'] else '样本不足'
        rows.append(f"| {run['run_id']} | {run['target_rps']}/{run['actual_rps']:.3f} | {run['successes']}/{run['attempts']} | {run['dropped_iterations']} | {latency('list')} | {latency('search')} | {'通过' if run['record']['load_point_passed'] else '未通过'} |")
    (root/'capacity-summary.md').write_text('\n'.join(rows)+'\n',encoding='utf-8')
    print(f'Archived {len(result["runs"])} actual completed load points; no new traffic.')


if __name__=='__main__':main()
