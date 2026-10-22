const { test, beforeEach } = require('node:test');
const assert = require('node:assert/strict');
const print = require('../.test-dist/services/printing.js');

const printer = {
  id: 'virtual', name: 'Virtual printer', model: '', location: '', state: 'idle',
  acceptingJobs: true, stateReasons: [],
  manualDuplexProfile: 'rear-face-up-v1',
  capabilities: { media: ['iso_a4_210x297mm'], sides: ['one-sided'], colorModes: ['monochrome'],
    copies: { min: 1, max: 99 }, defaultMedia: 'iso_a4_210x297mm', defaultColorMode: 'monochrome', defaultSides: 'one-sided' },
};
const doc = () => ({ id: 'd'.repeat(32), name: 'sample.docx', bytes: 500, pageCount: 17,
  createdAt: new Date().toISOString(), expiresAt: new Date(Date.now() + 3600000).toISOString() });
const plan = () => ({ id: 'a'.repeat(32), documentId: doc().id, name: doc().name,
  settings: print.defaultSettings(printer), pageCount: 17, sheetCount: 17, selectedPages: [1, 2, 3],
  sheets: [{ front: [1], back: [] }], createdAt: doc().createdAt, expiresAt: doc().expiresAt });
const job = () => ({ id: 'b'.repeat(32), planId: plan().id, name: doc().name,
  settings: print.defaultSettings(printer), sheetCount: 17, state: 'queued', phase: 'single',
  cupsJobId: 0, cupsJobIds: [], problem: '', createdAt: doc().createdAt, updatedAt: doc().createdAt });
let storage, requests, routes, response, modalConfirm, modalCalls, scrolls;

beforeEach(() => {
  storage = new Map([['printbridge.connection.v1', { address: 'http://printbridge.test:8080',
    token: 'a'.repeat(43), expiresAt: new Date(Date.now() + 3600000).toISOString() }]]);
  requests = []; routes = []; scrolls = []; modalConfirm = true; modalCalls = 0;
  response = options => {
    if (options.url.endsWith('/printers')) return { printers: [printer] };
    if (options.url.includes('/documents/')) return doc();
    if (options.url.endsWith('/plans')) return plan();
    if (options.url.includes('/plans/')) return plan();
    if (options.url.endsWith('/jobs') && options.method === 'GET') return { jobs: [job()] };
    return job();
  };
  global.getApp = () => ({ pendingEntry: null });
  global.wx = {
    getStorageSync: key => storage.get(key),
    setStorageSync: (key, value) => storage.set(key, value),
    removeStorageSync: key => storage.delete(key),
    request: options => {
      requests.push(options);
      queueMicrotask(() => options.success({ statusCode: options.method === 'POST' ? 201 : 200, data: response(options) }));
    },
    downloadFile: options => {
      requests.push(options);
      const task = { abort() {} };
      queueMicrotask(() => { options.success({ statusCode: 200, tempFilePath: 'wxfile://plan.png' }); options.complete?.(); });
      return task;
    },
    getImageInfo: options => options.success({}),
    getFileSystemManager: () => ({ unlink() {} }),
    showModal: options => { modalCalls++; options.success({ confirm: modalConfirm }); },
    redirectTo: options => routes.push(options.url),
    navigateTo: options => routes.push(options.url),
    reLaunch: options => routes.push(options.url),
    previewImage: () => {},
    pageScrollTo: options => scrolls.push(options),
  };
});
async function page(name, options) {
  let definition;
  global.Page = value => { definition = value; };
  const file = require.resolve(`../.test-dist/pages/${name}/index.js`);
  delete require.cache[file]; require(file);
  const p = { ...definition, data: structuredClone(definition.data), setData(value) { Object.assign(this.data, value); } };
  await p.onLoad(options);
  return p;
}
const flush = () => new Promise(resolve => setImmediate(resolve));
async function automaticPreview(t) {
  t.mock.timers.tick(400);
  await flush();
}

test('print settings use capabilities; manual duplex remains an explicit choice', () => {
  const settings = print.defaultSettings(printer);
  assert.equal(settings.sides, 'one-sided');
  assert.equal(settings.media, 'iso_a4_210x297mm');
  assert.equal(settings.calibrated, false);
  assert.equal(settings.outputOrder, 'reverse');
  assert.equal(settings.manualFirstSide, 'back');
  assert.equal(settings.reverse, false);
  assert.equal(settings.manualDuplexProfile, 'rear-face-up-v1');
  assert.equal(print.hasManualDuplex({ ...printer, manualDuplexProfile: undefined }), false);
  assert.equal(print.hasManualDuplex({ ...printer, manualDuplexProfile: 'future-profile' }), false);
});

