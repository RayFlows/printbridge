const { test, beforeEach } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');

const root = path.resolve(__dirname, '..');

beforeEach(() => {
  global.getApp = () => ({ takeEntry: () => null, pendingEntry: null });
});

function loadPage(name, options = {}) {
  let definition;
  global.Page = page => { definition = page; };
  const file = path.join(root, '.test-dist', 'pages', name, 'index.js');
  delete require.cache[require.resolve(file)];
  require(file);
  const page = {
    ...definition,
    data: structuredClone(definition.data),
    setData(values) { Object.assign(this.data, values); },
  };
  page.loaded = page.onLoad?.(options);
  return page;
}

test('registered pages resolve TDesign components inside the mini-program runtime root', () => {
  const app = JSON.parse(fs.readFileSync(path.join(root, 'src', 'app.json'), 'utf8'));
  for (const page of app.pages) {
    for (const extension of ['ts', 'json', 'wxml', 'wxss']) {
      assert.ok(fs.existsSync(path.join(root, 'src', `${page}.${extension}`)), `${page}.${extension}`);
    }
    const config = JSON.parse(fs.readFileSync(path.join(root, 'src', `${page}.json`), 'utf8'));
    const markup = fs.readFileSync(path.join(root, 'src', `${page}.wxml`), 'utf8');
    for (const [tag, reference] of Object.entries(config.usingComponents)) {
      assert.ok(markup.includes(`<${tag}`));
      const local = reference.startsWith('/');
      for (const extension of ['json', local ? 'ts' : 'js', 'wxml', 'wxss']) {
        const runtimeFile = local ? path.join(root, 'src', `${reference.slice(1)}.${extension}`) :
          path.join(root, 'src', 'miniprogram_npm', `${reference}.${extension}`);
        assert.ok(fs.existsSync(runtimeFile), `Missing runtime component: ${reference}.${extension}; run npm run setup`);
      }
    }
  }
});

function entryFixture() {
  const storage = new Map();
  const requests = [];
  const routes = [];
  const privacyCalls = [];
  const entry = {
    type: 'printbridge', version: 2, server: 'http://printbridge.test:8080',
    pairingCode: 'test-only-pairing-code-0123456789012345',
  };
  global.wx = {
    getStorageSync: key => storage.get(key),
    setStorageSync: (key, value) => storage.set(key, value),
    removeStorageSync: key => storage.delete(key),
    redirectTo: options => routes.push(options.url),
    switchTab: options => routes.push(options.url),
    reLaunch: options => routes.push(options.url),
    requirePrivacyAuthorize: options => { privacyCalls.push(options); options.success({}); },
    scanCode: options => options.success({ result: JSON.stringify(entry) }),
    request: options => {
      requests.push(options);
      options.success({
        statusCode: options.method === 'POST' ? 201 : 200,
        data: options.url.endsWith('/server')
          ? { name: 'PrintBridge', apiVersion: 'v1' }
          : { token: 'a'.repeat(43), expiresAt: new Date(Date.now() + 3600000).toISOString() },
      });
    },
  };
  return { storage, requests, routes, entry, privacyCalls };
}

test('scanning uses native privacy consent without another login page or exposing QR credentials', async () => {
  const { requests, routes, storage, entry, privacyCalls } = entryFixture();
  wx.requirePrivacyAuthorize = options => privacyCalls.push(options);
  const page = loadPage('connect');
  const pending = page.onScan();
  await new Promise(setImmediate);
  assert.equal(requests.length, 0);
  assert.equal(privacyCalls.length, 1);
  assert.equal(page.data.hasTarget, true);
  assert.deepEqual(routes, []);
  privacyCalls[0].success({});
  await pending;
  assert.equal(requests.length, 2);
  assert.deepEqual(requests[1].data, { pairingCode: entry.pairingCode });
  assert.deepEqual(routes, ['/pages/printers/index']);
  assert.ok(!JSON.stringify(page.data).includes(entry.pairingCode));
  assert.ok(!JSON.stringify([...storage.values()]).includes(entry.pairingCode));
  assert.equal(page.data.scanning, false);
  const markup = fs.readFileSync(path.join(root, 'src/pages/connect/index.wxml'), 'utf8');
  assert.doesNotMatch(markup, /<t-input|bind:tap="onConnect"|onLogin|登录并使用打印机|<t-popup|agreePrivacyAuthorization/);
  assert.match(markup, /<t-button wx:if="{{!hasTarget}}" id="scan"/);
  assert.doesNotMatch(markup, /隐私保护指引|onPrivacy/);
});

