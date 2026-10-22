const { test, beforeEach } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { DocumentSession, chooseChatFile, choosePhoto, parseDocument, maxDocumentBytes } = require('../.test-dist/services/documents.js');

let storage, requests, uploads, downloads, removed, images, reply, downloadReply, chosen, routes;
const connection = () => ({
  address: 'http://printbridge.test:8080', token: 'a'.repeat(43),
  expiresAt: new Date(Date.now() + 3600000).toISOString(),
});
const document = () => ({
  id: 'a'.repeat(32), name: 'Synthetic.pdf', bytes: 1200, pageCount: 16,
  createdAt: new Date().toISOString(), expiresAt: new Date(Date.now() + 3600000).toISOString(),
});
const selected = () => ({ name: 'Synthetic.pdf', size: 1200, path: 'wxfile://chosen.pdf' });

beforeEach(() => {
  storage = connection();
  requests = []; uploads = []; downloads = []; removed = []; images = []; routes = [];
  reply = () => ({ statusCode: 201, data: JSON.stringify(document()) });
  downloadReply = () => ({ statusCode: 200, tempFilePath: `wxfile://preview-${downloads.length}.png` });
  chosen = selected();
  global.getApp = () => ({ pendingEntry: null });
  global.wx = {
    getStorageSync: () => storage,
    removeStorageSync: () => { storage = null; },
    chooseMessageFile: options => options.success({ tempFiles: chosen ? [chosen] : [] }),
    uploadFile: options => {
      uploads.push(options);
      const task = { abort() {}, onProgressUpdate(fn) { this.progress = fn; } };
      queueMicrotask(() => { task.progress?.({ progress: 100 }); options.success(reply()); options.complete?.(); });
      return task;
    },
    downloadFile: options => {
      downloads.push(options);
      const task = { abort() {} };
      queueMicrotask(() => { options.success(downloadReply()); options.complete?.(); });
      return task;
    },
    request: options => { requests.push(options); queueMicrotask(() => options.success({ statusCode: 200, data: document() })); },
    getFileSystemManager: () => ({
      unlink: options => { removed.push(options.filePath); options.success?.(); },
      readFileSync: () => JSON.stringify({ error: { code: 'document_busy' } }),
    }),
    getImageInfo: options => options.success({ width: 800, height: 1200 }),
    previewImage: options => { images.push(options); options.success?.(); },
    showToast: () => {},
    reLaunch: () => {},
    navigateTo: options => routes.push(options.url),
    redirectTo: options => routes.push(options.url),
  };
});

function page() {
  let definition;
  global.Page = value => { definition = value; };
  const file = require.resolve('../.test-dist/pages/document/index.js');
  delete require.cache[file];
  require(file);
  const result = { ...definition, data: structuredClone(definition.data), setData(value) { Object.assign(this.data, value); } };
  result.onLoad({ printer: 'Synthetic', name: 'Synthetic' });
  return result;
}

test('chat selection cancellation and file limits never upload anything', async () => {
  assert.deepEqual(await chooseChatFile(), selected());
  chosen = null;
  assert.equal(await chooseChatFile(), null);
  wx.chooseMessageFile = options => options.fail({ errMsg: 'chooseMessageFile:fail cancel' });
  assert.equal(await chooseChatFile(), null);
  wx.chooseMessageFile = options => options.success({ tempFiles: [{ ...selected(), size: maxDocumentBytes + 1 }] });
  await assert.rejects(chooseChatFile(), { code: 'document_too_large' });
  wx.chooseMessageFile = options => options.success({ tempFiles: [{ ...selected(), name: 'test.docx' }] });
  assert.equal((await chooseChatFile()).name, 'test.docx');
  assert.equal(uploads.length, 0);
});

test('chat selection allows documents and images, and photo selection is restricted to the album', async () => {
  wx.chooseMessageFile = options => {
    assert.equal(options.type, 'all');
    assert.equal(options.extension, undefined);
    options.success({ tempFiles: [{ ...selected(), name: 'sheet.xlsx' }] });
  };
  assert.equal((await chooseChatFile()).name, 'sheet.xlsx');
  wx.chooseMedia = options => {
    assert.deepEqual(options.sourceType, ['album']);
    assert.deepEqual(options.mediaType, ['image']);
    assert.deepEqual(options.sizeType, ['original']);
    options.success({ tempFiles: [{ tempFilePath: 'wxfile://photo.png', size: 500 }] });
  };
  assert.deepEqual(await choosePhoto(), { path: 'wxfile://photo.png', name: '照片.png', size: 500 });
});

