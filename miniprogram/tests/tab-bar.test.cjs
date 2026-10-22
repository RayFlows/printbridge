const { test, beforeEach } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');

const root = path.resolve(__dirname, '..');
let route, requests, toasts, bounds;

beforeEach(() => {
  route = 'pages/printers/index';
  requests = [];
  toasts = [];
  bounds = { width: 200 };
  global.getCurrentPages = () => [{ route }];
  global.getApp = () => ({ pendingEntry: true });
  global.wx = {
    switchTab: options => requests.push(options),
    showToast: options => toasts.push(options),
  };
});

function tabBar() {
  let hostDefinition;
  global.Page = value => { hostDefinition = value; };
  const hostFile = path.join(root, '.test-dist', `${route}.js`);
  delete require.cache[require.resolve(hostFile)];
  require(hostFile);
  let definition;
  global.Component = value => { definition = value; };
  const file = path.join(root, '.test-dist/custom-tab-bar/index.js');
  delete require.cache[require.resolve(file)];
  require(file);
  const component = {
    ...definition.methods,
    data: structuredClone(definition.data),
    setData(values) { Object.assign(this.data, values); },
    createSelectorQuery() {
      return { select() { return this; }, boundingClientRect(callback) { callback(bounds); return this; }, exec() {} };
    },
    show() {
      hostDefinition.onShow.call({ ...hostDefinition, getTabBar: () => this, refresh() {} });
      definition.pageLifetimes.show.call(this);
    },
    hide() { definition.pageLifetimes.hide.call(this); },
    detach() { definition.lifetimes.detached.call(this); },
    resize() { definition.pageLifetimes.resize.call(this); },
  };
  definition.lifetimes.attached.call(component);
  definition.lifetimes.ready.call(component);
  component.show();
  return component;
}

function touch(x, y = 10, id = 1) {
  return { identifier: id, clientX: x, clientY: y, pageX: x, pageY: y };
}
function event(touches, changedTouches = touches) { return { touches, changedTouches }; }
function tap(index) { return { currentTarget: { dataset: { index } } }; }

test('floating tabs use bundled icons, match registered routes, and expose tab accessibility', () => {
  const bar = tabBar();
  const app = JSON.parse(fs.readFileSync(path.join(root, 'src/app.json'), 'utf8'));
  assert.deepEqual(bar.data.tabs.map(tab => tab.pagePath), app.tabBar.list.map(tab => tab.pagePath));
  for (const tab of bar.data.tabs) {
    for (const icon of [tab.icon, tab.selectedIcon]) {
      const png = fs.readFileSync(path.join(root, 'src', icon));
      assert.equal(png.subarray(1, 4).toString(), 'PNG');
      assert.equal(png.readUInt32BE(16), 96);
      assert.equal(png.readUInt32BE(20), 96);
    }
  }
  const markup = fs.readFileSync(path.join(root, 'src/custom-tab-bar/index.wxml'), 'utf8');
  assert.match(markup, /aria-role="tablist"/);
  assert.match(markup, /aria-selected=/);
  assert.match(markup, /bindtouchcancel=/);
});

test('each cached tab instance follows its owning page on show, including programmatic navigation', () => {
  const print = tabBar();
  assert.equal(print.data.selected, 0);
  print.hide();
  route = 'pages/mine/index';
  const mine = tabBar();
  assert.equal(mine.data.selected, 1);
  mine.hide();
  // Component show may run while the global route stack still refers to My.
  print.show();
  assert.equal(print.data.selected, 0);
  assert.equal(print.data.position, 0);
  mine.show();
  assert.equal(mine.data.selected, 1);
});

test('a cached printer page selects Print even if the route stack still exposes My during onShow', () => {
  const bar = tabBar();
  bar.hide();
  route = 'pages/mine/index';
  let definition;
  global.Page = value => { definition = value; };
  const file = path.join(root, '.test-dist/pages/printers/index.js');
  delete require.cache[require.resolve(file)];
  require(file);
  global.getApp = () => ({ pendingEntry: true });
  const host = { ...definition, getTabBar: () => bar };
  bar.show();
  host.onShow();
  assert.equal(bar.data.selected, 0);
  assert.equal(bar.data.position, 0);
});

