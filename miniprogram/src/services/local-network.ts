import { normalizeAddress } from './address';
import { ServiceError } from './errors';

function ipv4(value: unknown): number | null {
  if (typeof value !== 'string') return null;
  const parts = value.split('.');
  if (parts.length !== 4 || parts.some(part => !/^(0|[1-9][0-9]{0,2})$/.test(part) || Number(part) > 255)) {
    return null;
  }
  return parts.reduce((result, part) => result * 256 + Number(part), 0);
}

export function normalizePrivateOrigin(value: string): string {
  const address = normalizeAddress(value);
  const match = /^(https?):\/\/([0-9.]+)(?::([0-9]+))?$/.exec(address);
  const ip = match ? ipv4(match[2]) : null;
  if (!match || ip === null ||
    !((ip >>> 24) === 10 || (ip >>> 20) === 0xac1 || (ip >>> 16) === 0xc0a8)) {
    throw new ServiceError('invalid_address', '多网络打印码必须使用局域网 IPv4 地址');
  }
  const port = match[3] ? Number(match[3]) : undefined;
  const defaultPort = match[1] === 'http' ? 80 : 443;
  return `${match[1]}://${match[2]}${port === undefined || port === defaultPort ? '' : `:${port}`}`;
}

function networkUnavailable(): ServiceError {
  return new ServiceError('local_network_unavailable', '无法读取当前 Wi-Fi 网络，请连接打印机所在 Wi-Fi 后重试');
}

export function selectLocalServer(servers: string[], localip: string, netmask: string): string {
  const local = ipv4(localip);
  const mask = ipv4(netmask);
  if (local === null || mask === null || mask === 0) throw networkUnavailable();
  const hostMask = (~mask) >>> 0;
  if ((hostMask & (hostMask + 1)) !== 0) throw networkUnavailable();
  const candidates = servers.map(normalizePrivateOrigin).filter(server => {
    const host = ipv4(/^https?:\/\/([0-9.]+)/.exec(server)![1])!;
    return host !== local && (host & mask) === (local & mask) &&
      (host & hostMask) !== 0 && ((host & hostMask) >>> 0) !== hostMask;
  });
  if (candidates.length === 0) {
    throw new ServiceError('printer_network_required', '请先连接打印机的访客 Wi-Fi 或主 Wi-Fi，再重试');
  }
  if (candidates.length !== 1) {
    throw new ServiceError('ambiguous_network', '打印码在当前网络有多个入口，请联系管理员更新打印码');
  }
  return candidates[0];
}

export function resolveLocalServer(servers: string[]): Promise<string> {
  if (typeof wx.getLocalIPAddress !== 'function') {
    return Promise.reject(new ServiceError('local_network_unavailable', '当前微信版本无法识别打印网络，请升级微信后重试'));
  }
  return new Promise((resolve, reject) => {
    const timer = setTimeout(() => reject(networkUnavailable()), 8000);
    try {
      wx.getLocalIPAddress({
        success: result => {
          clearTimeout(timer);
          try { resolve(selectLocalServer(servers, result.localip, result.netmask)); } catch (error) { reject(error); }
        },
        fail: () => { clearTimeout(timer); reject(networkUnavailable()); },
      });
    } catch {
      clearTimeout(timer);
      reject(networkUnavailable());
    }
  });
}
