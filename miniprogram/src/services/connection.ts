import { normalizeAddress } from './address';
import { ServiceError, isSessionExpired } from './errors';
import { request } from './http';
import { User, parseUser } from './identity';

const storageKey = 'printbridge.connection.v1';

export interface Connection {
  address: string;
  token: string;
  expiresAt: string;
  user?: User;
}

export function clearConnection(): void {
  wx.removeStorageSync(storageKey);
}

export function getConnection(): Connection | null {
  const value: unknown = wx.getStorageSync(storageKey);
  if (!value || typeof value !== 'object') return null;
  const c = value as Partial<Connection>;
  try {
    if (typeof c.address !== 'string' || typeof c.token !== 'string' ||
      !/^[a-zA-Z0-9_-]{43}$/.test(c.token) || typeof c.expiresAt !== 'string' ||
      !Number.isFinite(Date.parse(c.expiresAt)) || Date.parse(c.expiresAt) <= Date.now()) {
      clearConnection();
      return null;
    }
    return { address: normalizeAddress(c.address), token: c.token, expiresAt: c.expiresAt,
      ...(c.user ? { user: parseUser(c.user) } : {}) };
  } catch {
    clearConnection();
    return null;
  }
}

export async function createConnection(address: string, pairingCode: string): Promise<Connection> {
  const origin = normalizeAddress(address);
  if (pairingCode.length < 32 || pairingCode.length > 128 || !/^[!-~]+$/.test(pairingCode)) {
    throw new ServiceError('invalid_code', '打印码缺少有效授权，请联系部署者更新');
  }
  const info = await request(origin, '/api/v1/server') as Record<string, unknown> | null;
  if (!info || info.name !== 'PrintBridge' || info.apiVersion !== 'v1') {
    throw new ServiceError('incompatible_server', '打印码对应的服务与当前版本不兼容');
  }
  if (info.loginMode !== undefined && info.loginMode !== 'wechat') {
    throw new ServiceError('incompatible_server', '打印服务的登录方式暂不受支持');
  }
  let loginCode: string | undefined;
  if (info.loginMode === 'wechat') {
    loginCode = await new Promise<string>((resolve, reject) => wx.login({
      timeout: 10000,
      success: result => result.code ? resolve(result.code) :
        reject(new ServiceError('wechat_login_failed', '微信登录失败，请重新扫码')),
      fail: () => reject(new ServiceError('wechat_login_failed', '微信登录失败，请稍后重试')),
    }));
  }
  const session = await request(origin, '/api/v1/sessions', 'POST', undefined,
    { pairingCode, ...(loginCode ? { loginCode } : {}) }) as Record<string, unknown> | null;
  if (!session || typeof session.token !== 'string' || !/^[a-zA-Z0-9_-]{43}$/.test(session.token) ||
    typeof session.expiresAt !== 'string' || !Number.isFinite(Date.parse(session.expiresAt)) ||
    Date.parse(session.expiresAt) <= Date.now()) {
    throw new ServiceError('invalid_response', '服务返回的连接信息无效');
  }
  const user = info.loginMode === 'wechat' ? parseUser(session.user) : undefined;
  return { address: origin, token: session.token, expiresAt: session.expiresAt, ...(user ? { user } : {}) };
}

export function saveConnection(connection: Connection): void {
  wx.setStorageSync(storageKey, connection);
}

export async function authorizedRequest(path: string, method: 'GET' | 'POST' | 'DELETE' = 'GET',
  data?: Record<string, unknown>, timeout?: number): Promise<unknown> {
  const c = getConnection();
  if (!c) throw new ServiceError('session_expired', '打印授权已过期，请重新扫码');
  try {
    const value = await request(c.address, path, method, c.token, data, timeout);
    const active = getConnection();
    if (!active || active.address !== c.address || active.token !== c.token) {
      throw new ServiceError('session_expired', '打印授权已改变，请重新进入');
    }
    return value;
  } catch (error) {
    // An old request must not erase a newer connection established in the meantime.
    if (isSessionExpired(error) && getConnection()?.token === c.token) clearConnection();
    throw error;
  }
}

export async function revokeConnection(c: Connection): Promise<void> {
  try {
    await request(c.address, '/api/v1/session', 'DELETE', c.token);
  } catch (error) {
    if (!isSessionExpired(error)) throw error;
  }
}

export async function disconnect(): Promise<void> {
  const c = getConnection();
  if (!c) {
    clearConnection();
    return;
  }
  await revokeConnection(c);
  if (getConnection()?.token === c.token) clearConnection();
}
