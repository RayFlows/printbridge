const { test, beforeEach } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const connection = require('../.test-dist/services/connection.js');
const root = path.resolve(__dirname, '..');
const origin = 'https://printbridge.test';
const pairingCode = 'test-only-pairing-code-0123456789012345';
const token = 'a'.repeat(43);
const user = () => ({
  id: 'b'.repeat(32), name: '', namePrompted: false, banned: false, isAdmin: false,
  createdAt: new Date().toISOString(), lastSeenAt: new Date().toISOString(),
});
let storage, requests, routes, reply, loginCalls, modal;
function profile(overrides = {}) {
  return { user: { ...user(), ...overrides }, settings: { printingEnabled: true } };
}
function save(userValue = user()) {
  connection.saveConnection({ address: origin, token, user: userValue, expiresAt: new Date(Date.now() + 3600000).toISOString() });
}
function page(name) {
  let definition;
  global.Page = p => { definition = p; };
  const file = path.join(root, '.test-dist/pages', name, 'index.js');
  delete require.cache[require.resolve(file)];
  require(file);
  const p = { ...definition, data: structuredClone(definition.data),
    setData(value) { Object.assign(this.data, value); } };
  p.onLoad?.({});
  return p;
}
beforeEach(() => {
  storage = new Map(); requests = []; routes = []; loginCalls = 0;
  reply = options => {
    if (options.url.endsWith('/server')) return { name: 'PrintBridge', apiVersion: 'v1', loginMode: 'wechat' };
    if (options.url.endsWith('/sessions')) return { token, expiresAt: new Date(Date.now() + 3600000).toISOString(), user: user() };
    if (options.url.endsWith('/me')) return profile({ namePrompted: options.method === 'POST', name: options.data?.name || '' });
    if (options.url.includes('/jobs')) return { jobs: [] };
    if (options.url.endsWith('/printers')) return { printers: [] };
    return {};
  };
  modal = options => options.success({ confirm: false, cancel: true, content: '' });
  global.getApp = () => ({ pendingEntry: null, takeEntry: () => null });
  global.wx = {
    getStorageSync: key => storage.get(key),
    setStorageSync: (key, value) => storage.set(key, value),
    removeStorageSync: key => storage.delete(key),
    login: options => { loginCalls++; options.success({ code: `fresh-login-${loginCalls}` }); },
    requirePrivacyAuthorize: options => options.success({}),
    request: options => {
      requests.push(options);
      const value = reply(options);
      if (value?.error) options.success({ statusCode: 403, data: value });
      else options.success({ statusCode: 200, data: value });
    },
    showModal: options => modal(options),
    switchTab: options => routes.push(options.url),
    reLaunch: options => routes.push(options.url),
    navigateTo: options => routes.push(options.url),
    stopPullDownRefresh() {},
  };
});

test('WeChat login sends a one-use code, not client-supplied identity; clearing cache preserves the server user', async () => {
  const first = await connection.createConnection(origin, pairingCode);
  connection.saveConnection(first);
  storage.clear();
  const second = await connection.createConnection(origin, pairingCode);
  assert.equal(first.user.id, second.user.id);
  assert.equal(loginCalls, 2);
  assert.deepEqual(requests[1].data, { pairingCode, loginCode: 'fresh-login-1' });
  assert.deepEqual(requests[3].data, { pairingCode, loginCode: 'fresh-login-2' });
  assert.ok(!('openid' in requests[3].data));
  assert.ok(!('userId' in requests[3].data));
});

test('failed wx.login never falls back to code-only pairing', async () => {
  wx.login = options => options.fail({ errMsg: 'private internal error' });
  await assert.rejects(connection.createConnection(origin, pairingCode), { code: 'wechat_login_failed' });
  assert.equal(requests.length, 1);
  wx.login = options => options.success({ code: '' });
  await assert.rejects(connection.createConnection(origin, pairingCode), { code: 'wechat_login_failed' });
  assert.equal(requests.length, 2);
});

test('verified-mode session must have a complete user and cannot downgrade to legacy', async () => {
  const original = reply;
  reply = options => options.url.endsWith('/sessions') ?
    { token, expiresAt: new Date(Date.now() + 3600000).toISOString() } : original(options);
  await assert.rejects(connection.createConnection(origin, pairingCode), { code: 'invalid_response' });
  assert.equal(storage.size, 0);
});

