const { test, beforeEach, afterEach } = require('node:test');
const assert = require('node:assert/strict');
const { defaultSettings } = require('../.test-dist/services/printing.js');

const printer = {
  id: 'virtual', name: 'Virtual printer', model: '', location: '', state: 'idle',
  acceptingJobs: true, stateReasons: [], manualDuplexProfile: 'rear-face-up-v1',
  capabilities: { media: ['iso_a4_210x297mm'], sides: ['one-sided'], colorModes: ['color', 'monochrome'],
    copies: { min: 1, max: 99 }, defaultMedia: 'iso_a4_210x297mm', defaultColorMode: 'color', defaultSides: 'one-sided' },
};
const flush = () => new Promise(setImmediate);
let f;

function fixture() {
  const storage = new Map([['printbridge.connection.v1', { address: 'http://printbridge.test',
    token: 'a'.repeat(43), expiresAt: new Date(Date.now() + 3600000).toISOString() }]]);
  const stack = [], requests = [], uploads = [], downloads = [], removed = [], menus = [];
  const docs = new Map(), plans = new Map();
  let counter = 0;
  const id = () => (++counter).toString(16).padStart(32, '0');
  const document = (name = 'sample.pdf', pageCount = 3) => {
    const doc = { id: id(), name, bytes: 500, pageCount, createdAt: new Date().toISOString(),
      expiresAt: new Date(Date.now() + 3600000).toISOString() };
    docs.set(doc.id, doc);
    return doc;
  };
  const makePlan = options => {
    const doc = docs.get(options.data.documentId);
    const plan = { id: id(), documentId: doc.id, name: doc.name, settings: options.data.settings,
      pageCount: doc.pageCount, sheetCount: doc.pageCount, selectedPages: [1],
      sheets: [{ front: [1], back: [] }], createdAt: doc.createdAt, expiresAt: doc.expiresAt };
    plans.set(plan.id, plan);
    return plan;
  };
  const mount = (name, options = {}) => {
    let definition;
    global.Page = value => { definition = value; };
    const file = require.resolve(`../.test-dist/pages/${name}/index.js`);
    delete require.cache[file];
    require(file);
    const page = { ...definition, data: structuredClone(definition.data),
      setData(value) { Object.assign(this.data, value); } };
    const entry = { name, page };
    stack.push(entry);
    entry.loaded = page.onLoad(options);
    return page;
  };
  const pop = () => { stack.pop()?.page.onUnload?.(); };
  const navigate = (options, replace) => {
    const url = new URL(options.url, 'https://mini.test');
    if (replace) pop();
    mount(url.pathname.split('/')[2], Object.fromEntries(url.searchParams));
    options.success?.({});
  };
  const api = { stack, requests, uploads, downloads, removed, menus, storage, document, makePlan,
    selected: { name: 'replacement.pdf', path: 'wxfile://replacement.pdf', size: 500 },
    source: 0, uploadReply: null, modalCalls: 0,
    top: () => stack.at(-1).page,
    names: () => stack.map(entry => entry.name),
    back: pop,
    async settle() { await flush(); await stack.at(-1)?.loaded; await flush(); },
    async settings(name) {
      const doc = document(name);
      const p = mount('settings', { document: doc.id, printer: printer.id });
      await this.settle();
      return p;
    },
  };
  global.getApp = () => ({ pendingEntry: null });
  global.wx = {
    getStorageSync: key => storage.get(key),
    setStorageSync: (key, value) => storage.set(key, value),
    removeStorageSync: key => storage.delete(key),
    navigateTo: options => navigate(options, false),
    redirectTo: options => navigate(options, true),
    reLaunch: options => { while (stack.length) pop(); api.lastRelaunch = options.url; },
    showActionSheet: options => {
      menus.push(options);
      if (api.source === null) options.fail({ errMsg: 'showActionSheet:fail cancel' });
      else options.success({ tapIndex: api.source });
    },
    chooseMessageFile: options => options.success({ tempFiles: api.selected ? [api.selected] : [] }),
    chooseMedia: options => {
      assert.deepEqual(options.sourceType, ['album']);
      options.success({ tempFiles: api.selected ? [{ tempFilePath: 'wxfile://photo.png', size: 500 }] : [] });
    },
    uploadFile: options => {
      uploads.push(options);
      const task = { abort() {}, onProgressUpdate() {} };
      queueMicrotask(() => {
        options.success(api.uploadReply || { statusCode: 201,
          data: JSON.stringify(document(options.formData.name)) });
        options.complete?.();
      });
      return task;
    },
    request: options => {
      requests.push(options);
      const value = options.url.endsWith('/printers') ? { printers: [printer] }
        : options.url.endsWith('/plans') ? makePlan(options)
        : options.url.includes('/documents/') ? docs.get(options.url.split('/').at(-1))
        : plans.get(options.url.split('/').at(-1));
      queueMicrotask(() => options.success({ statusCode: options.method === 'POST' ? 201 : 200, data: value }));
    },
    downloadFile: options => {
      downloads.push(options);
      const task = { abort() {} };
      queueMicrotask(() => {
        options.success({ statusCode: 200, tempFilePath: `wxfile://preview-${downloads.indexOf(options)}.png` });
        options.complete?.();
      });
      return task;
    },
    getImageInfo: options => options.success({}),
    getFileSystemManager: () => ({ unlink: options => removed.push(options.filePath) }),
    showModal: () => { api.modalCalls++; assert.fail('File selection must never ask to print'); },
    stopPullDownRefresh() {},
  };
  const p = mount('printers');
  p.setData({ printers: [printer], status: 'ready', tasksKnown: true });
  return api;
}

