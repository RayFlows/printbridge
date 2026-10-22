import type { EntryApp } from '../../app';
import { createConnection, getConnection, revokeConnection, saveConnection } from '../../services/connection';
import { EntryQuery, PrintEntry, entryFromQuery, hasEntryQuery, resolveEntryServer, scanPrintEntry } from '../../services/entry';
import { errorMessage } from '../../services/errors';
import { requirePrivacyConsent } from '../../services/privacy';

Page({
  data: {
    busy: false,
    scanning: false,
    error: '',
    canRetry: false,
    hasTarget: false,
  },
  alive: true,
  entryHandled: false,
  attempt: 0,
  target: null as PrintEntry | null,

  async onLoad(options: Record<string, string | undefined>) {
    this.alive = true;
    const query = getApp<EntryApp>().takeEntry() ?? options;
    if (hasEntryQuery(query)) await this.openQuery(query);
    else if (options.scan === '1') await this.onScan();
    else if (options.expired === '1') {
      this.entryHandled = true;
      this.setData({ error: '打印授权已过期，请重新扫码' });
    }
  },

  async onShow() {
    const query = getApp<EntryApp>().takeEntry();
    if (query) await this.openQuery(query);
    else if (!this.entryHandled && getConnection()) wx.switchTab({ url: '/pages/printers/index' });
  },

  onUnload() {
    this.alive = false;
    this.attempt++;
    this.target = null;
  },

  async openQuery(query: EntryQuery) {
    this.entryHandled = true;
    this.attempt++;
    this.target = null;
    try {
      await this.openEntry(entryFromQuery(query));
    } catch (error) {
      this.setData({ busy: false, scanning: false, canRetry: false, hasTarget: false, error: errorMessage(error) });
    }
  },

  async onScan() {
    if (this.data.busy || this.data.scanning) return;
    this.entryHandled = true;
    this.target = null;
    const attempt = ++this.attempt;
    this.setData({ scanning: true, canRetry: false, hasTarget: false, error: '' });
    let entry: PrintEntry | null = null;
    try {
      entry = await scanPrintEntry();
    } catch (error) {
      if (this.alive && attempt === this.attempt) this.setData({ error: errorMessage(error) });
      return;
    } finally {
      if (this.alive && attempt === this.attempt) this.setData({ scanning: false });
    }
    if (!this.alive || attempt !== this.attempt) return;
    if (entry) await this.openEntry(entry);
    else if (getConnection()) wx.switchTab({ url: '/pages/printers/index' });
  },

  async openEntry(entry: PrintEntry) {
    this.attempt++;
    this.target = entry;
    this.setData({ busy: false, scanning: false, error: '', canRetry: false, hasTarget: true });
    await this.authorizeEntry();
  },

  onRetry() {
    return this.authorizeEntry();
  },

  async authorizeEntry() {
    if (!this.alive || this.data.busy || this.data.scanning || !this.target) return;
    const entry = this.target;
    const attempt = ++this.attempt;
    this.setData({ busy: true, scanning: false, error: '', canRetry: false });
    try {
      await requirePrivacyConsent();
      if (!this.alive || attempt !== this.attempt) return;
      const server = await resolveEntryServer(entry);
      if (!this.alive || attempt !== this.attempt) return;
      const connection = await createConnection(server, entry.pairingCode);
      if (!this.alive || attempt !== this.attempt) {
        // A late authorization must neither overwrite the new printer nor consume a session slot.
        await revokeConnection(connection).catch(() => undefined);
        return;
      }
      const previous = getConnection();
      saveConnection(connection);
      this.target = null;
      this.setData({ hasTarget: false });
      wx.switchTab({ url: '/pages/printers/index' });
      if (previous && (previous.address !== connection.address || previous.token !== connection.token)) {
        await revokeConnection(previous).catch(() => undefined);
      }
    } catch (error) {
      if (this.alive && attempt === this.attempt) this.setData({ error: errorMessage(error), canRetry: true });
    } finally {
      if (this.alive && attempt === this.attempt) this.setData({ busy: false });
    }
  },
});
