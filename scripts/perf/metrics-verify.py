"""Verify the real Docker P2 metrics after actual CRUD/import/export traffic."""
import argparse
import json
from pathlib import Path
import re
import subprocess
import urllib.error
import urllib.request


def parse(text):
    result = []
    for line in text.splitlines():
        if line.startswith('#') or not line.strip():
            continue
        match = re.fullmatch(r'([a-zA-Z_:][a-zA-Z0-9_:]*)(\{.*\})? ([+\-\w.]+)', line)
        if not match:
            raise RuntimeError('Invalid Prometheus sample')
        result.append((match[1], match[2] or '', float(match[3])))
    return result


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--out-dir', required=True, type=Path)
    args = parser.parse_args()
    root = Path(__file__).resolve().parents[2]
    out = args.out_dir.resolve() / 'metrics-verification'
    if not out.is_relative_to(root / 'data/perf'):
        raise ValueError('Use the local performance archive')
    out.mkdir(parents=True, exist_ok=True)
    result = {'passed': False, 'services': {}}
    try:
        for service, port in (('gateway',8080),('iam-service',8081),('asset-service',8082),('governance-service',8083),('task-report-service',8084),('task-publisher',8085),('task-worker',8086)):
            raw = subprocess.run(['docker','exec',f'asset-amqp-opt-{service}-1','wget','-T','5','-qO-',f'http://127.0.0.1:{port}/metrics/service'], capture_output=True, text=True, check=True, timeout=20).stdout
            (out / f'{service}.prom').write_text(raw, encoding='utf-8')
            samples = parse(raw)
            names = {s[0] for s in samples}
            wanted = {'service_go_goroutines','service_go_heap_alloc_bytes','service_go_gc_cycles_total','service_process_cpu_seconds_total','service_process_resident_memory_bytes'}
            if not wanted <= names:
                raise RuntimeError(f'Missing runtime metrics: {service}')
            if service in ('asset-service','iam-service'):
                cap = [s[2] for s in samples if s[0]=='service_db_pool_max_open_connections']
                if cap != [10] or 'service_database_duration_seconds_count' not in names:
                    raise RuntimeError('Unchanged 10-connection budget / database traces not observed')
            if service in ('asset-service','gateway') and 'service_upstream_duration_seconds_count' not in names:
                raise RuntimeError('Actual authorization/proxy calls missing')
            if service == 'task-worker':
                observed = {re.search(r'stage="([^"]+)"',s[1])[1] for s in samples if s[0]=='service_task_stage_duration_seconds_count' and s[2]>0}
                expected = {'import_parse','import_record_rows','import_asset_batch','import_mark_progress','import_final_audit','export_read','export_encode','export_put','dispatch_wait'}
                if not expected <= observed:
                    raise RuntimeError('Actual worker stages not observed: '+str(expected-observed))
            for _, labels, _ in samples:
                if re.search(r'(?:task_id|asset_id|token|request_id|query_fingerprint)=', labels) or re.search(r'[0-9a-f]{8}-[0-9a-f]{4}-',labels):
                    raise RuntimeError('High-cardinality/private metric labels')
            result['services'][service] = {'samples':len(samples),'metric_names':sorted(names),'passed':True}
        for path in ('/metrics/service','/debug/pprof/'):
            try:
                status = urllib.request.urlopen('http://127.0.0.1:59080'+path,timeout=10).status
            except urllib.error.HTTPError as error:
                status = error.code
            expected = 403 if path=='/metrics/service' else 404
            if status != expected:
                raise RuntimeError(f'Public diagnostic access {path}: {status}, expected {expected}')
            result[path] = status
        result['passed'] = True
    finally:
        (out / 'result.json').write_text(json.dumps(result,ensure_ascii=False,indent=2),encoding='utf-8')
    print('Real Docker route, pool, upstream, runtime, task-stage metrics and private access passed.')


if __name__ == '__main__':
    main()
