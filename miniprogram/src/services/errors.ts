export class ServiceError extends Error {
  constructor(public readonly code: string, message: string) {
    super(message);
    this.name = 'ServiceError';
  }
}

export function errorMessage(error: unknown): string {
  if (error instanceof ServiceError) return error.message;
  return '操作未完成，请重试';
}

export function isSessionExpired(error: unknown): boolean {
  return error instanceof ServiceError && error.code === 'session_expired';
}
