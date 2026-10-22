const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs/promises');
const os = require('node:os');
const path = require('node:path');
const { buildCodeRequest, createPrinterCode } = require('../scripts/create-printer-code.cjs');
const { entryFromPath } = require('../.test-dist/services/entry.js');

const options = {
  server: 'http://printbridge.test:8080',
  pairingCode: 'test-only-pairing-code-%25&+abcdef',
  accessToken: 'synthetic-access-token',
  environment: 'trial',
};
// A synthetic pixel validates the API transport and file handling, not QR recognition.
const png = Buffer.from(
  'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+aXs8AAAAASUVORK5CYII=',
  'base64',
);

async function outputDirectory(t) {
  const directory = await fs.mkdtemp(path.join(os.tmpdir(), 'printbridge-code-'));
  t.after(async () => {
    assert.equal(path.dirname(await fs.realpath(directory)), await fs.realpath(os.tmpdir()));
    assert.ok(path.basename(directory).startsWith('printbridge-code-'));
    await fs.rm(directory, { recursive: true, force: true });
  });
  return directory;
}

test('the official code request round trips through the real client entry parser', () => {
  const request = buildCodeRequest(options);
  assert.equal(request.env_version, 'trial');
  assert.ok(request.path.length <= 1024);
  assert.deepEqual(entryFromPath(request.path), {
    type: 'printbridge', version: 2, server: options.server, pairingCode: options.pairingCode,
  });
  assert.equal(buildCodeRequest({ ...options, environment: undefined }).env_version, 'release');
});

test('one print code can carry private main and guest origins without changing legacy entries', () => {
  const input = { ...options, server: 'http://192.168.40.8:8080', alternateServers: ['http://172.22.60.1:8080'] };
  assert.deepEqual(entryFromPath(buildCodeRequest(input).path), {
    type: 'printbridge', version: 2, server: input.server,
    alternateServers: input.alternateServers, pairingCode: input.pairingCode,
  });
  for (const changes of [
    { alternateServers: [] }, { alternateServers: null }, { alternateServers: [input.server] },
    { alternateServers: [1] }, { alternateServers: ['http://8.8.8.8'] },
    { alternateServers: ['http://127.1'] }, { alternateServers: ['http://172.022.60.1'] },
    { alternateServers: Array(4).fill(input.alternateServers[0]) }, { server: 'https://printer.test' },
  ]) assert.throws(() => buildCodeRequest({ ...input, ...changes }));
});

test('invalid deployment values fail before calling WeChat', async () => {
  for (const changes of [
    { server: undefined }, { server: 'file:///tmp/code' }, { server: 'http://user:pass@printer.test' },
    { server: 'https://printer.test/path' }, { server: 'https://printer.test?secret=x' },
    { server: 'https://printer.test#secret' }, { pairingCode: '' }, { environment: 'production' },
    { server: 'http://a_b.test' }, { server: 'http://a..test' }, { server: 'http://printer.test.' },
  ]) assert.throws(() => buildCodeRequest({ ...options, ...changes }));
  await assert.rejects(createPrinterCode({ ...options, accessToken: '' }, () => {
    assert.fail('Missing credentials must not call WeChat');
  }), /WECHAT_ACCESS_TOKEN/);
  assert.throws(() => buildCodeRequest({
    ...options, server: `https://${Array(20).fill('a'.repeat(60)).join('.')}.test`,
  }), /1024/);
});

test('code generation sends the official request and saves the returned image without overwriting', async t => {
  const directory = await outputDirectory(t);
  const input = { ...options, outputBase: path.join(directory, 'private', 'printer') };
  const fetchImage = async (url, request) => {
    const endpoint = new URL(url);
    assert.equal(endpoint.origin, 'https://api.weixin.qq.com');
    assert.equal(endpoint.pathname, '/wxa/getwxacode');
    assert.equal(endpoint.searchParams.get('access_token'), options.accessToken);
    assert.equal(request.method, 'POST');
    assert.equal(request.redirect, 'error');
    assert.deepEqual(JSON.parse(request.body), buildCodeRequest(options));
    return new Response(png, { headers: { 'Content-Type': 'image/png' } });
  };
  const output = await createPrinterCode(input, fetchImage);
  assert.equal(output, `${input.outputBase}.png`);
  assert.deepEqual(await fs.readFile(output), png);
  await assert.rejects(createPrinterCode(input, fetchImage), /already exists/);
  assert.deepEqual(await fs.readFile(output), png);
});

test('JPEG responses receive a matching extension', async t => {
  const directory = await outputDirectory(t);
  const jpeg = Buffer.from([0xff, 0xd8, 0xff, 0xdb, 0xff, 0xd9]);
  const output = await createPrinterCode({ ...options, outputBase: path.join(directory, 'printer') },
    async () => new Response(jpeg, { headers: { 'Content-Type': 'image/jpeg' } }));
  assert.equal(path.extname(output), '.jpg');
  assert.deepEqual(await fs.readFile(output), jpeg);
});

test('API errors and transport details cannot leak credentials or create a fake code image', async t => {
  const directory = await outputDirectory(t);
  const secret = `${options.pairingCode} ${options.accessToken}`;
  for (const fetchImage of [
    async () => new Response(JSON.stringify({ errcode: 40001, errmsg: secret })),
    async () => new Response(secret, { status: 500 }),
    async () => new Response(secret),
    async () => { throw new Error(`network error at ${secret}`); },
    async () => new Response(png, { headers: { 'Content-Length': '3000000' } }),
    async () => new Response(Buffer.alloc(2 * 1024 * 1024 + 1)),
  ]) {
    await assert.rejects(createPrinterCode({ ...options, outputBase: path.join(directory, 'printer') }, fetchImage), error => {
      assert.ok(!error.message.includes(options.pairingCode));
      assert.ok(!error.message.includes(options.accessToken));
      return true;
    });
    assert.deepEqual(await fs.readdir(directory), []);
  }
});
