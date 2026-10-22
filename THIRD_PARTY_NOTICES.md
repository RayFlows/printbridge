# 第三方来源记录

## 当前状态

当前使用以下固定版本依赖，未复制 cups-web 或其他模板的业务实现。
Go 工具链及 GitHub Actions 等开发工具保留各自许可；它们不因此成为本项目原创代码。

| 依赖 | 固定版本 | 使用范围 | 许可 |
| --- | --- | --- | --- |
| `github.com/OpenPrinting/goipp` | `v1.2.0` | IPP 编码与解码，由本项目 `server/internal/cups` 调用 | [BSD-2-Clause](LICENSES/goipp-BSD-2-Clause.txt) |
| `modernc.org/sqlite` | `v1.60.1` | 无 CGO 的 SQLite 驱动，由 `server/internal/store` 调用 | [BSD-3-Clause 及内嵌组件许可](LICENSES/go/modernc.org_sqlite@v1.60.1/LICENSE-3RD-PARTY.md) |
| `github.com/pdfcpu/pdfcpu` | `v0.16.2` | 图片转 PDF、页面抽取、拼版、缩放、补空白、旋转、水印及页面效果，由 `server/internal/pdf` 调用 | [Apache-2.0](LICENSES/go/github.com_pdfcpu_pdfcpu@v0.16.2/LICENSE.txt)，内嵌 LZW 与 PKCS7 许可保留在相应子目录 |
| `github.com/disintegration/imaging` | `v1.6.2` | JPEG EXIF 方向归一化 | [MIT](LICENSES/go/github.com_disintegration_imaging@v1.6.2/LICENSE) |
| `golang.org/x/image` | `v0.46.0` | BMP、TIFF、WebP 解码及水印字体解析/栅格化；内嵌 Go Regular 作为拉丁文字体兜底 | [BSD-3-Clause](LICENSES/go/golang.org_x_image@v0.46.0/LICENSE)，[Go 字体许可](LICENSES/go/golang.org_x_image@v0.46.0/Go-Fonts-LICENSE.txt) |
| `tdesign-miniprogram` | `1.17.0` | 小程序按钮、图标和加载指示 | [MIT](LICENSES/TDesign-MIT.txt) |
| `tdesign-icons-svg` | `0.4.5` | 浮动底栏的打印、用户图标原稿及本地 PNG | [MIT](LICENSES/TDesign-icons-MIT.txt) |
| `miniprogram-api-typings` | `5.2.3` | 微信 API 开发期类型检查 | MIT，随 npm 包附带 |
| `typescript` | `5.9.3` | 开发期编译及检查 | Apache-2.0，随 npm 包附带 |

Go 依赖由 `server/go.mod` / `go.sum` 固定并校验，客户端依赖由 `miniprogram/package-lock.json`
固定并校验。未修改上述包源代码；更新时跑对应协议、客户端服务和页面测试。
分发服务端二进制和小程序时应同时保留 `LICENSES/` 中的运行时依赖许可。
客户端准备脚本将 TDesign `1.17.0` 包的 `miniprogram_dist/`（含其内嵌依赖）原样复制到
被忽略的 `miniprogram/src/miniprogram_npm/tdesign-miniprogram/`，同时复制包的 `LICENSE`；
不修改组件或内嵌依赖代码，依赖更新仍通过锁文件和回归检查完成。

SQLite 新增的运行时传递依赖以 `server/go.mod` 固定版本，完整包内
LICENSE / COPYING / NOTICE / AUTHORS 文件按原样保存在 `LICENSES/go/<module>@<version>/`：
`go-humanize`、`uuid`、`go-isatty`、`go-strftime`、`bigfft`、`golang.org/x/sys`，
以及 `modernc.org/libc`、`mathutil`、`memory`。没有修改依赖源代码；
分发二进制时连同此目录保留声明。驱动随包附带的 SQLite 公有领域声明及其他内嵌组件许可一并保留。

格式/布局功能新增的运行时传递依赖亦按 `go.mod` 中版本保存包内许可：
`clipperhouse/uax29/v2`、`hhrutter/tiff`、`go-runewidth`、`xdg-go/stringprep`、
`go.yaml.in/yaml/v3`、`golang.org/x/crypto`、`golang.org/x/text`。
库代码未作本地修改；本仓库只调用固定版本 API，不复制其业务源码。

文档工具通过独立进程调用外部安装的 Poppler `pdfinfo` / `pdftoppm`，
不复制或随本仓库分发其可执行文件。2026-10-14 本地验证版本为 `26.07.0`，
部署时由操作系统维护该依赖。后续若制作包含工具的镜像或安装包，需按实际打包版本
保留其许可证、版权及相应分发材料，不能将外部工具当作本项目 MIT 源码。

水印默认读取操作系统中已安装的 Noto CJK/微软雅黑/PingFang，本仓库不分发这些字体。
Go Regular 随 `golang.org/x/image/font/gofont/goregular` 编入二进制，字体版权为
Bigelow & Holmes Inc.；原包 `font/gofont/ttfs/README` 的完整许可保留在
`LICENSES/go/golang.org_x_image@v0.46.0/Go-Fonts-LICENSE.txt`。

