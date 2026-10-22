# 开发说明

## 环境

服务端使用 Go 1.26 或更高版本，IPP 依赖通过 Go Modules 固定。
自动检查不需要外部 CUPS、数据库服务或真实打印机；设备读取使用模拟 IPP，
SQLite 使用临时目录，默认文档工具检查使用测试进程。
小程序使用原生 TypeScript 和 TDesign，开发步骤见 [客户端说明](../miniprogram/README.md)。

命令均在 `server/` 中运行：

```sh
go run ./cmd/printbridge
go run ./cmd/printbridge -version
go vet ./...
go test -race ./...
```

race 检查需要支持的 C 编译器。Windows 上没有编译器时可先运行 `go test ./...`，
但这不能代替 CI 的 race 检查，提交说明中要区分两者。

可选 Canon port monitor 的解析与管道测试包含在上述检查中。
Linux 的命令测试使用继承的假管道，默认不依赖 CUPS 或实体打印机。
安装器的假队列检查在仓库根目录运行：

```sh
python3 -m unittest discover -s deploy/tests
```

另可在已有 CUPS 工具的 Linux 开发机上，以普通用户运行独立调度器验证：

```sh
cd server
go build -o ../bin/printbridge-canon-monitor ./cmd/printbridge-canon-monitor
cd ..
python3 deploy/verify-canon-monitor.py --binary ./bin/printbridge-canon-monitor
```

脚本仅使用随机回环端口、临时目录、合成 PPD 和自己的假后端，不接入现有队列。
正式安装另见部署说明，不能把独立调度器的通过当作实体缺纸续打验收。

## 配置

配置只在启动时读取，传给各模块。非法值使进程以非零状态退出，不静默使用其他值。

| 环境变量 | 默认值 | 约束 |
| --- | --- | --- |
| `PRINTBRIDGE_LISTEN_ADDR` | `127.0.0.1:8080` | `host:port`；IPv6 使用方括号；端口范围 0 到 65535，0 用于临时端口 |
| `PRINTBRIDGE_LOG_LEVEL` | `info` | `debug`、`info`、`warn`、`error`，不区分大小写 |
| `PRINTBRIDGE_SHUTDOWN_TIMEOUT` | `10s` | Go duration 格式，必须大于 0 |
| `PRINTBRIDGE_CUPS_URL` | `http://localhost:631` | CUPS 的 HTTP(S) origin，不含凭据、查询、片段或其他路径 |
| `PRINTBRIDGE_CUPS_TIMEOUT` | `5s` | 大于 0、不超过 `1m` |
| `PRINTBRIDGE_PAIRING_CODE` | 未设置 | 未设置时关闭配对；设置后必须为 32 至 128 位无空格可打印 ASCII 字符 |
| `PRINTBRIDGE_SESSION_TTL` | `24h` | 大于 0、不超过 `720h`；schema 2 保留未到期会话，更换访问码撤销旧会话 |
| `PRINTBRIDGE_WECHAT_APP_ID` | 空 | 与 AppSecret 成对配置；启用服务端微信身份验证 |
| `PRINTBRIDGE_WECHAT_APP_SECRET` | 空 | 仅保存在服务端私有环境文件，不进入小程序、源码或日志 |
| `PRINTBRIDGE_ADMIN_USER_ID` | 空 | 唯一机主的 32 位内部编号；需先启用微信登录，不按先到顺序授权 |
| `PRINTBRIDGE_DATA_DIR` | `data` | 相对进程工作目录或绝对路径，保存 SQLite 和文档；不作为 HTTP 静态目录 |
| `PRINTBRIDGE_DOCUMENT_TTL` | `24h` | 大于 0、不超过 `168h`；到期立即禁止读取，每分钟回收文件 |
| `PRINTBRIDGE_PDF_TIMEOUT` | `30s` | 单次外部工具执行时间，大于 0、不超过 `1m` |
| `PRINTBRIDGE_PDFINFO` | `pdfinfo` | PATH 中的命令名或完整可执行文件路径，不含命令参数 |
| `PRINTBRIDGE_PDFTOPPM` | `pdftoppm` | PATH 中的命令名或完整可执行文件路径，不含命令参数 |
| `PRINTBRIDGE_OFFICE_COMMAND` | `soffice` | LibreOffice 可执行文件；Windows 推荐 `soffice.com` 完整路径 |
| `PRINTBRIDGE_OFFICE_TIMEOUT` | `2m` | 单次 Office 转换超时，大于 0、不超过 `5m` |
| `PRINTBRIDGE_WATERMARK_FONT` | 自动探测系统字体 | 可选 TTF/OTF/TTC 文件路径；Linux 优先 Noto CJK，Windows 微软雅黑，macOS PingFang；无系统字体时 Go Regular 仅兜底拉丁文。缺少字形返回明确错误 |
| `PRINTBRIDGE_MANUAL_DUPLEX_PROFILES` | 未设置 | JSON 对象，队列 ID 到翻纸配置 ID；目前仅支持 `rear-face-up-v1`，未知 ID 启动失败；不修改 CUPS |

