import { Connection, getConnection, saveConnection } from '../../services/connection';
import { getProfile, saveName, Profile } from '../../services/accounts';
import { errorMessage, isSessionExpired } from '../../services/errors';
import { openPrivacyContract } from '../../services/privacy';

function isCurrent(connection: Connection | null): boolean {
  const active = getConnection();
  return !!connection && active?.address === connection.address && active.token === connection.token;
}

Page({
  data: {
    loading: true, savingName: false, error: '', profile: null as Profile | null,
    legacy: false, nameDraft: '', nameChanged: false, nameError: '',
  },
  alive: true,
  revision: 0,
  onLoad() { this.alive = true; },
  onShow() { this.getTabBar?.()?.setSelected(1); void this.refresh(); },
  onHide() { this.revision++; },
  onUnload() { this.alive = false; this.revision++; },
  async onRefresh() { if (!this.data.savingName) await this.refresh(); },
  async refresh() {
    const revision = ++this.revision;
    const connection = getConnection();
    if (!connection) { wx.reLaunch({ url: '/pages/connect/index?expired=1' }); return; }
    if (!connection.user) {
      this.setData({ loading: false, legacy: true, profile: null, nameDraft: '', nameChanged: false });
      return;
    }
    this.setData({ loading: true, error: '', profile: null, legacy: false, nameDraft: '', nameChanged: false, nameError: '' });
    try {
      const profile = await getProfile();
      if (this.alive && revision === this.revision && isCurrent(connection)) {
        saveConnection({ ...connection, user: profile.user });
        this.setData({ profile, nameDraft: profile.user.name, nameChanged: false });
      }
    } catch (error) {
      if (this.alive && revision === this.revision && (isCurrent(connection) || !getConnection())) {
        this.setData({ error: errorMessage(error) });
        if (isSessionExpired(error)) wx.reLaunch({ url: '/pages/connect/index?expired=1' });
      }
    } finally {
      if (this.alive && revision === this.revision) this.setData({ loading: false });
    }
  },
  onNameInput(event: WechatMiniprogram.Input) {
    if (this.data.savingName || !this.data.profile) return;
    const nameDraft = event.detail.value;
    this.setData({ nameDraft, nameChanged: nameDraft !== this.data.profile.user.name, nameError: '' });
  },
  async onSaveName() {
    const profile = this.data.profile;
    const connection = getConnection();
    if (!profile || this.data.savingName || !this.data.nameChanged || !connection ||
      connection.user?.id !== profile.user.id) return;
    const revision = this.revision;
    this.setData({ savingName: true, nameError: '' });
    try {
      const updated = await saveName(this.data.nameDraft);
      if (this.alive && revision === this.revision && isCurrent(connection)) {
        this.setData({ profile: updated, nameDraft: updated.user.name, nameChanged: false });
        saveConnection({ ...connection, user: updated.user });
      }
    } catch (error) {
      if (this.alive && revision === this.revision && (isCurrent(connection) || !getConnection())) {
        this.setData({ nameError: errorMessage(error) });
        if (isSessionExpired(error) && !getConnection()) wx.reLaunch({ url: '/pages/connect/index?expired=1' });
      }
    } finally { if (this.alive) this.setData({ savingName: false }); }
  },
  onCopy() {
    if (this.data.profile) wx.setClipboardData({ data: this.data.profile.user.id });
  },
  onAdmin() {
    if (this.data.profile?.user.isAdmin) wx.navigateTo({ url: '/pages/admin/index' });
  },
  async onPrivacy() {
    const revision = this.revision;
    try {
      await openPrivacyContract();
    } catch (error) {
      if (this.alive && revision === this.revision) wx.showToast({ title: errorMessage(error), icon: 'none' });
    }
  },
});
