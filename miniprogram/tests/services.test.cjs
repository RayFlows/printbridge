const { test, beforeEach } = require('node:test');
const assert = require('node:assert/strict');
const { normalizeAddress } = require('../.test-dist/services/address.js');
const { entryFromQR } = require('../.test-dist/services/entry.js');
const { createConnection, saveConnection, disconnect, getConnection, authorizedRequest } = require('../.test-dist/services/connection.js');
const { listPrinters, parsePrinters } = require('../.test-dist/services/printers.js');
const { presentPrinter } = require('../.test-dist/pages/printers/presentation.js');

const origin = 'http://printbridge.test:8080';
const pairingCode = 'test-only-pairing-code-0123456789012345';
const token = 'a'.repeat(43);
const storageKey = 'printbridge.connection.v1';
let storage;
let requests;
let reply;

beforeEach(() => {
  storage = new Map();
  requests = [];
  reply = () => ({ statusCode: 200, data: {} });
  global.wx = {
    getStorageSync: key => storage.get(key),
    setStorageSync: (key, value) => storage.set(key, value),
    removeStorageSync: key => storage.delete(key),
    request: options => {
      requests.push(options);
      const result = reply(options);
      queueMicrotask(() => {
        if (result.fail) options.fail({ errMsg: 'request:fail' });
        else options.success(result);
      });
    },
  };
});

function session() {
  return { token, expiresAt: new Date(Date.now() + 3600000).toISOString() };
}

function storeSession(overrides = {}) {
  storage.set(storageKey, { address: origin, ...session(), ...overrides });
}

function printer() {
  return {
    id: 'Synthetic', name: 'Synthetic printer', model: '', location: '',
    state: 'unknown', acceptingJobs: null, stateReasons: [],
    capabilities: {
      media: [], sides: [], colorModes: [], copies: null,
      defaultMedia: '', defaultSides: '', defaultColorMode: '',
    },
  };
}

test('addresses are normalized without allowing credentials, paths or query strings', () => {
  assert.equal(normalizeAddress(' https://Printer.Example.test:8443/ '), 'https://printer.example.test:8443');
  assert.equal(normalizeAddress('http://[::1]:8080'), 'http://[::1]:8080');
  for (const value of [
    'file:///etc', 'printer.test', 'http://name:password@printer.test', 'http://printer.test/other',
    'http://printer.test?token=x', 'http://printer.test#secret', 'http://printer.test:0',
    'http://printer.test:65536', 'http://-printer.test', 'http://a..test', 'http://999.1.1.1',
    'http://127.1', 'http://127.0.0.01', 'http://printer.test\\other',
  ]) assert.throws(() => normalizeAddress(value), { code: 'invalid_address' }, value);
});

test('a print QR carries its own authorization; parsing alone does not send requests', () => {
  const qr = { type: 'printbridge', version: 2, server: origin, pairingCode };
  assert.deepEqual(entryFromQR(JSON.stringify(qr)), qr);
  for (const value of [
    'not-json', origin, JSON.stringify({ ...qr, token }), JSON.stringify({ ...qr, pairingCode: '' }),
    JSON.stringify({ ...qr, version: 3 }), JSON.stringify({ ...qr, type: 'another-app' }),
    JSON.stringify({ ...qr, server: 'http://user:secret@printer.test' }),
  ]) assert.throws(() => entryFromQR(value), { code: 'invalid_qr' });
  assert.equal(requests.length, 0);
});

test('connect verifies the server and stores only the expiring session', async () => {
  reply = options => options.url.endsWith('/server')
    ? { statusCode: 200, data: { name: 'PrintBridge', apiVersion: 'v1', version: 'test' } }
    : { statusCode: 201, data: session() };
  const connection = await createConnection(origin, pairingCode);
  assert.equal(storage.size, 0, 'A response must not be committed before the caller checks for a newer scan');
  saveConnection(connection);
  assert.equal(requests.length, 2);
  assert.equal(requests[0].method, 'GET');
  assert.equal(requests[0].header.Authorization, undefined);
  assert.equal(requests[1].method, 'POST');
  assert.deepEqual(requests[1].data, { pairingCode });
  assert.equal(getConnection().token, token);
  assert.ok(!JSON.stringify([...storage.values()]).includes(pairingCode));
});

test('an incompatible server never receives the pairing code', async () => {
  reply = () => ({ statusCode: 200, data: '<html>other service</html>' });
  await assert.rejects(createConnection(origin, pairingCode), { code: 'incompatible_server' });
  assert.equal(requests.length, 1);
  assert.equal(storage.size, 0);
});

test('malformed session responses are rejected rather than saved', async () => {
  reply = options => options.url.endsWith('/server')
    ? { statusCode: 200, data: { name: 'PrintBridge', apiVersion: 'v1' } }
    : { statusCode: 201, data: { token: 'too-short', expiresAt: 'not-a-date' } };
  await assert.rejects(createConnection(origin, pairingCode), { code: 'invalid_response' });
  assert.equal(storage.size, 0);
});

