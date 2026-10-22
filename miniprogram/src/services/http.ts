import { ServiceError } from './errors';

const messages: Record<string, string> = {
  wechat_login_required: '此打印服务需要微信登录，请更新小程序后重新扫码',
  wechat_login_failed: '微信登录未成功，请重试',
  wechat_unavailable: '暂时无法连接微信登录服务，请稍后重试',
  user_banned: '管理员已停用你的打印权限，请联系打印机管理员',
  admin_required: '仅打印机管理员可以进行此操作',
  invalid_name: '称谓或昵称最多 40 个字，不能包含换行或控制字符',
  user_not_found: '用户不存在，请刷新后重试',
  printing_paused: '管理员已暂停接收新打印，已有任务仍可继续',
  invalid_pairing_code: '打印码授权已失效，请联系部署者更新',
  session_expired: '打印授权已过期，请重新扫码',
  pairing_disabled: '打印服务尚未开放扫码访问，请联系部署者',
  pairing_rate_limited: '扫码尝试过于频繁，请一分钟后重试',
  session_capacity: '当前访问人数已达上限，请稍后再试',
  cups_unavailable: '暂时无法读取打印机，请检查设备上的 CUPS 服务',
  invalid_request: '请求参数不正确',
  document_not_found: '文档已过期或不属于当前授权，请重新选择文件',
  unsupported_format: '这种文件暂不支持打印，请选择文档或图片',
  conversion_failed: '文件转换失败，文件可能已加密、损坏或格式不兼容',
  office_unavailable: '设备尚未配置 Office 转换工具，请联系部署者',
  invalid_pdf: '文件无法读取或已加密，请选择未加密的 PDF',
  document_too_large: '文件过大，原文件限 20 MiB，转换结果限 128 MiB',
  too_many_pages: '文档转换后不能超过 500 页',
  invalid_page: '页码超出文档范围',
  document_busy: '设备正在处理另一份文档，请稍后重试',
  document_timeout: '文档处理超时，请重试或选择较小的文件',
  pdf_tools_unavailable: '设备缺少 PDF 处理工具，请联系部署者',
  preview_failed: '这一页暂时无法预览，请重试',
  document_storage_full: '设备文档空间已满，请稍后重试或联系部署者',
  document_error: '文档处理失败，请重试',
  invalid_settings: '打印设置无效，请检查页码、纸张和双面设置',
  print_not_found: '排版或任务已过期，或不属于当前授权',
  print_conflict: '任务状态已改变，请刷新后再操作',
  active_print_exists: '这台打印机还有未完成的任务，请先继续或取消当前任务',
  print_busy: '设备正在准备另一份打印，请稍后重试',
  print_capacity: '打印任务或存储空间已达上限，请稍后再试',
  watermark_font_unavailable: '设备水印字体缺失或不包含所输入的文字，请修改水印或联系管理员配置字体',
};

export function responseError(value: unknown): ServiceError {
  const body = value as { error?: { code?: unknown } } | null;
  const code = body && typeof body.error?.code === 'string' ? body.error.code : 'server_error';
  return new ServiceError(code, messages[code] || '打印服务暂时不可用，请稍后重试');
}

export function request(
  address: string,
  path: string,
  method: 'GET' | 'POST' | 'DELETE' = 'GET',
  token?: string,
  data?: Record<string, unknown>,
  timeout = 12000,
): Promise<unknown> {
  return new Promise((resolve, reject) => {
    const header: Record<string, string> = { 'Content-Type': 'application/json' };
    if (token) header.Authorization = `Bearer ${token}`;
    wx.request({
      url: `${address}${path}`,
      method, data, header,
      timeout,
      success: result => {
        if (result.statusCode >= 200 && result.statusCode < 300) {
          resolve(result.data);
          return;
        }
        reject(responseError(result.data));
      },
      fail: () => reject(new ServiceError('network_error', '暂时无法访问打印机，请确认与打印机处于同一网络后重试')),
    });
  });
}