test('new documents prefer supported monochrome modes over the queue color default', () => {
  for (const [colorModes, defaultColorMode, expected] of [
    [['color', 'auto-monochrome', 'monochrome'], 'color', 'monochrome'],
    [['color', 'monochrome'], '', 'monochrome'],
    [['color', 'auto-monochrome'], 'color', 'auto-monochrome'],
    [[], 'monochrome', 'monochrome'],
    [['color'], 'color', 'color'],
    [['color'], '', 'color'],
    [[], '', ''],
  ]) {
    const capabilities = { ...printer.capabilities, colorModes, defaultColorMode };
    assert.equal(print.defaultSettings({ ...printer, capabilities }).colorMode, expected);
    assert.equal(capabilities.defaultColorMode, defaultColorMode);
  }
});

test('paper options prioritize common PPD and IPP sizes without adding or changing supported keywords', () => {
  for (const media of [
    ['w609h864', 'Legal', 'A6', 'B5', 'A5', 'A4', 'another-size', 'Letter', 'ISOB5'],
    ['w609h864', 'na_legal_8.5x14in', 'iso_a6_105x148mm', 'jis_b5_182x257mm', 'iso_a5_148x210mm',
      'iso_a4_210x297mm', 'another-size', 'na_letter_8.5x11in', 'iso_b5_176x250mm'],
  ]) {
    const original = [...media];
    const device = { ...printer, capabilities: { ...printer.capabilities, media, defaultMedia: media[1] } };
    const options = print.paperOptions(device);
    assert.deepEqual(options.map(option => option.value),
      [media[5], media[4], media[3], media[8], media[2], media[7], media[1], media[0], media[6]]);
    assert.deepEqual(media, original);
    assert.notEqual(options[2].label, options[3].label, 'JIS and ISO B5 have different dimensions');
  }
  const unknown = { ...printer, capabilities: { ...printer.capabilities, defaultMedia: 'vendor-default', media: ['custom-a'] } };
  assert.deepEqual(print.paperOptions(unknown).map(option => option.value), ['vendor-default', 'custom-a']);
  const empty = { ...printer, capabilities: { ...printer.capabilities, defaultMedia: '', media: [] } };
  assert.deepEqual(print.paperOptions(empty).map(option => option.value), ['']);
  assert.deepEqual(print.paperOptions(printer).map(option => option.value), ['iso_a4_210x297mm']);
});

test('sorting paper choices preserves a non-first default, sends the chosen keyword and resets correctly for a new file', async () => {
  const media = ['Legal', 'w609h864', 'A5', 'A4', 'B5'];
  const device = { ...printer, capabilities: { ...printer.capabilities, media, defaultMedia: 'Legal' } };
  const original = response;
  response = options => options.url.endsWith('/printers') ? { printers: [device] }
    : options.url.endsWith('/plans') ? { ...plan(), settings: options.data.settings } : original(options);
  const p = await page('settings', { document: doc().id, printer: printer.id });
  assert.deepEqual(p.data.media.map(option => option.value), ['A4', 'A5', 'B5', 'Legal', 'w609h864']);
  assert.equal(p.data.mediaIndex, 3);
  assert.equal(p.data.media[p.data.mediaIndex].value, p.data.settings.media);
  assert.equal(requests.findLast(r => r.url.endsWith('/plans')).data.settings.media, 'Legal');
  p.onOption({ currentTarget: { dataset: { field: 'media' } }, detail: { value: '1' } });
  await p.onPrepare();
  assert.equal(p.data.mediaIndex, 1);
  assert.equal(p.data.settings.media, 'A5');
  assert.equal(requests.findLast(r => r.url.endsWith('/plans')).data.settings.media, 'A5');
  p.useDocument({ ...doc(), id: 'e'.repeat(32) });
  await p.onPrepare();
  assert.equal(p.data.mediaIndex, 3);
  assert.equal(p.data.media[p.data.mediaIndex].value, 'Legal');
  assert.equal(requests.findLast(r => r.url.endsWith('/plans')).data.settings.media, 'Legal');
  assert.ok(!requests.some(r => r.url.endsWith('/jobs')));
  p.onUnload();
});

