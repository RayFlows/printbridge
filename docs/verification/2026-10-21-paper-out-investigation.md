# G2810 缺纸状态调查

日期：2026-10-21。本次只读调查，没有提交、取消或继续打印任务，
没有改动 CUPS、驱动、队列、服务或打印机设置。

后续已完成适配器开发及隔离验证，正式启用仍等待现有任务结束；
最新状态见 [缺纸适配与弹窗验证](2026-10-21-paper-out-adapter.md)。
以下保留调查当时的现场证据与结论。

## 现场复现

用户确认自己点击打印后没有放纸。任务 30 创建于 14:11:01；
14:16、14:17、14:18 和 14:27 多次读取 CUPS，均未报告缺纸：

```text
printer-state = processing
printer-state-message = Printing page 1, 26%
printer-state-reasons = none
job-state = processing
job-state-reasons = job-printing
job-printer-state-reasons = none
job-media-progress = 26
```

这是实际缺纸与 CUPS 状态的现场对照，不是通过本次调查创建的测试任务。
空纸盒不作为自动测试隔离。

已有 CUPS 日志显示，USB 后端接收到多次 512/482 字节的 back-channel 数据。
日志只记录长度，单凭日志不能判断报文内容；随后取得的现场证据见下文。

## 授权后的现场回传观察

用户提供管理员授权后，先检查 CUPS USB 后端已有的 FD 3 管道。
诊断程序使用 Linux `tee(2)` 复制管道内容到自己的临时管道，
只读取副本，不从 CUPS 原管道消耗数据。
独立假管道自检覆盖重复复制不消耗、空管道、XML 完整及截断报文和字段筛选。

原管道容量 65,536 字节，检查前后均积压 61,472 字节。
副本中有 60 份完整的佳能 IVEC XML 状态报文：
`GetStatusResponse`、`status=idle`，`current_support_code` 为空。
它们是管道里未被消费的缓存，不能当作设备此刻的状态。
这也说明仅取缓存中的第一份状态可能得到过期结果。

为区分缓存与当前回传，随后对同一个既有 USB 后端做短时系统调用观察：

- `strace` 只输出 `ioctl` 的编号、地址和返回值，不输出打印数据。
- 对已观察到提交的 USB IN 传输，从进程内存中读取对应回传缓冲区；
  不读取 USB OUT 数据，不额外打开设备或发送 USB 请求。
- 每轮最长 8 秒，结束后脱离原进程。输出只包含允许的状态字段及长度，
  不输出原始 XML、任务标识、文档内容或访问凭据。

两次 8 秒观察均在新回传中取得同样的、已闭合的 XML 字段：

```text
operation = GetStatusResponse
response = OK
status = stopped
status_detail = MediaEmpty
```

每次观察取得两个 512 字节片段。片段中的以上字段完整，可以确定设备已反馈缺纸；
未取得整份完整 XML，因此不声称已经取得具体数字错误码或所有状态信息。
分片 XML 的诊断解析使用 XMLPullParser，独立夹具覆盖截断报文中已闭合字段的读取。

14:43:55 再次查询，CUPS 仍返回：

```text
printer-state = processing
printer-state-message = Printing page 1, 26%
printer-state-reasons = none
```

原任务 30 和原来的 USB/过滤进程仍在，没有残留 strace 进程。
因此本次已确认：这台打印机通过现有 USB 连接返回了缺纸，
当前打印链路没有将它转换成 CUPS 的标准缺纸状态。
不能再将此现象描述为“打印机不会反馈缺纸”。

## 当前链路与源码

安装版本：

```text
cups / cups-daemon: 2.4.10-3+rpt2+deb13u2
printer-driver-gutenprint / libgutenprint9:
  5.3.4.20220624T01008808d602-4
```

活动程序为 `rastertogutenprint.5.3`（lp 用户）和 `usb`（root 用户）。
读取当前过滤程序动态符号，没有 `cupsBackChannelRead`、
`cupsSideChannelDoRequest` 或普通 `read` 导入。

为排除只查看其他版本源码造成的误判，下载并解包 Debian 对应原始源码：

