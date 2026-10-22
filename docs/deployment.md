# 树莓派部署

适用于已有 CUPS 的 Debian / Raspberry Pi OS ARM64 设备。打印服务以普通用户运行，
二进制、数据和凭据分开保存；安装脚本不配置 CUPS、打印队列或驱动。

## 准备文档工具

在目标设备安装 Poppler、LibreOffice Writer/Calc/Impress 和中文字体：

```sh
sudo bash deploy/install-dependencies.sh
```

这一步需要系统软件安装权限。脚本使用系统包管理器，不执行整机升级；
依赖解析若要求改变 CUPS 或打印驱动包会停止。只有 Poppler 时可以先部署，
PDF 和图片可用，Office 上传会明确返回 `office_unavailable`。
服务不需要额外配置 CUPS 管理员密码。

## 构建与首次安装

在源码的 `server/` 目录构建，Go 版本要求见开发说明：

```sh
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath \
  -ldflags="-X main.version=0.3.0-dev" -o ../bin/printbridge-linux-arm64 ./cmd/printbridge
```

将二进制和仓库 `deploy/` 目录传到设备，使用将来运行服务的普通用户执行：

```sh
chmod 700 ./printbridge-linux-arm64
bash deploy/install-user.sh ./printbridge-linux-arm64 0.3.0-dev
```

同一版本和相同二进制可重复执行；不同版本或已有不同服务文件会要求先按升级流程处理。
初次安装随机生成设备访问码，保存到权限为 `0600` 的私有配置中，不输出到安装日志。
默认监听 `0.0.0.0:8765`，直连本机 `http://localhost:631`。
端口被占用时先选择空闲端口，创建下述私有配置后再安装。

| 路径（相对于服务用户 HOME） | 用途 |
| --- | --- |
| `.config/printbridge/service.env` | 服务环境变量、设备访问码 |
| `.config/systemd/user/printbridge.service` | 用户级 systemd 单元 |
| `.local/share/printbridge/releases/<release>/printbridge` | 保留版本的二进制 |
| `.local/share/printbridge/current` | 当前版本软链接 |
| `.local/share/printbridge/data/` | SQLite、文档、准备结果和打印任务 |

配置项见 [开发说明](development.md)。修改私有配置后重启服务。

## 微信身份与机主权限

在服务端私有 `service.env` 配置以下变量，AppSecret 仅由服务端读取：

```sh
PRINTBRIDGE_WECHAT_APP_ID=<与小程序一致的AppID>
PRINTBRIDGE_WECHAT_APP_SECRET=<微信后台的AppSecret>
# 自己登录后，在“我的”复制打印账户编号，再设置下面一项：
PRINTBRIDGE_ADMIN_USER_ID=<自己的32位内部用户编号>
```

AppID 与 AppSecret 必须一起配置。没有真实凭据时保持旧匿名模式，无法提供跨会话恢复，
客户端“我的”会说明尚未启用。启用后旧匿名令牌不可继续访问；打印码仍是入口授权，
不能代替微信登录。小程序包不含 AppSecret，也不直接调用 code2Session。
服务端需要访问微信 HTTPS API；真机还需配置微信后台合法域名和可达的 HTTPS 服务，
开发工具跳过校验不能替代这些要求的验收。

管理员只由上述内部用户编号指定，最先扫码者没有管理权限。
只有管理员在“我的”看见“打印管理”，可查看用户、设备和当前任务，封禁/解封，
暂停接收新打印，以及单独确认取消任务。全局关闭只拒绝新计划/新任务，
原有任务继续、取消及幂等核对仍可用；封禁则禁止该用户的所有服务访问。
封禁不自动取消物理作业，需按需要在看板独立确认。