test('old local calibration cannot override the device profile', async () => {
  const address = storage.get('printbridge.connection.v1').address;
  storage.set('printbridge.calibration.v2', { address, printer: printer.id, order: 'reverse', rotation: 180, firstSide: 'front' });
  const p = await page('settings', { document: doc().id, printer: printer.id });
  assert.equal(p.data.settings.manualFirstSide, 'back');
  assert.equal(p.data.settings.backOrder, 'same');
  assert.equal(p.data.settings.backRotation, 0);
  assert.equal(p.data.settings.calibrated, false);
  assert.equal(p.data.settings.manualDuplexProfile, printer.manualDuplexProfile);
  p.onUnload();
});

test('a submission timeout reuses its stored idempotency key across retry', async () => {
  const request = wx.request;
  let first;
  wx.request = options => { first = options; options.fail({ errMsg: 'timeout' }); };
  await assert.rejects(print.submitPlan(plan()), { code: 'network_error' });
  wx.request = request;
  await print.submitPlan(plan());
  assert.equal(requests[0].data.idempotencyKey, first.data.idempotencyKey);
  assert.equal(requests[0].data.planId, plan().id);
  assert.match(first.data.idempotencyKey, /^[A-Za-z0-9_-]{16,128}$/);
});

test('new print responses reject incomplete data and unsafe record identifiers', async () => {
  for (const value of [null, {}, { ...job(), id: '../file' }, { ...job(), state: 'made-up' },
    { ...job(), settings: { ...job().settings, manualFirstSide: 'unknown' } }]) {
    assert.throws(() => print.parseJob(value), { code: 'invalid_response' });
  }
  for (const value of [null, {}, { ...plan(), pageCount: 0 }, { ...plan(), sheets: [{}] },
    { ...plan(), settings: { ...plan().settings, manualFirstSide: 'unknown' } },
    ...[0, -1, 1.5, 18, null, '3'].map(previewPageCount => ({ ...plan(), previewPageCount }))]) {
    assert.throws(() => print.parsePlan(value), { code: 'invalid_response' });
  }
  await assert.rejects(print.getJob('../file'), { code: 'invalid_response' });
  await assert.rejects(print.cancelJob('../file'), { code: 'invalid_response' });
  assert.equal(requests.length, 0);
});

test('reading previews use server count and never navigate to automatic duplex padding', async () => {
  const original = response;
  response = options => options.url.includes('/plans')
    ? { ...plan(), pageCount: 4, previewPageCount: 3 } : original(options);
  const p = await page('settings', { document: doc().id, printer: printer.id });
  assert.match(requests.at(-1).url, /\/pages\/1\?order=reading$/);
  await p.onNext();
  await p.onNext();
  assert.equal(p.data.page, 3);
  const before = requests.length;
  await p.onNext();
  assert.equal(requests.length, before);
  assert.equal(p.data.page, 3);
  assert.equal(p.data.sheetMap, undefined);
  assert.equal(p.onMap, undefined);
  const fs = require('node:fs');
  const markup = fs.readFileSync(require('node:path').join(__dirname, '../src/pages/settings/index.wxml'), 'utf8');
  assert.ok(!markup.includes('纸张安排'));
  assert.match(markup, /plan\.previewPageCount/);
  assert.ok(!requests.some(r => r.url.endsWith('/jobs')));
  p.onUnload();
});

test('changing settings invalidates the old preview; preparation does not create a print job', async () => {
  const p = await page('settings', { document: doc().id, printer: printer.id });
  assert.equal(p.data.document.pageCount, 17);
  assert.equal(p.data.sides.filter(s => s.value.startsWith('two-sided')).length, 0);
  p.onPages({ detail: { value: '1-5,8' } });
  await p.onPrepare();
  assert.equal(p.data.plan.id, plan().id);
  assert.equal(p.data.previewPath, 'wxfile://plan.png');
  const prepare = requests.findLast(r => r.url.endsWith('/plans'));
  assert.equal(prepare.data.settings.pages, '1-5,8');
  assert.ok(!requests.some(r => r.url.endsWith('/jobs')));
  p.onCopies({ detail: { value: '2' } });
  assert.equal(p.data.plan, null);
  assert.equal(p.data.previewPath, '');
  await p.onPrint();
  assert.equal(modalCalls, 0);
  p.onUnload();
});

