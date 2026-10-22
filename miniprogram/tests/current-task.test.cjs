const { test, beforeEach } = require('node:test');
const assert = require('node:assert/strict');
const { defaultSettings } = require('../.test-dist/services/printing.js');

const printer = {
  id: 'virtual', name: 'Virtual printer', model: 'Virtual device', location: 'Test room', state: 'idle',
  acceptingJobs: true, deviceState: 'connected', stateReasons: [],
  capabilities: { media: ['a4'], sides: ['one-sided'], colorModes: ['monochrome'],
    copies: { min: 1, max: 99 }, defaultMedia: 'a4', defaultColorMode: 'monochrome', defaultSides: 'one-sided' },
};
const makeJob = changes => ({ id: 'a'.repeat(32), planId: 'b'.repeat(32), name: 'document.pdf',
  settings: defaultSettings(printer), sheetCount: 8, state: 'awaiting_flip', phase: 'back',
  cupsJobId: 1, cupsJobIds: [1], problem: '', createdAt: new Date().toISOString(),
  updatedAt: new Date().toISOString(), ...changes });
let jobs, requests, routes, error;

function page(name, options = {}) {
  let definition;
  global.Page = value => { definition = value; };
  const file = require.resolve(`../.test-dist/pages/${name}/index.js`);
  delete require.cache[file];
  require(file);
  const instance = { ...definition, data: structuredClone(definition.data),
    setData(value) { Object.assign(this.data, value); } };
  instance.onLoad(options);
  return instance;
}
const tap = () => ({ currentTarget: { dataset: { id: printer.id } } });

beforeEach(() => {
  jobs = []; requests = []; routes = []; error = false;
  global.getApp = () => ({ pendingEntry: null });
  global.wx = {
    getStorageSync: () => ({ address: 'http://printbridge.test', token: 'a'.repeat(43),
      expiresAt: new Date(Date.now() + 3600000).toISOString() }),
    stopPullDownRefresh() {},
    request: options => {
      requests.push(options);
      const isJobs = options.url.includes('/jobs');
      queueMicrotask(() => options.success(isJobs && error
        ? { statusCode: 503, data: { error: { code: 'cups_unavailable' } } }
        : { statusCode: 200, data: isJobs ? { jobs } : { printers: [printer] } }));
    },
    navigateTo: options => routes.push(options.url),
    redirectTo: options => routes.push(options.url),
    switchTab: options => routes.push(options.url),
    reLaunch: options => routes.push(options.url),
  };
});

test('printer card resumes its unfinished task; completion restores file selection', async () => {
  jobs = [makeJob()];
  const p = page('printers');
  await p.refresh();
  assert.equal(p.data.printers[0].activeJob.label, '等待翻纸');
  assert.equal(p.data.printers[0].activeJob.actionLabel, '继续任务');
  assert.equal(p.data.printers[0].activeJob.name, jobs[0].name);
  p.onSelect(tap());
  assert.equal(routes.at(-1), `/pages/jobs/index?id=${jobs[0].id}`);
  assert.ok(requests.some(r => r.url.endsWith('/jobs?active=true')));
  jobs = [makeJob({ state: 'completed' })];
  await p.refresh();
  assert.equal(p.data.printers[0].activeJob, null);
  p.onSelect(tap());
  assert.match(routes.at(-1), /^\/pages\/document\/index\?/);
  assert.ok(requests.every(r => r.method === 'GET'));
  p.onUnload();
});

test('canceling task opens progress and never offers an action that resumes printing', async () => {
  jobs = [makeJob({ state: 'canceling', deviceCancelRequired: true })];
  const p = page('printers');
  await p.refresh();
  assert.equal(p.data.printers[0].activeJob.actionLabel, '查看进度');
  p.onSelect(tap());
  assert.equal(routes.at(-1), `/pages/jobs/index?id=${jobs[0].id}`);
  assert.ok(requests.every(request => request.method === 'GET'));
  p.onUnload();
});

test('legacy backlog resumes oldest job on the selected printer, without history navigation', async () => {
  const oldest = makeJob({ id: 'c'.repeat(32), createdAt: new Date(Date.now() - 60000).toISOString() });
  jobs = [makeJob(), makeJob({ settings: { ...defaultSettings(printer), printerId: 'other' } }), oldest];
  const p = page('printers');
  await p.refresh();
  assert.equal(p.data.printers[0].activeJob.id, oldest.id);
  p.onSelect(tap());
  assert.equal(routes.at(-1), `/pages/jobs/index?id=${oldest.id}`);
  p.onUnload();
});

test('unknown task state prevents starting another upload; cached task remains recoverable', async () => {
  const p = page('printers');
  await p.refresh();
  error = true;
  await p.refresh(true);
  assert.equal(p.data.tasksKnown, false);
  p.onSelect(tap());
  assert.equal(routes.length, 0);
  assert.match(p.data.statusError, /状态更新失败/);
  error = false; jobs = [makeJob()];
  await p.refresh();
  error = true;
  await p.refresh(true);
  p.onSelect(tap());
  assert.equal(routes.at(-1), `/pages/jobs/index?id=${jobs[0].id}`);
  p.onUnload();
});

test('printer info opens a separate page, reads current capabilities and reports a missing queue', async () => {
  const home = page('printers');
  home.onInfo(tap());
  assert.equal(routes[0], '/pages/printer-info/index?printer=virtual');
  const info = page('printer-info', { printer: printer.id });
  await info.refresh();
  assert.equal(info.data.printer.model, printer.model);
  assert.equal(info.data.printer.deviceLabel, 'USB 已连接');
  assert.equal(info.data.printer.sidesLabel, '单面');
  assert.ok(requests.every(r => r.url.endsWith('/printers')));
  info.printerId = 'removed';
  await info.refresh();
  assert.equal(info.data.printer, null);
  assert.match(info.data.error, /不可用/);
  info.onUnload(); home.onUnload();
});

test('late printer info responses cannot update an unloaded page', async () => {
  let pending;
  wx.request = options => { pending = options; };
  const info = page('printer-info', { printer: printer.id });
  const refreshing = info.refresh();
  info.onUnload();
  info.setData = () => assert.fail('unloaded page changed');
  pending.success({ statusCode: 200, data: { printers: [printer] } });
  await refreshing;
});

test('old job-list entry returns to printers without requesting history', async () => {
  const p = page('jobs');
  await p.refresh();
  assert.deepEqual(routes, ['/pages/printers/index']);
  assert.equal(requests.length, 0);
  p.onUnload();
});