微信“扫一扫”进入小程序后，客户端先调用 `wx.requirePrivacyAuthorize`，
等待微信原生隐私确认成功，再获取登录 code 并申请会话。无需在小程序中再扫码或另点登录；
使用微信官方隐私弹窗，不注册自定义同意处理，也不自行绘制授权弹层；
基础库需支持 2.32.3 起提供的隐私接口。不支持或拒绝时不会继续登录。
需要同意时由用户在原生弹窗操作，已有有效同意时由微信直接放行；
已登录用户普通重入继续恢复有效会话，不重复获取 code。
称谓或昵称在“我的”页内自愿填写、保存和清空，不弹窗，也不要求首次必须填写。
这是用户自定义的称呼，不校验真实身份、不自动获取微信昵称，沿用 `name` 字段保存。
上线前在微信后台配置《小程序用户隐私保护指引》，如实声明身份、用户自填称谓或昵称、
用户选择的文件/图片等实际处理内容、用途和处理联系方式，
并核对当前账号实际审核结果，不把个人主体资格或备案等同于所有能力已经通过审核。
用户在“我的”可清空称谓或昵称；OpenID 映射、封禁和用户编号需要独立账号保留策略，
不和临时打印文件一起删除。需要删除账号时由机主核对后处理其数据和备份。
“打印账户编号”是本服务随机生成的内部 ID，供机主权限配置使用，不是原始 OpenID。
小程序不提供独立退出入口，普通重入继续使用有效会话。
需要更换设备时从打印页扫码，新连接成功后替换并撤销旧会话；取消扫码保留原连接。
会话失效后需要重新扫码，称谓、用户身份及其任务归属保留在服务端。

新功能的 schema 3 升级/回退见 [迁移说明](migrations.md)，
活动打印先结束或经用户明确确认处理，不能为启用身份登录擅自取消现有任务。

## 翻纸配置

手动双面的翻纸方式由部署者配置一次，普通用户不再填写页序、旋转或校准确认。
在 `service.env` 按实际 CUPS 队列 ID 设置，例如：

```sh
PRINTBRIDGE_MANUAL_DUPLEX_PROFILES='{"my-rear-feed-queue":"rear-face-up-v1"}'
```

`rear-face-up-v1` 的约定是：后进纸、打印面朝上出纸，第一轮偶数面，
整叠沿纸张物理长边左右翻面，第二轮同序；重新装入时空白面朝向使用者，
原先先出纸的短边先进入。不要逐张倒序，保留奇数文档补齐的空白纸。
长边与短边装订使用相同放纸动作；服务器按装订边和横纵方向计算第二轮旋转。
纸张仍竖着装入后纸盘，横向文档不等于横着放纸。

这是明确选择的设备方案，不是自动检测或“全机型已校准”的声明。启用前应以带方向
标记的短文档核对实际进出纸；设备实测状态见验证记录。其他纸路不要套用此配置。
未配置队列仍可单面/设备支持的自动双面打印，小程序不提供未经配置的手动双面。
已有任务保留创建时的参数和图示版本，更新配置不会改写任务或阶段文件。

用户服务在开机及退出 SSH 后运行需要启用 linger：

```sh
loginctl show-user "$USER" -p Linger
# 仅在尚未启用时由管理员执行：
sudo loginctl enable-linger "$USER"
```

## 检查与日志

Linux 上连接本机 CUPS 的 USB 队列会通过只读 sysfs 检查连接状态；远程 CUPS、
没有 USB 身份或无法读取时显示未知。USB 已连接不等于纸张、墨水及机械状态就绪，
USB 未连接也不能区分关机与拔线。CUPS 可能在设备接回后仍保留暂停，需要独立处理队列；
服务不会自动恢复或修改它。

中文水印默认使用已安装的 Noto Sans CJK；也可以在私有配置指定
`PRINTBRIDGE_WATERMARK_FONT=/path/to/font.ttc`。字体缺失或不含输入字形时预览失败，
不能继续提交该预览；已经准备好的任务不依赖以后再次读取字体。

```sh
systemctl --user status printbridge.service
systemctl --user is-enabled printbridge.service
journalctl --user-unit=printbridge.service -n 50 --no-pager
curl --retry 8 --retry-connrefused --retry-delay 1 --retry-max-time 15 \
  --fail http://127.0.0.1:8765/healthz
```

启动命令返回不代表已完成数据库打开，检查时允许短暂等待。
`healthz` 仅检查服务存活；文档工具与 CUPS 需要单独验收。

以下手动部署检查需要 Python 3、仓库合成夹具和已有 CUPS 队列。
只读取队列能力，创建文档和准备结果，不调用创建、继续或取消打印任务接口，
不作为依赖实体设备的 CI 测试：

```sh
python3 deploy/verify-preview.py --fixtures server/internal/pdf/testdata --office
systemctl --user restart printbridge.service
curl --retry 8 --retry-connrefused --retry-delay 1 --retry-max-time 15 \
  --fail http://127.0.0.1:8765/healthz
python3 deploy/verify-preview.py --fixtures server/internal/pdf/testdata --resume
```

