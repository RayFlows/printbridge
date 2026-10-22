import { clearConnection, Connection, getConnection } from './connection';
import { ServiceError, isSessionExpired } from './errors';
import { request, responseError } from './http';

export const maxDocumentBytes = 20 * 1024 * 1024;

export interface ChosenFile {
  path: string;
  name: string;
  size: number;
}

export interface Document {
  id: string;
  name: string;
  bytes: number;
  pageCount: number;
  createdAt: string;
  expiresAt: string;
}

export function chooseChatFile(): Promise<ChosenFile | null> {
  return new Promise((resolve, reject) => {
    wx.chooseMessageFile({
      count: 1,
      type: 'all',
      success: result => {
        const file = result.tempFiles[0];
        if (!file) { resolve(null); return; }
        if (file.size <= 0) {
          reject(new ServiceError('invalid_file', '请选择非空文件'));
        } else if (file.size > maxDocumentBytes) {
          reject(responseError({ error: { code: 'document_too_large' } }));
        } else {
          resolve({ path: file.path, name: file.name, size: file.size });
        }
      },
      fail: error => {
        if (/cancel/i.test(error.errMsg)) resolve(null);
        else reject(new ServiceError('choose_failed', '未能打开聊天文件，请重试'));
      },
    });
  });
}

export function choosePhoto(): Promise<ChosenFile | null> {
  return new Promise((resolve, reject) => {
    wx.chooseMedia({
      count: 1, mediaType: ['image'], sourceType: ['album'], sizeType: ['original'],
      success: result => {
        const file = result.tempFiles[0];
        if (!file) { resolve(null); return; }
        if (file.size > maxDocumentBytes) { reject(responseError({ error: { code: 'document_too_large' } })); return; }
        const ext = /\.(jpe?g|png|webp|bmp|tiff?)$/i.exec(file.tempFilePath)?.[1] || 'jpg';
        resolve({ path: file.tempFilePath, name: `照片.${ext}`, size: file.size });
      },
      fail: error => {
        if (/cancel/i.test(error.errMsg)) resolve(null);
        else reject(new ServiceError('choose_failed', '未能读取照片，请重试'));
      },
    });
  });
}

export function fileIcon(name: string): string {
  if (/\.(docx?|odt|rtf)$/i.test(name)) return 'file-word';
  if (/\.(xlsx?|ods|csv)$/i.test(name)) return 'file-excel';
  if (/\.(pptx?|odp)$/i.test(name)) return 'file-powerpoint';
  if (/\.pdf$/i.test(name)) return 'file-pdf';
  if (/\.(jpe?g|png|webp|bmp|tiff?)$/i.test(name)) return 'image';
  return 'file';
}

export function parseDocument(value: unknown): Document {
  const doc = value as Partial<Document> | null;
  if (!doc || typeof doc.id !== 'string' || !/^[a-f0-9]{32}$/.test(doc.id) ||
    typeof doc.name !== 'string' || !doc.name ||
    typeof doc.bytes !== 'number' || !Number.isInteger(doc.bytes) || doc.bytes <= 0 || doc.bytes > maxDocumentBytes ||
    typeof doc.pageCount !== 'number' || !Number.isInteger(doc.pageCount) || doc.pageCount < 1 || doc.pageCount > 500 ||
    typeof doc.createdAt !== 'string' || !Number.isFinite(Date.parse(doc.createdAt)) ||
    typeof doc.expiresAt !== 'string' || !Number.isFinite(Date.parse(doc.expiresAt)) ||
    Date.parse(doc.expiresAt) <= Date.parse(doc.createdAt)) {
    throw new ServiceError('invalid_response', '服务返回的文档信息不完整');
  }
  return doc as Document;
}

export function fileSize(bytes: number): string {
  return bytes < 1024 * 1024 ? `${Math.max(1, Math.ceil(bytes / 1024))} KiB` : `${(bytes / 1024 / 1024).toFixed(1)} MiB`;
}

function removeTemp(path: string): void {
  wx.getFileSystemManager().unlink({ filePath: path, fail: () => {} });
}

// This object belongs to one page and one authorization. It never survives a scan.
export class DocumentSession {
  private readonly connection: Connection;
  private disposed = false;
  private tasks = new Set<WechatMiniprogram.UploadTask | WechatMiniprogram.DownloadTask>();
  private previews = new Map<string, string>();

  constructor() {
    const connection = getConnection();
    if (!connection) throw responseError({ error: { code: 'session_expired' } });
    this.connection = connection;
  }

