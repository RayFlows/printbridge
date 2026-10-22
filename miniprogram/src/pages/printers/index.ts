import type { EntryApp } from '../../app';
import { getConnection, saveConnection } from '../../services/connection';
import { errorMessage, isSessionExpired } from '../../services/errors';
import { listPrinters } from '../../services/printers';
import { listJobs, jobLabel, terminal } from '../../services/printing';
import { presentPrinter, PrinterRow } from './presentation';
import { getProfile } from '../../services/accounts';

type Card = PrinterRow & { activeJob: { id: string; name: string; label: string; sheetCount: number; actionLabel: string } | null };

Page({
  data: {
    status: 'loading',
    printers: [] as Card[],
    error: '',
    actionError: '',
    tasksKnown: false,
    statusError: '',
    printingEnabled: true,
    identifying: false,
  },
  alive: true,
  loading: false,
  visible: false,
  refreshTimer: null as ReturnType<typeof setTimeout> | null,
  identifyingProfile: false,
  profileKnown: false,

  onLoad() { this.alive = true; },
  onShow() {
    this.getTabBar?.()?.setSelected(0);
    this.visible = true;
    if (getApp<EntryApp>().pendingEntry) return;
    const connection = getConnection();
    if (!connection) {
      wx.reLaunch({ url: '/pages/connect/index?expired=1' });
      return;
    }
    if (connection.user && !this.identifyingProfile) void this.identify();
    void this.refresh();
  },
  onHide() { this.visible = false; this.stopRefresh(); },
  onUnload() { this.alive = false; this.visible = false; this.stopRefresh(); },
  stopRefresh() {
    if (this.refreshTimer !== null) clearTimeout(this.refreshTimer);
    this.refreshTimer = null;
  },
  scheduleRefresh() {
    this.stopRefresh();
    if (this.alive && this.visible && getConnection()) this.refreshTimer = setTimeout(() => {
      this.refreshTimer = null;
      void this.refresh(true);
    }, 5000);
  },
  onPullDownRefresh() { this.onRefresh(); },
  onRefresh() {
    if (getConnection()?.user && !this.identifyingProfile) void this.identify();
    void this.refresh();
  },
  onScan() { wx.reLaunch({ url: '/pages/connect/index?scan=1' }); },
  onInfo(event: WechatMiniprogram.TouchEvent) {
    wx.navigateTo({ url: `/pages/printer-info/index?printer=${encodeURIComponent(String(event.currentTarget.dataset.id))}` });
  },
  onSelect(event: WechatMiniprogram.TouchEvent) {
    const printer = this.data.printers.find(item => item.id === String(event.currentTarget.dataset.id));
    if (printer?.activeJob) {
      wx.navigateTo({ url: `/pages/jobs/index?id=${printer.activeJob.id}` });
      return;
    }
    if (printer && this.data.tasksKnown && this.data.printingEnabled &&
      (!getConnection()?.user || this.profileKnown)) wx.navigateTo({
      url: `/pages/document/index?printer=${encodeURIComponent(printer.id)}&name=${encodeURIComponent(printer.name || printer.id)}`,
    });
  },
  async identify() {
    this.identifyingProfile = true;
    this.profileKnown = false;
    this.setData({ printingEnabled: false, identifying: true });
    const connection = getConnection();
    try {
      const profile = await getProfile();
      if (!this.alive || !this.visible || !connection ||
        getConnection()?.token !== connection.token || getConnection()?.address !== connection.address) return;
      this.profileKnown = true;
      saveConnection({ ...connection, user: profile.user });
      this.setData({ printingEnabled: profile.settings.printingEnabled });
    } catch (error) {
      if (this.alive && this.visible && (!getConnection() ||
        (getConnection()?.token === connection?.token && getConnection()?.address === connection?.address))) {
        this.setData({ actionError: errorMessage(error) });
        if (isSessionExpired(error)) wx.reLaunch({ url: '/pages/connect/index?expired=1' });
      }
    } finally { this.identifyingProfile = false; if (this.alive) this.setData({ identifying: false }); }
  },

  async refresh(silent = false) {
    if (this.loading) {
      wx.stopPullDownRefresh();
      return;
    }
    this.loading = true;
    this.stopRefresh();
    if (!silent) this.setData({ status: 'loading', error: '', actionError: '' });
    try {
      const [printers, jobs] = await Promise.all([listPrinters(), listJobs(true)]);
      const active = jobs.filter(job => !terminal(job)).sort((a, b) => Date.parse(a.createdAt) - Date.parse(b.createdAt));
      if (this.alive) {
        this.setData({
          printers: printers.map(printer => {
            const job = active.find(job => job.settings.printerId === printer.id);
            return { ...presentPrinter(printer), activeJob: job ?
              { id: job.id, name: job.name, label: jobLabel(job), sheetCount: job.sheetCount,
                actionLabel: job.state === 'awaiting_flip' ? '继续任务' : '查看进度' } : null };
          }),
          status: printers.length ? 'ready' : 'empty',
          tasksKnown: true,
          statusError: '',
        });
      }
    } catch (error) {
      if (this.alive) {
        this.setData({ tasksKnown: false });
        if (isSessionExpired(error)) wx.reLaunch({ url: '/pages/connect/index?expired=1' });
        else if (silent) this.setData({ statusError: `状态更新失败：${errorMessage(error)}` });
        else this.setData({ status: 'error', error: errorMessage(error) });
      }
    } finally {
      this.loading = false;
      wx.stopPullDownRefresh();
      this.scheduleRefresh();
    }
  },
});