test('a WeChat entry directly requests native consent without asking for another scan or login tap', async () => {
  const { entry, requests, routes, privacyCalls } = entryFixture();
  wx.scanCode = () => assert.fail('An entry code must not require a second scan');
  wx.requirePrivacyAuthorize = options => privacyCalls.push(options);
  const page = loadPage('connect', { entry: encodeURIComponent(JSON.stringify(entry)) });
  assert.equal(requests.length, 0);
  assert.equal(privacyCalls.length, 1);
  assert.deepEqual(routes, []);
  privacyCalls[0].success({});
  await page.loaded;
  assert.equal(requests.length, 2);
  assert.deepEqual(routes, ['/pages/printers/index']);
});

test('a cached printer cannot override a new QR while authorization is in progress', async () => {
  const { entry, requests, routes, storage } = entryFixture();
  storage.set('printbridge.connection.v1', {
    address: 'http://previous.test', token: 'b'.repeat(43),
    expiresAt: new Date(Date.now() + 3600000).toISOString(),
  });
  let finish;
  const request = wx.request;
  wx.request = options => {
    if (options.method === 'POST') finish = () => request(options);
    else request(options);
  };
  const page = loadPage('connect', { entry: JSON.stringify(entry) });
  await page.onShow();
  assert.deepEqual(routes, []);
  await new Promise(setImmediate);
  finish();
  await page.loaded;
  assert.equal(requests[1].url, `${entry.server}/api/v1/sessions`);
  assert.equal(storage.get('printbridge.connection.v1').address, entry.server);
  assert.deepEqual(routes, ['/pages/printers/index']);
  assert.equal(requests.at(-1).url, 'http://previous.test/api/v1/session');
  assert.equal(requests.at(-1).method, 'DELETE');
});

test('an invalid entry stays on its error instead of opening a cached printer', async () => {
  const { storage, routes, requests } = entryFixture();
  storage.set('printbridge.connection.v1', {
    address: 'http://previous.test', token: 'b'.repeat(43),
    expiresAt: new Date(Date.now() + 3600000).toISOString(),
  });
  const page = loadPage('connect', { entry: '%' });
  await page.loaded;
  await page.onShow();
  assert.match(page.data.error, /打印码/);
  assert.equal(page.data.canRetry, false);
  assert.equal(requests.length, 0);
  assert.deepEqual(routes, []);
});

test('retry uses the scanned target again without asking for another scan', async () => {
  const { requests, routes } = entryFixture();
  const request = wx.request;
  wx.request = options => options.fail({ errMsg: 'request:fail secret diagnostic' });
  const page = loadPage('connect');
  await page.onScan();
  assert.match(page.data.error, /网络/);
  assert.equal(page.data.canRetry, true);
  wx.scanCode = () => assert.fail('Retry must not scan again');
  wx.request = request;
  await page.onRetry();
  assert.equal(requests.length, 2);
  assert.deepEqual(routes, ['/pages/printers/index']);
  assert.equal(page.data.error, '');
  assert.equal(page.target, null);
});

