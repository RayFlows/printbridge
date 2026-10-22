const { test, beforeEach } = require('node:test');
const assert = require('node:assert/strict');

const printer = {
  id: 'virtual', name: 'Virtual', model: '', location: '', state: 'processing',
  acceptingJobs: true, stateReasons: ['media-empty-error'], deviceState: 'connected',
  capabilities: { media: [], sides: [], colorModes: [], copies: null,
    defaultMedia: '', defaultSides: '', defaultColorMode: '' },
};
const job = {
  id: 'a'.repeat(32), planId: 'b'.repeat(32), name: 'fixture.pdf',
  settings: { printerId: printer.id }, sheetCount: 3,
  state: 'printing', phase: 'single', cupsJobId: 42, cupsJobIds: [42],
  problem: '', createdAt: new Date().toISOString(), updatedAt: new Date().toISOString(),
};
let reports, currentJob, modals, requests, failStatus, pendingStatus;
beforeEach(() => {
  reports = [structuredClone(printer)]; currentJob = structuredClone(job);
  modals = []; requests = []; failStatus = false; pendingStatus = null;
  const storage = new Map([['printbridge.connection.v1', { address: 'http://virtual.test',
    token: 'a'.repeat(43), expiresAt: new Date(Date.now() + 3600000).toISOString() }]]);
  global.getApp = () => ({ pendingEntry: null });
  global.wx = {
    getStorageSync: k => storage.get(k), removeStorageSync: k => storage.delete(k),
    request(options) {
      requests.push(options);
      if (options.url.endsWith('/printers')) {
        if (pendingStatus) { pendingStatus(options); return; }
        if (failStatus) { options.fail({ errMsg: 'status unavailable' }); return; }
        options.success({ statusCode: 200, data: { printers: reports } });
      } else options.success({ statusCode: 200, data: currentJob });
    },
    showModal(options) { modals.push(options); options.success?.({ confirm: true }); },
    reLaunch() {}, redirectTo() {},
  };
});
const flush = () => new Promise(resolve => setImmediate(resolve));
function page() {
  let definition;
  global.Page = p => { definition = p; };
  const path = require.resolve('../.test-dist/pages/jobs/index.js');
  delete require.cache[path]; require(path);
  const p = { ...definition, data: structuredClone(definition.data),
    setData(v) { Object.assign(this.data, v); }, schedule() {} };
  p.onLoad({ id: job.id });
  p.visible = true;
  return p;
}

test('one shortage popup per incident, with recovery and no print mutation', async () => {
  const p = page();
  await p.refresh();
  assert.equal(modals.length, 1);
  assert.match(modals[0].title, /缺纸/);
  assert.equal(modals[0].showCancel, false);
  await p.refresh();
  failStatus = true;
  await p.refresh();
  assert.ok(p.data.error);
  assert.equal(p.data.job.id, job.id);
  failStatus = false;
  await p.refresh();
  assert.equal(modals.length, 1, 'failed lookup must not reset the incident');
  reports[0].stateReasons = ['none'];
  await p.refresh();
  assert.equal(p.data.error, '');
  reports[0].stateReasons = ['media-empty-warning'];
  await p.refresh();
  assert.equal(modals.length, 2);
  assert.ok(requests.every(r => r.method === 'GET'));
  p.onUnload();
});

test('other printers, disconnected devices and non-printing tasks do not alert', async () => {
  for (const kind of ['other', 'disconnected', 'completed', 'awaiting_flip', 'queued']) {
    reports = [structuredClone(printer)]; currentJob = structuredClone(job);
    if (kind === 'other') reports[0].id = 'other';
    else if (kind === 'disconnected') reports[0].deviceState = 'disconnected';
    else currentJob.state = kind;
    const p = page();
    await p.refresh();
    assert.equal(modals.length, 0, kind);
    p.onUnload();
  }
});

test('hidden, unloaded and stale visibility responses do not open a popup', async () => {
  for (const action of ['onHide', 'onUnload', 'reopen']) {
    modals = [];
    const p = page();
    let reply;
    pendingStatus = options => { reply = options; };
    const fetching = p.refresh();
    await flush();
    assert.ok(reply);
    if (action === 'reopen') { p.onHide(); p.onShow(); } else p[action]();
    reply.success({ statusCode: 200, data: { printers: reports } });
    await fetching;
    assert.equal(modals.length, 0);
    pendingStatus = null;
    if (action === 'reopen') {
      await p.refresh();
      assert.equal(modals.length, 1);
    }
    p.onUnload();
  }
});

test('unknown and disconnected observations do not rearm the alert', async () => {
  const p = page();
  await p.refresh();
  reports[0] = { ...printer, state: 'unknown', stateReasons: [] };
  await p.refresh();
  reports[0] = { ...printer, deviceState: 'disconnected', stateReasons: [] };
  await p.refresh();
  reports[0] = { ...printer };
  await p.refresh();
  assert.equal(modals.length, 1);
  p.onUnload();
});

test('an open paper alert cannot overlap cancellation and modal failure can retry', async () => {
  const p = page();
  wx.showModal = options => { modals.push(options); };
  await p.refresh();
  await p.onCancel();
  await p.refresh();
  assert.equal(modals.length, 1);
  assert.ok(requests.every(r => r.method === 'GET'));
  modals[0].fail({ errMsg: 'modal failed' });
  await p.refresh();
  assert.equal(modals.length, 2);
  p.onUnload();
});

test('the old permanent paper-out help is removed', () => {
  const fs = require('node:fs');
  const wxml = fs.readFileSync(require('node:path').join(__dirname, '../src/pages/jobs/index.wxml'), 'utf8');
  assert.doesNotMatch(wxml, /如果中途缺纸|paper-help/);
});

test('a CUPS job still waiting behind another job never gets its paper-out popup', async () => {
  currentJob.cupsJobState = 0;
  const p = page();
  await p.refresh();
  assert.equal(modals.length, 0, 'release has not yet confirmed this job is processing');
  currentJob.cupsJobState = 3;
  await p.refresh();
  assert.equal(modals.length, 0);
  assert.match(p.data.printerNotice, /缺纸.*等待/);
  currentJob.cupsJobState = 5;
  await p.refresh();
  assert.equal(modals.length, 1);
  p.onUnload();
});

test('queued tasks show shared cancellation and flip barriers without requesting a print action', async () => {
  for (const state of ['canceling', 'awaiting_flip']) {
    currentJob.state = 'queued';
    reports[0] = { ...printer, stateReasons: [], activity: { state, waitingJobs: 2 } };
    const p = page();
    await p.refresh();
    assert.match(p.data.printerNotice, state === 'canceling' ? /清理/ : /翻纸/);
    assert.match(p.data.printerNotice, /等待/);
    assert.equal(modals.length, 0);
    assert.ok(requests.every(request => request.method === 'GET'));
    p.onUnload();
  }
});
