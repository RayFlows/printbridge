const { test } = require('node:test');
const assert = require('node:assert/strict');
const { presentPrinter, taskPrinterNotice } = require('../.test-dist/pages/printers/presentation.js');
const { jobLabel } = require('../.test-dist/services/printing.js');
const { parsePrinters } = require('../.test-dist/services/printers.js');

const printer = {
  id: 'virtual', name: 'Virtual', model: '', location: '', state: 'processing',
  acceptingJobs: true, stateReasons: ['media-empty-error'], stateMessage: 'Rendering completed',
  capabilities: { media: [], sides: [], colorModes: [], copies: null,
    defaultMedia: '', defaultSides: '', defaultColorMode: '' },
  activity: { state: 'printing', waitingJobs: 2 },
};

test('paper-out is shared, with backlog information and no driver progress noise', () => {
  const view = presentPrinter(printer);
  assert.equal(view.stateLabel, '打印机缺纸');
  assert.doesNotMatch(view.reasonsLabel, /Rendering completed/);
  assert.match(view.activityLabel, /2.*等待/);
  assert.match(taskPrinterNotice(printer, 'queued'), /缺纸.*等待/);
  assert.equal(taskPrinterNotice(printer, 'completed'), '');
  assert.match(taskPrinterNotice({ ...printer, activity: { state: 'canceling', waitingJobs: 2 } }, 'queued'), /清理.*等待/);
  assert.match(taskPrinterNotice({ ...printer, stateReasons: [], activity: { state: 'awaiting_flip', waitingJobs: 2 } }, 'queued'), /翻纸.*等待/);
});

test('disconnection suppresses stale paper-out and task state remains independent', () => {
  assert.equal(presentPrinter({ ...printer, deviceState: 'disconnected' }).stateLabel, '设备未连接');
  assert.doesNotMatch(taskPrinterNotice({ ...printer, deviceState: 'disconnected' }, 'queued'), /缺纸/);
  assert.equal(jobLabel({ state: 'printing', phase: 'single', settings: {}, cupsJobState: 3 }), '等待打印');
  assert.equal(jobLabel({ state: 'printing', phase: 'single', settings: {}, cupsJobState: 5 }), '正在打印');
  const idle = presentPrinter({ ...printer, state: 'idle', activity: { state: 'idle', waitingJobs: 0 } });
  assert.equal(idle.stateLabel, '队列空闲');
  assert.match(idle.reasonsLabel, /上次报告/);
  assert.doesNotMatch(taskPrinterNotice({ ...printer, state: 'idle' }, 'queued'), /缺纸/);
});

test('activity remains optional for old servers, and invalid activity is rejected', () => {
  const { activity, ...legacy } = printer;
  assert.equal(parsePrinters({ printers: [legacy] }).length, 1);
  for (const value of [null, { state: 'invented', waitingJobs: 0 }, { state: 'idle', waitingJobs: -1 },
    { state: 'idle', waitingJobs: 1.5 }]) {
    assert.throws(() => parsePrinters({ printers: [{ ...legacy, activity: value }] }));
  }
});