test('wrong pairing code has a specific error and is never persisted', async () => {
  reply = options => options.url.endsWith('/server')
    ? { statusCode: 200, data: { name: 'PrintBridge', apiVersion: 'v1' } }
    : { statusCode: 401, data: { error: { code: 'invalid_pairing_code', message: 'untrusted details' } } };
  await assert.rejects(createConnection(origin, pairingCode), {
    code: 'invalid_pairing_code', message: '打印码授权已失效，请联系部署者更新',
  });
  assert.equal(storage.size, 0);
});

test('printer listing passes authorization and preserves empty queues', async () => {
  storeSession();
  reply = () => ({ statusCode: 200, data: { printers: [] } });
  assert.deepEqual(await listPrinters(), []);
  assert.equal(requests[0].url, `${origin}/api/v1/printers`);
  assert.equal(requests[0].header.Authorization, `Bearer ${token}`);
});

test('CUPS failures and network failures do not discard a valid connection', async () => {
  storeSession();
  reply = () => ({ statusCode: 503, data: { error: { code: 'cups_unavailable' } } });
  await assert.rejects(listPrinters(), { code: 'cups_unavailable' });
  assert.ok(getConnection());
  reply = () => ({ fail: true });
  await assert.rejects(listPrinters(), { code: 'network_error' });
  assert.ok(getConnection());
});

test('expired local sessions do not cause network requests', async () => {
  storeSession({ expiresAt: '2000-01-01T00:00:00Z' });
  await assert.rejects(listPrinters(), { code: 'session_expired' });
  assert.equal(requests.length, 0);
  assert.equal(storage.size, 0);
});

test('server expiry removes only the session used by that request', async () => {
  storeSession();
  reply = () => ({ statusCode: 401, data: { error: { code: 'session_expired' } } });
  await assert.rejects(listPrinters(), { code: 'session_expired' });
  assert.equal(getConnection(), null);
  storeSession();
  const pending = authorizedRequest('/api/v1/printers');
  storeSession({ token: 'b'.repeat(43) });
  await assert.rejects(pending, { code: 'session_expired' });
  assert.equal(getConnection().token, 'b'.repeat(43));
});

test('disconnect revokes remotely, then removes the local token', async () => {
  storeSession();
  reply = () => ({ statusCode: 204, data: '' });
  await disconnect();
  assert.equal(requests[0].method, 'DELETE');
  assert.equal(requests[0].url, `${origin}/api/v1/session`);
  assert.equal(storage.size, 0);
});

test('disconnect does not claim revocation when the server cannot be reached', async () => {
  storeSession();
  reply = () => ({ fail: true });
  await assert.rejects(disconnect(), { code: 'network_error' });
  assert.ok(getConnection());
});

test('malformed printer capabilities and duplicate identifiers are rejected', () => {
  for (const value of [
    null, {}, { printers: [null] }, { printers: [{ id: 'a' }] },
    { printers: [{ ...printer(), state: 'invented' }] },
    { printers: [{ ...printer(), capabilities: { ...printer().capabilities, media: [1] } }] },
    { printers: [{ ...printer(), capabilities: { ...printer().capabilities, copies: { min: 2, max: 1 } } }] },
    { printers: [printer(), printer()] },
  ]) assert.throws(() => parsePrinters(value), { code: 'invalid_response' });
});

test('unknown capabilities remain unknown and queue warnings are visible', () => {
  const row = presentPrinter(printer());
  assert.equal(row.stateLabel, '未报告');
  assert.equal(row.sidesLabel, '未报告');
  assert.equal(row.colorLabel, '未报告');
  const warning = presentPrinter({ ...printer(), stateReasons: ['none', 'media-empty-warning'] });
  assert.equal(warning.reasonsLabel, '队列上次报告：缺纸');
});

test('physical USB presence and queue pause are reported independently', () => {
  const stopped = { ...printer(), state: 'stopped', stateReasons: ['paused'],
    stateMessage: 'Unplugged or turned off' };
  const offline = presentPrinter({ ...stopped, deviceState: 'disconnected' });
  assert.equal(offline.stateLabel, '设备未连接');
  assert.match(offline.reasonsLabel, /关机|连接/);
  assert.equal(offline.queueLabel, '队列已暂停');
  const connected = presentPrinter({ ...stopped, deviceState: 'connected' });
  assert.equal(connected.stateLabel, '队列已暂停');
  assert.doesNotMatch(connected.reasonsLabel, /关机|拔线/);
  assert.equal(connected.deviceLabel, 'USB 已连接');
  const unknown = presentPrinter(stopped);
  assert.equal(unknown.stateLabel, '队列已暂停');
  assert.match(unknown.reasonsLabel, /队列.*报告|上次/);
  assert.equal(unknown.deviceLabel, '未检测');
  const idle = presentPrinter({ ...printer(), state: 'idle', deviceState: 'disconnected' });
  assert.equal(idle.stateLabel, '设备未连接', 'idle CUPS state must not override absent USB');
});