test('first printer visit reads the profile without a name popup or writing a skipped name', async () => {
  save();
  modal = () => assert.fail('Optional name must not open a popup');
  const p = page('printers');
  p.visible = true;
  await p.identify();
  assert.equal(p.profileKnown, true);
  assert.equal(p.data.printingEnabled, true);
  assert.equal(requests.length, 1);
  assert.equal(requests[0].method, 'GET');
  assert.equal(connection.getConnection().user.namePrompted, false);
});

test('WeChat QR entry waits for native privacy agreement before calling wx.login', async () => {
  const p = page('connect');
  let consent;
  wx.requirePrivacyAuthorize = options => { consent = options; };
  const pending = p.openQuery({ entry: JSON.stringify({ type: 'printbridge', version: 2, server: origin, pairingCode }) });
  assert.equal(loginCalls, 0);
  assert.equal(requests.length, 0);
  consent.fail({ errno: 104 });
  await pending;
  assert.equal(loginCalls, 0);
  assert.equal(requests.length, 0);
  wx.requirePrivacyAuthorize = options => options.success({});
  await p.onRetry();
  assert.equal(loginCalls, 1);
  assert.equal(requests.filter(r => r.url.endsWith('/sessions')).length, 1);
});

test('a name typed on an old account cannot be saved to another login', async () => {
  save();
  const p = page('mine');
  await p.refresh();
  p.onNameInput({ detail: { value: '旧用户' } });
  save({ ...user(), id: 'c'.repeat(32) });
  await p.onSaveName();
  assert.equal(requests.filter(r => r.method === 'POST').length, 0);
});

test('My page edits and clears the optional name through the account service', async () => {
  save();
  modal = () => assert.fail('Name editing must be inline');
  const p = page('mine');
  await p.refresh();
  p.onNameInput({ detail: { value: '测试姓名' } });
  assert.equal(requests.length, 1, 'Typing must not save automatically');
  assert.equal(p.data.nameChanged, true);
  await p.onSaveName();
  assert.equal(p.data.profile.user.name, '测试姓名');
  assert.equal(connection.getConnection().user.name, '测试姓名');
  assert.equal(p.data.nameDraft, '测试姓名');
  assert.equal(p.data.nameChanged, false);
  await p.onSaveName();
  assert.equal(requests.length, 2, 'Unchanged name must not be submitted again');
  p.onNameInput({ detail: { value: '' } });
  await p.onSaveName();
  assert.equal(p.data.profile.user.name, '');
  assert.deepEqual(requests.at(-1).data, { name: '' });
  assert.equal(p.data.nameChanged, false);
});

test('My privacy entry opens the native contract without account requests or replacing account errors', async () => {
  const p = page('mine');
  p.setData({ error: '账户暂时不可用' });
  let opened = 0;
  const toasts = [];
  wx.showToast = options => toasts.push(options.title);
  wx.openPrivacyContract = options => { opened++; options.success({}); };
  await p.onPrivacy();
  assert.equal(opened, 1);
  wx.openPrivacyContract = options => options.fail({ errMsg: 'private diagnostic' });
  await p.onPrivacy();
  assert.match(toasts.at(-1), /无法打开/);
  assert.doesNotMatch(toasts.at(-1), /diagnostic/);
  delete wx.openPrivacyContract;
  await p.onPrivacy();
  assert.match(toasts.at(-1), /升级微信/);
  assert.equal(p.data.error, '账户暂时不可用');
  assert.equal(requests.length, 0);
  assert.equal(loginCalls, 0);
  assert.equal(storage.size, 0);
});

test('a late privacy-contract failure cannot show a toast after leaving My', async () => {
  const p = page('mine');
  let fail;
  wx.openPrivacyContract = options => { fail = options.fail; };
  wx.showToast = () => assert.fail('Hidden page must not show a stale privacy error');
  const pending = p.onPrivacy();
  p.onHide();
  fail({});
  await pending;
});

test('failed name save preserves the draft for retry and does not replace the saved profile', async () => {
  save();
  const p = page('mine');
  await p.refresh();
  p.onNameInput({ detail: { value: '重试姓名' } });
  const request = wx.request;
  wx.request = options => options.fail({ errMsg: 'private diagnostic' });
  await p.onSaveName();
  assert.equal(p.data.savingName, false);
  assert.equal(p.data.nameChanged, true);
  assert.equal(p.data.nameDraft, '重试姓名');
  assert.equal(p.data.profile.user.name, '');
  assert.equal(connection.getConnection().user.name, '');
  assert.match(p.data.nameError, /暂时无法访问/);
  assert.equal(p.data.error, '');
  wx.request = request;
  await p.onSaveName();
  assert.equal(p.data.profile.user.name, '重试姓名');
  assert.equal(p.data.nameError, '');
});

