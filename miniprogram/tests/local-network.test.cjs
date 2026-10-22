const { test } = require('node:test');
const assert = require('node:assert/strict');
const { selectLocalServer, resolveLocalServer } = require('../.test-dist/services/local-network.js');
const { entryFromQR, entryFromPath, resolveEntryServer } = require('../.test-dist/services/entry.js');

const entry = {
  type: 'printbridge', version: 2, server: 'http://192.168.40.8:8080',
  alternateServers: ['http://172.22.60.1:8080'],
  pairingCode: 'test-only-pairing-code-0123456789012345',
};

test('a multi-network entry preserves its bounded explicit origins through QR and path parsing', () => {
  assert.deepEqual(entryFromQR(JSON.stringify(entry)), entry);
  assert.deepEqual(entryFromPath(`pages/connect/index?entry=${encodeURIComponent(JSON.stringify(entry))}`), entry);
});

test('multi-network codes reject unsafe origins, duplicates, malformed lists and unknown fields', () => {
  for (const changes of [
    { alternateServers: null }, { alternateServers: [] }, { alternateServers: 'http://172.22.60.1' },
    { alternateServers: [23] }, { alternateServers: Array(4).fill(entry.alternateServers[0]) },
    { alternateServers: [entry.server] }, { alternateServers: ['https://printer.test'] },
    { alternateServers: ['http://127.0.0.1'] }, { alternateServers: ['http://169.254.4.2'] },
    { alternateServers: ['http://8.8.8.8'] }, { alternateServers: ['http://172.32.60.1'] },
    { alternateServers: ['http://172.022.60.1'] }, { alternateServers: ['http://192.168.60.256'] },
    { alternateServers: ['http://[fd00::1]'] }, { server: 'https://printer.test' },
    { server: 'http://192.168.40.8:80', alternateServers: ['http://192.168.40.8'] },
    { alternateServers: ['http://172.22.60.1/secret'] }, { unknown: true },
  ]) assert.throws(() => entryFromQR(JSON.stringify({ ...entry, ...changes })), { code: 'invalid_qr' });
});

test('subnet selection uses the actual netmask rather than a guessed /24 or trial requests', () => {
  const servers = [entry.server, ...entry.alternateServers];
  assert.equal(selectLocalServer(servers, '192.168.40.123', '255.255.255.0'), entry.server);
  assert.equal(selectLocalServer(servers, '172.22.61.123', '255.255.254.0'), entry.alternateServers[0]);
  assert.throws(() => selectLocalServer(servers, '172.22.61.123', '255.255.255.0'),
    { code: 'printer_network_required' });
  assert.equal(selectLocalServer(['http://10.88.9.1'], '10.88.9.200', '255.255.255.0'), 'http://10.88.9.1');
});

test('missing, invalid and noncontiguous masks cannot authorize a network origin', () => {
  for (const mask of [undefined, '', '0.0.0.0', '255.0.255.0', '255.255.255.256', '255.255.255.01']) {
    assert.throws(() => selectLocalServer([entry.server], '192.168.40.123', mask),
      { code: 'local_network_unavailable' });
  }
  for (const local of ['', '127.1', '192.168.40.999']) {
    assert.throws(() => selectLocalServer([entry.server], local, '255.255.255.0'),
      { code: 'local_network_unavailable' });
  }
});

test('self, broadcast, subnet addresses and ambiguous matching origins fail closed', () => {
  for (const server of ['http://192.168.40.123', 'http://192.168.40.0', 'http://192.168.40.255']) {
    assert.throws(() => selectLocalServer([server], '192.168.40.123', '255.255.255.0'),
      { code: 'printer_network_required' });
  }
  assert.throws(() => selectLocalServer([entry.server, 'http://192.168.40.9:8080'],
    '192.168.40.123', '255.255.255.0'), { code: 'ambiguous_network' });
});

test('the native address API selects only the matching origin; legacy codes need no new API', async () => {
  global.wx = {
    getLocalIPAddress: options => options.success({ localip: '172.22.60.100', netmask: '255.255.255.0' }),
  };
  assert.equal(await resolveEntryServer(entry), entry.alternateServers[0]);
  delete wx.getLocalIPAddress;
  const { alternateServers: _, ...legacy } = entry;
  assert.equal(await resolveEntryServer(legacy), legacy.server);
  await assert.rejects(resolveEntryServer(entry), { code: 'local_network_unavailable' });
});

test('network API failures, malformed responses, exceptions and timeout expose no platform diagnostics', async () => {
  const savedTimeout = global.setTimeout;
  const savedClear = global.clearTimeout;
  try {
    let expire, clears = 0;
    global.setTimeout = (callback, delay) => { assert.equal(delay, 8000); expire = callback; return 1; };
    global.clearTimeout = () => { clears++; };
    for (const action of [
      options => options.fail({ errMsg: 'sensitive platform diagnostic' }),
      options => options.success({ localip: '172.22.60.100' }),
      () => { throw new Error('sensitive platform diagnostic'); },
    ]) {
      global.wx = { getLocalIPAddress: action };
      await assert.rejects(resolveLocalServer([entry.server]), error =>
        error.code === 'local_network_unavailable' && !error.message.includes('diagnostic'));
    }
    assert.equal(clears, 3);
    global.wx = { getLocalIPAddress: () => {} };
    const pending = resolveLocalServer([entry.server]);
    expire();
    await assert.rejects(pending, { code: 'local_network_unavailable' });
  } finally {
    global.setTimeout = savedTimeout;
    global.clearTimeout = savedClear;
  }
});