Office 尚未安装时把 `--office` 换成 `--expect-office-unavailable`。
检查会话状态保存在用户缓存目录，权限为 `0600`；不要公开此文件。
预览检查不会证明实际进出纸、翻纸方向或纸面效果正确。
该脚本以打印访问码申请匿名会话，仅适用于未启用微信身份的部署。
微信身份模式请用实际登录的小程序完成上传与预览检查，不关闭身份校验绕过登录。

## 升级与回退

1. 核对并完成或取消现有 PrintBridge 活动任务，再停止 `printbridge.service`。
2. 备份整个 `.local/share/printbridge/data/`、私有配置、服务单元和当前版本软链接。
   数据目录包含 WAL/SHM 和文档，不可只复制一个数据库文件。
3. 将新二进制放入新的 `releases/<release>/`，保留旧版本。
4. 将 `current` 切换到新版本，启动服务，检查日志、存活、合成文件预览及重启恢复。
5. 失败时停止服务；按 [迁移说明](migrations.md) 恢复匹配的完整数据备份和旧二进制。
   存在新提交或结果不确定的作业时必须先核对 CUPS，不能直接恢复旧任务快照重发。

当前没有自动降级 SQL，安装脚本不自动覆盖已有不同版本，也不自动恢复旧数据库。

## 文件保留与当前任务

小程序仅展示当前用户的未完成任务，不提供历史列表。同一用户在同一打印机有任务时，
首页提供“继续任务”，服务端也会拒绝另一份新提交；微信身份模式下重新登录仍可恢复。
兼容匿名模式按会话区分，新会话不能认领旧会话任务；启用微信身份也不自动认领旧匿名任务。

已结束任务的原文件、转换结果、预览和打印阶段文件在下一次成功清理时回收，
正常周期一分钟；同一文档仍被其他活动任务使用时保留所需内容。
未提交文档和计划默认 24 小时过期，终态任务记录及幂等键保留 3 天。
活动任务创建满 3 天先进入取消核对流程，无法确认 CUPS 结果时保留恢复文件和占用，
由管理员核对，不能通过删除数据库记录来强行放行。

这些期限只覆盖 PrintBridge 在线存储。CUPS 的任务文件、历史记录及升级备份是独立副本，
需由部署者核对其保留设置。数据和配置备份按敏感文件管理，不能因网页不显示历史而长期保留。
新版本首次启动可能清理旧终态内容；回退二进制不能还原已清理的文件。

## 中途缺纸

保留当前任务，补纸后按打印机面板提示恢复，不重新上传或再点一次打印。
小程序“继续打印另一面”仅在第一轮完成、纸张全部输出并翻好后使用，
不能替代设备缺纸恢复或 CUPS 队列恢复。

佳能 G2010 系列的支持代码 1000 指引为：在后纸盘正确装纸、调整导纸器，
然后按设备的“黑白”或“彩色”按钮；“停止”会取消打印。
来源：`https://ij.manual.canon/ij/webmanual/ErrorCode/G2010%20series/SC/ERR/1000.html`
（2026-10-21 核对官方原始页面）。
现有 Gutenprint Canon 过滤链没有消费设备回传。已现场确认 G2810 会反馈
`MediaEmpty`，可用下述可选适配器转成标准 CUPS 缺纸状态。
暂停不等同于缺纸；不提供未经验证的远程恢复按钮，实体补纸续打仍待现场验收。

## 可选：Canon 缺纸适配器

仅在已有 Canon IVEC 队列明确需要时由管理员启用；不是驱动安装器，
不替换 Gutenprint、USB 后端或整个 CUPS，也不在应用用户服务安装时自动运行。
本轮已用 G2810 字段和独立 CUPS 验证，其他机型不能凭“Canon”名称推定兼容。
需要本机已有 Python 3、CUPS 管理客户端和 `ipptool`。

先构建适配器，目标架构按设备选择：

```sh
cd server
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath \
  -o ../bin/printbridge-canon-monitor-linux-arm64 ./cmd/printbridge-canon-monitor
cd ..
# 必须先完成或明确取消队列中全部未完成作业。
sudo python3 deploy/install-canon-monitor.py install \
  --queue YOUR_CANON_QUEUE --binary ./bin/printbridge-canon-monitor-linux-arm64
```