test('duplicate saves, refresh taps and edits cannot race an in-flight name save', async () => {
  save();
  const p = page('mine');
  await p.refresh();
  let finish, writes = 0;
  wx.request = options => {
    writes++;
    finish = () => options.success({ statusCode: 200, data: profile({ name: options.data.name, namePrompted: true }) });
  };
  p.onNameInput({ detail: { value: '测试姓名' } });
  const pending = p.onSaveName();
  await p.onSaveName();
  await p.onRefresh();
  p.onNameInput({ detail: { value: '过期输入' } });
  assert.equal(p.data.nameDraft, '测试姓名');
  assert.equal(writes, 1);
  finish();
  await pending;
  assert.equal(p.data.savingName, false);
  assert.equal(p.data.profile.user.name, '测试姓名');
});

test('a late name response cannot overwrite another account or hidden page', async () => {
  save();
  const p = page('mine');
  await p.refresh();
  let finish;
  wx.request = options => {
    finish = () => options.success({ statusCode: 200, data: profile({ name: '旧姓名', isAdmin: true }) });
  };
  p.onNameInput({ detail: { value: '旧姓名' } });
  const pending = p.onSaveName();
  p.onHide();
  save({ ...user(), id: 'c'.repeat(32), name: '另一个人' });
  finish();
  await pending;
  assert.equal(p.data.profile.user.name, '');
  assert.equal(connection.getConnection().user.name, '另一个人');
  assert.equal(connection.getConnection().user.isAdmin, false);
  assert.deepEqual(routes, []);
});

test('Print and My keep the current connection and only Print offers QR switching', async () => {
  save();
  for (const name of ['printers', 'mine']) {
    const p = page(name);
    assert.equal(p.onDisconnect, undefined);
    assert.equal(p.onForget, undefined);
    const markup = fs.readFileSync(path.join(root, 'src/pages', name, 'index.wxml'), 'utf8');
    assert.doesNotMatch(markup, /logout|onDisconnect|onForget|退出/);
  }
  const mine = page('mine');
  await mine.refresh();
  assert.equal(connection.getConnection().token, token);
  assert.equal(requests.some(request => request.method === 'DELETE'), false);
  page('printers').onScan();
  assert.equal(routes.at(-1), '/pages/connect/index?scan=1');
  assert.equal(connection.getConnection().token, token);
});

test('a server-confirmed administrator can open management from My', async () => {
  save();
  const original = reply;
  reply = options => options.url.endsWith('/me') ? profile({ isAdmin: true }) : original(options);
  const p = page('mine');
  await p.refresh();
  assert.equal(p.data.profile.user.isAdmin, true);
  assert.equal(connection.getConnection().user.isAdmin, true);
  p.onAdmin();
  assert.deepEqual(routes, ['/pages/admin/index']);
});

test('cached administrator flag cannot grant access to management', async () => {
  save({ ...user(), isAdmin: true });
  const p = page('mine');
  await p.refresh();
  p.onAdmin();
  assert.deepEqual(routes, []);
  const admin = page('admin');
  await admin.refresh();
  assert.equal(admin.data.allowed, false);
  assert.match(admin.data.error, /管理员/);
  assert.equal(requests.filter(r => r.url.includes('/admin/')).length, 0);
});

test('banned accounts see the denial and do not enter a rescan loop', async () => {
  save();
  reply = () => ({ error: { code: 'user_banned' } });
  const p = page('mine');
  await p.refresh();
  assert.equal(p.data.profile, null);
  assert.match(p.data.error, /停用/);
  assert.deepEqual(routes, []);
  assert.ok(connection.getConnection());
});

test('a hidden My page cannot display stale privileges from a late request', async () => {
  save();
  let finish;
  wx.request = options => { finish = () => options.success({ statusCode: 200, data: profile({ isAdmin: true }) }); };
  const p = page('mine');
  const pending = p.refresh();
  p.onHide();
  finish();
  await pending;
  assert.equal(p.data.profile, null);
});

