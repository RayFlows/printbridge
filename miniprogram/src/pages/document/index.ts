import type { EntryApp } from '../../app';
import { getConnection } from '../../services/connection';
import { ChosenFile, chooseChatFile, choosePhoto, Document, DocumentSession, fileSize, fileIcon } from '../../services/documents';
import { errorMessage, isSessionExpired } from '../../services/errors';

Page({
  data: {
    printerId: '',
    printerName: '',
    status: 'empty',
    fileName: '',
    fileSize: '',
    fileIcon: 'file',
    document: null as Document | null,
    error: '',
    progress: 0,
    choosing: false,
  },
  alive: true,
  busy: false,
  generation: 0,
  navigating: false,
  client: null as DocumentSession | null,
  selected: null as ChosenFile | null,

  onLoad(options: Record<string, string | undefined>) {
    this.alive = true;
    try {
      this.setData({ printerId: decodeURIComponent(options.printer || ''), printerName: decodeURIComponent(options.name || options.printer || '') });
      this.client = new DocumentSession();
    } catch {
      wx.reLaunch({ url: '/pages/connect/index?expired=1' });
    }
  },

  onShow() {
    if (getApp<EntryApp>().pendingEntry) return;
    if (!getConnection()) this.expired();
  },

  onUnload() {
    this.alive = false;
    this.generation++;
    this.client?.dispose();
    this.client = null;
    this.selected = null;
  },

  expired() {
    this.client?.dispose();
    wx.reLaunch({ url: '/pages/connect/index?expired=1' });
  },

  onChoose() { return this.selectFile(chooseChatFile); },
  onChoosePhoto() { return this.selectFile(choosePhoto); },

  async selectFile(picker: () => Promise<ChosenFile | null>) {
    if (!this.alive || this.busy || this.data.choosing || this.navigating || !this.client) return;
    this.setData({ choosing: true, error: '' });
    try {
      const file = await picker();
      if (!file || !this.alive) return;
      this.selected = file;
      this.generation++;
      this.setData({
        document: null, fileName: file.name, fileSize: fileSize(file.size), fileIcon: fileIcon(file.name),
      });
      await this.upload();
    } catch (error) {
      if (this.alive) this.setData({ error: errorMessage(error) });
    } finally {
      if (this.alive) this.setData({ choosing: false });
    }
  },

  async upload() {
    if (!this.alive || this.busy || this.navigating || !this.selected || !this.client) return;
    this.busy = true;
    const generation = this.generation;
    this.setData({ status: 'uploading', error: '', progress: 0 });
    try {
      const doc = await this.client.upload(this.selected, value => {
        if (this.alive && generation === this.generation) {
          this.setData({ progress: value, status: value === 100 ? 'preparing' : 'uploading' });
        }
      });
      if (!this.alive || generation !== this.generation) return;
      this.setData({ document: doc, status: 'ready', fileName: doc.name, fileSize: fileSize(doc.bytes) });
    } catch (error) {
      if (this.alive && generation === this.generation) {
        this.setData({ status: 'error', error: errorMessage(error) });
        if (isSessionExpired(error)) this.expired();
      }
    } finally {
      this.busy = false;
    }
    if (this.alive && this.data.status === 'ready') this.openSettings();
  },

  onRetryUpload() {
    if (!this.alive || this.data.choosing || this.busy || this.navigating) return;
    if (this.data.document) return this.openSettings();
    return this.upload();
  },
  openSettings() {
    if (!this.alive || !this.data.document || this.busy || this.navigating) return;
    this.navigating = true;
    wx.redirectTo({
      url: `/pages/settings/index?document=${this.data.document.id}&printer=${encodeURIComponent(this.data.printerId)}`,
      fail: () => {
        if (!this.alive) return;
        this.navigating = false;
        this.setData({ status: 'error', error: '未能打开打印设置，请重试' });
      },
    });
  },
});
