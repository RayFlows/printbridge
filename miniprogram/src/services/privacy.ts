import { ServiceError } from './errors';

export function requirePrivacyConsent(): Promise<void> {
  if (typeof wx.requirePrivacyAuthorize !== 'function') {
    return Promise.reject(new ServiceError('privacy_unavailable', '当前微信版本不支持隐私确认，请升级微信后重试'));
  }
  return new Promise((resolve, reject) => wx.requirePrivacyAuthorize({
    success: () => resolve(),
    fail: () => reject(new ServiceError('privacy_required', '尚未同意隐私保护指引，未登录打印服务')),
  }));
}

export function openPrivacyContract(): Promise<void> {
  if (typeof wx.openPrivacyContract !== 'function') {
    return Promise.reject(new ServiceError('privacy_unavailable', '当前微信版本不支持查看隐私指引，请升级微信后重试'));
  }
  return new Promise((resolve, reject) => wx.openPrivacyContract({
    success: () => resolve(),
    fail: () => reject(new ServiceError('privacy_contract_unavailable', '隐私保护指引暂时无法打开，请稍后重试')),
  }));
}
