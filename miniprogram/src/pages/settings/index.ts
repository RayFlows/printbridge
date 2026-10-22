import type { EntryApp } from '../../app';
import { DocumentSession, Document, chooseChatFile, choosePhoto, fileSize, fileIcon } from '../../services/documents';
import { getConnection } from '../../services/connection';
import { errorMessage, isSessionExpired, ServiceError } from '../../services/errors';
import { listPrinters } from '../../services/printers';
import { PrintSettings, Plan, defaultSettings, sideOptions, paperOptions, colorLabel,
  preparePlan, getPlan, submitPlan, hasManualDuplex, scalingOptions, layoutOptions,
  mediaTypeLabel, mediaSourceLabel, listJobs, terminal } from '../../services/printing';

const emptySettings: PrintSettings = { printerId: '', media: '', colorMode: '', copies: 1, pages: '',
  subset: 'all', reverse: false, orientation: 'portrait', pagesPerSheet: 1, scalePercent: 100,
  sides: 'one-sided', manualFirstSide: 'back', backOrder: 'same', backRotation: 0, calibrated: false, outputOrder: 'reverse' };
Page({
  data: {
    loading: true, busy: false, preparing: false, error: '', printerName: '', document: null as Document | null,
    settings: emptySettings, plan: null as Plan | null, manual: false, page: 1,
    previewPath: '', previewError: '', previewBusy: false,
    media: [] as { value: string; label: string }[], mediaIndex: 0,
    colors: [] as { value: string; label: string }[], colorIndex: 0,
    sides: sideOptions, sideIndex: 0, orientations: ['纵向', '横向'], orientationIndex: 0,
    subsets: ['全部', '奇数页', '偶数页'], subsetIndex: 0,
    nups: ['1 页', '2 页', '4 页', '6 页', '9 页', '16 页'], nupIndex: 0,
    expanded: false, fileMeta: '', fileIcon: 'file',
    scalings: scalingOptions, scalingIndex: 0, layouts: layoutOptions, layoutIndex: 0,
    mediaTypes: [] as { value: string; label: string }[], mediaTypeIndex: 0,
    mediaSources: [] as { value: string; label: string }[], mediaSourceIndex: 0,
    maxCopies: 99,
    imageDocument: false,
    replacing: false, replacementStatus: '', replacementProgress: 0, replacementError: '',
  },
  alive: true,
  previewGeneration: 0,
  settingsRevision: 0,
  prepareTimer: null as ReturnType<typeof setTimeout> | null,
  requestRunning: false,
  prepareQueued: false,
  replacesPlanId: '',
  documentId: '',
  printerId: '',
  client: null as DocumentSession | null,
  replacementClient: null as DocumentSession | null,
  printerDefaults: emptySettings,

  async onLoad(options: Record<string, string | undefined>) {
    this.alive = true;
    try {
      this.documentId = options.document || '';
      this.printerId = decodeURIComponent(options.printer || '');
      this.client = new DocumentSession();
      await this.load();
    } catch (error) { this.failure(error); }
  },
  onShow() {
    if (getApp<EntryApp>().pendingEntry) return;
    if (!getConnection()) wx.reLaunch({ url: '/pages/connect/index?expired=1' });
  },
  onUnload() {
    this.alive = false;
    this.settingsRevision++;
    this.previewGeneration++;
    this.cancelScheduledPreview();
    this.prepareQueued = false;
    this.client?.dispose();
    this.replacementClient?.dispose();
  },
  failure(error: unknown) {
    if (!this.alive) return;
    this.setData({ error: errorMessage(error) });
    if (isSessionExpired(error)) wx.reLaunch({ url: '/pages/connect/index?expired=1' });
  },
  async load() {
    if (!this.client) return;
    this.setData({ loading: true, error: '' });
    try {
      const [doc, printers] = await Promise.all([this.client.get(this.documentId), listPrinters()]);
      const printer = printers.find(p => p.id === this.printerId);
      if (!printer) throw new Error('打印机已不存在，请返回重新选择');
      const settings = defaultSettings(printer);
      this.printerDefaults = settings;
      const media = paperOptions(printer);
      const colors = [...new Set([settings.colorMode, ...printer.capabilities.colorModes])].map(value => ({ value, label: colorLabel(value) }));
      const mediaTypes = [...new Set(['', ...(printer.capabilities.mediaTypes || []), printer.capabilities.defaultMediaType || ''])]
        .map(value => ({ value, label: mediaTypeLabel(value) }));
      const mediaSources = [...new Set(['', ...(printer.capabilities.mediaSources || []), printer.capabilities.defaultMediaSource || ''])]
        .map(value => ({ value, label: mediaSourceLabel(value) }));
      if (this.alive) {
        this.setData({ printerName: printer.name, media, colors, mediaTypes, mediaSources,
          maxCopies: Math.min(99, printer.capabilities.copies?.max || 99),
          sides: sideOptions.filter(s => s.value.startsWith('manual-') ? hasManualDuplex(printer) :
            !s.value.startsWith('two-sided') || printer.capabilities.sides.includes(s.value)) });
        this.useDocument(doc);
      }
    } catch (error) { this.failure(error); }
    finally { if (this.alive) this.setData({ loading: false }); }
    if (this.alive && this.data.document && !this.data.error) await this.onPrepare();
  },
  useDocument(doc: Document) {
    this.documentId = doc.id;
    this.replacesPlanId = '';
    this.settingsRevision++;
    this.previewGeneration++;
    this.cancelScheduledPreview();
    this.prepareQueued = false;
    this.setData({
      document: doc, settings: { ...this.printerDefaults }, plan: null, page: 1,
      fileMeta: `${fileSize(doc.bytes)} · ${doc.pageCount} 页`, fileIcon: fileIcon(doc.name),
      imageDocument: /\.(jpe?g|png|webp|bmp|tiff?|gif)$/i.test(doc.name),
      previewPath: '', previewError: '', previewBusy: false, preparing: true,
      manual: false, expanded: false, error: '',
      mediaIndex: Math.max(0, this.data.media.findIndex(option => option.value === this.printerDefaults.media)),
      colorIndex: 0, sideIndex: 0, orientationIndex: 0, subsetIndex: 0,
      nupIndex: 0, scalingIndex: 0, layoutIndex: 0, mediaTypeIndex: 0, mediaSourceIndex: 0,
    });
  },
  onRetry() { return this.load(); },
  cancelScheduledPreview() {
    if (this.prepareTimer !== null) clearTimeout(this.prepareTimer);
    this.prepareTimer = null;
  },
  change(patch: Partial<PrintSettings>) {
    if (!this.alive || this.data.busy || this.data.replacing) return;
    this.settingsRevision++;
    this.previewGeneration++;
    this.client?.clearPreviews();
    const settings = { ...this.data.settings, ...patch };
    this.setData({ settings, plan: null, previewPath: '', previewError: '', previewBusy: false,
      preparing: true, error: '', manual: settings.sides.startsWith('manual-') });
    this.cancelScheduledPreview();
    this.prepareQueued = false;
    this.prepareTimer = setTimeout(() => {
      this.prepareTimer = null;
      void this.onPrepare();
    }, 400);
  },
  onOption(event: WechatMiniprogram.PickerChange) {
    const field = String(event.currentTarget.dataset.field);
    const index = Number(event.detail.value);
    if (!Number.isInteger(index) || index < 0) return;
    const choices: Record<string, (string | number)[]> = {
      media: this.data.media.map(v => v.value), colorMode: this.data.colors.map(v => v.value),
      sides: this.data.sides.map(v => v.value), orientation: ['portrait', 'landscape'],
      subset: ['all', 'odd', 'even'], pagesPerSheet: [1, 2, 4, 6, 9, 16],
      mediaType: this.data.mediaTypes.map(v => v.value), mediaSource: this.data.mediaSources.map(v => v.value),
      scaling: scalingOptions.map(v => v.value), layoutOrder: layoutOptions.map(v => v.value),
    };
    const value = choices[field]?.[index];
    if (value === undefined) return;
    const indexFields: Record<string, string> = { media: 'mediaIndex', colorMode: 'colorIndex', sides: 'sideIndex',
      orientation: 'orientationIndex', subset: 'subsetIndex', pagesPerSheet: 'nupIndex',
      mediaType: 'mediaTypeIndex', mediaSource: 'mediaSourceIndex', scaling: 'scalingIndex', layoutOrder: 'layoutIndex' };
    this.change({ [field]: value,
      ...(field === 'pagesPerSheet' && value === 1 ? { pageBorder: false } : {}) });
    if (!this.data.busy && !this.data.replacing) this.setData({ [indexFields[field]]: index });
  },
  onPages(event: WechatMiniprogram.Input) { this.change({ pages: event.detail.value }); },
  onCopies(event: WechatMiniprogram.Input) { this.change({ copies: Number(event.detail.value) }); },
  onCopyStep(event: WechatMiniprogram.TouchEvent) {
    const copies = this.data.settings.copies + Number(event.currentTarget.dataset.step);
    this.change({ copies: Math.min(this.data.maxCopies, Math.max(1, copies)) });
  },
  onScale(event: WechatMiniprogram.Input) { this.change({ scalePercent: Number(event.detail.value) }); },
  onReverse(event: WechatMiniprogram.SwitchChange) {
    this.change({ outputOrder: event.detail.value ? 'reverse' : 'normal' });
  },
  onBorder(event: WechatMiniprogram.SwitchChange) { this.change({ pageBorder: event.detail.value }); },
  onMirror(event: WechatMiniprogram.SwitchChange) { this.change({ mirror: event.detail.value }); },
  onInvert(event: WechatMiniprogram.SwitchChange) { this.change({ invertImages: event.detail.value }); },
  onWatermark(event: WechatMiniprogram.Input) { this.change({ watermark: event.detail.value }); },
  onExpand() { this.setData({ expanded: !this.data.expanded }); },
  async onReplace() {
    if (!this.alive || this.data.loading || this.data.busy || this.data.replacing || !this.data.document) return;
    this.setData({ replacing: true, replacementStatus: 'choosing', replacementProgress: 0, replacementError: '' });
    try {
      const source = await new Promise<number | null>((resolve, reject) => wx.showActionSheet({
        itemList: ['从微信聊天选择', '从相册选择'],
        success: result => resolve(result.tapIndex),
        fail: error => /cancel/i.test(error.errMsg) ? resolve(null) : reject(new Error('未能打开文件来源，请重试')),
      }));
      if (!this.alive || source === null) return;
      const file = await (source === 0 ? chooseChatFile() : choosePhoto());
      if (!this.alive || !file) return;
      const client = new DocumentSession();
      this.replacementClient = client;
      this.setData({ replacementStatus: 'uploading' });
      const doc = await client.upload(file, progress => {
        if (this.alive && this.replacementClient === client) {
          this.setData({ replacementProgress: progress, replacementStatus: progress === 100 ? 'preparing' : 'uploading' });
        }
      });
      if (!this.alive) return;
      const previous = this.client;
      this.client = client;
      this.replacementClient = null;
      this.useDocument(doc);
      previous?.dispose();
      this.setData({ replacementStatus: 'previewing' });
      await this.onPrepare();
    } catch (error) {
      if (this.alive) {
        this.setData({ replacementError: errorMessage(error) });
        if (isSessionExpired(error)) this.failure(error);
      }
    } finally {
      this.replacementClient?.dispose();
      this.replacementClient = null;
      if (this.alive) this.setData({ replacing: false, replacementStatus: '' });
    }
  },
  onSegment(event: WechatMiniprogram.TouchEvent) {
    return this.onOption({ currentTarget: event.currentTarget,
      detail: { value: String(event.currentTarget.dataset.index) } } as WechatMiniprogram.PickerChange);
  },
  async onPrepare() {
    if (!this.alive || this.data.busy || !this.data.document) return;
    this.cancelScheduledPreview();
    // Serialize layout and image work; only the newest debounced settings run next.
    if (this.requestRunning) { this.prepareQueued = true; return; }
    const revision = this.settingsRevision;
    const documentId = this.documentId;
    const s = { ...this.data.settings };
    if (!Number.isInteger(s.copies) || s.copies < 1 || s.copies > this.data.maxCopies ||
      !Number.isInteger(s.scalePercent) || s.scalePercent < 10 || s.scalePercent > 400) {
      this.setData({ preparing: false, error: `份数须为 1–${this.data.maxCopies}；缩放须为 10–400 的整数` }); return;
    }
    if (this.data.manual && !s.manualDuplexProfile) {
      this.setData({ preparing: false, error: '这台打印机尚未配置翻纸方式，请联系管理员' }); return;
    }
    this.requestRunning = true;
    this.setData({ preparing: true, error: '', previewError: '', plan: null, previewPath: '' });
    try {
      const plan = await preparePlan(documentId, s, this.replacesPlanId);
      if (!this.alive || documentId !== this.documentId) return;
      this.replacesPlanId = plan.id;
      if (revision !== this.settingsRevision) return;
      this.setData({ plan, page: 1 });
      await this.downloadPreview(plan, 1);
    } catch (error) {
      if (this.alive && revision === this.settingsRevision) {
        this.setData({ previewError: errorMessage(error) });
        if (isSessionExpired(error)) this.failure(error);
      }
    } finally {
      if (this.alive && revision === this.settingsRevision) this.setData({ preparing: false });
      this.finishPreviewRequest();
    }
  },
  finishPreviewRequest() {
    this.requestRunning = false;
    if (this.alive && this.prepareQueued && this.prepareTimer === null) {
      this.prepareQueued = false;
      void this.onPrepare();
    }
  },
  async showPage(page: number) {
    const plan = this.data.plan;
    if (!plan || this.data.busy || this.data.replacing || this.data.preparing || this.requestRunning ||
      page < 1 || page > (plan.previewPageCount ?? plan.pageCount)) return;
    this.requestRunning = true;
    try { await this.downloadPreview(plan, page); }
    finally { this.finishPreviewRequest(); }
  },
  async downloadPreview(plan: Plan, page: number) {
    const client = this.client;
    if (!client) return;
    const generation = ++this.previewGeneration;
    this.setData({ page, previewPath: '', previewError: '', previewBusy: true });
    try {
      const path = await client.preview(plan, page, 'plans', 'reading');
      if (this.alive && generation === this.previewGeneration) this.setData({ previewPath: path });
      else client.clearPreviews();
    } catch (error) {
      if (this.alive && generation === this.previewGeneration) {
        this.setData({ previewError: errorMessage(error) });
        if (isSessionExpired(error)) this.failure(error);
      }
    } finally { if (this.alive && generation === this.previewGeneration) this.setData({ previewBusy: false }); }
  },
  onPrevious() { if (this.data.previewBusy) return; return this.showPage(this.data.page - 1); },
  onNext() { if (this.data.previewBusy) return; return this.showPage(this.data.page + 1); },
  onPreviewRetry() {
    if (this.data.busy || this.data.replacing || this.data.preparing || this.data.previewBusy) return;
    this.client?.clearPreviews();
    return this.data.plan ? this.showPage(this.data.page) : this.onPrepare();
  },
  onImageError() { this.setData({ previewPath: '', previewError: '预览图片未能显示，请重试' }); },
  async onZoom() {
    const plan = this.data.plan;
    const path = this.data.previewPath;
    if (!plan || !path || this.data.busy || this.data.replacing || this.data.preparing || this.data.previewBusy) return;
    try {
      await getPlan(plan.id);
      if (this.alive && !this.data.replacing && this.data.plan?.id === plan.id && this.data.previewPath === path) {
        wx.previewImage({ current: path, urls: [path], showmenu: false });
      }
    } catch (error) { this.failure(error); }
  },
  async onPrint() {
    const plan = this.data.plan;
    if (!this.alive || !plan || !this.data.previewPath || this.data.busy || this.data.replacing || this.data.preparing ||
      this.data.previewBusy || this.requestRunning) return;
    this.setData({ busy: true, error: '' });
    try {
      const confirmed = await new Promise<boolean>((resolve, reject) => wx.showModal({
        title: '确认打印', content: `${plan.name}\n${plan.sheetCount} 张纸 · ${plan.settings.copies} 份`,
        confirmText: '开始打印', success: result => resolve(result.confirm), fail: reject,
      }));
      if (!confirmed || !this.alive) return;
      const job = await submitPlan(plan);
      if (this.alive) wx.redirectTo({ url: `/pages/jobs/index?id=${job.id}` });
    } catch (error) {
      this.failure(error);
      if (this.alive && error instanceof ServiceError && error.code === 'active_print_exists') {
        try {
          const jobs = await listJobs(true);
          const current = jobs.filter(job => !terminal(job) && job.settings.printerId === plan.settings.printerId)
            .sort((a, b) => Date.parse(a.createdAt) - Date.parse(b.createdAt))[0];
          if (this.alive && current) wx.redirectTo({ url: `/pages/jobs/index?id=${current.id}` });
        } catch (lookupError) { this.failure(lookupError); }
      }
    }
    finally { if (this.alive) this.setData({ busy: false }); }
  },
});