test('color and monochrome changes prepare the selected mode and download the matching plan preview', async (t) => {
  t.mock.timers.enable({ apis: ['setTimeout'] });
  const original = response;
  let sequence = 0;
  response = options => {
    if (options.url.endsWith('/printers')) return { printers: [{ ...printer, capabilities: {
      ...printer.capabilities, colorModes: ['color', 'monochrome'], defaultColorMode: 'color',
    } }] };
    if (options.url.endsWith('/plans')) return { ...plan(), id: (++sequence).toString(16).padStart(32, '0'),
      settings: options.data.settings };
    return original(options);
  };
  const p = await page('settings', { document: doc().id, printer: printer.id });
  assert.equal(p.data.settings.colorMode, 'monochrome');
  assert.equal(p.data.colors[p.data.colorIndex].value, 'monochrome');
  assert.equal(requests.findLast(request => request.url.endsWith('/plans')).data.settings.colorMode, 'monochrome');
  for (const colorMode of ['color', 'monochrome', 'color']) {
    const oldId = p.data.plan.id;
    p.onOption({ currentTarget: { dataset: { field: 'colorMode' } },
      detail: { value: String(p.data.colors.findIndex(option => option.value === colorMode)) } });
    assert.equal(p.data.previewPath, '');
    await automaticPreview(t);
    assert.equal(requests.findLast(request => request.url.endsWith('/plans')).data.settings.colorMode, colorMode);
    assert.equal(p.data.plan.settings.colorMode, colorMode);
    assert.notEqual(p.data.plan.id, oldId);
    assert.ok(requests.some(request => request.url.includes(`/plans/${p.data.plan.id}/pages/1?order=reading`)));
  }
  assert.ok(!requests.some(request => request.url.endsWith('/jobs')));
  p.onUnload();
});

test('printing requires a preview, confirmation and only one submit despite double tap', async () => {
  const p = await page('settings', { document: doc().id, printer: printer.id });
  await p.onPrepare();
  modalConfirm = false;
  await p.onPrint();
  assert.ok(!requests.some(r => r.url.endsWith('/jobs')));
  modalConfirm = true;
  const pending = p.onPrint();
  await p.onPrint();
  await pending;
  assert.equal(requests.filter(r => r.url.endsWith('/jobs')).length, 1);
  assert.deepEqual(routes, [`/pages/jobs/index?id=${job().id}`]);
  p.onUnload();
});

test('manual duplex automatically previews using the configured profile without calibration confirmation', async (t) => {
  t.mock.timers.enable({ apis: ['setTimeout'] });
  const p = await page('settings', { document: doc().id, printer: printer.id });
  const prepared = requests.filter(r => r.url.endsWith('/plans')).length;
  p.onOption({ currentTarget: { dataset: { field: 'sides' } }, detail: { value: '1' } });
  assert.equal(p.data.manual, true);
  await automaticPreview(t);
  assert.equal(p.data.error, '');
  assert.equal(requests.filter(r => r.url.endsWith('/plans')).length, prepared + 1);
  assert.equal(requests.at(-2).data.settings.manualDuplexProfile, 'rear-face-up-v1');
  assert.equal(modalCalls, 0);
  assert.ok(!requests.some(r => r.url.endsWith('/jobs')));
  p.onUnload();
});

test('manual profile sends default reverse output and technical parameters have no page controls', async () => {
  const p = await page('settings', { document: doc().id, printer: printer.id });
  p.onOption({ currentTarget: { dataset: { field: 'sides' } }, detail: { value: '1' } });
  await p.onPrepare();
  const settings = requests.findLast(r => r.url.endsWith('/plans')).data.settings;
  assert.equal(settings.manualFirstSide, 'back');
  assert.equal(settings.outputOrder, 'reverse');
  assert.equal(settings.backOrder, 'same');
  const preview = p.data.plan;
  p.onOption({ currentTarget: { dataset: { field: 'manualFirstSide' } }, detail: { value: '1' } });
  p.onOption({ currentTarget: { dataset: { field: 'backOrder' } }, detail: { value: '1' } });
  assert.equal(p.data.settings.manualFirstSide, 'back');
  assert.equal(p.data.settings.backOrder, 'same');
  assert.equal(p.data.plan, preview);
  assert.ok(!requests.some(r => r.url.endsWith('/jobs')));
  p.onUnload();
});

test('unconfigured or unfamiliar devices do not inherit a reload guide or offer manual duplex', async () => {
  const original = response;
  for (const profile of [undefined, 'future-profile']) {
    response = options => options.url.endsWith('/printers')
      ? { printers: [{ ...printer, manualDuplexProfile: profile }] } : original(options);
    const p = await page('settings', { document: doc().id, printer: printer.id });
    assert.equal(p.data.sides.length, 1);
    assert.equal(p.data.settings.manualDuplexProfile, undefined);
    assert.ok(p.data.previewPath);
    p.onUnload();
  }
});

