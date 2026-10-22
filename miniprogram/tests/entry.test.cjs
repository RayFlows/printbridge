const { test } = require('node:test');
const assert = require('node:assert/strict');
const { entryFromQR, entryFromPath, entryFromQuery, scanPrintEntry } = require('../.test-dist/services/entry.js');

const entry = {
  type: 'printbridge', version: 2, server: 'http://printbridge.test:8080',
  pairingCode: 'test-only-special-code-%25+&=abcdef',
};
const payload = JSON.stringify(entry);
const entryPath = `pages/connect/index?entry=${encodeURIComponent(payload)}`;

test('encoded and decoded WeChat query parameters preserve authorization characters exactly', () => {
  assert.deepEqual(entryFromQuery({ entry: payload }), entry);
  assert.deepEqual(entryFromQuery({ entry: encodeURIComponent(payload) }), entry);
  assert.deepEqual(entryFromPath(entryPath), entry);
  assert.deepEqual(entryFromPath(`/${entryPath}&scancode_time=123`), entry);
});

test('invalid, oversized, legacy and unsupported short scene codes fail explicitly', () => {
  for (const query of [{}, { entry: 123 }, { entry: '%' }, { entry: 'x'.repeat(2049) }, { scene: 'short-id' }]) {
    assert.throws(() => entryFromQuery(query), { code: 'invalid_qr' });
  }
  for (const path of [
    'pages/another/index?entry=x', 'pages/connect/index', `${entryPath}&entry=x`,
    `${entryPath}&token=x`, `${entryPath}&broken`, 'x'.repeat(2049),
  ]) assert.throws(() => entryFromPath(path), { code: 'invalid_qr' });
  assert.throws(() => entryFromQR(JSON.stringify({
    type: 'printbridge', version: 1, server: entry.server,
  })), { code: 'outdated_qr' });
  for (const code of ['', 'x'.repeat(31), 'x'.repeat(129), `${'x'.repeat(32)} `]) {
    assert.throws(() => entryFromQR(JSON.stringify({ ...entry, pairingCode: code })), { code: 'invalid_qr' });
  }
  assert.throws(() => entryFromQR('x'.repeat(2049)), { code: 'invalid_qr' });
});

test('scan accepts a current mini-program code path as well as a local development QR', async () => {
  global.wx = {
    scanCode: options => {
      assert.deepEqual(options.scanType, ['qrCode', 'wxCode']);
      options.success({ path: entryPath, result: 'WeChat opaque result' });
    },
  };
  assert.deepEqual(await scanPrintEntry(), entry);
  wx.scanCode = options => options.success({ result: payload });
  assert.deepEqual(await scanPrintEntry(), entry);
  wx.scanCode = options => options.success({ path: 'pages/other/index?entry=x', result: payload });
  await assert.rejects(scanPrintEntry(), { code: 'invalid_qr' });
});
