// P2 preauthenticated protocol, derived from the unchanged api.js baseline.
// Setup credentials stay in k6 memory; never include setup data in summaries.
// Authentication and fixture preparation are excluded from business metrics.
import http from 'k6/http';
import exec from 'k6/execution';
import { check } from 'k6';
import { SharedArray } from 'k6/data';
import { Counter, Rate, Trend } from 'k6/metrics';

function hasEnv(name) { return __ENV[name] !== undefined && String(__ENV[name]).trim() !== ''; }
function numberEnv(name, fallback, minimum = 0) {
  const value = hasEnv(name) ? Number(__ENV[name]) : fallback;
  if (!Number.isFinite(value) || value < minimum) throw new Error(`Invalid ${name}`);
  return value;
}
function readJSON(path) { return JSON.parse(open(path).replace(/^\uFEFF/, '')); }
const manifestPath = __ENV.SEED_MANIFEST;
if (!manifestPath) throw new Error('SEED_MANIFEST is required');
// Parse the large manifest once globally, not once per VU JavaScript isolate.
let manifest = new SharedArray('perf-api-manifest-meta', () => {
  const source = readJSON(manifestPath);
  return [{ schema_version: source.schema_version, dataset: source.dataset, rows: source.rows, seed: source.seed,
    base_url: source.base_url, asset_types: source.asset_types, expected_counts: source.expected_counts,
    search_cases: source.search_cases, departments: source.departments, write_template: source.write_template, default_run_id: Date.now() }];
})[0];
const mode = __ENV.MODE || 'list';
const modes = ['read', 'list', 'detail', 'versions', 'search', 'deep', 'mixed', 'reporting', 'burst', 'soak'];
if (!modes.includes(mode)) throw new Error(`Unsupported MODE ${mode}`);
const writes = ['mixed', 'burst', 'soak'].includes(mode);
let base = (__ENV.BASE_URL || manifest.base_url || '').replace(/\/+$/, '');
if (!/^https?:\/\//.test(base)) throw new Error('A Gateway BASE_URL is required');
if (!base.endsWith('/api/v1')) base += '/api/v1';
const rate = numberEnv('RATE', 1, 1 / 60);
if (Math.abs(rate * 60 - Math.round(rate * 60)) > 0.000001) throw new Error('RATE must be a multiple of 1/60 RPS');
const warmupSeconds = numberEnv('WARMUP_SECONDS', 60);
const durationSeconds = numberEnv('DURATION_SECONDS', mode === 'soak' ? 3600 : mode === 'burst' ? 300 : 180, 1);
const burstSeconds = numberEnv('BURST_SECONDS', 30, 1);
const windowSeconds = numberEnv('WINDOW_SECONDS', 60, 1);
const maxVUs = Math.floor(numberEnv('MAX_VUS', Math.min(256, Math.max(16, Math.ceil(rate * (mode === 'burst' ? 8 : 4)))) , 1));
const preallocatedVUs = Math.floor(numberEnv('PRE_ALLOCATED_VUS', Math.min(maxVUs, Math.max(4, Math.ceil(rate * 3))), 1));
if (preallocatedVUs > maxVUs) throw new Error('PRE_ALLOCATED_VUS exceeds MAX_VUS');
const runID = __ENV.RUN_ID || `api-${mode}-${manifest.default_run_id}`;
if (!/^[A-Za-z0-9_.-]{1,100}$/.test(runID)) throw new Error('RUN_ID must contain 1-100 safe filename characters');
const outDir = (__ENV.OUT_DIR || '.').replace(/[\\/]+$/, '');
const dataset = String(manifest.dataset || 'unknown');
const requestedType = __ENV.ASSET_TYPE || '';
let searchCases = manifest.search_cases || [];
if (hasEnv('SEARCH_QUERY')) searchCases = [{
  asset_type: requestedType || 'responsible-department', query: __ENV.SEARCH_QUERY,
  label: __ENV.SEARCH_MATCH || 'custom', expected_min: Number(__ENV.SEARCH_EXPECT_MIN || 0),
  expected_max: hasEnv('SEARCH_EXPECT_MAX') ? Number(__ENV.SEARCH_EXPECT_MAX) : undefined,
}];
if ((writes || mode === 'search') && !searchCases.length) throw new Error('Search requires manifest.search_cases or SEARCH_QUERY');
if ((writes || mode === 'search') && requestedType && !searchCases.some(c => (c.asset_type || c.type) === requestedType)) {
  throw new Error('ASSET_TYPE has no search case; use responsible-department or supply SEARCH_QUERY for that type');
}
const writeTemplate = manifest.write_template || { type: 'responsible-department', fields: {} };
// Only this small projection survives in each VU; the full seed manifest does not.
const meta = {
  schema_version: manifest.schema_version, dataset, rows: manifest.rows, seed: manifest.seed,
  manifest_path: manifestPath, asset_types: manifest.asset_types || Object.keys(manifest.expected_counts || {}),
};
manifest = null;

function permittedDepartments(account, tree) {
  const ids = [account.department_id].filter(Boolean);
  if (account.data_scope === 'descendants') {
    for (let changed = true; changed;) {
      changed = false;
      for (const dep of tree) if (ids.includes(dep.parent_id) && !ids.includes(dep.id)) { ids.push(dep.id); changed = true; }
    }
  }
  return ids;
}
function visible(record, account) {
  if (record.deleted || record.deleted_at || record.is_deleted) return false;
  switch (account.data_scope || account.scope) {
    case 'global': return true;
    case 'own': return record.owner_id === account.id;
    case 'department': case 'descendants': return account.department_ids.includes(record.department_id);
    default: return false;
  }
}
const accounts = new SharedArray('perf-api-accounts', () => {
  const source = readJSON(manifestPath);
  const credentials = source.credentials_file ? readJSON(source.credentials_file) : { users: source.accounts || [] };
  const candidates = [...(credentials.users || [])];
  if (credentials.admin && !candidates.some(a => a.username === credentials.admin.username)) candidates.push(credentials.admin);
  const assets = source.assets || (Array.isArray(source.samples) ? source.samples : []);
  return candidates.map(credential => {
    const user = (source.users || []).find(a => a.id === (credential.id || credential.user_id) || a.username === credential.username) || {};
    const account = { ...user, ...credential };
    account.id = account.id || account.user_id;
    account.data_scope = account.data_scope || account.scope || 'global';
    account.roles = account.roles || [];
    account.department_ids = permittedDepartments(account, source.departments || []);
    account.samples = {};
    const derivedCounts = {};
    for (const record of assets) if (visible(record, account)) {
      const type = record.asset_type || record.type;
      derivedCounts[type] = (derivedCounts[type] || 0) + 1;
      const samples = account.samples[type] || (account.samples[type] = []);
      if (samples.length < 20) samples.push({ id: record.id, version: record.version, owner_id: record.owner_id, department_id: record.department_id, asset_type: type });
    }
    account.visible_counts = (source.per_user_counts || {})[account.id] || (source.per_user_counts || {})[account.username] || account.visible_counts || derivedCounts;
    return account;
  }).filter(a => a.username && a.password && a.id)
    .filter(a => !__ENV.ACCOUNT_SCOPE || a.data_scope === __ENV.ACCOUNT_SCOPE)
    .filter(a => !__ENV.ACCOUNT_ROLE || a.roles.includes(__ENV.ACCOUNT_ROLE))
    .filter(a => !__ENV.ACCOUNT_USERNAME || a.username === __ENV.ACCOUNT_USERNAME)
    .filter(a => !writes || a.roles.includes('admin') || a.roles.includes('reporter'));
});
if (!accounts.length) throw new Error('No usable seed accounts match the account filters');

const attempts = new Counter('business_attempts');
const successes = new Counter('business_successes');
const errors = new Rate('business_errors');
const correctness = new Counter('correctness_errors');
const duration = new Trend('business_duration', true);
const successDuration = new Trend('business_success_duration', true);
const failureDuration = new Trend('business_failure_duration', true);
const wallDuration = new Trend('business_wall_duration', true);
const authErrors = new Rate('auth_errors');
const authRequests = new Counter('auth_requests');
const fixtureCreates = new Counter('fixture_creates');
const bytesReceived = new Counter('business_response_bytes');
const selectedOperations = mode === 'read' ? ['list', 'detail', 'versions'] : writes ? ['list', 'detail', 'search', 'versions', 'create', 'update'] : [mode];
const supportedTypes = ['data', 'hardware-software', 'responsible-department', 'software-statistics', 'supply-chain', 'system-info', 'vulnerability'];
const supportedScopes = ['own', 'department', 'descendants', 'global'];
const breakdownAxes = {
  asset_type: mode === 'reporting' ? ['all'] : supportedTypes,
  scope: supportedScopes,
};
function breakdownSelector(axis, value, operation) { return `{phase:steady,operation:${operation},${axis}:${value}}`; }
const thresholds = {
  'auth_requests{phase:auth,operation:login}': ['count==0'],
  'auth_requests{phase:auth,operation:refresh}': ['count==0'],
  preauth_sessions: ['count>0'],
  'business_errors{phase:steady}': ['rate<0.01'],
  'correctness_errors{phase:steady}': ['count==0'],
  'checks{phase:steady}': ['rate>0.99'],
  auth_errors: ['rate==0'],
  'auth_requests{operation:login}': ['count>=0'],
  'auth_requests{operation:refresh}': ['count>=0'],
  dropped_iterations: ['count==0'],
};
const preauthSessions = new Counter('preauth_sessions');
const adoptedSessions = new Counter('preauth_adopted_sessions');
for (const operation of selectedOperations) {
  const ordinary = ['list', 'detail', 'versions'].includes(operation);
  const p95 = operation === 'reporting' ? 5000 : ordinary ? 500 : 1000;
  const p99 = ordinary ? 1000 : 2000;
  const selector = `{phase:steady,operation:${operation}}`;
  thresholds[`business_duration${selector}`] = operation === 'reporting' ? [`p(95)<=${p95}`] : [`p(95)<=${p95}`, `p(99)<=${p99}`];
  thresholds[`business_attempts${selector}`] = ['count>=0'];
  thresholds[`business_successes${selector}`] = ['count>=0'];
  thresholds[`business_errors${selector}`] = ['rate<0.01'];
  // These selectors retain successful/failed latency distributions in summary JSON.
  thresholds[`business_success_duration${selector}`] = [];
  thresholds[`business_failure_duration${selector}`] = [];
  thresholds[`business_successes{operation:${operation}}`] = ['count>=0'];
}
// Empty selectors materialize bounded summary submetrics without adding pass /
// fail thresholds. Types and scopes remain separate dimensions, never a cross
// product, and neither dimension contains dynamic IDs or arbitrary labels.
for (const [axis, values] of Object.entries(breakdownAxes)) {
  for (const value of values) for (const operation of selectedOperations) {
    const selector = breakdownSelector(axis, value, operation);
    for (const metric of ['business_attempts', 'business_successes', 'business_errors', 'business_duration']) {
      thresholds[`${metric}${selector}`] = [];
    }
  }
}
function scenario(targetRate, seconds, startTime, phase) {
  return {
    executor: 'constant-arrival-rate', rate: Math.round(targetRate * 60), timeUnit: '1m',
    duration: `${seconds}s`, startTime: `${startTime}s`, preAllocatedVUs: preallocatedVUs,
    maxVUs, gracefulStop: '30s', exec: 'business', tags: { phase },
  };
}
const scenarios = {};
if (warmupSeconds) scenarios.warmup = scenario(rate, warmupSeconds, 0, 'warmup');
if (mode === 'burst') {
  scenarios.steady_before = scenario(rate, durationSeconds, warmupSeconds, 'steady');
  scenarios.burst = scenario(rate * 2, burstSeconds, warmupSeconds + durationSeconds, 'burst');
  scenarios.steady_after = scenario(rate, durationSeconds, warmupSeconds + durationSeconds + burstSeconds, 'steady');
} else scenarios.steady = scenario(rate, durationSeconds, warmupSeconds, 'steady');
// Bounded time-window selectors preserve burst recovery and soak trends without
// requiring a large raw-metric stream. They do not impose percentile claims on
// low-count windows and do not replace the external resource stop monitor.
const windows = [];
for (const [segment, config] of Object.entries(scenarios)) {
  const seconds = Number(config.duration.slice(0, -1));
  for (let index = 0; index < Math.ceil(seconds / windowSeconds); index++) {
    const selector = `{segment:${segment},window:${index}}`;
    const secondsInWindow = Math.min(windowSeconds, seconds - index * windowSeconds);
    windows.push({ segment, index, selector, phase: config.tags.phase, scheduled_seconds: secondsInWindow,
      start_seconds: Number(config.startTime.slice(0, -1)) + index * windowSeconds,
      target_rps: config.rate / 60 });
    thresholds[`business_attempts${selector}`] = ['count>=0'];
    thresholds[`business_errors${selector}`] = [];
    thresholds[`business_duration${selector}`] = [];
    for (const operation of selectedOperations) {
      thresholds[`business_duration{segment:${segment},window:${index},operation:${operation}}`] = [];
    }
  }
}
export const options = {
  scenarios, thresholds, noCookiesReset: true, setupTimeout: '10m',
  systemTags: ['status', 'method', 'name', 'scenario', 'expected_response', 'error_code', 'proto', 'tls_version'],
  summaryTrendStats: ['count', 'avg', 'min', 'med', 'max', 'p(95)', 'p(99)'],
  tags: { dataset, mode, run_id: runID },
};

function accountForVU(vu) { return accounts[(vu - 1) % accounts.length]; }
function parse(response) { try { return response.json(); } catch (_) { return null; } }
function request(method, path, body, token, tags) {
  return http.request(method, base + path, body === null ? null : JSON.stringify(body), {
    headers: { 'Content-Type': 'application/json', ...(token ? { Authorization: `Bearer ${token}` } : {}) },
    tags, timeout: '35s', redirects: 0,
  });
}
function auth(account, refresh = false, phase = 'auth') {
  authRequests.add(1, { operation: refresh ? 'refresh' : 'login', phase });
  const response = request('POST', refresh ? '/auth/refresh' : '/auth/login', refresh ? null : {
    username: account.username, password: account.password,
  }, '', { name: refresh ? 'auth_refresh' : 'auth_login', kind: 'auth', phase, operation: refresh ? 'refresh' : 'login' });
  const body = parse(response);
  const ok = response.status === 200 && body && typeof body.access_token === 'string' && body.expires_in > 60 && body.user && !body.user.must_change_password && body.user.user_id === account.id;
  authErrors.add(!ok, { operation: refresh ? 'refresh' : 'login', phase });
  if (!ok) exec.test.abort(`Authentication failed (${response.status}); credentials and response are withheld`);
  return body;
}
function writeFields(vu, iteration, update = false) {
  const unique = `${runID.slice(-45)}-${vu}-${iteration}`.slice(0, 90);
  if (update) return { [writeTemplate.mutable_field || 'department_code']: `P-${unique}`.slice(0, 50) };
  return { ...(writeTemplate.fields || {}), department_name: `Perf-${unique}`.slice(0, 100), department_code: `P-${unique}`.slice(0, 50) };
}

// The fixture phase runs before the arrival clock. Every VU receives a fresh
// private record owned by its selected account, so updates never compete.
export function setup() {
  if (!writes) throw new Error('Preauthenticated protocol currently supports mixed/burst/soak writes only');
  if ((writeTemplate.type || 'responsible-department') !== 'responsible-department') throw new Error('The current write generator supports responsible-department only');
  const tokens = {};
  const fixtures = [];
  for (let vu = 1; vu <= maxVUs * Object.keys(scenarios).length; vu++) {
    // Scenario VU IDs can exceed one scenario's maxVUs when graceful periods overlap.
    const account = accountForVU(vu);
    if (!tokens[account.username]) tokens[account.username] = auth(account, false, 'setup').access_token;
    const response = request('POST', '/assets/responsible-department', {
      department_id: account.department_id || '', fields: writeFields(vu, 'fixture'),
    }, tokens[account.username], { kind: 'setup', phase: 'setup', operation: 'fixture_create', name: 'fixture_create' });
    const body = parse(response);
    if (response.status !== 201 || !body || !body.asset || body.asset.version !== 1) exec.test.abort(`Fixture preparation failed (${response.status})`);
    fixtures.push({ id: body.asset.id, version: body.asset.version });
    fixtureCreates.add(1);
  }
  // Authenticate every possible VU, including separate warmup/steady pools.
  // Each login creates a distinct server-side session and refresh token.
  const sessions = [];
  const cookieValues = new Set();
  const setupStarted = Date.now();
  for (let vu = 1; vu <= fixtures.length; vu++) {
    const account = accountForVU(vu);
    const pair = auth(account, false, 'preauth_setup');
    const values = http.cookieJar().cookiesForURL(`${base}/auth/refresh`).asset_refresh;
    if (!values || values.length !== 1 || !values[0] || cookieValues.has(values[0])) {
      exec.test.abort('Preauthentication requires a unique refresh cookie per VU; values withheld');
    }
    cookieValues.add(values[0]);
    sessions.push({ vu, account_id: account.id, token: pair.access_token, principal: pair.user,
      refresh_cookie: values[0], refresh_at: Date.now() + (pair.expires_in - 60 - (vu % 30)) * 1000 });
    preauthSessions.add(1);
  }
  const endSeconds = Math.max(...Object.values(scenarios).map(config => Number(config.startTime.slice(0, -1)) + Number(config.duration.slice(0, -1))));
  if (sessions.some(value => value.refresh_at < Date.now() + (endSeconds + 30) * 1000)) {
    exec.test.abort('Preauthenticated sessions cannot cover the scheduled window; shorten protocol, never disable refresh');
  }
  return { fixtures, fixture_count: fixtures.length, sessions,
    preauth: { session_count: sessions.length, started_epoch_ms: setupStarted, completed_epoch_ms: Date.now() } };
}

let session;
let currentAccount;
let ownWrite;
let errorSamples = 0;
function ensureSession(data) {
  if (!currentAccount) currentAccount = accountForVU(exec.vu.idInTest);
  if (!session) {
    const prepared = data.sessions[exec.vu.idInTest - 1];
    if (!prepared || prepared.vu !== exec.vu.idInTest || prepared.account_id !== currentAccount.id || prepared.principal.user_id !== currentAccount.id) {
      exec.test.abort('Missing or mismatched preauthenticated VU session; no lazy login allowed');
    }
    const jar = http.cookieJar();
    jar.clear(`${base}/auth/refresh`);
    jar.set(`${base}/auth/refresh`, 'asset_refresh', prepared.refresh_cookie, { path: '/api/v1/auth' });
    const restored = jar.cookiesForURL(`${base}/auth/refresh`).asset_refresh;
    if (!restored || restored.length !== 1 || restored[0] !== prepared.refresh_cookie) exec.test.abort('VU refresh cookie restoration failed; values withheld');
    session = { token: prepared.token, principal: prepared.principal, refresh_at: prepared.refresh_at };
    adoptedSessions.add(1, { phase: exec.scenario.name === 'warmup' ? 'warmup' : 'steady' });
  }
  if (!session || Date.now() >= session.refresh_at) {
    const pair = auth(currentAccount, !!session);
    // The cookie jar is unique to this VU and persists between iterations.
    // A refresh is synchronous: no two rotations use the same refresh token.
    session = { token: pair.access_token, principal: pair.user,
      refresh_at: Date.now() + (pair.expires_in - 60 - (exec.vu.idInTest % 30)) * 1000 };
  }
}
function operationForIteration(iteration) {
  if (!writes) return mode === 'read' ? ['list', 'detail', 'versions'][iteration % 3] : mode;
  // A coprime permutation spreads each 100-request mix throughout the clock.
  const slot = (iteration * 37) % 100;
  return slot < 35 ? 'list' : slot < 60 ? 'detail' : slot < 75 ? 'search' : slot < 80 ? 'versions' : slot < 90 ? 'create' : 'update';
}
function contentValid(record, type) {
  return record && record.id && record.type === type && record.fields && typeof record.fields === 'object' && !Array.isArray(record.fields) && Number.isInteger(record.version) && record.version >= 1 && !record.deleted_at && visible(record, currentAccount);
}
function safeSample(response, operation, reason) {
  if (errorSamples++ >= 5) return;
  console.error(JSON.stringify({ event: 'business_failure', run_id: runID, vu: exec.vu.idInTest,
    operation, status: response.status, reason, request_id: response.headers['X-Request-Id'] || response.headers['X-Request-ID'] || '' }));
}
function utf8Bytes(value) {
  let bytes = 0;
  for (let i = 0; i < value.length; i++) {
    const code = value.charCodeAt(i);
    if (code < 0x80) bytes++;
    else if (code < 0x800) bytes += 2;
    else if (code >= 0xD800 && code <= 0xDBFF && i + 1 < value.length && value.charCodeAt(i + 1) >= 0xDC00 && value.charCodeAt(i + 1) <= 0xDFFF) { bytes += 4; i++; }
    else bytes += 3;
  }
  return bytes;
}

export function business(data) {
  ensureSession(data);
  const iteration = exec.scenario.iterationInTest;
  const operation = operationForIteration(iteration);
  const phase = exec.scenario.name === 'warmup' ? 'warmup' : exec.scenario.name === 'burst' ? 'burst' : 'steady';
  const types = Object.keys(currentAccount.samples).filter(type => !requestedType || type === requestedType);
  if (!types.length && operation !== 'reporting') exec.test.abort('Selected account has no visible seed samples');
  let type = types[(iteration * 31 + exec.vu.idInTest) % types.length];
  let samples = currentAccount.samples[type] || [];
  let sample = samples[iteration % samples.length];
  let method = 'GET';
  let path;
  let payload = null;
  let expectedStatus = 200;
  let searchCase;
  let offset = 0;
  const limit = Math.floor(numberEnv('LIMIT', 20, 1));
  if (![20, 100].includes(limit)) throw new Error('LIMIT must be 20 or 100');
  const segment = exec.scenario.name;
  const segmentSeconds = Number(scenarios[segment].duration.slice(0, -1));
  const window = Math.min(Math.ceil(segmentSeconds / windowSeconds) - 1,
    Math.max(0, Math.floor((Date.now() - exec.scenario.startTime) / (windowSeconds * 1000))));
  const tags = { phase, segment, window: String(window), operation, kind: 'business', asset_type: type || 'all', scope: currentAccount.data_scope, name: `asset_${operation}`, outcome: 'pending' };
  if (operation === 'reporting') { path = '/reporting/asset-counts'; tags.asset_type = 'all'; }
  else if (operation === 'list' || operation === 'deep') {
    if (operation === 'deep') {
      const count = Number(currentAccount.visible_counts[type] || 0);
      const offsets = [0, 1000, 10000, Math.max(0, Math.floor(Math.max(0, count - 1) / limit) * limit)];
      offset = hasEnv('OFFSET') ? Math.floor(numberEnv('OFFSET', 0)) : offsets[iteration % offsets.length];
    }
    path = `/assets/${type}?limit=${limit}&offset=${offset}`;
    tags.page = operation === 'deep' ? (offset === 0 ? 'first' : offset >= 10000 ? 'deep' : 'middle') : 'first';
    tags.limit = String(limit);
  } else if (operation === 'detail') path = `/assets/${type}/${sample.id}`;
  else if (operation === 'versions') path = `/assets/${type}/${sample.id}/versions`;
  else if (operation === 'search') {
    const available = searchCases.filter(c => (!requestedType || (c.asset_type || c.type) === requestedType) && currentAccount.samples[c.asset_type || c.type]);
    if (!available.length) exec.test.abort('No search case matches the selected account/type');
    searchCase = available[iteration % available.length]; type = searchCase.asset_type || searchCase.type;
    tags.asset_type = type; tags.search_case = searchCase.label || 'custom';
    path = `/assets/${type}?limit=${limit}&offset=0&search=${encodeURIComponent(searchCase.query)}`;
  } else if (operation === 'create') {
    type = 'responsible-department'; tags.asset_type = type; method = 'POST'; path = `/assets/${type}`; expectedStatus = 201;
    payload = { department_id: currentAccount.department_id || '', fields: writeFields(exec.vu.idInTest, `${exec.scenario.name}-${iteration}`) };
  } else if (operation === 'update') {
    if (!ownWrite) ownWrite = { ...data.fixtures[exec.vu.idInTest - 1] };
    if (!ownWrite.id) exec.test.abort('No private write fixture exists for this VU');
    type = 'responsible-department'; tags.asset_type = type; method = 'PATCH'; path = `/assets/${type}/${ownWrite.id}`;
    payload = { version: ownWrite.version, fields: writeFields(exec.vu.idInTest, `${exec.scenario.name}-${iteration}`, true) };
  }
  attempts.add(1, tags);
  const start = Date.now();
  const response = request(method, path, payload, session.token, tags);
  const body = parse(response);
  const statusOK = response.status === expectedStatus;
  let valid = false;
  if (statusOK && body) {
    if (['list', 'deep', 'search'].includes(operation)) {
      const items = body.items === null ? [] : body.items;
      valid = Array.isArray(items) && items.length <= limit && items.every(item => contentValid(item, type));
      if (valid && operation !== 'search') {
        const expected = Math.min(limit, Math.max(0, Number(currentAccount.visible_counts[type] || 0) - offset));
        valid = writes ? items.length >= expected : items.length === expected;
      }
      if (valid && operation === 'search') {
        const query = String(searchCase.query).toLowerCase();
        valid = items.every(item => Object.values(item.fields).some(v => String(v).toLowerCase().includes(query)));
        if (currentAccount.data_scope === 'global') {
          if (searchCase.expected_min !== undefined) valid = valid && items.length >= Math.min(limit, searchCase.expected_min);
          if (searchCase.expected_max !== undefined) valid = valid && items.length <= searchCase.expected_max;
        }
      }
    } else if (operation === 'versions') {
      valid = Array.isArray(body.versions) && body.versions.length >= sample.version && body.versions.every((v, i) => v.version >= 1 && v.snapshot && v.snapshot.id === sample.id && (i === 0 || body.versions[i - 1].version > v.version));
    } else if (operation === 'reporting') {
      const expected = currentAccount.visible_counts;
      valid = Array.isArray(body.items) && body.items.length === 7 && new Set(body.items.map(item => item.asset_type)).size === 7 &&
        body.items.every(item => Number(item.total) === Number(expected[item.asset_type] || 0));
    } else {
      valid = contentValid(body.asset, type);
      if (operation === 'detail') valid = valid && body.asset.id === sample.id && body.asset.version >= sample.version;
      if (operation === 'create') valid = valid && body.asset.version === 1 && body.asset.owner_id === currentAccount.id && body.asset.fields.department_name === payload.fields.department_name;
      if (operation === 'update') {
        valid = valid && body.asset.id === ownWrite.id && body.asset.version === ownWrite.version + 1 &&
          Object.keys(payload.fields).every(field => body.asset.fields[field] === payload.fields[field]);
        if (valid) ownWrite.version = body.asset.version;
      }
    }
  }
  const ok = statusOK && valid;
  tags.outcome = ok ? 'success' : 'failure';
  const latency = response.timings.duration;
  duration.add(latency, tags); wallDuration.add(Date.now() - start, tags);
  (ok ? successDuration : failureDuration).add(latency, tags);
  bytesReceived.add(response.body ? utf8Bytes(response.body) : 0, tags);
  errors.add(!ok, tags);
  correctness.add(statusOK && !valid ? 1 : 0, tags);
  successes.add(ok ? 1 : 0, tags);
  check(response, { 'expected HTTP status': () => statusOK, 'business content and scope': () => ok }, tags);
  if (!ok) safeSample(response, operation, statusOK ? 'invalid_content_or_scope' : 'unexpected_status');
  if (response.status === 401) session = null;
  if (statusOK && !valid) exec.test.abort(`Correctness check failed for ${operation}; inspect bounded failure samples`);
  // A transport/HTTP failure after a PATCH can leave the committed version
  // unknown. Abort rather than silently retry or cause artificial 409 traffic.
  if (operation === 'update' && !ok) exec.test.abort(`Update failed (${response.status}); private fixture version is uncertain`);
}

export function handleSummary(data) {
  const steadySeconds = durationSeconds * (mode === 'burst' ? 2 : 1);
  const result = {
    schema_version: 1, run_id: runID, generated_at_utc: new Date().toISOString(), mode, base_url: base,
    dataset: meta, target_rps: rate, warmup_seconds: warmupSeconds, steady_seconds: steadySeconds,
    burst_seconds: mode === 'burst' ? burstSeconds : 0, burst_target_rps: mode === 'burst' ? rate * 2 : null,
    preallocated_vus: preallocatedVUs, max_vus_per_scenario: maxVUs, account_count: accounts.length,
    authentication_protocol: 'all-possible-VU independent sessions in setup; original business auth/refresh retained',
    preauthentication: { sessions: data.metrics.preauth_sessions?.values.count || 0,
      adopted_vus: data.metrics.preauth_adopted_sessions?.values.count || 0,
      window_logins: data.metrics['auth_requests{phase:auth,operation:login}']?.values.count || 0,
      window_refreshes: data.metrics['auth_requests{phase:auth,operation:refresh}']?.values.count || 0,
      setup_secrets_persisted: false },
    filters: { scope: __ENV.ACCOUNT_SCOPE || 'all', role: __ENV.ACCOUNT_ROLE || 'all', username: __ENV.ACCOUNT_USERNAME || 'all', asset_type: requestedType || 'all' },
    definitions: { business_duration: 'k6 response.timings.duration; all business responses including failures',
      business_wall_duration: 'wall time around HTTP call, JSON decoding and assertions; excludes login/refresh',
      actual_rps: 'business attempts in steady / scheduled steady seconds; compare with target and dropped_iterations',
      percentile_evidence: 'p95 requires >=100 successful samples per endpoint; p99 requires >=1000; thresholds alone do not prove sufficient evidence',
      fixtures: 'mixed/burst/soak prepare private per-VU assets in setup; these additions are outside baseline and must be included in post-run reconciliation',
      burst_rate: 'RATE is the low plateau (60% of validated stable load); burst is 2*RATE (120%)',
      cookies: 'independent persistent VU jars; synchronous refresh expires_in minus 60-89 seconds',
    },
    operations: {}, windows: [], metrics: data.metrics, state: data.state,
  };
  let steadyAttempts = 0;
  for (const operation of selectedOperations) {
    const selector = `{phase:steady,operation:${operation}}`;
    const count = data.metrics[`business_attempts${selector}`]?.values.count || 0;
    const succeeded = data.metrics[`business_successes${selector}`]?.values.count || 0;
    steadyAttempts += count;
    result.operations[operation] = { attempts: count, successes: succeeded, actual_rps: count / steadySeconds,
      errors: data.metrics[`business_errors${selector}`]?.values.rate,
      duration_ms: data.metrics[`business_duration${selector}`]?.values,
      successful_duration_ms: data.metrics[`business_success_duration${selector}`]?.values,
      failed_duration_ms: data.metrics[`business_failure_duration${selector}`]?.values,
      p95_evidence_sufficient: succeeded >= 100, p99_evidence_sufficient: succeeded >= 1000 };
  }
  result.actual_steady_rps = steadyAttempts / steadySeconds;
  result.arrival_rate_within_5_percent = Math.abs(result.actual_steady_rps - rate) <= rate * 0.05;
  result.dropped_iterations = data.metrics.dropped_iterations?.values.count || 0;
  result.thresholds_passed = Object.values(data.metrics).every(metric => !metric.thresholds || Object.values(metric.thresholds).every(t => t.ok));
  result.load_point_passed = result.thresholds_passed && result.arrival_rate_within_5_percent && result.dropped_iterations === 0;
  result.p95_evidence_sufficient = Object.values(result.operations).every(op => op.p95_evidence_sufficient);
  result.p99_evidence_sufficient = Object.values(result.operations).every(op => op.p99_evidence_sufficient);
  result.baseline_eligible = result.load_point_passed && result.p95_evidence_sufficient;
  result.auth_requests = {
    login: data.metrics['auth_requests{operation:login}']?.values.count || 0,
    refresh: data.metrics['auth_requests{operation:refresh}']?.values.count || 0,
    failure_rate: data.metrics.auth_errors?.values.rate || 0,
  };
  result.write_effects = {
    asset_type: writes ? 'responsible-department' : null,
    fixture_creates: data.metrics.fixture_creates?.values.count || 0,
    successful_creates_including_warmup: data.metrics['business_successes{operation:create}']?.values.count || 0,
    successful_updates_including_warmup: data.metrics['business_successes{operation:update}']?.values.count || 0,
    reconciled: false,
  };
  result.write_effects.expected_added_versions = result.write_effects.fixture_creates +
    result.write_effects.successful_creates_including_warmup + result.write_effects.successful_updates_including_warmup;
  for (const window of windows) {
    const count = data.metrics[`business_attempts${window.selector}`]?.values.count || 0;
    const operationValues = {};
    for (const operation of selectedOperations) {
      operationValues[operation] = data.metrics[`business_duration{segment:${window.segment},window:${window.index},operation:${operation}}`]?.values;
    }
    result.windows.push({ segment: window.segment, phase: window.phase, index: window.index, start_seconds: window.start_seconds,
      scheduled_seconds: window.scheduled_seconds, target_rps: window.target_rps, attempts: count,
      actual_rps: count / window.scheduled_seconds, error_rate: data.metrics[`business_errors${window.selector}`]?.values.rate,
      duration_ms: data.metrics[`business_duration${window.selector}`]?.values, operations: operationValues,
      full_window_observed: Number(data.state?.testRunDurationMs || 0) >= (window.start_seconds + window.scheduled_seconds) * 1000 });
  }
  result.breakdowns = {
    note: 'Steady operation metrics by asset type and by data scope independently; unobserved cells are explicit. Durations include failed responses. No cross-product or averaged percentiles.',
    asset_type: {}, scope: {},
  };
  for (const [axis, values] of Object.entries(breakdownAxes)) {
    for (const value of values) {
      const operations = {};
      for (const operation of selectedOperations) {
        const selector = breakdownSelector(axis, value, operation);
        const count = data.metrics[`business_attempts${selector}`]?.values.count || 0;
        const succeeded = data.metrics[`business_successes${selector}`]?.values.count || 0;
        operations[operation] = {
          observed: count > 0, attempts: count, successes: succeeded, actual_rps: count / steadySeconds,
          error_rate: count > 0 ? data.metrics[`business_errors${selector}`]?.values.rate : null,
          duration_ms: count > 0 ? data.metrics[`business_duration${selector}`]?.values : null,
          p95_evidence_sufficient: succeeded >= 100, p99_evidence_sufficient: succeeded >= 1000,
        };
      }
      result.breakdowns[axis][value] = operations;
    }
  }
  const summaryPath = `${outDir}/${runID}-summary.json`;
  return { [summaryPath]: JSON.stringify(result, null, 2), stdout: JSON.stringify({ run_id: runID, summary: summaryPath,
    mode, target_rps: rate, actual_steady_rps: result.actual_steady_rps, dropped_iterations: result.dropped_iterations,
    thresholds_passed: result.thresholds_passed, baseline_eligible: result.baseline_eligible, operations: result.operations }, null, 2) + '\n' };
}
