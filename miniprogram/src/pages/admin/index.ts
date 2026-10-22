import { getConnection } from '../../services/connection';
import { banUser, cancelAdminTask, configurePrinting, getDashboard, getProfile, listUsers, User } from '../../services/accounts';
import { errorMessage, isSessionExpired } from '../../services/errors';
import { presentPrinter, PrinterRow } from '../printers/presentation';
import { AdminTask } from '../../services/accounts';
import { jobStateLabel } from '../../services/printing';

type TaskRow = AdminTask & { label: string; shortId: string };
type UserRow = User & { shortId: string };
Page({
  data: {
    loading: true, acting: false, error: '', tab: 'overview', printingEnabled: true, allowed: false,
    printerStatusAvailable: true,
    printers: [] as PrinterRow[], tasks: [] as TaskRow[], users: [] as User[], filteredUsers: [] as UserRow[],
    search: '', userCount: 0, bannedCount: 0, unnamedCount: 0,
  },
  alive: true,
  fetching: false,
  visible: false,
  revision: 0,
  timer: null as ReturnType<typeof setTimeout> | null,
  onLoad() { this.alive = true; },
  onShow() { this.visible = true; void this.refresh(); },
  onHide() { this.visible = false; this.revision++; this.stop(); },
  onUnload() { this.alive = false; this.visible = false; this.revision++; this.stop(); },
  stop() { if (this.timer !== null) clearTimeout(this.timer); this.timer = null; },
  schedule() {
    this.stop();
    if (this.alive && this.visible && this.data.allowed) this.timer = setTimeout(() => { void this.refresh(true); }, 5000);
  },
  async refresh(silent = false) {
    if (this.fetching || this.data.acting) return;
    if (!getConnection()) { wx.reLaunch({ url: '/pages/connect/index?expired=1' }); return; }
    this.fetching = true;
    const revision = ++this.revision;
    this.stop();
    if (!silent) this.setData({ loading: true, error: '', allowed: false });
    try {
      const profile = await getProfile();
      if (!this.alive || revision !== this.revision) return;
      if (!profile.user.isAdmin) {
        this.setData({ allowed: false, users: [], filteredUsers: [], printers: [], tasks: [], error: '仅打印机管理员可访问' });
        return;
      }
      const [dashboard, users] = await Promise.all([getDashboard(), listUsers()]);
      if (!this.alive || revision !== this.revision) return;
      this.setData({
        allowed: true, printingEnabled: dashboard.settings.printingEnabled,
        printerStatusAvailable: dashboard.printerStatusAvailable,
        printers: dashboard.printers.map(presentPrinter),
        tasks: dashboard.tasks.map(task => ({ ...task, label: jobStateLabel(task.state, task.cupsJobState), shortId: task.id.slice(-8) })),
        users, userCount: users.length, bannedCount: users.filter(user => user.banned).length,
        unnamedCount: users.filter(user => !user.name).length, error: '',
      });
      this.filter();
    } catch (error) {
      if (this.alive && revision === this.revision) {
        this.setData({ error: errorMessage(error), allowed: false, users: [], filteredUsers: [], printers: [], tasks: [] });
        if (isSessionExpired(error)) wx.reLaunch({ url: '/pages/connect/index?expired=1' });
      }
    } finally {
      this.fetching = false;
      if (this.alive && revision === this.revision) this.setData({ loading: false });
      this.schedule();
    }
  },
  onRefresh() { return this.refresh(); },
  onTab(event: WechatMiniprogram.CustomEvent<{ value: string }>) {
    const tab = event.detail.value;
    if (tab === 'users' || tab === 'overview') this.setData({ tab });
  },
  onSearch(event: WechatMiniprogram.CustomEvent<{ value: string }>) { this.setData({ search: event.detail.value }); this.filter(); },
  filter() {
    const search = this.data.search.trim().toLowerCase();
    this.setData({ filteredUsers: this.data.users.filter(user =>
      `${user.name} ${user.id}`.toLowerCase().includes(search)).map(user => ({ ...user, shortId: user.id.slice(-8) })) });
  },
  onInfo(event: WechatMiniprogram.TouchEvent) {
    wx.navigateTo({ url: `/pages/printer-info/index?printer=${encodeURIComponent(String(event.currentTarget.dataset.id))}` });
  },
  async onPrinting(event: WechatMiniprogram.SwitchChange) {
    const enabled = event.detail.value;
    return this.act(async () => {
      const settings = await configurePrinting(enabled);
      if (this.alive) this.setData({ printingEnabled: settings.printingEnabled });
    });
  },
  async onBan(event: WechatMiniprogram.TouchEvent) {
    const user = this.data.users.find(user => user.id === String(event.currentTarget.dataset.id));
    if (!user || user.isAdmin) return;
    return this.act(async () => {
      const result = await new Promise<WechatMiniprogram.ShowModalSuccessCallbackResult>(resolve => wx.showModal({
        title: user.banned ? '解除封禁' : '封禁用户',
        content: `${user.name || '未设置昵称'}（${user.id.slice(-8)}）${user.banned ? '将恢复打印权限。' : '将无法访问打印服务。已提交任务需在看板单独取消。'}`,
        confirmText: user.banned ? '解除封禁' : '封禁', success: resolve, fail: () => resolve({ confirm: false, cancel: true, content: '', errMsg: '' }),
      }));
      if (result.confirm && this.alive && this.visible) await banUser(user.id, !user.banned);
    });
  },
  async onCancel(event: WechatMiniprogram.TouchEvent) {
    const id = String(event.currentTarget.dataset.id);
    return this.act(async () => {
      const result = await new Promise<WechatMiniprogram.ShowModalSuccessCallbackResult>(resolve => wx.showModal({
        title: '取消打印任务', content: '已输出的纸张无法撤回。确认取消剩余打印？',
        confirmText: '取消任务', success: resolve, fail: () => resolve({ confirm: false, cancel: true, content: '', errMsg: '' }),
      }));
      if (result.confirm && this.alive && this.visible) await cancelAdminTask(id);
    });
  },
  async act(action: () => Promise<void>) {
    if (this.fetching || this.data.acting || !this.data.allowed) return;
    this.stop();
    this.setData({ acting: true, error: '' });
    let succeeded = false;
    try { await action(); succeeded = true; }
    catch (error) { if (this.alive) this.setData({ error: errorMessage(error) }); }
    finally {
      if (this.alive) this.setData({ acting: false });
      if (succeeded && this.alive && this.visible) await this.refresh(true);
      else this.schedule();
    }
  },
});