安装器在任何持久配置变更前检查未完成作业及 IPP 打印机状态；
有任务、仍在处理或状态未知就退出。作业已从列表消失也可能仍有取消中的后端，
应等待退出后再改配置，不能仅凭列表为空判断。
空闲时暂时拒收新任务并再次检查，保存 PPD 和原监视器，先声明再选用适配器，
最后恢复原接收状态。失败时尝试恢复原 PPD 和监视器。
安装输出给出权限受限的备份目录，回退示例：

```sh
sudo python3 deploy/install-canon-monitor.py rollback /var/backups/printbridge-canon-monitor/BACKUP
```

回退同样要求无未完成作业，且 PPD / 监视器没有后续修改，否则退出供管理员核对。
二进制使用内容摘要命名并保留，避免覆盖或删除其他队列正在使用的版本。
安装、升级和回退都不取消或重发作业。中途进程被强制结束时，先核对备份、队列
接收状态和当前监视器，不盲目恢复接收或再次打印。

适配器在打印期间并行读取 CUPS FD 3，只有明确成功的设备状态才改变缺纸原因。
输入数据结束后继续观察后端；有效重复报文延长观察，连续静默 60 秒后结束，
但不会因超时清除未恢复的缺纸。单文档后端关闭管道时立即结束。
此功能不是打印机空闲时的持续遥测；其他客户端多文档在文件间可能有静默等待。
墨量、其他机械错误和实体完成不在已支持范围内。

小程序重新编译后，任务页在对应任务正在打印且设备报告缺纸时弹窗一次。
补纸并按面板提示继续，无需重新提交；明确恢复后再次缺纸可重新提醒。
页面隐藏或状态读取失败不弹猜测性提醒，也不把未知状态当成恢复。
无需改 HTTP 契约、迁移数据库或重启 PrintBridge 服务。
正式启用和现场验收状态见 [验证记录](verification/2026-10-21-paper-out-adapter.md)。

## 取消中的设备与升级

小程序取消已开始的打印后，先等待 CUPS 后端释放，再自动清理已启用适配的设备。
清理失败保留取消中及占位，不提前开始下一任务；客户端自动轮询，不要求面板停止确认。
旧 CUPS USB 后端可能延迟退出，此适配器不会自行强杀后端。
如确实遇到取消等待数分钟，可按下文显式缩短调度器的取消超时。

G2010/G2810 的可选设备取消依赖主机已有 Python 3、libusb、sudo 和 CUPS 客户端。
目前只支持实测的 04a9:183a、打印接口 1 和精确序列号；其他型号失败时不放行。
无未完成任务且队列空闲时安装（替换服务用户名和队列名）：

```sh
sudo python3 deploy/canon-cancel.py --install SERVICE_USER YOUR_CANON_QUEUE
mkdir -p ~/.config/systemd/user/printbridge.service.d
install -m 600 deploy/systemd/canon-cancel.conf \
  ~/.config/systemd/user/printbridge.service.d/canon-cancel.conf
```

在服务私有环境文件中设置：

```text
PRINTBRIDGE_CANON_CANCEL_HELPER=/usr/local/libexec/printbridge-canon-cancel
```

然后 `systemctl --user daemon-reload` 并重启 PrintBridge。
此配置只允许本机默认 631 端口 CUPS；不能用于远程或测试 CUPS。
专用 drop-in 关闭该服务的 NoNewPrivileges 以允许受限 sudo，主服务身份保持普通用户。
sudoers 仅允许该服务用户对指定队列执行 helper；helper 和策略由 root 所有，
不开放任意 Python、shell 或 USB 命令。安装器拒绝覆盖已有策略，升级前需审查和备份。
卸载时先处理活动任务、备份配置，然后删除环境项、该 drop-in 和对应 sudoers 策略，
重新加载并重启服务；不应在取消尚未完成时直接移除适配。

此修正需同时升级服务端和重新编译小程序。无数据库迁移，未完成状态保存在原任务 JSON 内。
回退到旧服务前必须先处理全部活动任务；旧服务无法理解这次加入的取消保护。
升级仍按本文的停止服务、备份与切换版本步骤进行，不重装驱动或更改 CUPS 队列。
无 helper 的旧部署仍有已废弃的确认 API，但当前小程序不提供该操作，
需由管理员处理设备再使用兼容接口或部署适配器。
详见 [自动设备取消验证](verification/2026-10-21-device-cancellation.md)。

### 可选：缩短 CUPS 取消等待