beforeEach(() => { f = fixture(); });
afterEach(() => {
  assert.ok(!f.requests.some(r => /\/jobs(?:\/|$)/.test(r.url)), 'No printing during selection or preview');
  while (f.stack.length) f.back();
});

test('upload replaces selection; Back from settings returns directly to printers', async () => {
  f.top().onSelect({ currentTarget: { dataset: { id: printer.id } } });
  assert.deepEqual(f.names(), ['printers', 'document']);
  await f.top().onChoose();
  await f.settle();
  assert.deepEqual(f.names(), ['printers', 'settings']);
  assert.equal(f.uploads.length, 1);
  assert.ok(f.downloads.every(r => r.url.includes('/plans/')), 'No redundant original-document preview');
  assert.ok(f.top().data.previewPath);
  f.back();
  assert.deepEqual(f.names(), ['printers']);
});

test('repeated reupload replaces the current document without growing the page stack', async () => {
  const p = await f.settings();
  let previous = p.data.document.id;
  for (let i = 0; i < 12; i++) {
    await p.onReplace();
    await f.settle();
    assert.deepEqual(f.names(), ['printers', 'settings']);
    assert.equal(f.top(), p);
    assert.notEqual(p.data.document.id, previous);
    assert.equal(p.data.plan.documentId, p.data.document.id);
    assert.ok(p.data.previewPath);
    previous = p.data.document.id;
  }
  assert.equal(f.uploads.length, 12);
  assert.equal(f.menus.length, 12);
  f.back();
  assert.deepEqual(f.names(), ['printers']);
});

test('cancelled source and file pickers preserve document, settings and usable preview', async () => {
  const p = await f.settings();
  const previous = { document: p.data.document, plan: p.data.plan, path: p.data.previewPath, settings: p.data.settings };
  f.source = null;
  await p.onReplace();
  f.source = 0;
  f.selected = null;
  await p.onReplace();
  assert.equal(p.data.document, previous.document);
  assert.equal(p.data.plan, previous.plan);
  assert.equal(p.data.previewPath, previous.path);
  assert.equal(p.data.settings, previous.settings);
  assert.equal(p.data.replacing, false);
  assert.equal(f.uploads.length, 0);
  assert.deepEqual(f.names(), ['printers', 'settings']);
});

test('failed replacement preserves the old file and preview; album retry resets all print parameters', async () => {
  const p = await f.settings('old-image.png');
  p.onOption({ currentTarget: { dataset: { field: 'colorMode' } },
    detail: { value: String(p.data.colors.findIndex(option => option.value === 'color')) } });
  p.change({ pages: '2-3', copies: 4, subset: 'even', invertImages: true, watermark: 'old', mirror: true,
    sides: 'manual-long-edge', outputOrder: 'normal' });
  await p.onPrepare();
  const previous = { document: p.data.document, plan: p.data.plan, path: p.data.previewPath, settings: p.data.settings };
  f.uploadReply = { statusCode: 422, data: JSON.stringify({ error: { code: 'conversion_failed' } }) };
  await p.onReplace();
  assert.equal(p.data.document, previous.document);
  assert.equal(p.data.plan, previous.plan);
  assert.equal(p.data.previewPath, previous.path);
  assert.equal(p.data.settings, previous.settings);
  assert.equal(p.data.settings.colorMode, 'color');
  assert.ok(p.data.replacementError);
  assert.equal(p.data.replacing, false);
  f.uploadReply = null;
  f.source = 1;
  await p.onReplace();
  assert.equal(p.data.document.name, '照片.png');
  assert.equal(f.uploads.at(-1).filePath, 'wxfile://photo.png');
  assert.deepEqual(p.data.settings, defaultSettings(printer));
  assert.equal(p.data.settings.colorMode, 'monochrome');
  assert.equal(p.data.colors[p.data.colorIndex].value, 'monochrome');
  assert.equal(p.data.page, 1);
  assert.equal(p.data.manual, false);
  assert.equal(p.data.replacementError, '');
  const sent = f.requests.findLast(r => r.url.endsWith('/plans')).data;
  assert.equal(sent.documentId, p.data.document.id);
  assert.equal(sent.settings.colorMode, 'monochrome');
  assert.equal(sent.replacesPlanId, undefined, 'Plans belong to a single document');
  assert.ok(f.removed.includes(previous.path));
});