```text
gutenprint_5.3.4.20220624T01008808d602.orig.tar.xz
SHA256:
ef514d4ca567b5871cfe7697127bee2c14d5d71d7fbc68d8fee96b0faaed90e7
```

检查得到：

- `src/cups/rastertogutenprint.c` 和 `src/main/print-canon.c`
  没有读取 CUPS back-channel/side-channel 的代码，
  也没有通过 `STATE:` 上报 Canon 缺纸的实现。
- `rastertogutenprint.c:1657-1666` 按
  `100 * row / cupsHeight` 计算进度，并写入消息及 `job-media-progress`。
  26% 表示软件处理图像行数的进度，不能解释为已完成相同比例的实体出纸。
- `src/cups/commandtocanon.c` 是清洗、校准及测试页等维护命令过滤程序。
  其中 `ReportLevels` 分支为空，没有实际读取墨量；本次没有运行该程序。
- CUPS v2.4.10 的 `backend/usb-libusb.c` 中，`read_thread` 读取 USB 回传，
  记录字节数，再调用 `cupsBackChannelWrite` 转发；这段代码不解析佳能私有缺纸信息。
- CUPS 官方接口支持通过 `STATE: +media-empty` / `STATE: -media-empty`
  设置和清除缺纸原因。因此不能把当前现象归因于 CUPS 根本没有缺纸状态机制。

PrintBridge 已读取 `printer-state-reasons`；打印机页也会把
`media-empty` 及其 warning/error/report 后缀映射为“缺纸”。
这次直接向 CUPS 查询就没有该状态，不能通过单独修改小程序补出可靠的缺纸信号。

## 可以继续的方向

优先研究在 CUPS 过滤链中添加独立的状态适配程序，
通过已有 back-channel 解析设备报文，再向 CUPS 报告标准状态。
这保留现有打印数据生成路径，也让客户端继续只读取 CUPS 状态。
现场已确定 `MediaEmpty` 信号存在；适配程序仍是候选方案，尚未实现或验证。
后续实现需要连续消费及拼接分片、处理过期缓存，并在真实恢复后清除缺纸原因，
不能只在启动时读取一次或根据打印耗时猜测缺纸。

另阅读了 `noble6/canon-universal-driver` 的 `src/ivec/status.c`：
它包含 `MediaEmpty` 到 `media-empty` 的解析映射，可作为佳能状态协议的研究参考。
现场随后证实 G2810 的回传中有同名 IVEC 字段；这不是该项目已经适配 G2810 的证据。
本次没有提取、集成或安装其代码，也没有验证该项目的完整设备兼容声明。

## 尚未验证

- 缺纸状态的完整报文拼接、数字错误码及生产适配。
- 任务之外的状态读取，以及其他状态是否需要额外查询。
- 补纸及面板确认后，状态和当前任务如何恢复。
- 其他错误、真实墨量信息和实体完成反馈。

最初只读 SSH 权限不足；后续观察使用用户明确授予的管理员权限。
授权密码没有写入脚本、配置、日志或调查记录。
没有另开 USB 连接、解除驱动绑定、恢复队列或重启任务。

临时诊断工具位于忽略目录 `bin/inspect-cups-backchannel.py`，
不在正式服务或小程序内运行。已在树莓派执行其独立自检及上述现场读取。
生产代码没有变化，未运行 Go/小程序回归；已确认设备缺纸信号，
仍不代表已修复 CUPS 缺纸识别或完成缺纸续打验收。

## 来源

- [Debian 对应 Gutenprint 原始源码](https://deb.debian.org/debian/pool/main/g/gutenprint/gutenprint_5.3.4.20220624T01008808d602.orig.tar.xz)
- [CUPS v2.4.10 USB 后端源码](https://github.com/OpenPrinting/cups/blob/v2.4.10/backend/usb-libusb.c)
- [CUPS 过滤程序及后端接口](https://openprinting.github.io/cups/doc/api-filter.html)
- [Linux tee(2)，不消耗输入管道数据](https://man7.org/linux/man-pages/man2/tee.2.html)
- [佳能状态解析参考，未集成、未在 G2810 验证](https://github.com/noble6/canon-universal-driver/blob/main/src/ivec/status.c)
