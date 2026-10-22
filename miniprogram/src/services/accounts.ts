import { authorizedRequest } from './connection';
import { ServiceError } from './errors';
import { Printer, parsePrinters } from './printers';
import { User, parseUser } from './identity';
export type { User } from './identity';

export interface AccountSettings { printingEnabled: boolean }
export interface Profile { user: User; settings: AccountSettings }
export interface AdminTask {
  id: string; userId: string; userName: string; printerId: string;
  state: string; sheetCount: number; createdAt: string;
  problem?: string; deviceCancelRequired?: boolean;
  cupsJobState?: number;
}
export interface Dashboard { printers: Printer[]; printerStatusAvailable: boolean; tasks: AdminTask[]; settings: AccountSettings }
const record = (value: unknown): value is Record<string, unknown> => !!value && typeof value === 'object';
function invalid(): never { throw new ServiceError('invalid_response', '服务返回的用户信息不完整'); }
function parseSettings(value: unknown): AccountSettings {
  if (!record(value) || typeof value.printingEnabled !== 'boolean') invalid();
  return { printingEnabled: value.printingEnabled };
}
function parseProfile(value: unknown): Profile {
  if (!record(value)) invalid();
  return { user: parseUser(value.user), settings: parseSettings(value.settings) };
}
export async function getProfile(): Promise<Profile> {
  return parseProfile(await authorizedRequest('/api/v1/me'));
}
export async function saveName(name: string): Promise<Profile> {
  return parseProfile(await authorizedRequest('/api/v1/me', 'POST', { name }));
}
export async function listUsers(): Promise<User[]> {
  const result = await authorizedRequest('/api/v1/admin/users');
  if (!record(result) || !Array.isArray(result.users)) invalid();
  return result.users.map(parseUser);
}
export async function banUser(id: string, banned: boolean): Promise<User> {
  if (!/^[a-f0-9]{32}$/.test(id)) invalid();
  return parseUser(await authorizedRequest(`/api/v1/admin/users/${id}/ban`, 'POST', { banned }));
}
export async function configurePrinting(printingEnabled: boolean): Promise<AccountSettings> {
  return parseSettings(await authorizedRequest('/api/v1/admin/settings', 'POST', { printingEnabled }));
}
export async function getDashboard(): Promise<Dashboard> {
  const result = await authorizedRequest('/api/v1/admin/dashboard');
  if (!record(result) || !Array.isArray(result.tasks) ||
    (result.printerStatusAvailable !== undefined && typeof result.printerStatusAvailable !== 'boolean')) invalid();
  const states = ['queued', 'creating', 'created', 'sending', 'releasing', 'printing', 'awaiting_flip', 'canceling'];
  const tasks = result.tasks.map(task => {
    if (!record(task) || typeof task.id !== 'string' || !/^[a-f0-9]{32}$/.test(task.id) ||
      ['userId', 'userName', 'printerId', 'state', 'createdAt'].some(key => typeof task[key] !== 'string') ||
      !states.includes(String(task.state)) || !Number.isFinite(Date.parse(String(task.createdAt))) ||
      (task.problem !== undefined && typeof task.problem !== 'string') ||
      (task.deviceCancelRequired !== undefined && typeof task.deviceCancelRequired !== 'boolean') ||
      (task.cupsJobState !== undefined && (!Number.isInteger(task.cupsJobState) ||
        Number(task.cupsJobState) < 0 || Number(task.cupsJobState) > 9)) ||
      !Number.isInteger(task.sheetCount) || Number(task.sheetCount) < 1) invalid();
    return task as unknown as AdminTask;
  });
  return { printers: parsePrinters(result), printerStatusAvailable: result.printerStatusAvailable !== false,
    tasks, settings: parseSettings(result.settings) };
}
export async function cancelAdminTask(id: string): Promise<void> {
  if (!/^[a-f0-9]{32}$/.test(id)) invalid();
  const result = await authorizedRequest(`/api/v1/admin/jobs/${id}/cancel`, 'POST', {});
  if (!record(result) || result.id !== id || typeof result.state !== 'string') invalid();
}
export async function confirmAdminCancel(id: string): Promise<void> {
  if (!/^[a-f0-9]{32}$/.test(id)) invalid();
  const result = await authorizedRequest(`/api/v1/admin/jobs/${id}/confirm-cancel`, 'POST', { deviceStopped: true });
  if (!record(result) || result.id !== id || typeof result.state !== 'string') invalid();
}