test('opening settings prepares a preview but never creates a job or asks to print', async () => {
  const p = await page('settings', { document: doc().id, printer: printer.id });
  assert.equal(requests.filter(r => r.url.endsWith('/plans')).length, 1);
  assert.ok(p.data.previewPath);
  assert.equal(modalCalls, 0);
  assert.ok(!requests.some(r => r.url.endsWith('/jobs')));
  assert.equal(scrolls.length, 0);
  assert.equal(p.data.expanded, false);
  p.onUnload();
});

test('advanced controls automatically preview exact capability keywords without scrolling or printing', async (t) => {
  t.mock.timers.enable({ apis: ['setTimeout'] });
  const rich = { ...printer, capabilities: { ...printer.capabilities, copies: { min: 1, max: 2 },
    mediaTypes: ['stationery', 'photographic-glossy'], mediaSources: ['main', 'rear'] } };
  const oldResponse = response;
  response = options => options.url.endsWith('/printers') ? { printers: [rich] } : oldResponse(options);
  const p = await page('settings', { document: doc().id, printer: printer.id });
  for (const [field, index] of [['mediaType', 2], ['mediaSource', 2], ['pagesPerSheet', 2], ['layoutOrder', 3], ['scaling', 4]]) {
    p.onOption({ currentTarget: { dataset: { field } }, detail: { value: String(index) } });
    assert.equal(p.data.plan, null);
  }
  p.onBorder({ detail: { value: true } });
  p.onScale({ detail: { value: '40' } });
  p.onCopyStep({ currentTarget: { dataset: { step: 10 } } });
  assert.equal(p.data.settings.copies, 2);
  await automaticPreview(t);
  const sent = requests.findLast(r => r.url.endsWith('/plans')).data.settings;
  assert.equal(sent.mediaType, 'photographic-glossy');
  assert.equal(sent.mediaSource, 'rear');
  assert.equal(sent.pagesPerSheet, 4);
  assert.equal(sent.layoutOrder, 'tbrl');
  assert.equal(sent.pageBorder, true);
  assert.equal(sent.scaling, 'custom');
  assert.equal(sent.scalePercent, 40);
  assert.equal(modalCalls, 0);
  assert.ok(!requests.some(r => r.url.endsWith('/jobs')));
  assert.equal(scrolls.length, 0);
  p.onOption({ currentTarget: { dataset: { field: 'pagesPerSheet' } }, detail: { value: '0' } });
  assert.equal(p.data.settings.pageBorder, false);
  p.onUnload();
});

test('rapid edits debounce into one preview while printing stays disabled', async (t) => {
  t.mock.timers.enable({ apis: ['setTimeout'] });
  const p = await page('settings', { document: doc().id, printer: printer.id });
  p.onCopies({ detail: { value: '2' } });
  t.mock.timers.tick(250);
  p.onCopies({ detail: { value: '3' } });
  p.onReverse({ detail: { value: false } });
  t.mock.timers.tick(399);
  await p.onPrint();
  assert.equal(requests.filter(r => r.url.endsWith('/plans')).length, 1);
  assert.equal(p.data.plan, null);
  assert.equal(modalCalls, 0);
  await automaticPreview(t);
  const prepares = requests.filter(r => r.url.endsWith('/plans'));
  assert.equal(prepares.length, 2);
  assert.equal(prepares[1].data.settings.copies, 3);
  assert.equal(prepares[1].data.settings.outputOrder, 'normal');
  assert.equal(prepares[1].data.replacesPlanId, plan().id);
  assert.equal(p.data.preparing, false);
  assert.ok(p.data.previewPath);
  assert.ok(!requests.some(r => r.url.endsWith('/jobs')));
  p.onUnload();
});

test('document effects automatically update the plan without submitting a job', async (t) => {
  t.mock.timers.enable({ apis: ['setTimeout'] });
  const oldResponse = response;
  response = options => options.url.includes('/documents/') ? { ...doc(), name: 'image.tiff' } : oldResponse(options);
  const p = await page('settings', { document: doc().id, printer: printer.id });
  assert.equal(p.data.imageDocument, true);
  p.onMirror({ detail: { value: true } });
  p.onInvert({ detail: { value: true } });
  p.onWatermark({ detail: { value: '内部资料 / 100%: x,y' } });
  assert.equal(p.data.plan, null);
  await automaticPreview(t);
  const sent = requests.findLast(r => r.url.endsWith('/plans')).data.settings;
  assert.equal(sent.mirror, true);
  assert.equal(sent.invertImages, true);
  assert.equal(sent.watermark, '内部资料 / 100%: x,y');
  assert.ok(!requests.some(r => r.url.endsWith('/jobs')));
  p.onUnload();
  response = oldResponse;
  const documentPage = await page('settings', { document: doc().id, printer: printer.id });
  assert.equal(documentPage.data.imageDocument, false);
  assert.equal(documentPage.data.settings.invertImages, false);
  documentPage.onUnload();
});