例如 PowerShell：

```powershell
$env:PRINTBRIDGE_LISTEN_ADDR = '127.0.0.1:8081'
go run ./cmd/printbridge
```

Linux/macOS：

```sh
PRINTBRIDGE_LISTEN_ADDR=127.0.0.1:8081 go run ./cmd/printbridge
```

监听地址写入启动日志，配对码和会话令牌不写日志。默认只接受本机连接。
Ctrl+C 触发平滑关闭。服务先停止接受连接，等待已有请求，超过配置时间后关闭剩余连接。

## 设备配对与 CUPS

先构建二进制，再生成并保存独立的设备配对码。该码不是 CUPS 管理密码，不要复用账号密码。
生成命令只将新码输出到当前终端，正常运行不会把它输出到日志：

```sh
./printbridge -new-pairing-code
```

PowerShell 本机验证示例，在仓库根目录运行：

```powershell
$env:PRINTBRIDGE_PAIRING_CODE = & .\bin\printbridge.exe -new-pairing-code
$env:PRINTBRIDGE_LISTEN_ADDR = '127.0.0.1:8080'
.\bin\printbridge.exe
```

部署到已有 CUPS 的 Linux 设备时，把同一配对码保存到设备的私有服务配置中，
通过 `PRINTBRIDGE_PAIRING_CODE` 传入进程，监听地址改为设备的局域网地址或 `0.0.0.0:8080`。
不要将配置中的配对码提交到 Git。M1 最初不包含 systemd 安装；
当前用户级服务、目录布局及安装步骤见 [部署说明](deployment.md)，不修改 CUPS 队列或驱动。
CUPS 运行在同一设备时使用默认 URL 即可；本项目通过 IPP 读取信息，不依赖 `lpstat` 命令解析。