Office 转换通过独立安装的 LibreOffice 执行，不随 Git 仓库分发可执行文件。
2026-10-14 本地验证使用 LibreOffice 26.8.1，官方 Windows 安装包的
The Document Foundation 数字签名有效；仅解包到被忽略的开发工具目录。
该外部工具及其组件适用各自许可证，制作容器或安装包时需另按实际分发范围保留许可。

合成 PDF 的可选再生成脚本使用 ReportLab；测试运行只读取仓库合成 PDF，
不要求或分发该 Python 依赖。该文档由本项目自行生成，不含用户资料。
Office/图像夹具的再生成脚本使用 python-docx、openpyxl、python-pptx、Pillow 和可选 LibreOffice；
夹具为本项目生成的测试内容，Go 测试不需要这些 Python 包。

## 已调研的参考项目

| 项目 | 调研版本 | 用途 | 当前是否复制实现 |
| --- | --- | --- | --- |
| `hanxi/cups-web` | `v0.2.16` / `b2a3082b635b7fd2abd0e5efadfdd69609200e52` | 文档转换、IPP、部署经验；M2 文件信息与分页预览布局参考 | 否 |
| `Tencent/tdesign-miniprogram-starter-retail` | 2026-10-14 调研，未引入 | 原生小程序零售模板；业务不匹配，未整体复用 | 否 |
| `wechat-miniprogram/miniprogram-demo` | 2026-10-14 调研，未引入 | 官方平台组件/API 示例；未复制业务代码 | 否 |
| `noble6/canon-universal-driver` | `c087e1b0da01a7cc33532a774c56384c7da81e81` | USB/IVEC 取消协议调研，原项目 GPL-3.0-or-later；本机仅软复位已实测有效 | 否 |

设备取消参考文件为 `docs/PROTOCOL_IVEC.md`、`src/backend/canonivec.c`、
`research/usbprobe.c`；未复制、翻译或链接该项目源码。
`deploy/canon-cancel.py` 为本项目独立编写的受限适配器，调用操作系统安装的
libusb 运行库，不随仓库分发该外部库。实际分发含运行库的镜像时应另保留对应许可。

来源仓库：`https://github.com/hanxi/cups-web`。
M2 阅读的具体参考文件为 `frontend/src/components/print/FileUpload.vue`、
`PrintPreview.vue`、`PdfCanvas.vue`。本项目独立编写原生微信页面及 Go 文档实现，
没有将这些上游文件复制或翻译成项目源代码。

小程序选型来源：`https://github.com/Tencent/tdesign-miniprogram`、
`https://github.com/Tencent/tdesign-miniprogram-starter-retail`、
`https://github.com/wechat-miniprogram/miniprogram-demo`。以官方仓库的说明、LICENSE 和 npm 包元数据核对，
选用组件库而非整个应用模板；具体职责见 [ADR 0002](docs/decisions/0002-device-connection.md)。

## 后续复用记录

2026-10-21 底栏图标从 npm 发布包 `tdesign-icons-svg@0.4.5` 的
`src/print.svg`、`src/print-filled.svg`、`src/user.svg`、`src/user-filled.svg`
原样复制到 `miniprogram/src/assets/tabs/`。包 SHA-512 完整性为
`TPnCjvKYIMXeqPuXDsd6MjT3WqFWanlB63EU6U1MDz9GO1zYg3vb2oZwhpmD0VpIap12u8H8fUbNze9AahGooQ==`。
上游为 `https://github.com/Tencent/tdesign-icons`，原包 `LICENSE` 保存在上述许可路径。
PNG 使用 SVG 的 `currentColor` 分别替换为 `#697579` / `#087f73`，
将发布原稿的 `view-box` 规范为 `viewBox` 并设置 24×24 视口，再通过 sharp 栅格化到
96×96；不改动图标路径。保留的 SVG 原稿未修改。运行时只读取 PNG，未引入新的包依赖。
后续图标更新需固定上游版本、更新原稿和 PNG，并运行客户端资源与导航检查。

2026-10-19 翻纸图示参考佳能 G2010 系列（包含 G2810）手册的装纸事实，
来源见 `docs/decisions/0010-manual-reload-guide.md`。
`miniprogram/src/assets/reload-*.svg` 为本项目独立绘制的示意，不包含厂商图片、
商标或手册原文，按本仓库 MIT 许可证分发；不表示厂商认可或实体校准完成。

引入第三方代码时，同一次修改必须增加以下信息：

- 上游仓库、固定提交或版本、来源文件路径。
- 本项目对应路径、复制范围和本地修改。
- 原作者版权声明和完整许可证所在位置。
- 后续跟踪上游修复的方式，以及覆盖本地修改的测试。

完整许可放在 `LICENSES/` 下，随第一次实际引入建立。新仓库或新文件名不改变继承代码的许可义务。
普通包依赖通过对应包管理器的清单和锁文件固定；分发产物的许可清单在引入依赖和制作发布包时同步维护。