test('scan cancellation and camera failure do not request authorization', async () => {
  const { requests, routes } = entryFixture();
  wx.scanCode = options => options.fail({ errMsg: 'scanCode:fail cancel' });
  const page = loadPage('connect');
  await page.onScan();
  assert.equal(page.data.error, '');
  assert.equal(page.data.scanning, false);
  wx.scanCode = options => options.fail({ errMsg: 'scanCode:fail denied' });
  await page.onScan();
  assert.match(page.data.error, /扫码未完成/);
  assert.equal(page.data.scanning, false);
  assert.equal(requests.length, 0);
  assert.deepEqual(routes, []);
});

test('cancelling a change returns to the previous authorized printer', async () => {
  const { storage, routes, requests } = entryFixture();
  const previous = {
    address: 'http://previous.test', token: 'b'.repeat(43),
    expiresAt: new Date(Date.now() + 3600000).toISOString(),
  };
  storage.set('printbridge.connection.v1', previous);
  wx.scanCode = options => options.fail({ errMsg: 'scanCode:fail cancel' });
  const page = loadPage('connect', { scan: '1' });
  await page.loaded;
  assert.equal(requests.length, 0);
  assert.deepEqual(storage.get('printbridge.connection.v1'), previous);
  assert.deepEqual(routes, ['/pages/printers/index']);
});

test('repeated scan taps open the camera only once', async () => {
  entryFixture();
  let scans = 0;
  let finish;
  wx.scanCode = options => {
    scans++;
    finish = () => options.fail({ errMsg: 'scanCode:fail cancel' });
  };
  const page = loadPage('connect');
  const first = page.onScan();
  await page.onScan();
  assert.equal(scans, 1);
  finish();
  await first;
});

test('leaving during authorization revokes the late session without saving or navigating', async () => {
  const { entry, storage, requests, routes } = entryFixture();
  const request = wx.request;
  let finish;
  wx.request = options => {
    if (options.method === 'POST') finish = () => request(options);
    else request(options);
  };
  const page = loadPage('connect', { entry: JSON.stringify(entry) });
  await new Promise(setImmediate);
  page.onUnload();
  finish();
  await page.loaded;
  assert.equal(storage.size, 0);
  assert.deepEqual(routes, []);
  assert.equal(requests.at(-1).method, 'DELETE');
  assert.equal(page.target, null);
});

test('a late response from an earlier scan cannot overwrite a newer target', async () => {
  const { entry, storage, requests, routes } = entryFixture();
  const request = wx.request;
  let finish;
  wx.request = options => {
    if (options.url === `${entry.server}/api/v1/sessions`) finish = () => request(options);
    else request(options);
  };
  const page = loadPage('connect', { entry: JSON.stringify(entry) });
  await new Promise(setImmediate);
  const newer = { ...entry, server: 'http://newer.test:8080' };
  await page.openQuery({ entry: JSON.stringify(newer) });
  finish();
  await page.loaded;
  assert.equal(storage.get('printbridge.connection.v1').address, newer.server);
  assert.deepEqual(routes, ['/pages/printers/index']);
  assert.equal(requests.at(-1).url, `${entry.server}/api/v1/session`);
  assert.equal(requests.at(-1).method, 'DELETE');
});

test('an old camera failure cannot replace the result of a newer external entry', async () => {
  const { entry, routes, storage } = entryFixture();
  let finish;
  wx.scanCode = options => { finish = () => options.fail({ errMsg: 'scanCode:fail denied' }); };
  const page = loadPage('connect');
  const pending = page.onScan();
  await page.openQuery({ entry: JSON.stringify(entry) });
  finish();
  await pending;
  assert.equal(page.data.error, '');
  assert.equal(page.data.scanning, false);
  assert.equal(storage.get('printbridge.connection.v1').address, entry.server);
  assert.deepEqual(routes, ['/pages/printers/index']);
});