服务地址和访问码由部署者写入小程序码，普通用户只扫码，不手工配对。
在 `miniprogram/` 使用 `npm run printer-code` 调用微信出码接口，步骤与环境变量见
[客户端说明](../miniprogram/README.md#生成打印码)。微信 access token 仅用于部署者出码工具，
不传入 Go 打印服务，不进入小程序运行目录。用户手工输入地址的旧入口已移除。

客户端从码中解析地址和访问码，先读取 `/api/v1/server`，
再自动向 `POST /api/v1/sessions` 发送 `{"pairingCode":"..."}`。
随后将得到的令牌放入 `Authorization: Bearer ...`，读取 `GET /api/v1/printers`。
`DELETE /api/v1/session` 撤销当前会话。不能用配对码直接访问设备接口。
默认每个服务每分钟最多 10 次配对尝试，最多 128 个有效会话；到期会话在验证或配对时清理。

没有 CUPS 的开发机仍可启动：存活/版本和已配置的配对可用，打印机列表明确返回 `503 cups_unavailable`。
没有配对码时申请会话返回 `503 pairing_disabled`，未经授权的队列访问返回 `401 session_expired`。
查看 [OpenAPI](../api/openapi.json) 获取完整响应和错误码。

初版用于可信局域网，HTTP 会明文传输配对和会话。需要传输保密时通过现有 HTTPS 反向代理接入，
不要直接把当前服务公开到互联网。微信网络权限、域名、入口与账号资格以真机验证为准。

## 构建

### PDF 工具

M2 需要设备已安装 Poppler 的 `pdfinfo` 和 `pdftoppm`。Debian / Raspberry Pi OS
可单独安装 `poppler-utils`；不需要因此替换 CUPS 或打印驱动。Windows 将工具目录放入 PATH，
或设置上述两个可执行文件路径。启动不要求工具存在，缺失时文档接口返回
`503 pdf_tools_unavailable`，扫码及队列读取仍能工作。

每次上传一个文件，原文件 20 MiB，转换结果 128 MiB / 500 页；HTTP multipart 请求体额外允许 64 KiB 封装。
HTTP 读取超时 90 秒。一次接收一份上传，一次执行一个文档操作；繁忙返回 `503 document_busy`，
客户端提供重试。文档最多 64 份，文件预算 512 MiB（不含 SQLite 和 HTTP 暂存）。
预览长边最多 1600 像素、单张 12 MiB，生成时可能临时占用额外一张图的空间。
页数和预览失败不触发打印，不伪造可用结果。

真实工具联调只使用仓库中的三页合成 PDF（第 2 页横向）：

```sh
PRINTBRIDGE_TEST_POPPLER=1 go test ./internal/pdf -run TestPopplerWithSyntheticDocument -v
```

PowerShell 先设置 `$env:PRINTBRIDGE_TEST_POPPLER = '1'`，再运行相同 `go test`。
此检查明确要求两个工具都在 PATH；未启用时会跳过，必须区分实际运行与跳过。
测试 PDF 的 ReportLab 生成源在 `internal/pdf/testdata/generate.py`；运行测试不需要 Python。

### Office、图片与打印

Office 类型（DOC/DOCX、XLS/XLSX、PPT/PPTX、ODT/ODS/ODP、RTF、TXT/CSV）
通过独立 LibreOffice 进程转 PDF，每次使用隔离、禁宏的临时 profile。
设备需另行安装 LibreOffice Writer/Calc/Impress 和文档所需中文字体；不随之安装或替换 CUPS。
缺少转换器返回 `503 office_unavailable`，转换失败返回 `422 conversion_failed`。
字体替代及复杂 Office 排版可能与原应用不同，必须查看预览。

JPG/JPEG、PNG、WebP、BMP、TIFF、GIF 使用 Go 图像解码和 pdfcpu；
JPEG 应用 EXIF 方向，多页 TIFF 保留页数，GIF 只取首帧。
每张图解码上限 4000 万像素。原文件保留于独立路径，不充当公开下载文件。

真实工具测试使用合成文档与图片，不发送任何 CUPS 打印：

```sh
PRINTBRIDGE_TEST_POPPLER=1 PRINTBRIDGE_TEST_OFFICE=soffice go test -race ./...
```

`pdftotext` 也需在 PATH，供页序独立核对。未设置工具测试变量时相应测试明确 skip。
测试夹具已经包含在仓库；可选再生成脚本为 `generate_formats.py`，
需要 python-docx、openpyxl、python-pptx、Pillow；`--office soffice` 可生成旧格式和 ODF 夹具。
这些开发工具不作为运行时服务依赖。

打印准备使用独立 Plan。布局预览显示最终纸面；手动背面的顺序和设备旋转在阶段文件中应用。
打印存储预算 2 GiB，最多 64 个未过期 Plan / 32 个活动 Job，输出面数乘份数最多 2000。
预算在操作前后检查，转换中间文件可能额外占用磁盘，部署时需保留余量。
未提交的 Plan 24 小时到期；终态作业的阶段文件在下一次成功清理时回收，正常周期一分钟。
同一文档没有其他活动任务时，一并回收原文件、转换结果、预览和相关计划。
终态记录及幂等键保留 3 天。活动任务创建满 3 天先取消并核对 CUPS，
无法确认取消的任务保留文件，不能强行释放打印机。当前没有跨会话认领接口。
同一已验证用户在同一打印机只能有一个未完成任务；匿名兼容模式仍按会话区分，
幂等重试返回原任务。
小程序使用 `GET /api/v1/jobs?active=true` 恢复当前任务，不显示历史列表。
CUPS spool 与部署备份不由本服务清理，需独立确定保留期限。

CUPS 需允许运行服务的 IPP 身份创建 held job、发送、查询、释放和取消自己的作业，
并报告 job-name、job-id、job-state、job-hold-until、number-of-documents。
不满足可靠恢复所需属性时，任务保持不确定状态，不自动重复发文件。
`awaiting_flip` 锁住本程序对该打印机的其他任务；直接系统打印的作业不受此锁保护。
实际打印和校准必须由操作者明确指定文档、纸张并确认，不作为 CI 或启动检查。

### 编译命令

```sh
go build -trimpath -o ../bin/printbridge ./cmd/printbridge
go build -trimpath -ldflags="-X main.version=0.1.0-dev" -o ../bin/printbridge ./cmd/printbridge
```

Windows 产物可使用 `../bin/printbridge.exe`。版本未注入时为 `dev`。

Linux ARM64 交叉编译：

```sh
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -o ../bin/printbridge-linux-arm64 ./cmd/printbridge
```

交叉编译仅证明目标平台构建成功，不代表已在树莓派运行或完成打印机验收。

## 检查与契约

Go 文件使用 `gofmt`。CI 对 Windows/Linux 运行 `go vet`、race 测试和构建，
并检查 Linux ARM64 构建。客户端 CI 使用锁文件安装，运行类型和行为检查。
CI 配置存在不代表远端已经执行；首次推送后需要查看实际结果。

`api/openapi.json` 描述已实现接口。测试覆盖响应、错误方法、未知路由、配置错误、停机、
配对限流、过期/撤销会话、异常 IPP、多个队列、能力缺失和装配后的完整只读 HTTP 流程。
增加业务接口时同步修改契约，并加入正常及失败路径测试。
测试数据应为合成或明确允许分发的文档，不能提交用户文件或个人机器配置。

## 变更与来源

功能变化同时更新 `docs/roadmap.md`。涉及依赖方向、存储或版本兼容的决策记录到
`docs/decisions/`。代码复用遵守 `THIRD_PARTY_NOTICES.md`，完整保留来源许可。
提交前确认未包含运行数据、访问凭据、个人 AppID 或开发者工具的私有配置。

当前 schema 3，备份、升级与回退步骤见 [迁移说明](migrations.md)。
首次从 M2 更新时内存会话失效，需重新扫码；此后持久化会话在有效期内跨重启恢复。
此前 v1 仅地址二维码需要由部署者更新为 v2；无需更改现有会话 API 或 CUPS 队列。

微信身份增量使用 `PRINTBRIDGE_WECHAT_APP_ID`、`PRINTBRIDGE_WECHAT_APP_SECRET` 与可选
`PRINTBRIDGE_ADMIN_USER_ID`。前两项成对设置，最后一项是唯一机主的内部用户编号，
用户登录后可从“我的”复制。自动检查用合成 AppID 与模拟微信传输，不接触个人账号。
`accounts` 与 `management` 的权限必须在服务端业务入口执行，小程序角色缓存只用于展示。
同一已验证用户的新令牌保持文档/任务归属；旧匿名任务保留原 owner，不自动分配。
暂停新打印的配置不会修改 CUPS；既有任务的继续、取消和幂等查询仍可用。
