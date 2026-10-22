import { authorizedRequest } from './connection';
import { ServiceError } from './errors';

export interface Printer {
  id: string;
  name: string;
  model: string;
  location: string;
  state: 'idle' | 'processing' | 'stopped' | 'unknown';
  acceptingJobs: boolean | null;
  stateReasons: string[];
  stateMessage?: string;
  deviceState?: 'connected' | 'disconnected' | 'unknown';
  manualDuplexProfile?: string;
  activity?: { state: 'idle' | 'submitting' | 'printing' | 'awaiting_flip' | 'canceling'; waitingJobs: number };
  capabilities: {
    media: string[];
    sides: string[];
    colorModes: string[];
    copies: { min: number; max: number } | null;
    defaultMedia: string;
    defaultSides: string;
    defaultColorMode: string;
    mediaTypes?: string[];
    mediaSources?: string[];
    defaultMediaType?: string;
    defaultMediaSource?: string;
  };
}

const isRecord = (value: unknown): value is Record<string, unknown> => typeof value === 'object' && value !== null;
const isStrings = (value: unknown): value is string[] => Array.isArray(value) && value.every(v => typeof v === 'string');

function isPrinter(value: unknown): value is Printer {
  if (!isRecord(value)) return false;
  const c = value.capabilities;
  if (!isRecord(c)) return false;
  const copies = c.copies;
  const activity = value.activity;
  return typeof value.id === 'string' && value.id.length > 0 &&
    ['name', 'model', 'location'].every(key => typeof value[key] === 'string') &&
    ['idle', 'processing', 'stopped', 'unknown'].includes(String(value.state)) &&
    (value.acceptingJobs === null || typeof value.acceptingJobs === 'boolean') &&
    isStrings(value.stateReasons) && isStrings(c.media) && isStrings(c.sides) && isStrings(c.colorModes) &&
    (value.stateMessage === undefined || typeof value.stateMessage === 'string') &&
    (value.deviceState === undefined || ['connected', 'disconnected', 'unknown'].includes(String(value.deviceState))) &&
    (value.manualDuplexProfile === undefined || typeof value.manualDuplexProfile === 'string') &&
    (activity === undefined || (isRecord(activity) &&
      ['idle', 'submitting', 'printing', 'awaiting_flip', 'canceling'].includes(String(activity.state)) &&
      typeof activity.waitingJobs === 'number' && Number.isInteger(activity.waitingJobs) && activity.waitingJobs >= 0)) &&
    ['defaultMedia', 'defaultSides', 'defaultColorMode'].every(key => typeof c[key] === 'string') &&
    ['mediaTypes', 'mediaSources'].every(key => c[key] === undefined || isStrings(c[key])) &&
    ['defaultMediaType', 'defaultMediaSource'].every(key => c[key] === undefined || typeof c[key] === 'string') &&
    (copies === null || (isRecord(copies) && typeof copies.min === 'number' && typeof copies.max === 'number' &&
      Number.isInteger(copies.min) && Number.isInteger(copies.max) && copies.min >= 1 && copies.max >= copies.min));
}

export function parsePrinters(value: unknown): Printer[] {
  if (!isRecord(value) || !Array.isArray(value.printers) || !value.printers.every(isPrinter) ||
    new Set(value.printers.map(p => p.id)).size !== value.printers.length) {
    throw new ServiceError('invalid_response', '服务返回的打印机信息不完整');
  }
  return value.printers;
}

export async function listPrinters(): Promise<Printer[]> {
  return parsePrinters(await authorizedRequest('/api/v1/printers'));
}
