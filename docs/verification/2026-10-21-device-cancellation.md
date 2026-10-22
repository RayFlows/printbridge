# G2810 自动设备取消验证

调查开始：2026-10-21；正式服务切换：2026-10-22（Asia/Shanghai）。

## 现场复现与设备反馈

2026-10-21 23:44 后，CUPS 队列 idle、无未完成作业，未发现 USB 后端或 monitor，
用户仍报告缺纸黄灯。直接独占打印接口 1 的 bulk IN 读取，重复得到：

```text
GetStatusResponse / OK
status=stopped
status_detail=MediaEmpty
jobID=00000001
current_support_code=1000
```

仅发送 USB printer-class SOFT_RESET（0x21 / request 2 / interface 1），
不提交页面内容。第一次回报仍为缺纸，随后连续回报 idle、空 status_detail、
无 jobID、空 current_support_code。
关闭、重新打开 USB 并主动发送 GetStatus 后，仍连续得到无错误的 idle。
没有执行 CancelJob，因为该设备的软复位已产生明确恢复结果。
不能把其他型号需要的三阶段取消直接当作这台设备的要求。

本次确认设备是 04a9:183a G2010 series（G2810），接口 1、OUT 0x01、IN 0x82。
没有发送打印数据、清洗或测试页，没有更换驱动、USB 后端、monitor 或 PPD。
只能远程确认设备报告的错误清除，黄灯实际状态及后续出纸需现场观察。

## 自动流程和回归

先增加服务回归，未实现自动调用时实际失败，证明仅等待 CUPS idle 不会清理设备。
实现后验证：

- CUPS 已终态但后端仍在处理时不访问 USB，不放行下一任务。
- 后端退出后调用 helper；设备未回报空闲、权限错误或型号不支持时保留取消中。
- 同一用户不能创建另一任务绕过保护；旧后台积压任务也不能开始发送。
- 重启后继续清理；客户端的旧 deviceStopped=true 不能绕过已配置的设备验证。
- 成功后清除占位，下一任务才开始；手动双面不会在取消后执行另一轮。
- 外部取消、异常终止和取消/完成竞态都经过设备清理。
- helper 在独占 USB 前后检查 CUPS、URI，精确匹配序列号及打开后的 USB 描述符。
- 缺纸、坏 XML、分片、重复字段、单个 idle、残留设备任务号、超大报文、
  目标改变和并发提交都不构成清理成功证据。

Windows 的 Go vet、全量 race、TypeScript、147 项客户端测试通过。
微信官方 WXML/WXSS 编译通过，覆盖任务页、管理页及全局样式。
部署 Python 检查 17 项通过，其中 Linux sysfs 名称测试在 Windows 跳过；
树莓派实际运行新增八项 Python 检查，包含该 sysfs 测试，全部通过。

树莓派实际运行 ARM64 取消回归。独立 CUPS 使用随机回环端口、临时 spool，
ServerBin 仅含 bridgefake 合成后端，没有 USB 后端：

1. 合成后端忽略 SIGTERM，复现 job canceled 但 printer processing。
2. 未释放后端时，设备取消适配调用次数为零。
3. 重建服务，旧积压任务仍未发送。
4. 后端退出后，模拟设备清理失败，任务保持 canceling。
5. 客户端人工确认无法绕过失败。
6. 模拟设备恢复后自动取消完成，下一任务实际收到自身的 synthetic-next 数据。

已有缺纸 monitor 的分片原因、恢复清除、打印字节一致、安装保护和回退仍通过。
所有自动打印检查均在隔离环境进行；空纸盒没有作为隔离手段。

## 安装和正式切换

2026-10-21 已安装 root 所有的 helper 和仅允许指定用户/队列的 sudoers 规则。
从普通用户通过受限 sudo 运行 helper，实际 USB 软复位及两次空闲确认返回：

```json
{"canceled":true,"deviceState":"idle"}
```

2026-10-22 11:02 后复查发现设备已不在 USB 列表，但 CUPS 仍报告 idle，
且保留此前的 media-empty-error。这进一步说明 CUPS 属性可能是旧状态，
不能用它证明当前设备在线、缺纸或已恢复。

确认应用和 CUPS 无活动任务后，停止服务、备份数据和私有配置，
切换至 `0.3.0-dev.20261021-devicecancel1`，配置 helper 和专用 systemd drop-in。
备份路径：

```text
/home/ray/.local/share/printbridge/backups/20261021-devicecancel1.9zGHvr/
```

服务为 active，版本及微信模式正确；数据库 integrity_check=ok，
仍为三个 canceled、四个 completed。没有新增正式打印任务。
私有环境文件为 0600；helper 为 root:0755，sudoers 为 root:0440。
服务继续运行在普通用户下；专用 drop-in 使受限 sudo 可用。

在相同普通用户和 systemd NoNewPrivileges=no 环境调用已安装 helper：
由于设备未连接，返回失败；没有伪报 idle 或清理完成。
当前应用无待取消任务，因此没有修改既有终态记录。

## 尚未验收

- 设备重新连接后，从小程序执行完整“缺纸 → 取消 → 新任务”的物理出纸回归。
- 更新后小程序的手机页面与现场黄灯观察。
- 旧 USB 后端持续阻塞的取消耗时；当前实现等待后端退出，不强杀进程。
- 其他 Canon 型号、非 USB 设备和未在允许范围内的固件。

部署已完成不代表这些实体环节已完成。当前设备未连接时，保护会保持等待，
不因 CUPS idle、超时或用户点击确认而放行。
