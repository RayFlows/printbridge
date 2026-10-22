import { EntryQuery, entryPage, hasEntryQuery } from './services/entry';

export interface EntryApp {
  pendingEntry: EntryQuery | null;
  lastEntry: string;
  takeEntry(): EntryQuery | null;
}

App<EntryApp>({
  pendingEntry: null,
  lastEntry: '',

  onShow(options) {
    if (options.path !== entryPage || !hasEntryQuery(options.query)) return;
    const query: EntryQuery = {
      entry: options.query.entry,
      scene: options.query.scene,
      scancode_time: options.query.scancode_time,
    };
    const identity = JSON.stringify(query);
    // A foreground resume must not replay the same entry and create another session.
    if (identity === this.lastEntry) return;
    this.lastEntry = identity;
    this.pendingEntry = query;
    const pages = getCurrentPages();
    if (pages.length && pages[pages.length - 1].route !== entryPage) {
      wx.reLaunch({ url: `/${entryPage}` });
    }
  },

  takeEntry() {
    const query = this.pendingEntry;
    this.pendingEntry = null;
    return query;
  },
});