test('management loads overview, filters users, confirms a ban and saves print admission', async () => {
  save();
  let banned = false, enabled = true;
  const visitor = { ...user(), id: 'd'.repeat(32), name: '访客', namePrompted: true };
  reply = options => {
    if (options.url.endsWith('/me')) return profile({ isAdmin: true });
    if (options.url.endsWith('/dashboard')) return { printers: [], tasks: [], settings: { printingEnabled: enabled } };
    if (options.url.endsWith('/users')) return { users: [{ ...user(), isAdmin: true }, { ...visitor, banned }] };
    if (options.url.endsWith('/ban')) { banned = options.data.banned; return { ...visitor, banned }; }
    if (options.url.endsWith('/settings')) { enabled = options.data.printingEnabled; return { printingEnabled: enabled }; }
    return {};
  };
  const p = page('admin');
  p.visible = true;
  await p.refresh();
  assert.equal(p.data.allowed, true);
  assert.equal(p.data.userCount, 2);
  p.onTab({ detail: { value: 'users' } });
  p.onTab({ detail: { value: '访客' } });
  assert.equal(p.data.tab, 'users', 'Nested input changes must not select a different tab');
  p.onSearch({ detail: { value: '访客' } });
  assert.equal(p.data.filteredUsers.length, 1);
  assert.equal(p.data.filteredUsers[0].shortId, visitor.id.slice(-8));
  p.onSearch({ detail: { value: visitor.id } });
  assert.equal(p.data.filteredUsers[0].id, visitor.id);
  p.onSearch({ detail: { value: '', trigger: 'clear' } });
  assert.equal(p.data.filteredUsers.length, 2);
  assert.equal(p.data.tab, 'users');
  modal = options => options.success({ confirm: false });
  await p.onBan({ currentTarget: { dataset: { id: visitor.id } } });
  assert.equal(banned, false);
  modal = options => options.success({ confirm: true });
  await p.onBan({ currentTarget: { dataset: { id: visitor.id } } });
  assert.equal(banned, true);
  assert.equal(p.data.bannedCount, 1);
  await p.onPrinting({ detail: { value: false } });
  assert.equal(enabled, false);
  assert.equal(p.data.printingEnabled, false);
  p.onUnload();
});

test('administrator polls device cleanup without asserting a physical stop', async () => {
  save();
  let problem = 'printer_busy';
  const task = { id: 'c'.repeat(32), userId: 'd'.repeat(32), userName: '访客', printerId: 'virtual',
    state: 'canceling', sheetCount: 1, createdAt: user().createdAt, deviceCancelRequired: true };
  reply = options => {
    if (options.url.endsWith('/me')) return profile({ isAdmin: true });
    if (options.url.endsWith('/dashboard')) return { printers: [], tasks: problem ? [{ ...task, problem }] : [], settings: { printingEnabled: true } };
    if (options.url.endsWith('/users')) return { users: [] };
    if (options.url.endsWith('/confirm-cancel')) return { id: task.id, state: 'canceled' };
    return {};
  };
  const p = page('admin');
  p.visible = true;
  await p.refresh();
  assert.equal(requests.some(r => r.url.endsWith('/confirm-cancel')), false);
  problem = 'device_cancel_required';
  await p.refresh();
  assert.equal(requests.some(r => r.url.endsWith('/confirm-cancel')), false);
  assert.equal(p.data.tasks[0].state, 'canceling');
  problem = '';
  await p.refresh();
  assert.equal(p.data.tasks.length, 0);
  const confirmations = requests.filter(r => r.url.endsWith('/confirm-cancel'));
  assert.equal(confirmations.length, 0);
  p.onUnload();
});

test('custom tabs retain Print and My routes, and navigation to Print uses switchTab', () => {
  const app = JSON.parse(fs.readFileSync(path.join(root, 'src/app.json'), 'utf8'));
  assert.deepEqual(app.tabBar.list.map(item => item.text), ['打印', '我的']);
  assert.equal(app.tabBar.custom, true);
  const jobs = page('jobs');
  jobs.onHome();
  assert.equal(routes.at(-1), '/pages/printers/index');
});

test('printer telemetry failure does not hide user management or misreport an empty printer list', async () => {
  save();
  reply = options => {
    if (options.url.endsWith('/me')) return profile({ isAdmin: true });
    if (options.url.endsWith('/dashboard')) return {
      printers: [], printerStatusAvailable: false, tasks: [], settings: { printingEnabled: true },
    };
    return { users: [{ ...user(), isAdmin: true }] };
  };
  const p = page('admin');
  await p.refresh();
  assert.equal(p.data.allowed, true);
  assert.equal(p.data.printerStatusAvailable, false);
  assert.equal(p.data.users.length, 1);
});
