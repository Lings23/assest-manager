"""Separate real login burst for controlled IAM profiling, not a capacity run."""
from concurrent.futures import ThreadPoolExecutor
import argparse
import json
from pathlib import Path
import time
import urllib.request


def main():
    parser=argparse.ArgumentParser()
    parser.add_argument('--out-dir',type=Path,required=True)
    parser.add_argument('--requests',type=int,default=128)
    args=parser.parse_args()
    root=Path(__file__).resolve().parents[2]
    out=args.out_dir.resolve()
    if not out.is_relative_to(root/'data/perf/optimization-p2') or not 1<=args.requests<=256:
        raise ValueError('Restricted local P2 profiling experiment')
    out.mkdir(parents=True,exist_ok=True)
    manifest=json.loads((root/'data/perf/20261016-local-baseline/seed-d1/seed-manifest.json').read_text(encoding='utf-8-sig'))
    credentials=json.loads(Path(manifest['credentials_file']).read_text(encoding='utf-8-sig'))
    passwords={u['username']:u['password'] for u in credentials['users']}
    accounts=[u for u in manifest['users'] if set(u['roles']) & {'admin','reporter'}]
    result={'passed':False,'kind':'separate_auth_burst_under_cpu_profile','requests':args.requests,'concurrency':args.requests,'started_epoch':time.time(),'responses':[]}
    def login(index):
        account=accounts[index%len(accounts)]
        request=urllib.request.Request('http://127.0.0.1:59080/api/v1/auth/login',data=json.dumps({'username':account['username'],'password':passwords[account['username']]}).encode(),headers={'Content-Type':'application/json'})
        started=time.perf_counter()
        with urllib.request.urlopen(request,timeout=30) as response:
            body=json.load(response)
            valid=response.status==200 and body['user']['user_id']==account['id'] and bool(body.get('access_token'))
            return {'status':response.status,'valid':valid,'duration_ms':(time.perf_counter()-started)*1000}
    try:
        with ThreadPoolExecutor(max_workers=args.requests) as executor:
            result['responses']=list(executor.map(login,range(args.requests)))
        result['passed']=len(result['responses'])==args.requests and all(r['valid'] for r in result['responses'])
        if not result['passed']:raise RuntimeError('Login diagnostic failed')
    finally:
        result['finished_epoch']=time.time()
        (out/'result.json').write_text(json.dumps(result,indent=2),encoding='utf-8')
    print('Real diagnostic logins succeeded; tokens/cookies discarded, no capacity claim.')


if __name__=='__main__':main()
