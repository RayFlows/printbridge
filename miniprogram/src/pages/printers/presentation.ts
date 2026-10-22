import { Printer } from '../../services/printers';

const stateLabels: Record<Printer['state'], string> = {
  idle: '队列空闲', processing: '队列处理中', stopped: '队列已暂停', unknown: '未报告',
};
const sideLabels: Record<string, string> = {
  'one-sided': '单面', 'two-sided-long-edge': '长边双面', 'two-sided-short-edge': '短边双面',
};
const colorLabels: Record<string, string> = {
  color: '彩色', monochrome: '黑白', auto: '自动', 'auto-monochrome': '自动黑白',
};
const reasonLabels: Record<string, string> = {
  'media-empty': '缺纸', 'media-jam': '卡纸', 'door-open': '机盖打开',
  'paused': '队列暂停', 'offline': '设备离线', 'connecting-to-device': '正在连接设备',
  'toner-low': '碳粉不足', 'marker-supply-low': '墨水或耗材不足',
};

export function paperEmpty(printer: Printer): boolean {
  return printer.deviceState !== 'disconnected' && ['processing', 'stopped'].includes(printer.state) &&
    printer.stateReasons.some(reason => /^media-empty(?:-(?:error|warning|report))?$/.test(reason));
}

export function taskPrinterNotice(printer: Printer, state: string): string {
  if (['completed', 'canceled', 'failed', 'awaiting_flip'].includes(state)) return '';
  if (printer.deviceState === 'disconnected') return '打印机未连接，请检查电源和连接线。';
  if (state === 'canceling') return '';
  const waiting = state === 'queued' || state === 'waiting';
  if (waiting && printer.activity?.state === 'canceling') return '正在清理前一个任务，你的任务正在等待。';
  if (paperEmpty(printer)) return waiting ? '打印机缺纸，你的任务正在等待，补纸恢复后会依次打印。' :
    '打印机缺纸，请补纸并按打印机面板提示继续。';
  if (waiting && printer.activity?.state === 'awaiting_flip') return '前一个任务正在等待翻纸，你的任务正在等待。';
  if (waiting && printer.state === 'stopped') return '打印队列已暂停，你的任务正在等待恢复。';
  return waiting ? '你的任务正在等待，轮到后会自动开始打印。' : '';
}

export function presentPrinter(printer: Printer) {
  const c = printer.capabilities;
  const disconnected = printer.deviceState === 'disconnected';
  const reasons = printer.stateReasons.filter(reason => reason !== 'none').map(reason => {
    const key = reason.replace(/-(warning|error|report)$/, '');
    if (key === 'media-empty' && !paperEmpty(printer)) return '队列上次报告：缺纸';
    return reasonLabels[key] || reason;
  });
  const rawMessage = printer.stateMessage?.trim();
  const message = rawMessage && !/^(?:Rendering completed\.?|Sending data to printer\.?|Waiting for printer to become available\.?)$/i.test(rawMessage) ?
    rawMessage : '';
  const wasDisconnected = message && /unplugged|turned off|not connected|offline/i.test(message);
  if (disconnected) reasons.unshift('未检测到 USB 设备，请检查电源和连接线');
  else if (wasDisconnected && printer.deviceState !== 'connected') reasons.push('队列上次报告：设备已拔出或关闭');
  else if (message && !wasDisconnected) reasons.push(message);
  return {
    ...printer,
    stateLabel: disconnected ? '设备未连接' : printer.activity?.state === 'canceling' ? '正在清理任务' :
      paperEmpty(printer) ? '打印机缺纸' : printer.activity?.state === 'awaiting_flip' ? '等待翻纸' : stateLabels[printer.state],
    stateClass: disconnected ? 'disconnected' : printer.state,
    queueLabel: stateLabels[printer.state],
    deviceLabel: printer.deviceState === 'connected' ? 'USB 已连接' :
      disconnected ? 'USB 未连接（关机或拔线）' : '未检测',
    acceptingLabel: printer.acceptingJobs === null ? '未报告' : printer.acceptingJobs ? '接受任务' : '暂停接收',
    sidesLabel: c.sides.map(side => sideLabels[side] || side).join('、') || '未报告',
    colorLabel: c.colorModes.map(mode => colorLabels[mode] || mode).join('、') || '未报告',
    mediaLabel: c.media.join('、') || '未报告',
    copiesLabel: c.copies ? `${c.copies.min} - ${c.copies.max}` : '未报告',
    reasonsLabel: [...new Set(reasons)].join('；'),
    activityLabel: printer.activity?.waitingJobs ? `${printer.activity.waitingJobs} 个任务等待打印` : '',
  };
}

export type PrinterRow = ReturnType<typeof presentPrinter>;