test('source selection locks reupload, setting edits and print confirmation until cancelled', async () => {
  const p = await f.settings();
  const previous = p.data.settings;
  let menu;
  wx.showActionSheet = options => { menu = options; };
  const pending = p.onReplace();
  await p.onReplace();
  p.onCopies({ detail: { value: '4' } });
  await p.onPrint();
  assert.equal(p.data.settings, previous);
  assert.equal(p.data.replacing, true);
  assert.equal(f.uploads.length, 0);
  menu.fail({ errMsg: 'showActionSheet:fail cancel' });
  await pending;
  assert.equal(p.data.replacing, false);
});

test('picker failure keeps the current file and does not upload', async () => {
  const p = await f.settings();
  const doc = p.data.document, preview = p.data.previewPath;
  wx.chooseMessageFile = options => options.fail({ errMsg: 'chooseMessageFile:fail denied' });
  await p.onReplace();
  assert.ok(p.data.replacementError);
  assert.equal(p.data.document, doc);
  assert.equal(p.data.previewPath, preview);
  assert.equal(p.data.replacing, false);
  assert.equal(f.uploads.length, 0);
});

test('leaving during file selection ignores the late selection without uploading', async () => {
  const p = await f.settings();
  let picker;
  wx.chooseMessageFile = options => { picker = options; };
  const pending = p.onReplace();
  await flush();
  f.back();
  p.setData = () => assert.fail('Unloaded page updated');
  picker.success({ tempFiles: [f.selected] });
  await pending;
  assert.equal(f.uploads.length, 0);
  assert.deepEqual(f.names(), ['printers']);
});

test('failed new-document preparation keeps the new file and retries without reuploading', async () => {
  const p = await f.settings();
  const previous = p.data.document.id;
  const request = wx.request;
  wx.request = options => options.url.endsWith('/plans') ? options.fail({ errMsg: 'timeout' }) : request(options);
  await p.onReplace();
  assert.notEqual(p.data.document.id, previous);
  assert.ok(p.data.previewError);
  assert.equal(p.data.plan, null);
  assert.equal(p.data.previewPath, '');
  assert.equal(p.data.replacing, false);
  assert.equal(p.data.preparing, false);
  await p.onPrint();
  wx.request = request;
  await p.onPreviewRetry();
  assert.equal(p.data.plan.documentId, p.data.document.id);
  assert.ok(p.data.previewPath);
  assert.equal(f.uploads.length, 1);
});

test('a late old-document plan cannot become the replacement plan or its replacesPlanId', async () => {
  const p = await f.settings();
  const request = wx.request;
  let old;
  wx.request = options => options.url.endsWith('/plans') ? old = options : request(options);
  p.onCopies({ detail: { value: '2' } });
  const preparing = p.onPrepare();
  await flush();
  const oldId = p.data.document.id;
  wx.request = request;
  await p.onReplace();
  assert.notEqual(p.data.document.id, oldId);
  assert.equal(p.data.plan, null);
  await p.onPrint();
  old.success({ statusCode: 201, data: f.makePlan(old) });
  await preparing;
  await flush();
  assert.equal(p.data.plan.documentId, p.data.document.id);
  assert.equal(p.data.preparing, false);
  const newest = f.requests.findLast(r => r.url.endsWith('/plans')).data;
  assert.equal(newest.documentId, p.data.document.id);
  assert.equal(newest.replacesPlanId, undefined);
});

test('a late old preview is removed without clearing the replacement preview', async () => {
  const p = await f.settings();
  const download = wx.downloadFile;
  let old, aborted = false;
  wx.downloadFile = options => {
    old = options;
    return { abort() { aborted = true; } };
  };
  const turning = p.onNext();
  await flush();
  wx.downloadFile = download;
  await p.onReplace();
  old.success({ statusCode: 200, tempFilePath: 'wxfile://obsolete.png' });
  await turning;
  await flush();
  assert.ok(aborted);
  assert.ok(f.removed.includes('wxfile://obsolete.png'));
  assert.equal(p.data.plan.documentId, p.data.document.id);
  assert.ok(p.data.previewPath);
  assert.ok(!f.removed.includes(p.data.previewPath));
});

test('leaving during replacement aborts upload and ignores late completion', async () => {
  const p = await f.settings();
  const previous = p.data.document;
  let upload, aborted = false;
  wx.uploadFile = options => {
    upload = options;
    return { abort() { aborted = true; }, onProgressUpdate() {} };
  };
  const pending = p.onReplace();
  await flush();
  f.back();
  p.setData = () => assert.fail('Unloaded page updated');
  assert.ok(aborted);
  upload.success({ statusCode: 201, data: JSON.stringify(f.document()) });
  await pending;
  assert.equal(p.data.document, previous);
  assert.deepEqual(f.names(), ['printers']);
});

test('expired authorization during replacement returns to scanning without preparing or printing', async () => {
  const p = await f.settings();
  const prepares = f.requests.filter(r => r.url.endsWith('/plans')).length;
  f.uploadReply = { statusCode: 401, data: JSON.stringify({ error: { code: 'session_expired' } }) };
  await p.onReplace();
  assert.equal(f.lastRelaunch, '/pages/connect/index?expired=1');
  assert.equal(f.storage.has('printbridge.connection.v1'), false);
  assert.equal(f.requests.filter(r => r.url.endsWith('/plans')).length, prepares);
});
