import type { EntryApp } from '../../app';
import { getConnection } from '../../services/connection';
import { errorMessage, isSessionExpired } from '../../services/errors';
import { listPrinters } from '../../services/printers';
import { presentPrinter, PrinterRow } from '../printers/presentation';

Page({
  data: { loading: true, refreshing: false, error: '', printer: null as PrinterRow | null },
  alive: true,
  visible: false,
  fetching: false,
  printerId: '',
  timer: null as ReturnType<typeof setTimeout> | null,
  onLoad(options: Record<string, string | undefined>) {
    this.alive = true;
    try { this.printerId = decodeURIComponent(options.printer || ''); }
    catch { this.printerId = ''; }
  },
  onShow() {
    if (getApp<EntryApp>().pendingEntry) return;
    if (!getConnection()) { wx.reLaunch({ url: '/pages/connect/index?expired=1' }); return; }
    this.visible = true;
    void this.refresh();
  },
  onHide() { this.visible = false; this.stop(); },
  onUnload() { this.alive = false; this.visible = false; this.stop(); },
  stop() { if (this.timer !== null) clearTimeout(this.timer); this.timer = null; },
  onRefresh() { return this.refresh(); },
  async refresh() {
    if (!this.alive || this.fetching) return;
    this.fetching = true;
    this.stop();
    this.setData({ refreshing: true });
    try {
      const printers = await listPrinters();
      if (!this.alive) return;
      const printer = printers.find(printer => printer.id === this.printerId);
      this.setData({ printer: printer ? presentPrinter(printer) : null,
        error: printer ? '' : '这台打印机已不可用，请返回重新选择' });
    } catch (error) {
      if (this.alive) {
        this.setData({ error: errorMessage(error) });
        if (isSessionExpired(error)) { this.visible = false; wx.reLaunch({ url: '/pages/connect/index?expired=1' }); }
      }
    } finally {
      this.fetching = false;
      if (this.alive) this.setData({ loading: false, refreshing: false });
      if (this.alive && this.visible) this.timer = setTimeout(() => { this.timer = null; void this.refresh(); }, 5000);
    }
  },
});