test('changing simplex to either duplex mode preserves the default sheet order', async (t) => {
  t.mock.timers.enable({ apis: ['setTimeout'] });
  const oldResponse = response;
  response = options => options.url.endsWith('/printers') ? { printers: [{
    ...printer, capabilities: { ...printer.capabilities, sides: ['one-sided', 'two-sided-long-edge'] },
  }] } : oldResponse(options);
  const p = await page('settings', { document: doc().id, printer: printer.id });
  for (const value of ['manual-long-edge', 'manual-short-edge', 'two-sided-long-edge', 'one-sided']) {
    p.onOption({ currentTarget: { dataset: { field: 'sides' } },
      detail: { value: String(p.data.sides.findIndex(side => side.value === value)) } });
    assert.equal(p.data.settings.outputOrder, 'reverse');
    assert.equal(p.data.settings.reverse, false);
  }
  p.onUnload();
});

test('a stale preparation cannot overwrite newer settings or start a competing request', async (t) => {
  t.mock.timers.enable({ apis: ['setTimeout'] });
  const p = await page('settings', { document: doc().id, printer: printer.id });
  const original = wx.request;
  const pending = [];
  wx.request = options => options.url.endsWith('/plans') ? pending.push(options) : original(options);
  p.onCopies({ detail: { value: '2' } });
  await automaticPreview(t);
  assert.equal(pending.length, 1);
  p.onCopies({ detail: { value: '3' } });
  await automaticPreview(t);
  p.onCopies({ detail: { value: '4' } });
  assert.equal(p.data.settings.copies, 4);
  assert.equal(p.data.busy, false);
  assert.equal(pending.length, 1);
  const staleId = 'c'.repeat(32);
  pending[0].success({ statusCode: 201, data: { ...plan(), id: staleId } });
  await flush();
  assert.equal(p.data.plan, null);
  assert.equal(p.data.previewPath, '');
  assert.equal(pending.length, 1);
  await automaticPreview(t);
  assert.equal(pending.length, 2);
  assert.equal(pending[1].data.settings.copies, 4);
  assert.equal(pending[1].data.replacesPlanId, staleId);
  pending[1].success({ statusCode: 201, data: { ...plan(), settings: pending[1].data.settings } });
  await flush();
  assert.equal(p.data.plan.settings.copies, 4);
  assert.equal(p.data.preparing, false);
  assert.ok(p.data.previewPath);
  assert.equal(modalCalls, 0);
  p.onUnload();
});

test('edits during image rendering wait for it and ignore its stale result', async (t) => {
  t.mock.timers.enable({ apis: ['setTimeout'] });
  const p = await page('settings', { document: doc().id, printer: printer.id });
  const download = wx.downloadFile;
  let old;
  wx.downloadFile = options => { old = options; return { abort() {} }; };
  const turning = p.onNext();
  p.onCopies({ detail: { value: '2' } });
  await automaticPreview(t);
  assert.equal(requests.filter(r => r.url.endsWith('/plans')).length, 1);
  assert.equal(p.data.plan, null);
  wx.downloadFile = download;
  old.success({ statusCode: 200, tempFilePath: 'wxfile://obsolete.png' });
  await turning;
  await flush();
  assert.equal(requests.filter(r => r.url.endsWith('/plans')).length, 2);
  assert.equal(p.data.previewPath, 'wxfile://plan.png');
  assert.equal(p.data.preparing, false);
  p.onUnload();
});

test('obsolete preparation failures stay silent and newest settings still update', async (t) => {
  t.mock.timers.enable({ apis: ['setTimeout'] });
  const p = await page('settings', { document: doc().id, printer: printer.id });
  const request = wx.request;
  let old;
  wx.request = options => { old = options; };
  p.onCopies({ detail: { value: '2' } });
  await automaticPreview(t);
  p.onCopies({ detail: { value: '3' } });
  await automaticPreview(t);
  wx.request = request;
  old.fail({ errMsg: 'timeout' });
  await flush();
  assert.equal(p.data.error, '');
  assert.equal(p.data.previewError, '');
  assert.ok(p.data.previewPath);
  assert.equal(requests.findLast(r => r.url.endsWith('/plans')).data.settings.copies, 3);
  p.onUnload();
});

