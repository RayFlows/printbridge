interface Gesture {
  id: number;
  x: number;
  y: number;
  position: number;
  moved: boolean;
}

Component({
  options: { pureDataPattern: /^_/ },
  data: {
    selected: 0,
    preview: 0,
    position: 0,
    dragging: false,
    tabs: [
      { pagePath: 'pages/printers/index', text: '打印', icon: '/assets/tabs/print.png', selectedIcon: '/assets/tabs/print-active.png' },
      { pagePath: 'pages/mine/index', text: '我的', icon: '/assets/tabs/user.png', selectedIcon: '/assets/tabs/user-active.png' },
    ],
    _width: 0,
    _gesture: null as Gesture | null,
    _active: false,
    _visible: false,
    _switching: false,
    _navigation: 0,
    _ignoreTapUntil: 0,
  },
  lifetimes: {
    attached() { this.data._active = true; this.data._visible = true; },
    ready() { this.measure(); },
    detached() {
      this.data._active = false;
      this.data._gesture = null;
      this.data._navigation++;
    },
  },
  pageLifetimes: {
    show() {
      this.data._visible = true;
      this.measure();
    },
    hide() {
      this.data._visible = false;
      this.data._navigation++;
      this.data._switching = false;
      this.resetGesture();
    },
    resize() { this.resetGesture(); this.measure(); },
  },
  methods: {
    setSelected(selected: number) {
      if (!Number.isInteger(selected) || selected < 0 || selected >= this.data.tabs.length) return;
      this.data._navigation++;
      this.data._switching = false;
      this.data._ignoreTapUntil = 0;
      this.data._gesture = null;
      this.setData({ selected, preview: selected, position: selected, dragging: false });
    },
    measure() {
      this.createSelectorQuery().select('.tab-track').boundingClientRect(rect => {
        if (this.data._active && rect && !Array.isArray(rect)) this.data._width = rect.width;
      }).exec();
    },
    resetGesture() {
      this.data._gesture = null;
      this.setData({ dragging: false, position: this.data.selected, preview: this.data.selected });
    },
    onTap(event: WechatMiniprogram.TouchEvent) {
      if (Date.now() < this.data._ignoreTapUntil) return;
      this.switchTo(Number(event.currentTarget.dataset.index));
    },
    switchTo(index: number) {
      if (!this.data._active || !this.data._visible || this.data._switching ||
        !Number.isInteger(index) || index < 0 || index >= this.data.tabs.length) return;
      this.setData({ position: index, preview: index, dragging: false });
      if (index === this.data.selected) return;
      this.data._switching = true;
      const navigation = ++this.data._navigation;
      wx.switchTab({
        url: `/${this.data.tabs[index].pagePath}`,
        fail: () => {
          if (!this.data._active || !this.data._visible || navigation !== this.data._navigation) return;
          this.resetGesture();
          wx.showToast({ title: '切换失败，请重试', icon: 'none' });
        },
        complete: () => {
          if (navigation === this.data._navigation) this.data._switching = false;
        },
      });
    },
    onTouchStart(event: WechatMiniprogram.TouchEvent) {
      if (this.data._switching || !this.data._width) return;
      if (event.touches.length !== 1) { this.onTouchCancel(); return; }
      const touch = event.touches[0];
      this.data._ignoreTapUntil = 0;
      this.data._gesture = { id: touch.identifier, x: touch.clientX, y: touch.clientY,
        position: this.data.selected, moved: false };
    },
    onTouchMove(event: WechatMiniprogram.TouchEvent) {
      const gesture = this.data._gesture;
      if (!gesture) return;
      const touch = event.touches[0];
      if (event.touches.length !== 1 || touch.identifier !== gesture.id) { this.onTouchCancel(); return; }
      const dx = touch.clientX - gesture.x;
      const dy = touch.clientY - gesture.y;
      if (!gesture.moved) {
        if (Math.abs(dy) > 6 && Math.abs(dy) > Math.abs(dx)) { this.onTouchCancel(); return; }
        if (Math.abs(dx) <= 6) return;
        gesture.moved = true;
      }
      // The measured track uses touch coordinates; visual sizes remain in rpx.
      const position = Math.max(0, Math.min(1, gesture.position + dx / (this.data._width / 2)));
      this.setData({ position, preview: position >= 0.5 ? 1 : 0, dragging: true });
    },
    onTouchEnd(event: WechatMiniprogram.TouchEvent) {
      const gesture = this.data._gesture;
      if (!gesture) return;
      if (event.touches.length) { this.onTouchCancel(); return; }
      this.data._gesture = null;
      if (!gesture.moved) return;
      this.data._ignoreTapUntil = Date.now() + 400;
      this.switchTo(this.data.position >= 0.5 ? 1 : 0);
    },
    onTouchCancel() {
      this.data._ignoreTapUntil = Date.now() + 400;
      this.resetGesture();
    },
  },
});
