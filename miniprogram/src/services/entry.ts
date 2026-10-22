import { normalizeAddress } from './address';
import { ServiceError } from './errors';
import { normalizePrivateOrigin, resolveLocalServer } from './local-network';

export const entryPage = 'pages/connect/index';
export type EntryQuery = Record<string, unknown>;

export interface PrintEntry {
  type: 'printbridge';
  version: 2;
  server: string;
  pairingCode: string;
  alternateServers?: string[];
}

function invalidQR(): ServiceError {
  return new ServiceError('invalid_qr', '无法识别这个打印码，请重新扫码');
}

export function hasEntryQuery(query: EntryQuery): boolean {
  return 'entry' in query || 'scene' in query;
}

export function entryFromQR(payload: string): PrintEntry {
  if (payload.length > 2048) throw invalidQR();
  let value: unknown;
  try { value = JSON.parse(payload); } catch { throw invalidQR(); }
  if (typeof value !== 'object' || value === null) throw invalidQR();
  const qr = value as Record<string, unknown>;
  if (qr.type === 'printbridge' && qr.version === 1) {
    throw new ServiceError('outdated_qr', '这个旧版打印码无法自动授权，请联系部署者更新');
  }
  if (qr.type !== 'printbridge' || qr.version !== 2 || typeof qr.server !== 'string' ||
    typeof qr.pairingCode !== 'string' || !/^[!-~]{32,128}$/.test(qr.pairingCode) ||
    Object.keys(qr).some(key => !['type', 'version', 'server', 'pairingCode', 'alternateServers'].includes(key))) {
    throw invalidQR();
  }
  let server: string;
  try { server = normalizeAddress(qr.server); } catch { throw invalidQR(); }
  let alternateServers: string[] | undefined;
  if ('alternateServers' in qr) {
    if (!Array.isArray(qr.alternateServers) || qr.alternateServers.length < 1 || qr.alternateServers.length > 3 ||
      qr.alternateServers.some(address => typeof address !== 'string')) throw invalidQR();
    try {
      server = normalizePrivateOrigin(server);
      alternateServers = qr.alternateServers.map(normalizePrivateOrigin);
    } catch { throw invalidQR(); }
    if (new Set([server, ...alternateServers]).size !== alternateServers.length + 1) throw invalidQR();
  }
  return { type: 'printbridge', version: 2, server, pairingCode: qr.pairingCode,
    ...(alternateServers ? { alternateServers } : {}) };
}

export function resolveEntryServer(entry: PrintEntry): Promise<string> {
  return entry.alternateServers ? resolveLocalServer([entry.server, ...entry.alternateServers]) : Promise.resolve(entry.server);
}

export function entryFromQuery(query: EntryQuery): PrintEntry {
  if (typeof query.entry !== 'string' || query.entry.length > 2048) throw invalidQR();
  // wx entry parameters may already be decoded; decode an encoded payload exactly once.
  let payload = query.entry;
  if (!payload.trimStart().startsWith('{')) {
    try { payload = decodeURIComponent(payload); } catch { throw invalidQR(); }
  }
  return entryFromQR(payload);
}

export function entryFromPath(path: string): PrintEntry {
  if (path.length > 2048) throw invalidQR();
  const separator = path.indexOf('?');
  if (path.slice(0, separator).replace(/^\//, '') !== entryPage) throw invalidQR();
  const query: EntryQuery = {};
  for (const pair of path.slice(separator + 1).split('&')) {
    const equals = pair.indexOf('=');
    if (equals < 0) throw invalidQR();
    const key = pair.slice(0, equals);
    if (!['entry', 'scancode_time'].includes(key) || key in query) throw invalidQR();
    query[key] = pair.slice(equals + 1);
  }
  return entryFromQuery(query);
}

export function scanPrintEntry(): Promise<PrintEntry | null> {
  return new Promise((resolve, reject) => {
    wx.scanCode({
      scanType: ['qrCode', 'wxCode'],
      success: result => {
        try {
          resolve(result.path ? entryFromPath(result.path) : entryFromQR(result.result));
        } catch (error) { reject(error); }
      },
      fail: result => {
        if (result.errMsg.includes('cancel')) resolve(null);
        else reject(new ServiceError('scan_failed', '扫码未完成，请重试'));
      },
    });
  });
}
