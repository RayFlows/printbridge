import type { EntryApp } from '../../app';
import { getConnection } from '../../services/connection';
import { errorMessage, isSessionExpired } from '../../services/errors';
import { PrintJob, getJob, continueJob, cancelJob, terminal, jobLabel, problemLabel } from '../../services/printing';
import { listPrinters } from '../../services/printers';
import { paperEmpty, taskPrinterNotice } from '../printers/presentation';

function present(job: PrintJob) {
  return { ...job, label: jobLabel(job), problemLabel: problemLabel(job), terminal: terminal(job),
    dateLabel: new Date(job.createdAt).toLocaleString(),
    illustratedReload: job.settings.manualDuplexProfile === 'rear-face-up-v1' };
}
type Row = ReturnType<typeof present>;
Page({
  data: {
    loading: true, refreshing: false, acting: false, error: '', paperConfirmed: false,
    job: null as Row | null, printerNotice: '',
  },
  alive: true,
  visible: false,
  fetching: false,
  viewRevision: 0,
  paperAlerted: false,
  paperAlertOpen: false,
  jobId: '',
  timer: null as ReturnType<typeof setTimeout> | null,
  onLoad(options: Record<string, string | undefined>) {
    this.alive = true;
    this.jobId = options.id || '';
    if (!this.jobId) wx.switchTab({ url: '/pages/printers/index' });
  },
  onShow() {
    if (getApp<EntryApp>().pendingEntry || !this.jobId) return;
    if (!getConnection()) { wx.reLaunch({ url: '/pages/connect/index?expired=1' }); return; }
    this.visible = true;
    this.viewRevision++;
    void this.refresh();
  },
  onHide() { this.visible = false; this.viewRevision++; this.stop(); },
  onUnload() { this.alive = false; this.visible = false; this.viewRevision++; this.stop(); },
  stop() { if (this.timer) clearTimeout(this.timer); this.timer = null; },
  schedule() {
    this.stop();
    if (!this.alive || !this.visible) return;
    if (this.jobId && this.data.job?.terminal) return;
    this.timer = setTimeout(() => { this.timer = null; void this.refresh(); }, 2500);
  },
  async refresh() {
    if (this.fetching || this.data.acting || !this.alive || !this.jobId) return;
    this.fetching = true;
    const revision = this.viewRevision;
    this.stop();
    this.setData({ refreshing: true });
    try {
      const job = await getJob(this.jobId);
      if (this.alive) this.setData({ job: present(job), error: '', printerNotice: '',
        paperConfirmed: job.state === 'awaiting_flip' ? this.data.paperConfirmed : false });
      if (!terminal(job) && job.state !== 'awaiting_flip' && this.alive && this.visible &&
        revision === this.viewRevision) {
        const printers = await listPrinters();
        if (!this.alive || !this.visible || revision !== this.viewRevision) return;
        const printer = printers.find(p => p.id === job.settings.printerId);
        const waiting = job.state === 'printing' && [3, 4].includes(job.cupsJobState || 0);
        if (printer) this.setData({ printerNotice: taskPrinterNotice(printer, waiting ? 'waiting' : job.state) });
        // Old services omit the snapshot; upgraded services explicitly report
        // zero until CUPS has confirmed this job's own processing state.
        const processing = job.cupsJobState === undefined || [5, 6].includes(job.cupsJobState);
        if (job.state !== 'printing' || !job.cupsJobId || !processing) return;
        if (printer && printer.deviceState !== 'disconnected' && printer.state !== 'unknown') {
          const empty = paperEmpty(printer);
          if (empty) this.showPaperAlert();
          else if (printer.state === 'idle' || printer.state === 'processing') this.paperAlerted = false;
        }
      }
    } catch (error) {
      if (this.alive) {
        this.setData({ error: errorMessage(error) });
        if (isSessionExpired(error)) { this.visible = false; wx.reLaunch({ url: '/pages/connect/index?expired=1' }); }
      }
    } finally {
      this.fetching = false;
      if (this.alive) this.setData({ loading: false, refreshing: false });
      this.schedule();
    }
  },
  onRefresh() { return this.refresh(); },
  showPaperAlert() {
    if (this.paperAlerted || this.paperAlertOpen || this.data.acting || !this.alive || !this.visible) return;
    this.paperAlerted = true;
    this.paperAlertOpen = true;
    wx.showModal({
      title: '打印机缺纸',
      content: '请补纸，并按打印机面板提示继续。无需重新提交当前任务。',
      showCancel: false, confirmText: '知道了',
      success: () => { this.paperAlertOpen = false; },
      fail: () => { this.paperAlertOpen = false; this.paperAlerted = false; },
    });
  },
  onHome() { wx.switchTab({ url: '/pages/printers/index' }); },
  onPaper(event: WechatMiniprogram.CheckboxGroupChange) {
    this.setData({ paperConfirmed: event.detail.value.includes('ready') });
  },
  onContinue() {
    if (!this.data.paperConfirmed || this.data.job?.state !== 'awaiting_flip') return;
    return this.act('continue');
  },
  onCancel() { return this.act('cancel'); },
  async act(action: 'continue' | 'cancel') {
    const job = this.data.job;
    if (!job || this.data.acting || this.fetching || this.paperAlertOpen || job.terminal) return;
    this.stop();
    this.setData({ acting: true, error: '' });
    try {
      if (action === 'cancel') {
        const confirmed = await new Promise<boolean>((resolve, reject) => wx.showModal({
          title: '取消打印', content: '已输出的纸张无法撤回。确认取消剩余打印？',
          confirmText: '取消任务', success: result => resolve(result.confirm), fail: reject,
        }));
        if (!confirmed || !this.alive) return;
      }
      const updated = action === 'continue' ? await continueJob(job.id) : await cancelJob(job.id);
      if (this.alive) this.setData({ job: present(updated), paperConfirmed: false, printerNotice: '' });
    } catch (error) {
      if (this.alive) {
        this.setData({ error: errorMessage(error) });
        if (isSessionExpired(error)) wx.reLaunch({ url: '/pages/connect/index?expired=1' });
      }
    } finally {
      if (this.alive) this.setData({ acting: false });
      this.schedule();
    }
  },
});