  private check(): void {
    if (this.disposed) throw new ServiceError('cancelled', '操作已取消');
    const active = getConnection();
    if (!active || active.token !== this.connection.token || active.address !== this.connection.address) {
      throw responseError({ error: { code: 'session_expired' } });
    }
  }

  private handle(error: unknown): never {
    if (isSessionExpired(error) && getConnection()?.token === this.connection.token) clearConnection();
    throw error;
  }

  async upload(file: ChosenFile, progress: (value: number) => void): Promise<Document> {
    this.check();
    try {
      const value = await new Promise<unknown>((resolve, reject) => {
        const task = wx.uploadFile({
          url: `${this.connection.address}/api/v1/documents`,
          filePath: file.path,
          name: 'file',
          formData: { name: Array.from(file.name).slice(0, 180).join('') },
          header: { Authorization: `Bearer ${this.connection.token}` },
          timeout: 450000,
          success: result => {
            let data: unknown;
            try { data = JSON.parse(result.data); } catch { reject(responseError(null)); return; }
            if (result.statusCode === 201) resolve(data);
            else reject(responseError(data));
          },
          fail: () => reject(new ServiceError('network_error', '上传未完成，请检查网络后重试')),
          complete: () => this.tasks.delete(task),
        });
        this.tasks.add(task);
        task.onProgressUpdate(event => {
          if (!this.disposed) progress(Math.min(100, Math.max(0, event.progress)));
        });
      });
      this.check();
      return parseDocument(value);
    } catch (error) { return this.handle(error); }
  }

  async get(id: string): Promise<Document> {
    this.check();
    if (!/^[a-f0-9]{32}$/.test(id)) throw responseError({ error: { code: 'document_not_found' } });
    try {
      const result = await request(this.connection.address, `/api/v1/documents/${id}`, 'GET', this.connection.token);
      this.check();
      return parseDocument(result);
    } catch (error) { return this.handle(error); }
  }

  async preview(doc: Pick<Document, 'id' | 'expiresAt' | 'pageCount'> & { previewPageCount?: number },
    page: number, kind: 'documents' | 'plans' = 'documents', order: 'print' | 'reading' = 'print'): Promise<string> {
    this.check();
    if (Date.parse(doc.expiresAt) <= Date.now()) throw responseError({ error: { code: 'document_not_found' } });
    const reading = kind === 'plans' && order === 'reading';
    const count = reading ? doc.previewPageCount ?? doc.pageCount : doc.pageCount;
    if (!Number.isInteger(page) || page < 1 || page > count) throw responseError({ error: { code: 'invalid_page' } });
    const suffix = reading ? '?order=reading' : '';
    const key = `${kind}/${doc.id}/${page}${suffix}`;
    const cached = this.previews.get(key);
    if (cached) {
      this.previews.delete(key);
      this.previews.set(key, cached);
      return cached;
    }
    try {
      const path = await new Promise<string>((resolve, reject) => {
        const task = wx.downloadFile({
          url: `${this.connection.address}/api/v1/${kind}/${doc.id}/pages/${page}${suffix}`,
          header: { Authorization: `Bearer ${this.connection.token}` },
          timeout: 70000,
          success: result => {
            const local = result.tempFilePath;
            if (result.statusCode !== 200) {
              let data: unknown = null;
              try { data = JSON.parse(String(wx.getFileSystemManager().readFileSync(local, 'utf8'))); } catch { /* Fall back to the status. */ }
              if (!data && result.statusCode === 401) data = { error: { code: 'session_expired' } };
              removeTemp(local);
              reject(responseError(data));
              return;
            }
            wx.getImageInfo({
              src: local,
              success: () => resolve(local),
              fail: () => { removeTemp(local); reject(responseError({ error: { code: 'preview_failed' } })); },
            });
          },
          fail: () => reject(new ServiceError('network_error', '预览下载未完成，请检查网络后重试')),
          complete: () => this.tasks.delete(task),
        });
        this.tasks.add(task);
      });
      try { this.check(); } catch (error) { removeTemp(path); throw error; }
      this.previews.set(key, path);
      if (this.previews.size > 6) {
        const oldest = this.previews.keys().next().value as string;
        removeTemp(this.previews.get(oldest)!);
        this.previews.delete(oldest);
      }
      return path;
    } catch (error) { return this.handle(error); }
  }

  clearPreviews(): void {
    for (const path of this.previews.values()) removeTemp(path);
    this.previews.clear();
  }

  dispose(): void {
    this.disposed = true;
    for (const task of this.tasks) task.abort();
    this.tasks.clear();
    this.clearPreviews();
  }
}