test('album selection handles cancellation, empty results, failures and oversized files without upload', async () => {
  wx.chooseMedia = options => options.fail({ errMsg: 'chooseMedia:fail cancel' });
  assert.equal(await choosePhoto(), null);
  wx.chooseMedia = options => options.success({ tempFiles: [] });
  assert.equal(await choosePhoto(), null);
  wx.chooseMedia = options => options.fail({ errMsg: 'chooseMedia:fail auth denied' });
  await assert.rejects(choosePhoto(), { code: 'choose_failed' });
  wx.chooseMedia = options => options.success({
    tempFiles: [{ tempFilePath: 'wxfile://large.png', size: maxDocumentBytes + 1 }],
  });
  await assert.rejects(choosePhoto(), { code: 'document_too_large' });
  assert.equal(uploads.length, 0);
});

test('selection page uploads an album image once and opens settings without downloading a preview', async () => {
  wx.chooseMedia = options => {
    assert.deepEqual(options.sourceType, ['album']);
    options.success({ tempFiles: [{ tempFilePath: 'wxfile://photo.png', size: 500 }] });
  };
  reply = () => ({ statusCode: 201, data: JSON.stringify({ ...document(), name: '照片.png' }) });
  const p = page();
  await p.onChoosePhoto();
  assert.equal(uploads.length, 1);
  assert.equal(uploads[0].filePath, 'wxfile://photo.png');
  assert.equal(p.data.status, 'ready');
  assert.equal(p.data.fileName, '照片.png');
  assert.deepEqual(routes, [`/pages/settings/index?document=${document().id}&printer=Synthetic`]);
  assert.equal(downloads.length, 0);
  assert.ok([...requests, ...uploads, ...downloads].every(r => !/\/print|\/jobs/.test(r.url.replace('http://printbridge.test:8080', ''))));
  p.onUnload();
});

test('upload authenticates via header and validates returned page count', async () => {
  const client = new DocumentSession();
  let progress = 0;
  const doc = await client.upload(selected(), value => { progress = value; });
  assert.equal(progress, 100);
  assert.equal(doc.pageCount, 16);
  assert.equal(uploads[0].name, 'file');
  assert.deepEqual(uploads[0].formData, { name: 'Synthetic.pdf' });
  assert.equal(uploads[0].header.Authorization, `Bearer ${storage.token}`);
  assert.ok(!uploads[0].url.includes(storage.token));
  for (const value of [null, {}, { ...document(), pageCount: 0 }, { ...document(), pageCount: 501 },
    { ...document(), id: '../escape' }, { ...document(), bytes: 0 }, { ...document(), expiresAt: 'bad' }]) {
    assert.throws(() => parseDocument(value), { code: 'invalid_response' });
  }
  client.dispose();
});

test('preview reuses local pages with authorization and bounded cache; disposing removes them', async () => {
  const client = new DocumentSession();
  const doc = document();
  const first = await client.preview(doc, 1);
  assert.equal(await client.preview(doc, 1), first);
  assert.equal(downloads.length, 1);
  assert.equal(downloads[0].header.Authorization, `Bearer ${storage.token}`);
  for (let p = 2; p <= 7; p++) await client.preview(doc, p);
  assert.deepEqual(removed, [first]);
  assert.equal(uploads.length, 0);
  await assert.rejects(client.preview(doc, 17), { code: 'invalid_page' });
  await assert.rejects(client.preview({ ...doc, expiresAt: '2000-01-01' }, 2), { code: 'document_not_found' });
  client.dispose();
  assert.equal(removed.length, 7);
  await assert.rejects(client.preview(doc, 1), { code: 'cancelled' });
});

test('an old authorization failure cannot clear the newly scanned connection', async () => {
  const client = new DocumentSession();
  reply = () => ({ statusCode: 401, data: JSON.stringify({ error: { code: 'session_expired' } }) });
  const pending = client.upload(selected(), () => {});
  storage = { ...connection(), token: 'b'.repeat(43) };
  await assert.rejects(pending, { code: 'session_expired' });
  assert.equal(storage.token, 'b'.repeat(43));
  client.dispose();
});

test('reading and print previews have distinct caches and reading bounds exclude added padding', async () => {
  const client = new DocumentSession();
  const plan = { ...document(), pageCount: 4, previewPageCount: 3 };
  const physical = await client.preview(plan, 1, 'plans');
  const reading = await client.preview(plan, 1, 'plans', 'reading');
  assert.notEqual(physical, reading);
  assert.match(downloads[0].url, /\/plans\/.*\/pages\/1$/);
  assert.match(downloads[1].url, /\/plans\/.*\/pages\/1\?order=reading$/);
  assert.equal(await client.preview(plan, 1, 'plans', 'reading'), reading);
  assert.equal(await client.preview(plan, 1, 'plans'), physical);
  await assert.rejects(client.preview(plan, 4, 'plans', 'reading'), { code: 'invalid_page' });
  assert.equal(downloads.length, 2);
  await client.preview(plan, 4, 'plans');
  assert.equal(downloads.length, 3);
  client.dispose();
});

test('late download after disposal is deleted and never returned', async () => {
  const client = new DocumentSession();
  const pending = client.preview(document(), 1);
  client.dispose();
  await assert.rejects(pending, { code: 'cancelled' });
  assert.deepEqual(removed, ['wxfile://preview-1.png']);
});