已安装 CUPS 2.4.10 的 JobKillDelay 默认为 300 秒；不同版本可能不同。
它决定调度器何时强制结束已取消或保留作业的过滤器和后端。
这是主机范围的设置，影响该 CUPS 的所有队列，先核对是否适用于该部署。
仅在全部作业结束、所有队列不再 processing 且应用暂停接收时操作。
`lpstat -W not-completed -o` 为空并不单独证明后端已退出，还要检查 `lpstat -p`。

先备份 `/etc/cups/cupsd.conf`（保留所有者、权限），再使用 CUPS 自己的配置工具：

```sh
sudo /usr/sbin/cupsctl JobKillDelay=5
sudo /usr/sbin/cupsd -t
```

`cupsctl` 更新并重新加载配置，检查配置后还需核对服务正常和队列配置未改变。
5 秒超时后还可能有调度检查间隔和设备清理时间，不承诺五秒内取消完成。
失败时恢复备份、执行 `cupsd -t`，成功后重新加载 cups 服务。
不设置此项的部署继续使用原有等待策略，不能绕过设备取消核对。

隔离回归可在普通用户下运行，不包含物理 USB 后端：

```sh
python3 deploy/verify-canon-monitor.py --binary ./bin/printbridge-canon-monitor-linux-arm64 \
  --cancel-delay 5 --printing-test ./bin/printing-arm64.test
```

详细职责和响应兼容见 [ADR 0022](decisions/0022-cancellation-timeout-and-shared-status.md)。

## 微信与网络

手机必须能访问设备的局域网地址。服务地址和设备访问码由部署者配置到微信入口，
普通使用者不填写地址；私有访问码不能提交进源码。
开发者工具启动项可先指向设备，正式域名、小程序出码及真机入口另行验收。
局域网 HTTP 部署完成不等于微信正式版网络验收或外网访问已经完成。

### 隔离访客网

访客网络应有独立 SSID、桥、地址段和默认拒绝防火墙，并启用客户端隔离。
只允许 DHCP / DNS、上网和 PrintBridge 入口，不能直接开放整个主网、
树莓派 SSH、CUPS 或路由器后台。主网用户继续使用服务原地址。
访客 IPv6 如未配置等价隔离，必须同时关闭地址分配、中继与转发；
不能只封 IPv4 后保留另一条访问路径。

微信的局域网 HTTP 例外要求目标 IP 与手机处于同一网段。
一种做法是只在访客网关映射打印端口到主网服务，
打印码同时携带主网原地址和访客网关地址。例如：

```sh
npm run printer-code -- --server http://192.168.40.8:8080 \
  --alternate-server http://172.22.60.1:8080 --env trial
```

该示例为两个私有网络地址，不是仓库默认配置。
配对码和微信 access token 仍通过原环境变量提供，不写入命令参数或源码。
备用地址最多三个，全部为私有 IPv4 origin；不能混入域名、公网或相同地址。
先更新小程序再更换码：新客户端支持旧码，旧客户端无法识别多地址码。
切换 Wi-Fi 后重新扫码，界面按真实掩码选择入口，不能仅按 IP 前三段猜测。

限速按访客互联网总额设置，局域网打印/预览可单独放行。
启用硬件或软件 flow offload 时必须保证访客两个方向都不会绕过限速规则。
现场采用 nftables 超额丢包，上下行分别 5 / 20 Mbps，不提供 SQM 排队能力或最低速率保证。
其他固件、WAN 名称和透明代理配置需要独立检查，不能直接复制现场规则。

更改网络前保存私有配置和脚本备份，先验证规则，再带自动回滚增量重载。
不要为访客网重置整个路由器、替换认证脚本或强装不匹配的内核模块。
正式现场的地址、验收、备份位置和待测项目见
[访客网验证记录](verification/2026-10-22-guest-network.md)。

## G2010 系列电源管理

佳能 G2010 系列手册的“Managing the Printer Power”说明：Windows 的
Canon IJ Printer Assistant Tool 中可设置 Auto Power Off 超时或 Disable，
以及收到数据时开机的 Auto Power On。修改时打印机需要开着。
手册来源：`https://ij.manual.canon/ij/webmanual/PrinterDriver/W/G2010%20series/1.0/EN/PPG/dg-c_power.html`
（2026-10-20 核对原始页面）。

这不能证明当前机器的超时值，也不能证明树莓派驱动已支持关机唤醒。
当前服务没有读取/更改厂商电源设置的接口；是否能由现有 CUPS 路径自动唤醒需要另行实测。