test('failed automatic previews can retry without enabling print or submitting a job', async (t) => {
  t.mock.timers.enable({ apis: ['setTimeout'] });
  const p = await page('settings', { document: doc().id, printer: printer.id });
  const request = wx.request;
  wx.request = options => options.fail({ errMsg: 'timeout' });
  p.onCopies({ detail: { value: '2' } });
  await automaticPreview(t);
  assert.ok(p.data.previewError);
  assert.equal(p.data.preparing, false);
  assert.equal(p.data.plan, null);
  await p.onPrint();
  assert.equal(modalCalls, 0);
  wx.request = request;
  await p.onPreviewRetry();
  assert.ok(p.data.previewPath);
  assert.equal(p.data.previewError, '');
  assert.ok(!requests.some(r => r.url.endsWith('/jobs')));
  p.onUnload();
});

test('invalid intermediate inputs do not prepare; correcting them resumes automatic previews', async (t) => {
  t.mock.timers.enable({ apis: ['setTimeout'] });
  const p = await page('settings', { document: doc().id, printer: printer.id });
  p.onCopies({ detail: { value: '' } });
  await automaticPreview(t);
  assert.equal(requests.filter(r => r.url.endsWith('/plans')).length, 1);
  assert.ok(p.data.error);
  assert.equal(p.data.preparing, false);
  p.onCopies({ detail: { value: '2' } });
  await automaticPreview(t);
  assert.equal(requests.filter(r => r.url.endsWith('/plans')).length, 2);
  assert.equal(p.data.error, '');
  p.onUnload();
});

test('leaving cancels the debounce and ignores an in-flight completion', async (t) => {
  t.mock.timers.enable({ apis: ['setTimeout'] });
  const p = await page('settings', { document: doc().id, printer: printer.id });
  p.onCopies({ detail: { value: '2' } });
  p.onUnload();
  await automaticPreview(t);
  assert.equal(requests.filter(r => r.url.endsWith('/plans')).length, 1);

  const q = await page('settings', { document: doc().id, printer: printer.id });
  let pending;
  wx.request = options => { pending = options; };
  q.onCopies({ detail: { value: '3' } });
  await automaticPreview(t);
  q.onUnload();
  q.setData = () => assert.fail('unloaded page updated');
  pending.success({ statusCode: 201, data: plan() });
  await flush();
  assert.equal(modalCalls, 0);
  assert.ok(!requests.some(r => r.url.endsWith('/jobs')));
});

test('reopened manual task requires paper confirmation and repeated continue sends once', async () => {
  response = options => options.url.endsWith('/continue')
    ? { ...job(), phase: 'back' }
    : { ...job(), state: 'awaiting_flip', phase: 'front', settings: { ...job().settings, sides: 'manual-long-edge', manualFirstSide: undefined } };
  const p = await page('jobs', { id: job().id });
  await p.refresh();
  assert.equal(p.data.job.state, 'awaiting_flip');
  assert.equal(p.data.job.illustratedReload, true);
  await p.onContinue();
  assert.ok(!requests.some(r => r.url.endsWith('/continue')));
  p.onPaper({ detail: { value: ['ready'] } });
  const pending = p.onContinue();
  await p.onContinue();
  await pending;
  assert.equal(requests.filter(r => r.url.endsWith('/continue')).length, 1);
  assert.deepEqual(requests.at(-1).data, { paperConfirmed: true });
  assert.equal(p.data.paperConfirmed, false);
  assert.equal(p.data.job.phase, 'back');
  p.onUnload();
});