test('server rendering failure retries only the page; image decode failures are not cached', async () => {
  const client = new DocumentSession();
  downloadReply = () => ({ statusCode: 503, tempFilePath: 'wxfile://error.json' });
  await assert.rejects(client.preview(document(), 1), { code: 'document_busy' });
  downloadReply = () => ({ statusCode: 200, tempFilePath: 'wxfile://bad.png' });
  wx.getImageInfo = options => options.fail({});
  await assert.rejects(client.preview(document(), 1), { code: 'preview_failed' });
  wx.getImageInfo = options => options.success({});
  assert.equal(await client.preview(document(), 1), 'wxfile://bad.png');
  assert.equal(downloads.length, 3);
  assert.equal(uploads.length, 0);
  client.dispose();
});

test('page chooses and uploads once then replaces itself with settings without printing', async () => {
  const p = page();
  await p.onChoose();
  assert.equal(p.data.status, 'ready');
  assert.equal(p.data.document.pageCount, 16);
  assert.equal(downloads.length, 0);
  assert.equal(images.length, 0);
  await p.onShow();
  assert.equal(uploads.length, 1);
  assert.deepEqual(routes, [`/pages/settings/index?document=${document().id}&printer=Synthetic`]);
  assert.ok([...requests, ...uploads, ...downloads].every(r => !/\/print|\/jobs/.test(r.url.replace('http://printbridge.test:8080', ''))));
  assert.ok(!JSON.stringify(p.data).includes(storage.token));
  p.onUnload();
});

test('cancelled and failed initial file pickers do not upload or leave the selection page', async () => {
  const p = page();
  chosen = null;
  await p.onChoose();
  assert.equal(p.data.status, 'empty');
  assert.equal(p.data.choosing, false);
  wx.chooseMedia = options => options.fail({ errMsg: 'chooseMedia:fail auth denied' });
  await p.onChoosePhoto();
  assert.ok(p.data.error);
  assert.equal(p.data.choosing, false);
  assert.equal(uploads.length, 0);
  assert.equal(routes.length, 0);
  p.onUnload();
});

test('repeated initial selection and retry taps cannot start a second upload or navigation', async () => {
  const p = page();
  let choose;
  wx.chooseMessageFile = options => { choose = options; };
  const pending = p.onChoose();
  await p.onChoose();
  await p.onRetryUpload();
  assert.equal(uploads.length, 0);
  choose.success({ tempFiles: [selected()] });
  await pending;
  await p.onChoose();
  await p.onRetryUpload();
  assert.equal(uploads.length, 1);
  assert.equal(routes.length, 1);
  p.onUnload();
});

test('failed upload retries only the selected file; failed navigation never uploads again', async () => {
  const p = page();
  reply = () => ({ statusCode: 503, data: JSON.stringify({ error: { code: 'document_busy' } }) });
  await p.onChoose();
  assert.equal(p.data.status, 'error');
  assert.equal(uploads.length, 1);
  reply = () => ({ statusCode: 201, data: JSON.stringify(document()) });
  wx.redirectTo = options => options.fail({});
  await p.onRetryUpload();
  assert.equal(p.data.status, 'error');
  assert.match(p.data.error, /打印设置/);
  assert.equal(uploads.length, 2);
  wx.redirectTo = options => routes.push(options.url);
  await p.onRetryUpload();
  await p.onRetryUpload();
  assert.equal(routes.length, 1);
  assert.equal(uploads.length, 2);
  assert.equal(downloads.length, 0);
  p.onUnload();
});

test('leaving the page during upload ignores late completion and aborts operations', async () => {
  let complete;
  let aborted = false;
  wx.uploadFile = options => {
    complete = () => options.success({ statusCode: 201, data: JSON.stringify(document()) });
    return { abort() { aborted = true; }, onProgressUpdate() {} };
  };
  const p = page();
  const pending = p.onChoose();
  await new Promise(setImmediate);
  p.onUnload();
  complete();
  await pending;
  assert.ok(aborted);
  assert.equal(p.data.document, null);
  assert.equal(downloads.length, 0);
});

test('application-owned layout dimensions use rpx, excluding vendor sources', () => {
  const root = path.resolve(__dirname, '../src');
  const files = [path.join(root, 'app.wxss')];
  function walk(dir) {
    for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
      const full = path.join(dir, entry.name);
      if (entry.isDirectory()) walk(full);
      else if (/\.(wxml|wxss)$/.test(entry.name)) files.push(full);
    }
  }
  walk(path.join(root, 'pages'));
  walk(path.join(root, 'components'));
  walk(path.join(root, 'custom-tab-bar'));
  for (const file of files) {
    assert.doesNotMatch(fs.readFileSync(file, 'utf8'), /(?:\d|\.)px\b/, file);
  }
});
