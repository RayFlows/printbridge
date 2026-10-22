# 取消等待与共享状态验证

日期：2026-10-22，时间为北京时间。

## 现场复现

用户从小程序取消已缺纸的任务 36：

- 11:10:45，CUPS 接受取消，未完成列表不再包含该作业。
- 11:13，USB 后端 PID 2066 仍存在，队列 processing，显示 now printing …-0。
- 11:15:52，CUPS 日志报告此后端被信号 9 结束。
- 11:15:53，受限设备 helper 成功，PrintBridge 任务成为 canceled。

因此这次五分钟等待发生在 USB 后端释放前；既有软复位和实时空闲核对约一秒完成。
CUPS 2.4.10 的 `scheduler/conf.c` 将 JobKillDelay 初始化为 DEFAULT_TIMEOUT，
`scheduler/cupsd.h` 的宏为 300；本机 cupsd.conf 没有显式 JobKillDelay。
安装的手册写默认 30，与源码及现场不符；本次使用显式设置消除歧义。
源码固定版本：`https://github.com/OpenPrinting/cups/tree/v2.4.10`。

## 行为修正

- 打印机接口返回本服务占位状态和待提交数量；仅汇总，不带他人的文件名、
  用户或任务编号。共享缺纸、翻纸和取消清理原因对等待者可见。
- 普通用户继续仅能读取自己的任务；管理员保留完整未完成任务看板。
- 打印任务持久化自己的 CUPS 状态快照，待处理与处理中的显示区分。
  零值表示尚未核对或 release 后快照失效；不按其他任务的缺纸给它弹窗。
- 取消中按钮显示查看进度；等待翻纸时保留继续任务。
- 空闲、未知和断开情况下的旧缺纸原因标为上次报告；过滤 Rendering completed。
- 本机设置 JobKillDelay=5；由 CUPS 负责终止对应的已取消后端，
  后续仍必须完成独占 USB、软复位和连续实时空闲核对。

## 自动验证

先增加客户端回归，实际复现 pending 作业仍弹缺纸、共享状态缺失及驱动噪声；
实现后通过。服务端覆盖其他用户等待、占位优先于旧积压、聚合隐私、
CUPS 快照身份校验，以及响应中队列故障和占位状态的独立字段。

- 本地 gofmt、go vet、全量 go test -race 通过。
- TypeScript、153 项客户端检查和三页官方 WXML/WXSS 编译通过。
- 部署 Python 17 项检查通过，Windows 跳过仅适用 Linux 的 sysfs 名称测试；
  此前树莓派已执行该 sysfs 检查，本轮不改变设备 helper。
- 树莓派实际运行新状态回归和原自动取消回归，通过。
- 隔离 CUPS 为随机回环端口和临时 spool，唯一后端 bridgefake，不包含 USB 后端。
  合成后端忽略 SIGTERM；设置 5 秒后两次分别约 8.66 / 8.17 秒被调度器结束，
  不创建 recover / finish 协作标志来帮助取消。
- 隔离端到端自动取消回归约 9.50 秒完成：活后端阻止清理与后续任务，
  重启不绕过；后端释放后模拟设备清理失败仍阻塞，恢复后下一任务收到自身数据。
- 既有 monitor 缺纸回报、恢复、字节一致、安装保护和回退仍通过。
  CI 已包含短超时与编译后的 printing 集成测试。

这些检查未向实体队列发送页面。

## 正式更新

CUPS 配置备份：

```text
/var/backups/printbridge-cancel/20261022-timeout-oqoklk75/
```

通过 CUPS 自带 cupsctl 设置 JobKillDelay=5。cupsd -t、原 PPD 摘要和原配置权限
0640 核对通过，未修改驱动、port monitor 或队列。该设置影响本机 CUPS 全部队列，
不自动应用于其他部署。

应用停机备份：

```text
/home/ray/.local/share/printbridge/backups/20261022-cancelstatus1.VBimTG/
```

部署版本 `0.3.0-dev.20261022-cancelstatus1`。服务 active，健康和版本接口正常，
微信模式保留。数据库完整性通过，原八条记录保持四 canceled、四 completed。
CUPS idle，未完成列表为空，无 USB 后端进程。
私有服务环境文件仍为 0600，root helper 仍为 0755；没有修改私密凭据或迁移数据库。

## 剩余验收

- 客户端重新编译后的手机页面及多用户共享状态视觉检查。
- 实体“缺纸 → 取消 → 新文件”耗时和出纸，确认不会带出旧页。
  约八秒是隔离后端结果，不作为实际硬件耗时承诺。
- 关机、不可访问、设备清理失败仍可能持续等待，不能以超时直接宣称已取消。
