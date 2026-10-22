import { ServiceError } from './errors';

export interface User {
  id: string;
  name: string;
  namePrompted: boolean;
  banned: boolean;
  isAdmin: boolean;
  createdAt: string;
  lastSeenAt: string;
}

export function parseUser(value: unknown): User {
  const user = value as Partial<User> | null;
  if (!user || typeof user !== 'object' || typeof user.id !== 'string' || !/^[a-f0-9]{32}$/.test(user.id) ||
    typeof user.name !== 'string' || [...user.name].length > 40 ||
    typeof user.namePrompted !== 'boolean' || typeof user.banned !== 'boolean' || typeof user.isAdmin !== 'boolean' ||
    typeof user.createdAt !== 'string' || !Number.isFinite(Date.parse(user.createdAt)) ||
    typeof user.lastSeenAt !== 'string' || !Number.isFinite(Date.parse(user.lastSeenAt))) {
    throw new ServiceError('invalid_response', '服务返回的用户信息不完整');
  }
  return user as User;
}
