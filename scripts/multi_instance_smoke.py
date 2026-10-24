"""Real isolated Compose acceptance. Uses only Python's standard library."""
import argparse
import concurrent.futures
import http.client
import http.cookiejar
import json
from pathlib import Path
import subprocess
import time
import urllib.error
import urllib.request
import uuid

parser = argparse.ArgumentParser()
parser.add_argument('--project', required=True)
parser.add_argument('--env-file', required=True)
args = parser.parse_args()
assert args.project.startswith('asset-multi-')
env = dict(line.split('=', 1) for line in Path(args.env_file).read_text(encoding='utf-8-sig').splitlines() if line and not line.startswith('#') and '=' in line)
port = int(env.get('MULTI_INSTANCE_PORT', '59180'))
base = f'http://127.0.0.1:{port}'
compose = ['docker', 'compose', '-p', args.project, '--env-file', args.env_file, '-f', 'compose.yaml', '-f', 'deploy/test/compose.multi-instance.yaml']
evidence = Path('data/multi-instance') / args.project
evidence.mkdir(parents=True, exist_ok=True)
checks = []
token = ''
jar = http.cookiejar.CookieJar()
opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(jar))


def docker(*parts):
    result = subprocess.run(['docker', *parts], capture_output=True, text=True, encoding='utf-8')
    if result.returncode:
        raise RuntimeError('Docker operation failed: ' + result.stderr[-1500:])
    return result.stdout.strip()


def dc(*parts):
    result = subprocess.run([*compose, *parts], capture_output=True, text=True, encoding='utf-8')
    if result.returncode:
        raise RuntimeError('Compose operation failed: ' + result.stderr[-1500:])
    return result.stdout.strip()


def containers(service):
    return dc('ps', '-q', service).splitlines()


def req(path, method='GET', body=None, headers=None, expected=None, cookies=True):
    merged = {'X-Request-ID': 'multi-smoke-' + uuid.uuid4().hex}
    if token:
        merged['Authorization'] = 'Bearer ' + token
    merged.update(headers or {})
    data = None if body is None else json.dumps(body, ensure_ascii=False).encode()
    if data is not None:
        merged['Content-Type'] = 'application/json'
    request = urllib.request.Request(base + path, data=data, headers=merged, method=method)
    try:
        response = (opener if cookies else urllib.request.build_opener()).open(request, timeout=10)
    except urllib.error.HTTPError as error:
        response = error
    raw = response.read()
    status, result_headers = response.code, response.headers
    if expected is not None:
        assert status in ([expected] if isinstance(expected, int) else expected), (path, status, raw[:400])
    result = json.loads(raw) if 'application/json' in result_headers.get('Content-Type', '') and raw else raw
    if status >= 400 and isinstance(result, dict):
        assert result['error']['request_id'] == result_headers['X-Request-ID'], result
    return status, result_headers, result


def passed(name, **detail):
    checks.append({'name': name, 'status': 'passed', **detail})
    (evidence / 'acceptance.json').write_text(json.dumps(checks, ensure_ascii=False, indent=2), encoding='utf-8')
    print('PASS ' + name, flush=True)


def wait_task(task_id, states=('awaiting_confirmation',), timeout=60):
    end = time.monotonic() + timeout
    while time.monotonic() < end:
        task = req('/api/v1/tasks/' + task_id, expected=200)[2]['task']
        if task['state'] in states:
            return task
        assert task['state'] not in ('dead_letter', 'cancelled'), task
        time.sleep(.25)
    raise AssertionError('Task did not reach expected state: ' + task_id)


def stable_route(path, timeout=10):
    started = time.monotonic()
    good = 0
    while time.monotonic() - started < timeout:
        status, _, _ = req(path)
        good = good + 1 if status == 200 else 0
        if good >= 10:
            return round(time.monotonic() - started, 3)
        time.sleep(.1)
    raise AssertionError('Route did not stabilize: ' + path)


# Only project-owned containers may be stopped, killed, or restarted.
for service in ('iam-service', 'asset-service', 'task-report-service', 'task-worker'):
    assert len(containers(service)) == 2, service
    for container in containers(service):
        assert docker('inspect', '-f', '{{index .Config.Labels "com.docker.compose.project"}}', container) == args.project

req('/health/ready', expected=200)
req('/', expected=200)
for path in ('/internal/v1/assets/batch', '/metrics', '/metrics/service', '/debug/pprof', '/api/v1/unknown', '/api/v1/assets-extra'):
    req(path, expected=404)