test('failure to revoke the previous printer does not prevent entering the new printer', async () => {
  const { entry, storage, routes } = entryFixture();
  storage.set('printbridge.connection.v1', {
    address: 'http://previous.test', token: 'b'.repeat(43),
    expiresAt: new Date(Date.now() + 3600000).toISOString(),
  });
  const request = wx.request;
  wx.request = options => options.method === 'DELETE'
    ? options.fail({ errMsg: 'request:fail offline' })
    : request(options);
  const page = loadPage('connect', { entry: JSON.stringify(entry) });
  await page.loaded;
  assert.deepEqual(routes, ['/pages/printers/index']);
  assert.equal(page.data.error, '');
  assert.equal(storage.get('printbridge.connection.v1').address, entry.server);
});

test('warm entry switches printers; cold entry and ordinary resume do not duplicate authorization', async () => {
  const { entry, routes, requests } = entryFixture();
  let app;
  global.App = definition => { app = definition; };
  global.getApp = () => app;
  let pages = [];
  global.getCurrentPages = () => pages;
  const file = path.join(root, '.test-dist', 'app.js');
  delete require.cache[require.resolve(file)];
  require(file);
  const cold = { path: 'pages/connect/index', query: {
    entry: encodeURIComponent(JSON.stringify(entry)), scancode_time: '1',
  } };
  app.onShow(cold);
  const page = loadPage('connect', cold.query);
  await page.loaded;
  await page.onShow();
  assert.equal(requests.length, 2);
  assert.equal(app.pendingEntry, null);
  app.onShow(cold);
  assert.equal(app.pendingEntry, null);
  pages = [{ route: 'pages/printers/index' }];
  const warm = { path: cold.path, query: {
    entry: encodeURIComponent(JSON.stringify({ ...entry, server: 'http://newer.test' })), scancode_time: '2',
  } };
  app.onShow(warm);
  assert.equal(routes.at(-1), '/pages/connect/index');
  const secondPage = loadPage('connect');
  await secondPage.loaded;
  await secondPage.onShow();
  assert.equal(requests.length, 5);
  assert.equal(requests[2].url, 'http://newer.test/api/v1/server');
  assert.equal(requests[4].method, 'DELETE');
  app.onShow(warm);
  assert.equal(app.pendingEntry, null);
  assert.equal(routes.length, 3);
  app.onShow({ ...warm, query: { ...warm.query, scancode_time: '3' } });
  assert.ok(app.pendingEntry, 'Scanning the same code again is a new entry event');
});

test('privacy refusal leaves the target retryable and never sends login or session requests', async () => {
  const { entry, requests, routes } = entryFixture();
  wx.requirePrivacyAuthorize = options => options.fail({ errno: 104, errMsg: 'private platform diagnostic' });
  const page = loadPage('connect', { entry: JSON.stringify(entry) });
  await page.loaded;
  assert.match(page.data.error, /隐私/);
  assert.doesNotMatch(page.data.error, /diagnostic/);
  assert.equal(page.data.busy, false);
  assert.equal(page.data.canRetry, true);
  assert.equal(page.data.hasTarget, true);
  assert.equal(requests.length, 0);
  assert.deepEqual(routes, []);
  wx.requirePrivacyAuthorize = options => options.success({});
  await page.onRetry();
  assert.equal(requests.length, 2);
  assert.deepEqual(routes, ['/pages/printers/index']);
});

test('retry taps during native consent do not duplicate the dialog or session exchange', async () => {
  const { entry, requests } = entryFixture();
  let finish, consents = 0;
  wx.requirePrivacyAuthorize = options => { consents++; finish = () => options.success({}); };
  const page = loadPage('connect', { entry: JSON.stringify(entry) });
  await page.onRetry();
  assert.equal(consents, 1);
  assert.equal(requests.length, 0);
  finish();
  await page.loaded;
  assert.equal(requests.filter(r => r.method === 'POST').length, 1);
});

