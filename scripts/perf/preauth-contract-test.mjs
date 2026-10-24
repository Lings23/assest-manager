// Exercise the actual setup/session functions with isolated in-memory HTTP jars.
import assert from 'node:assert/strict';
import fs from 'node:fs';
import vm from 'node:vm';
const source = fs.readFileSync(new URL('./api-preauth.js', import.meta.url), 'utf8');
const baseline = fs.readFileSync(new URL('./api.js', import.meta.url), 'utf8');
const setup = source.slice(source.indexOf('export function setup()'), source.indexOf('\nlet session;')).replace('export function', 'function');
const session = source.slice(source.indexOf('let session;'), source.indexOf('\nfunction operationForIteration'));
function context({ duplicate = false, missing = false, ttl = 900 } = {}) {
  const jar = { value: '', clear() { this.value = ''; }, set(url, name, value, options) { assert.equal(options.path, '/api/v1/auth'); this.value = value; }, cookiesForURL() { return missing ? {} : { asset_refresh: [this.value] }; } };
  const calls = []; let created = 0;
  const box = { writes: true, writeTemplate: {}, maxVUs: 2, scenarios: { warmup: { startTime: '0s', duration: '60s' }, steady: { startTime: '60s', duration: '600s' } }, base: 'http://local/api/v1',
    exec: { vu: { idInTest: 1 }, scenario: { name: 'warmup' }, test: { abort(message) { throw new Error(message); } } },
    accountForVU(vu) { return { id: `account-${vu % 2}`, username: `u${vu % 2}`, department_id: 'd' }; },
    auth(account, refresh, phase) { calls.push({ account: account.id, refresh, phase }); jar.value = duplicate ? 'same' : `cookie-${calls.length}`; return { access_token: `token-${calls.length}`, user: { user_id: account.id }, expires_in: ttl }; },
    request() { return { status: 201, value: { asset: { id: `asset-${++created}`, version: 1 } } }; }, parse(response) { return response.value; }, writeFields() { return {}; },
    http: { cookieJar() { return jar; } }, fixtureCreates: { add() {} }, preauthSessions: { add() {} }, adoptedSessions: { add() {} }, calls, jar };
  vm.createContext(box); vm.runInContext(setup + '\n' + session, box); return box;
}
const prepared = context();
const data = prepared.setup();
assert.equal(data.sessions.length, 4); // Includes both scenario maxima.
assert.equal(new Set(data.sessions.map(s => s.refresh_cookie)).size, 4);
assert.equal(prepared.calls.filter(c => c.phase === 'preauth_setup').length, 4);
for (const vu of [1, 4]) {
  const box = context(); box.exec.vu.idInTest = vu; box.ensureSession(data);
  assert.equal(box.jar.value, data.sessions[vu - 1].refresh_cookie);
  assert.equal(box.calls.length, 0); // Adoption never performs a lazy login.
  vm.runInContext('session.refresh_at = 0', box); box.ensureSession(data);
  assert.equal(box.calls.length, 1); assert.equal(box.calls[0].refresh, true);
  assert.equal(box.calls[0].account, data.sessions[vu - 1].account_id);
}
assert.throws(() => context({ duplicate: true }).setup(), /unique refresh cookie/);
assert.throws(() => context({ missing: true }).setup(), /unique refresh cookie/);
assert.throws(() => context({ ttl: 120 }).setup(), /cannot cover/);
const mismatch = context(); mismatch.exec.vu.idInTest = 5;
assert.throws(() => mismatch.ensureSession(data), /Missing or mismatched/);
const body = text => text.slice(text.indexOf('export function business(data)'), text.indexOf('\nexport function handleSummary')).replace('ensureSession(data);', 'ensureSession();').replaceAll('\r\n', '\n');
assert.equal(body(source), body(baseline), 'Business requests, mix, assertions and accounting must remain identical');
for (const match of baseline.matchAll(/thresholds\[[^\n]+/g)) assert.ok(source.includes(match[0]));
console.log('PASS: independent complete VU setup, jar adoption, refresh retention, failure guards, identical business implementation');
