import { authorizedRequest, getConnection } from './connection';
import { ServiceError } from './errors';
import type { Printer } from './printers';

export interface PrintSettings {
  printerId: string; media: string; colorMode: string; copies: number; pages: string;
  subset: string; reverse: boolean; orientation: string; pagesPerSheet: number;
  scalePercent: number; sides: string; backOrder: string; backRotation: number; calibrated: boolean;
  mediaType?: string; mediaSource?: string; layoutOrder?: string; pageBorder?: boolean; scaling?: string;
  outputOrder?: 'normal' | 'reverse';
  manualFirstSide?: '' | 'front' | 'back';
  manualDuplexProfile?: string;
  mirror?: boolean;
  watermark?: string;
  invertImages?: boolean;
}
export interface Plan {
  id: string; documentId: string; name: string; settings: PrintSettings;
  pageCount: number; previewPageCount?: number; sheetCount: number; selectedPages: number[];
  sheets: { front: number[]; back: number[] }[]; createdAt: string; expiresAt: string;
}
export interface PrintJob {
  id: string; planId: string; name: string; settings: PrintSettings; sheetCount: number;
  state: string; phase: string; cupsJobId: number; cupsJobIds: number[];
  problem: string; createdAt: string; updatedAt: string;
  deviceCancelRequired?: boolean;
  cupsJobState?: number;
}
export const sideOptions = [
  { value: 'one-sided', label: '单面' },
  { value: 'manual-long-edge', label: '手动双面 · 长边装订' },
  { value: 'manual-short-edge', label: '手动双面 · 短边装订' },
  { value: 'two-sided-long-edge', label: '自动双面 · 长边装订' },
  { value: 'two-sided-short-edge', label: '自动双面 · 短边装订' },
];
export function hasManualDuplex(printer: Printer): boolean {
  return printer.manualDuplexProfile === 'rear-face-up-v1';
}
export function defaultSettings(printer: Printer): PrintSettings {
  const c = printer.capabilities;
  const colorMode = ['monochrome', 'auto-monochrome'].find(value =>
    c.colorModes.includes(value) || c.defaultColorMode === value) || c.defaultColorMode || c.colorModes[0] || '';
  return { printerId: printer.id, media: c.defaultMedia || c.media[0] || '',
    colorMode, copies: 1, pages: '',
    subset: 'all', reverse: false, orientation: 'portrait', pagesPerSheet: 1, scalePercent: 100,
    sides: 'one-sided', manualFirstSide: 'back', backOrder: 'same', backRotation: 0, calibrated: false,
    mediaType: '', mediaSource: '', layoutOrder: 'lrtb', pageBorder: false, scaling: 'fit', outputOrder: 'reverse',
    mirror: false, watermark: '', invertImages: false,
    ...(hasManualDuplex(printer) ? { manualDuplexProfile: printer.manualDuplexProfile } : {}) };
}
export const scalingOptions = [
  { value: 'fit', label: '适应纸张' }, { value: 'shrink', label: '仅缩小大页' },
  { value: 'actual', label: '实际大小' }, { value: 'fill', label: '填满纸张' },
  { value: 'custom', label: '自定义比例' },
];
export const layoutOptions = [
  { value: 'lrtb', label: '向右，再向下' }, { value: 'rltb', label: '向左，再向下' },
  { value: 'tblr', label: '向下，再向右' }, { value: 'tbrl', label: '向下，再向左' },
];
export function mediaTypeLabel(value: string): string {
  return ({ stationery: '普通纸', plain: '普通纸', photographic: '照片纸', photo: '照片纸',
    'photographic-glossy': '光面照片纸', glossy: '光面照片纸', 'photographic-matte': '哑光照片纸',
    matte: '哑光照片纸', envelope: '信封', cardstock: '卡片纸', labels: '标签纸',
    auto: '自动选择' } as Record<string, string>)[value] || value || '打印机默认';
}
export function mediaSourceLabel(value: string): string {
  const label = ({ auto: '自动选择', 'auto-select': '自动选择', main: '主纸盒', rear: '后部进纸',
    manual: '手动进纸', photo: '照片纸盒', top: '上纸盒', bottom: '下纸盒',
    bypass: '旁路进纸', multipurpose: '多功能纸盒' } as Record<string, string>)[value];
  return label || (/^tray-?\d+$/.test(value) ? `纸盒 ${value.replace(/^tray-?/, '')}` : value) || '打印机默认';
}
const id = (value: unknown): value is string => typeof value === 'string' && /^[a-f0-9]{32}$/.test(value);
function invalid(): never { throw new ServiceError('invalid_response', '服务返回的打印信息不完整'); }
export function parsePlan(value: unknown): Plan {
  const p = value as Plan | null;
  if (!p || !id(p.id) || !id(p.documentId) || typeof p.name !== 'string' ||
    !p.settings || typeof p.settings.printerId !== 'string' ||
    ![undefined, '', 'front', 'back'].includes(p.settings.manualFirstSide) ||
    (p.settings.manualDuplexProfile !== undefined && typeof p.settings.manualDuplexProfile !== 'string') ||
    !Number.isInteger(p.pageCount) || p.pageCount < 1 || p.pageCount > 500 ||
    (p.previewPageCount !== undefined && (!Number.isInteger(p.previewPageCount) ||
      p.previewPageCount < 1 || p.previewPageCount > p.pageCount)) ||
    !Number.isInteger(p.sheetCount) || p.sheetCount < 1 ||
    !Array.isArray(p.selectedPages) || !p.selectedPages.every(n => Number.isInteger(n) && n > 0) ||
    !Array.isArray(p.sheets) || !p.sheets.every(s => Array.isArray(s.front) && Array.isArray(s.back) &&
      [...s.front, ...s.back].every(n => Number.isInteger(n) && n >= 0)) ||
    !Number.isFinite(Date.parse(p.expiresAt))) invalid();
  return p;
}
const states = ['queued', 'creating', 'created', 'sending', 'releasing', 'printing', 'awaiting_flip', 'canceling', 'completed', 'canceled', 'failed'];
export function parseJob(value: unknown): PrintJob {
  const j = value as PrintJob | null;
  if (!j || !id(j.id) || !id(j.planId) || typeof j.name !== 'string' || !j.settings ||
    typeof j.settings.printerId !== 'string' || !states.includes(j.state) ||
    ![undefined, '', 'front', 'back'].includes(j.settings.manualFirstSide) ||
    (j.settings.manualDuplexProfile !== undefined && typeof j.settings.manualDuplexProfile !== 'string') ||
    !['front', 'back', 'single'].includes(j.phase) || typeof j.problem !== 'string' ||
    (j.deviceCancelRequired !== undefined && typeof j.deviceCancelRequired !== 'boolean') ||
    (j.cupsJobState !== undefined && (!Number.isInteger(j.cupsJobState) || j.cupsJobState < 0 || j.cupsJobState > 9)) ||
    !Number.isInteger(j.sheetCount) || j.sheetCount < 1 || !Number.isInteger(j.cupsJobId) ||
    !Array.isArray(j.cupsJobIds) || !j.cupsJobIds.every(n => Number.isInteger(n) && n > 0) ||
    !Number.isFinite(Date.parse(j.createdAt)) || !Number.isFinite(Date.parse(j.updatedAt))) invalid();
  return j;
}
export async function preparePlan(documentId: string, settings: PrintSettings, replacesPlanId?: string): Promise<Plan> {
  return parsePlan(await authorizedRequest('/api/v1/plans', 'POST', {
    documentId, settings, ...(replacesPlanId ? { replacesPlanId } : {}),
  }, 180000));
}
export async function getPlan(planId: string): Promise<Plan> {
  if (!id(planId)) invalid();
  return parsePlan(await authorizedRequest(`/api/v1/plans/${planId}`));
}
export async function submitPlan(plan: Plan): Promise<PrintJob> {
  const address = getConnection()?.address;
  if (!address) throw new ServiceError('session_expired', '打印授权已过期，请重新扫码');
  const storageKey = `printbridge.submission.${plan.id}`;
  const previous = wx.getStorageSync(storageKey) as { key?: string; address?: string } | null;
  const key = previous?.address === address && typeof previous.key === 'string' &&
    /^[A-Za-z0-9_-]{16,128}$/.test(previous.key) ? previous.key :
    `${Date.now().toString(36)}-${Math.random().toString(36).slice(2)}-${Math.random().toString(36).slice(2)}`;
  // Persist before the request so a timeout or page reload reuses the same submission.
  wx.setStorageSync(storageKey, { key, address, expiresAt: plan.expiresAt });
  return parseJob(await authorizedRequest('/api/v1/jobs', 'POST', { planId: plan.id, idempotencyKey: key }, 180000));
}
export async function listJobs(activeOnly = false): Promise<PrintJob[]> {
  const result = await authorizedRequest(`/api/v1/jobs${activeOnly ? '?active=true' : ''}`) as { jobs?: unknown[] } | null;
  if (!result || !Array.isArray(result.jobs)) invalid();
  return result.jobs.map(parseJob);
}
export async function getJob(jobId: string): Promise<PrintJob> {
  if (!id(jobId)) invalid();
  return parseJob(await authorizedRequest(`/api/v1/jobs/${jobId}`));
}
export async function cancelJob(jobId: string): Promise<PrintJob> {
  if (!id(jobId)) invalid();
  return parseJob(await authorizedRequest(`/api/v1/jobs/${jobId}/cancel`, 'POST', {}));
}
export async function confirmCancel(jobId: string): Promise<PrintJob> {
  if (!id(jobId)) invalid();
  return parseJob(await authorizedRequest(`/api/v1/jobs/${jobId}/confirm-cancel`, 'POST', { deviceStopped: true }));
}
export async function continueJob(jobId: string): Promise<PrintJob> {
  if (!id(jobId)) invalid();
  return parseJob(await authorizedRequest(`/api/v1/jobs/${jobId}/continue`, 'POST', { paperConfirmed: true }));
}
export function terminal(job: PrintJob): boolean { return ['completed', 'canceled', 'failed'].includes(job.state); }
export function jobStateLabel(state: string, cupsJobState?: number): string {
  if (state === 'printing' && [3, 4].includes(cupsJobState || 0)) return '等待打印';
  const labels: Record<string, string> = {
    queued: '等待打印', creating: '正在提交', created: '正在提交', sending: '正在发送文件',
    releasing: '正在开始打印', printing: '正在打印',
    awaiting_flip: '等待翻纸', canceling: '正在取消', completed: '队列报告完成', canceled: '已取消', failed: '打印失败',
  };
  return labels[state] || state;
}
export function jobLabel(job: PrintJob): string {
  if (job.state === 'printing' && [3, 4].includes(job.cupsJobState || 0)) return '等待打印';
  const pass = job.phase === (job.settings.manualFirstSide || 'front') ? '第一轮' : '第二轮';
  if (job.state === 'printing' && job.phase !== 'single') return `正在打印${pass}`;
  return jobStateLabel(job.state, job.cupsJobState);
}
export function problemLabel(job: PrintJob): string {
  const labels: Record<string, string> = {
    submission_uncertain: '正在核对提交结果，请勿重新创建任务。可取消当前任务；若一直无法核对，请联系部署者。',
    cups_unavailable: '暂时无法读取打印队列，恢复连接后将自动核对。',
    cancel_uncertain: '取消结果尚未确认，正在核对队列。',
    printer_busy: '打印机仍在处理上一任务，结束后才能继续。',
    device_cancel_required: '正在清理打印机上的旧任务，请稍候。',
    device_cancel_failed: '暂时无法确认打印机已清理，正在重试。请等待取消完成；持续失败请联系管理员。',
    printer_stopped: '打印队列已暂停，请检查纸张、墨水和打印机状态。',
    settings_rejected: '打印队列没有完整接受所选参数，任务已停止，请调整设置后重新预览。',
  };
  return labels[job.problem] || (job.problem ? '任务需要处理，请联系部署者' : '');
}
export function paperLabel(media: string): string {
  const names: Record<string, string> = { a4: 'A4', a5: 'A5', a6: 'A6',
    b5: 'B5 · 182 × 257 mm', isob5: 'B5（ISO） · 176 × 250 mm',
    b6: 'B6 · 128 × 182 mm', isob6: 'B6（ISO） · 125 × 176 mm', letter: 'Letter', legal: 'Legal',
    'iso_a4_210x297mm': 'A4 · 210 × 297 mm', 'iso_a5_148x210mm': 'A5 · 148 × 210 mm',
    'iso_a6_105x148mm': 'A6 · 105 × 148 mm',
    'jis_b5_182x257mm': 'B5 · 182 × 257 mm', 'iso_b5_176x250mm': 'B5（ISO） · 176 × 250 mm',
    'jis_b6_128x182mm': 'B6 · 128 × 182 mm', 'iso_b6_125x176mm': 'B6（ISO） · 125 × 176 mm',
    'na_letter_8.5x11in': 'Letter · 8.5 × 11 in', 'na_legal_8.5x14in': 'Legal · 8.5 × 14 in' };
  return names[media.toLowerCase()] || media || '默认纸张（A4 排版）';
}
const commonPaperSizes = [
  ['a4', 'iso_a4_210x297mm'],
  ['a5', 'iso_a5_148x210mm'],
  ['b5', 'jis_b5_182x257mm'],
  ['isob5', 'iso_b5_176x250mm'],
  ['a6', 'iso_a6_105x148mm'],
  ['b6', 'jis_b6_128x182mm'],
  ['isob6', 'iso_b6_125x176mm'],
  ['letter', 'na_letter_8.5x11in'],
  ['legal', 'na_legal_8.5x14in'],
];
export function paperOptions(printer: Printer): { value: string; label: string }[] {
  const c = printer.capabilities;
  const values = [...new Set([c.defaultMedia || c.media[0] || '', ...c.media])];
  const priority = (value: string): number => {
    const index = commonPaperSizes.findIndex(names => names.includes(value.toLowerCase()));
    return index < 0 ? commonPaperSizes.length : index;
  };
  return values.sort((a, b) => priority(a) - priority(b)).map(value => ({ value, label: paperLabel(value) }));
}
export function colorLabel(color: string): string {
  return ({ color: '彩色', monochrome: '黑白', auto: '自动', 'auto-monochrome': '自动黑白' } as Record<string, string>)[color] || color || '打印机默认';
}