test('leaving or receiving another QR during privacy consent cannot log into the old target', async () => {
  const { entry, requests, routes } = entryFixture();
  const finishes = [];
  wx.requirePrivacyAuthorize = options => { finishes.push(() => options.success({})); };
  const page = loadPage('connect', { entry: JSON.stringify(entry) });
  const newer = { ...entry, server: 'http://newer.test' };
  const second = page.openQuery({ entry: JSON.stringify(newer) });
  finishes.shift()();
  await page.loaded;
  assert.equal(requests.length, 0);
  assert.deepEqual(page.target, newer);
  assert.equal(page.data.busy, true);
  page.onUnload();
  finishes.shift()();
  await second;
  assert.equal(requests.length, 0);
  assert.deepEqual(routes, []);
});

test('unsupported privacy APIs cannot bypass consent', async () => {
  const { entry, requests } = entryFixture();
  delete wx.requirePrivacyAuthorize;
  const page = loadPage('connect', { entry: JSON.stringify(entry) });
  await page.loaded;
  assert.match(page.data.error, /升级微信/);
  assert.equal(requests.length, 0);
});

test('ordinary reentry restores a valid session without logging in again', async () => {
  const { storage, requests, routes, privacyCalls } = entryFixture();
  storage.set('printbridge.connection.v1', { address: 'http://previous.test', token: 'b'.repeat(43),
    expiresAt: new Date(Date.now() + 3600000).toISOString() });
  const page = loadPage('connect');
  await page.loaded;
  await page.onShow();
  assert.deepEqual(routes, ['/pages/printers/index']);
  assert.equal(requests.length, 0);
  assert.equal(privacyCalls.length, 0);
});

test('multi-network entry checks native consent before reading Wi-Fi and authorizes only the matching origin', async () => {
  const { entry, requests, storage } = entryFixture();
  const target = { ...entry, server: 'http://192.168.40.8:8080', alternateServers: ['http://172.22.60.1:8080'] };
  let consent, addressCalls = 0;
  wx.requirePrivacyAuthorize = options => { consent = () => options.success({}); };
  wx.getLocalIPAddress = options => {
    addressCalls++;
    options.success({ localip: '172.22.60.100', netmask: '255.255.255.0' });
  };
  const page = loadPage('connect', { entry: JSON.stringify(target) });
  assert.equal(addressCalls, 0);
  assert.equal(requests.length, 0);
  consent();
  await page.loaded;
  assert.equal(addressCalls, 1);
  assert.equal(requests.length, 2);
  assert.ok(requests.every(request => request.url.startsWith(`${target.alternateServers[0]}/`)));
  assert.equal(storage.get('printbridge.connection.v1').address, target.alternateServers[0]);
  assert.ok(!JSON.stringify([...storage.values()]).includes(entry.pairingCode));
});

test('unmatched Wi-Fi preserves the prior session and retry resolves the new network without rescanning', async () => {
  const { entry, requests, storage } = entryFixture();
  const previous = { address: 'http://previous.test', token: 'b'.repeat(43),
    expiresAt: new Date(Date.now() + 3600000).toISOString() };
  storage.set('printbridge.connection.v1', previous);
  const target = { ...entry, server: 'http://192.168.40.8:8080', alternateServers: ['http://172.22.60.1:8080'] };
  wx.getLocalIPAddress = options => options.success({ localip: '192.168.70.100', netmask: '255.255.255.0' });
  const page = loadPage('connect', { entry: JSON.stringify(target) });
  await page.loaded;
  assert.equal(requests.length, 0);
  assert.deepEqual(storage.get('printbridge.connection.v1'), previous);
  assert.match(page.data.error, /Wi-Fi/);
  assert.equal(page.data.canRetry, true);
  wx.getLocalIPAddress = options => options.success({ localip: '192.168.40.100', netmask: '255.255.255.0' });
  await page.onRetry();
  assert.equal(storage.get('printbridge.connection.v1').address, target.server);
  assert.equal(requests.filter(r => r.method === 'POST').length, 1);
});