test('even-first tasks retain their reload guide and continue only on confirmation', async () => {
  const settings = { ...job().settings, sides: 'manual-long-edge', manualFirstSide: 'back' };
  response = options => options.url.endsWith('/continue')
    ? { ...job(), settings, phase: 'front' }
    : { ...job(), settings, state: 'awaiting_flip', phase: 'back' };
  const p = await page('jobs', { id: job().id });
  await p.refresh();
  assert.equal(p.data.job.illustratedReload, true);
  assert.equal(print.jobLabel({ ...job(), settings, state: 'printing', phase: 'back' }), '正在打印第一轮');
  assert.equal(print.jobLabel({ ...job(), settings, state: 'printing', phase: 'front' }), '正在打印第二轮');
  await p.onContinue();
  assert.ok(!requests.some(r => r.url.endsWith('/continue')));
  p.onPaper({ detail: { value: ['ready'] } });
  const pending = p.onContinue();
  await p.onContinue();
  await pending;
  assert.equal(requests.filter(r => r.url.endsWith('/continue')).length, 1);
  assert.equal(p.data.job.phase, 'front');
  assert.equal(p.data.paperConfirmed, false);
  p.onUnload();
});
test('legacy jobs retain their saved paper handling without showing the new profile illustration', async () => {
  response = () => ({ ...job(), state: 'awaiting_flip', phase: 'front',
    settings: { ...job().settings, manualDuplexProfile: undefined, manualFirstSide: undefined, sides: 'manual-short-edge' } });
  const p = await page('jobs', { id: job().id });
  await p.refresh();
  assert.equal(p.data.job.illustratedReload, false);
  assert.equal(p.data.paperConfirmed, false);
  p.onUnload();
});
test('task cancel is confirmed and an uncertain task is never automatically resubmitted', async () => {
  response = options => ({ ...job(), state: options.url.endsWith('/cancel') ? 'canceling' : 'creating',
    problem: 'submission_uncertain' });
  const p = await page('jobs', { id: job().id });
  await p.refresh();
  assert.match(p.data.job.problemLabel, /核对/);
  modalConfirm = false; await p.onCancel();
  assert.ok(!requests.some(r => r.method === 'POST'));
  modalConfirm = true; await p.onCancel();
  assert.equal(p.data.job.state, 'canceling');
  assert.equal(requests.filter(r => r.url.endsWith('/cancel')).length, 1);
  assert.ok(!requests.some(r => r.url.endsWith('/jobs') && r.method === 'POST'));
  p.onUnload();
});

test('device cancellation is polled until verified without a physical stop assertion', async () => {
  let problem = 'printer_busy';
  response = options => ({ ...job(), state: problem ? 'canceling' : 'canceled',
    problem, deviceCancelRequired: true, cupsJobId: 42, cupsJobIds: [42] });
  const p = await page('jobs', { id: job().id });
  await p.refresh();
  assert.equal(p.data.job.terminal, false);
  assert.equal(requests.some(r => r.method === 'POST'), false);
  problem = 'device_cancel_required';
  await p.refresh();
  assert.match(p.data.job.problemLabel, /清理/);
  problem = '';
  await p.refresh();
  assert.equal(p.data.job.state, 'canceled');
  assert.equal(p.data.job.terminal, true);
  const confirmations = requests.filter(r => r.url.endsWith('/confirm-cancel'));
  assert.equal(confirmations.length, 0);
  assert.equal(requests.some(r => r.url.endsWith('/continue') || (r.url.endsWith('/jobs') && r.method === 'POST')), false);
  p.onUnload();
});

test('failed device cleanup retains the task across reopening without any confirmation request', async () => {
  response = () => ({ ...job(), state: 'canceling', problem: 'device_cancel_failed', deviceCancelRequired: true });
  const p = await page('jobs', { id: job().id });
  await p.refresh();
  assert.equal(p.data.job.state, 'canceling');
  assert.equal(p.data.job.terminal, false);
  assert.match(p.data.job.problemLabel, /正在重试/);
  p.onUnload();
  const reopened = await page('jobs', { id: job().id });
  await reopened.refresh();
  assert.equal(reopened.data.job.deviceCancelRequired, true);
  assert.equal(requests.some(r => r.method === 'POST'), false);
  assert.throws(() => print.parseJob({ ...job(), deviceCancelRequired: 'true' }), { code: 'invalid_response' });
  reopened.onUnload();
});

test('a competing submission resumes the existing task without retrying the new document', async () => {
  const p = await page('settings', { document: doc().id, printer: printer.id });
  const request = wx.request;
  wx.request = options => {
    if (options.url.endsWith('/jobs') && options.method === 'POST') {
      requests.push(options);
      options.success({ statusCode: 409, data: { error: { code: 'active_print_exists' } } });
    } else if (options.url.endsWith('/jobs?active=true')) {
      requests.push(options);
      options.success({ statusCode: 200, data: { jobs: [job()] } });
    } else request(options);
  };
  await p.onPrint();
  assert.equal(routes.at(-1), `/pages/jobs/index?id=${job().id}`);
  assert.equal(requests.filter(r => r.url.endsWith('/jobs') && r.method === 'POST').length, 1);
  assert.equal(p.data.busy, false);
  p.onUnload();
});

test('paper shortage cannot use the flip confirmation endpoint', async () => {
  response = () => ({ ...job(), state: 'printing', problem: 'printer_stopped',
    settings: { ...job().settings, sides: 'manual-long-edge' }, phase: 'back' });
  const p = await page('jobs', { id: job().id });
  await p.refresh();
  p.onPaper({ detail: { value: ['ready'] } });
  await p.onContinue();
  assert.ok(!requests.some(r => r.method === 'POST'));
  assert.equal(p.data.job.state, 'printing');
  p.onUnload();
});
