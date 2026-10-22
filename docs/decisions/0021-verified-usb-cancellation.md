# ADR 0021：自动清理 G2810 设备任务

日期：2026-10-21。

## 原因与现场证据

用户反馈前一版仍亮黄灯并显示缺纸，不接受把面板停止确认作为解决方案。
CUPS 已无任务且 idle，直接 USB 回报却为 stopped / MediaEmpty / 1000，
设备任务号 00000001。一次 printer-class SOFT_RESET 后，实时状态变为
idle、无任务号、无错误；重新打开设备并发送 GetStatus 后仍持续如此。

本次实际机型是 04a9:183a、G2010 series（G2810），打印接口为 1，
bulk OUT 0x01、IN 0x82。这个证据不扩展到其他 Canon 机型。
公开实现也记录了其他型号可能需要 CancelJob 和再次复位，因此不能照搬其机型结论。

## 决定

- 保留 ADR 0020 的 CUPS 终态、后端释放、跨重启占位和新任务保护。
- 为 printing 注入小型 DeviceCanceler 接口。只有对应 CUPS 任务已终态且
  队列 idle，才调用部署者明确启用的设备适配器。
- 适配器独立于 IPP 和 HTTP：通过 root 所有的 Python helper 调用系统已有 libusb。
  通过队列 USB URI、精确序列号、VID/PID、打印接口和打开后的设备描述符确认目标；
  不脱离其他内核驱动，不抢占接口，不杀后台进程。
- 取得独占接口后再次核对队列及 URI，再发送已实测的 SOFT_RESET 和只读状态查询。
  连续两个完整成功回报必须是 idle、没有设备任务号和错误，才报告成功。
  超时、缺纸、坏报文、断开、权限错误及不支持的型号都保留 canceling，随后重试。
- 配置 helper 后，旧 confirm-cancel 请求不能绕过设备验证。小程序删除停止键提示
  和人工停止确认，轮询最终结果；旧接口仅为未启用适配器的既有部署保留。
- helper 只接受指定队列名，sudoers 仅授权服务用户运行这一条完整命令。
  用户 systemd 的默认 NoNewPrivileges=yes 不变；启用此适配器需显式安装专用
  NoNewPrivileges=no drop-in，允许调用上述受限 helper，主服务继续使用普通用户。

## 边界

不更换 CUPS、驱动、port monitor、PPD 或队列；不发送页面内容，不修改电源配置。
后端尚未退出时仍等待，不承诺 Windows 驱动的即时取消速度。
外部客户端可能在此期间提交；helper 通过独占接口和二次状态核对避免清理正在发送的任务。
未部署 helper 的旧环境需要管理员处理设备取消，当前客户端不再要求用户声明面板操作。
回退前应处理全部活动任务，并同步恢复二进制、环境配置及 systemd drop-in。

## 协议参考

只研究协议事实，没有提取或翻译 GPL 实现代码。
来源：`noble6/canon-universal-driver`，固定提交
`c087e1b0da01a7cc33532a774c56384c7da81e81`，
`docs/PROTOCOL_IVEC.md`、`src/backend/canonivec.c`、`research/usbprobe.c`。
原项目 GPL-3.0-or-later；本项目独立编写匹配范围、权限、状态机与 Python ctypes 适配。