test('leaving or scanning again while Wi-Fi lookup is pending never authorizes the stale origin', async () => {
  const { entry, requests, storage, routes } = entryFixture();
  const target = { ...entry, server: 'http://192.168.40.8:8080', alternateServers: ['http://172.22.60.1:8080'] };
  const lookups = [];
  wx.getLocalIPAddress = options => lookups.push(options);
  const page = loadPage('connect', { entry: JSON.stringify(target) });
  await new Promise(setImmediate);
  const second = page.openQuery({ entry: JSON.stringify(target) });
  await new Promise(setImmediate);
  lookups[0].success({ localip: '192.168.40.100', netmask: '255.255.255.0' });
  await page.loaded;
  assert.equal(requests.length, 0);
  page.onUnload();
  lookups[1].success({ localip: '172.22.60.100', netmask: '255.255.255.0' });
  await second;
  assert.equal(requests.length, 0);
  assert.equal(storage.size, 0);
  assert.deepEqual(routes, []);
});

test('printer page distinguishes empty and failed lists, and supports retry', async () => {
  let fail = false;
  let pullStopped = 0;
  global.wx = {
    getStorageSync: () => ({
      address: 'http://printbridge.test:8080',
      token: 'a'.repeat(43),
      expiresAt: new Date(Date.now() + 3600000).toISOString(),
    }),
    request: options => fail
      ? options.success({ statusCode: 503, data: { error: { code: 'cups_unavailable' } } })
      : options.success({ statusCode: 200, data: options.url.includes('/jobs') ? { jobs: [] } : { printers: [] } }),
    stopPullDownRefresh: () => { pullStopped++; },
  };
  const page = loadPage('printers');
  await page.refresh();
  assert.equal(page.data.status, 'empty');
  fail = true;
  await page.refresh();
  assert.equal(page.data.status, 'error');
  assert.match(page.data.error, /CUPS/);
  assert.deepEqual(page.data.printers, []);
  fail = false;
  await page.refresh();
  assert.equal(page.data.status, 'empty');
  assert.equal(page.data.error, '');
  assert.equal(pullStopped, 3);
});

test('unloaded pages ignore pending list responses', async () => {
  const pendingRequests = [];
  global.wx = {
    getStorageSync: () => ({
      address: 'http://printbridge.test:8080', token: 'a'.repeat(43),
      expiresAt: new Date(Date.now() + 3600000).toISOString(),
    }),
    request: options => { pendingRequests.push(options); },
    stopPullDownRefresh: () => {},
  };
  const page = loadPage('printers');
  const pending = page.refresh();
  page.onUnload();
  for (const options of pendingRequests) options.success({ statusCode: 200,
    data: options.url.includes('/jobs') ? { jobs: [] } : { printers: [] } });
  await pending;
  assert.equal(page.data.status, 'loading');
});

test('printer presence refresh stops on hide and shows a visible error on failed polling', async (t) => {
  t.mock.timers.enable({ apis: ['setTimeout'] });
  let calls = 0;
  let fail = false;
  global.wx = {
    getStorageSync: () => ({ address: 'http://printbridge.test:8080', token: 'a'.repeat(43),
      expiresAt: new Date(Date.now() + 3600000).toISOString() }),
    stopPullDownRefresh() {},
    request: options => {
      calls++;
      if (fail) options.fail({ errMsg: 'unreachable' });
      else options.success({ statusCode: 200, data: options.url.includes('/jobs') ? { jobs: [] } : { printers: [] } });
    },
  };
  const p = loadPage('printers');
  p.onShow();
  await new Promise(resolve => setImmediate(resolve));
  assert.equal(calls, 2);
  fail = true;
  t.mock.timers.tick(5000);
  await new Promise(resolve => setImmediate(resolve));
  assert.equal(calls, 4);
  assert.match(p.data.statusError, /状态更新失败/);
  p.onHide();
  t.mock.timers.tick(10000);
  assert.equal(calls, 4);
  fail = false;
  p.onShow();
  await new Promise(resolve => setImmediate(resolve));
  assert.equal(calls, 6);
  assert.equal(p.data.statusError, '');
  p.onUnload();
  t.mock.timers.tick(10000);
  assert.equal(calls, 6);
});