req('/api/v1/assets', headers={'Origin': 'https://untrusted.invalid'}, expected=403)
_, headers, _ = req('/api/v1/assets', 'OPTIONS', headers={'Origin': base}, expected=204)
assert headers.get_all('Access-Control-Allow-Origin') == [base]
_, headers, payload = req('/api/v1/unknown', headers={'X-Request-ID': 'x' * 129}, expected=404)
assert headers['X-Request-ID'] != 'x' * 129 and payload['error']['request_id']
req('/api/v1/import-jobs', 'POST', headers={'Content-Length': '104857601'}, expected=413)
passed('public routes, CORS, request IDs, size limit and private-path isolation')

username = env.get('IAM_BOOTSTRAP_ADMIN_USERNAME', 'admin')
password = env['IAM_BOOTSTRAP_ADMIN_PASSWORD']
login_status, _, login = req('/api/v1/auth/login', 'POST', {'username': username, 'password': password}, expected=(200,401))
if login_status == 401:
    password += '-Multi1!'
    login = req('/api/v1/auth/login', 'POST', {'username': username, 'password': password}, expected=200)[2]
token = login['access_token']
if login['user']['must_change_password']:
    changed = password + '-Multi1!'
    req('/api/v1/auth/change-password', 'POST', {'current_password': password, 'new_password': changed}, expected=204)
    password = changed
    login = req('/api/v1/auth/login', 'POST', {'username': username, 'password': password}, expected=200)[2]
    token = login['access_token']

distribution = {}
for path in ('/api/v1/auth/me', '/api/v1/asset-types', '/api/v1/import-jobs'):
    counts = {}
    connection = http.client.HTTPConnection('127.0.0.1', port, timeout=10)
    for _ in range(30):
        connection.request('GET', path, headers={'Authorization': 'Bearer ' + token})
        response = connection.getresponse()
        response.read()
        assert response.status == 200
        instance = response.getheader('X-Instance-ID')
        counts[instance] = counts.get(instance, 0) + 1
    connection.close()
    assert len(counts) == 2, (path, counts)
    distribution[path] = counts
passed('same token works across IAM, Asset and Task API with HTTP keep-alive', distribution=distribution)

old_refresh = next(cookie.value for cookie in jar if cookie.name == 'asset_refresh')
for _ in range(4):
    token = req('/api/v1/auth/refresh', 'POST', expected=200)[2]['access_token']
req('/api/v1/auth/refresh', 'POST', headers={'Cookie': 'asset_refresh=' + old_refresh}, expected=401, cookies=False)
req('/api/v1/auth/refresh', 'POST', expected=401)
token = req('/api/v1/auth/login', 'POST', {'username': username, 'password': password}, expected=200)[2]['access_token']
passed('cross-replica refresh rotation and true replay still revoke the session family')

bad_user = 'missing-' + uuid.uuid4().hex[:12]
instances = set()
for i in range(5):
    _, headers, _ = req('/api/v1/auth/login', 'POST', {'username': bad_user, 'password': 'invalid-password'}, headers={'X-Forwarded-For': f'192.0.2.{i}'}, expected=401)
    instances.add(headers['X-Instance-ID'])
assert len(instances) == 2
req('/api/v1/auth/login', 'POST', {'username': bad_user, 'password': 'invalid-password'}, headers={'X-Forwarded-For': '203.0.113.9'}, expected=429)
docker('restart', containers('iam-service')[0])
time.sleep(5)
req('/api/v1/auth/login', 'POST', {'username': bad_user, 'password': 'invalid-password'}, expected=429)
passed('login failures share one quota across replicas, forged headers and process restart')

asset_path = '/api/v1/assets/responsible-department'
asset = req(asset_path, 'POST', {'fields': {'department_name': 'multi-smoke-' + uuid.uuid4().hex}}, expected=201)[2]['asset']
def edit(code):
    return req(asset_path + '/' + asset['id'], 'PATCH', {'version': 1, 'fields': {'department_code': code}}, cookies=False)[0]
with concurrent.futures.ThreadPoolExecutor(2) as pool:
    statuses = sorted(pool.map(edit, ['first', 'second']))
assert statuses == [200, 409], statuses
passed('concurrent asset updates preserve optimistic locking', statuses=statuses)

