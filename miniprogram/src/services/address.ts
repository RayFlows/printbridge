import { ServiceError } from './errors';

// The mini-program runtime does not provide the browser URL class.
export function normalizeAddress(value: string): string {
  const match = /^(https?):\/\/(\[[0-9a-fA-F:]+\]|[a-zA-Z0-9.-]+)(?::([0-9]{1,5}))?\/?$/.exec(value.trim());
  if (!match) throw new ServiceError('invalid_address', '二维码中的打印服务地址无效');
  const host = match[2].toLowerCase();
  if (!host.startsWith('[') && host.split('.').some(part =>
    !part || part.length > 63 || !/^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?$/.test(part)
  )) {
    throw new ServiceError('invalid_address', '服务地址中的主机名无效');
  }
  if (/^[\d.]+$/.test(host) && (host.split('.').length !== 4 || host.split('.').some(part =>
    Number(part) > 255 || (part.length > 1 && part.startsWith('0'))
  ))) {
    throw new ServiceError('invalid_address', '服务地址中的 IP 无效');
  }
  const port = match[3] ? Number(match[3]) : undefined;
  if (port !== undefined && (port < 1 || port > 65535)) {
    throw new ServiceError('invalid_address', '服务端口必须在 1 到 65535 之间');
  }
  return `${match[1]}://${host}${port === undefined ? '' : `:${port}`}`;
}