test('tap uses switchTab once, ignores the active tab and rejects invalid targets', () => {
  const bar = tabBar();
  for (const index of [0, -1, 2, 0.5, 'invalid']) bar.onTap(tap(index));
  assert.equal(requests.length, 0);
  bar.onTap(tap(1));
  bar.onTap(tap(1));
  assert.equal(requests.length, 1);
  assert.equal(requests[0].url, '/pages/mine/index');
  assert.equal(bar.data.position, 1);
});

test('a failed navigation restores the current selection and permits retry', () => {
  const bar = tabBar();
  bar.onTap(tap(1));
  requests[0].fail({});
  requests[0].complete({});
  assert.equal(bar.data.position, 0);
  assert.equal(bar.data.preview, 0);
  assert.equal(toasts.length, 1);
  bar.onTap(tap(1));
  assert.equal(requests.length, 2);
});

test('a late failure cannot change a hidden or detached tab or display a stale toast', () => {
  route = 'pages/mine/index';
  const bar = tabBar();
  bar.onTap(tap(0));
  bar.hide();
  bar.show();
  requests[0].fail({});
  requests[0].complete({});
  assert.equal(bar.data.selected, 1);
  assert.equal(bar.data.position, 1);
  assert.equal(toasts.length, 0);
  bar.onTap(tap(0));
  bar.detach();
  requests[1].fail({});
  requests[1].complete({});
  assert.equal(toasts.length, 0);
});

test('drag follows the finger, commits only on release and suppresses the following synthetic tap', () => {
  const bar = tabBar();
  bar.onTouchStart(event([touch(30)]));
  bar.onTouchMove(event([touch(100)]));
  assert.equal(bar.data.position, 0.7);
  assert.equal(bar.data.preview, 1);
  assert.equal(bar.data.dragging, true);
  assert.equal(requests.length, 0);
  bar.onTouchEnd(event([], [touch(100)]));
  assert.equal(requests.length, 1);
  assert.equal(requests[0].url, '/pages/mine/index');
  requests[0].complete({});
  bar.onTap(tap(1));
  assert.equal(requests.length, 1);
});

test('reverse drags and bounds work on small screens; a short drag snaps back', () => {
  route = 'pages/mine/index';
  bounds = { width: 120 };
  const bar = tabBar();
  bar.onTouchStart(event([touch(100)]));
  bar.onTouchMove(event([touch(80)]));
  bar.onTouchEnd(event([], [touch(80)]));
  assert.equal(requests.length, 0);
  assert.equal(bar.data.position, 1);
  bar.onTouchStart(event([touch(100)]));
  bar.onTouchMove(event([touch(-50)]));
  assert.equal(bar.data.position, 0);
  bar.onTouchEnd(event([], [touch(-50)]));
  assert.equal(requests[0].url, '/pages/printers/index');
});

test('vertical gestures, multiple fingers, cancellation and resize never switch tabs', () => {
  const bar = tabBar();
  bar.onTouchStart(event([touch(20)]));
  bar.onTouchMove(event([touch(22, 50)]));
  bar.onTouchEnd(event([]));
  bar.onTap(tap(1));
  assert.equal(requests.length, 0);
  bar.onTouchStart(event([touch(20)]));
  bar.onTouchMove(event([touch(100), touch(110, 10, 2)]));
  bar.onTouchEnd(event([]));
  assert.equal(bar.data.position, 0);
  bar.onTouchStart(event([touch(20)]));
  bar.onTouchMove(event([touch(100)]));
  bar.onTouchCancel();
  assert.equal(bar.data.dragging, false);
  assert.equal(bar.data.position, 0);
  bar.onTouchStart(event([touch(20)]));
  bar.onTouchMove(event([touch(100)]));
  bounds = { width: 160 };
  bar.resize();
  bar.onTouchEnd(event([]));
  assert.equal(bar.data._width, 160);
  assert.equal(bar.data.position, 0);
  assert.equal(requests.length, 0);
});

test('taps still work if measurement is unavailable and a touch without movement stays a tap', () => {
  bounds = null;
  const bar = tabBar();
  bar.onTouchStart(event([touch(20)]));
  bar.onTouchEnd(event([]));
  bar.onTap(tap(1));
  assert.equal(requests.length, 1);
});