workers = containers('task-worker')
docker('stop', *workers)
payload = {'asset_type': 'responsible-department', 'idempotency_key': uuid.uuid4().hex, 'csv': 'department_name,department_code\nMulti import,MULTI\n'}
task = req('/api/v1/import-jobs', 'POST', payload, expected=202)[2]['task']
assert task['state'] == 'pending' and task['phase'] == 'preflight', task
duplicate = req('/api/v1/import-jobs', 'POST', payload, expected=200)[2]['task']
assert duplicate['id'] == task['id']
req('/api/v1/import-jobs', 'POST', {**payload, 'csv': payload['csv'] + 'Other,OTHER\n'}, expected=409)
req('/api/v1/import-jobs/' + task['id'] + '/confirm', 'POST', expected=(404, 409))
api = containers('task-report-service')[0]
docker('kill', api)
docker('start', api)
docker('start', *workers)
ready = wait_task(task['id'])
assert ready['result']['valid'] == 1 and ready['phase'] == 'preflight', ready
req('/api/v1/import-jobs/' + task['id'] + '/confirm', 'POST', expected=202)
done = wait_task(task['id'], ('succeeded',))
assert done['result']['committed'] == 1 and done['phase'] == 'execution', done
passed('durable async preflight survives API kill, enforces digest and waits for confirmation')

invalid = req('/api/v1/import-jobs', 'POST', {**payload, 'idempotency_key': uuid.uuid4().hex, 'csv': 'department_name,department_code\n,INVALID\n'}, expected=202)[2]['task']
invalid = wait_task(invalid['id'])
assert invalid['result']['error_count'] > 0
req(invalid['result']['error_download_url'], expected=200)
req('/api/v1/import-jobs/' + invalid['id'] + '/confirm', 'POST', expected=(404, 409))
passed('invalid preflight publishes an authorized error report and cannot execute')

victim = containers('asset-service')[0]
docker('kill', victim)
recovery = stable_route('/api/v1/asset-types')
docker('start', victim)
time.sleep(5)
passed('HTTP SIGKILL removes the failed replica and recovers healthy routing', stable_after_seconds=recovery)

victim = containers('asset-service')[0]
with concurrent.futures.ThreadPoolExecutor(1) as pool:
    stopping = pool.submit(docker, 'stop', victim)
    failures = 0
    while not stopping.done():
        failures += req('/api/v1/asset-types')[0] != 200
        time.sleep(.1)
    stopping.result()
assert failures == 0, failures
docker('start', victim)
time.sleep(5)
passed('SIGTERM drains an HTTP replica while traffic continues', errors=failures)

# Docker DNS must add and remove concrete addresses while HAProxy is running.
dc('up', '-d', '--no-deps', '--scale', 'asset-service=3', 'asset-service')
end = time.monotonic() + 20
seen = set()
while time.monotonic() < end and len(seen) < 3:
    status, headers, _ = req('/api/v1/asset-types')
    if status == 200:
        seen.add(headers['X-Instance-ID'])
    time.sleep(.1)
assert len(seen) == 3, seen
dc('up', '-d', '--no-deps', '--scale', 'asset-service=2', 'asset-service')
time.sleep(5)
stable_route('/api/v1/asset-types')
passed('Docker DNS discovers a third address and removes it on scale-down', discovered_instances=len(seen))

minio = containers('minio')[0]
try:
    docker('stop', minio)
    req('/api/v1/import-jobs', expected=200)
    req('/api/v1/auth/me', expected=200)
finally:
    docker('start', minio)
passed('object storage failure does not withdraw task-list and IAM routes')

assets = containers('asset-service')
try:
    docker('stop', *assets)
    time.sleep(5)
    req('/api/v1/asset-types', expected=503)
    req('/api/v1/import-jobs', expected=200)
    req('/api/v1/auth/me', expected=200)
finally:
    docker('start', *assets)
time.sleep(5)
passed('all Asset replicas down returns JSON dependency error while other domains work')

# Capture individual metrics without routing through the load balancer.
for service, service_port in [('iam-service', 8081), ('asset-service', 8082), ('task-report-service', 8084)]:
    for container in containers(service):
        metrics = docker('exec', container, 'wget', '-qO-', f'http://127.0.0.1:{service_port}/metrics/service')
        (evidence / (container[:12] + '.prom')).write_text(metrics, encoding='utf-8')
passed('per-instance metrics captured directly', instances=6)
print(f'Acceptance evidence: {evidence / "acceptance.json"}', flush=True)
