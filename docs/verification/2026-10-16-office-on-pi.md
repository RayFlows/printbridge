# 树莓派 Office 验证

日期：2026-10-16。用户完成系统依赖安装后，直接在树莓派验证。
本轮没有修改服务端或小程序业务代码，也没有替换运行中的二进制。

## 环境

- Linux ARM64；PrintBridge `0.3.0-dev.20261014-rpi1` 用户服务为 `active/running`、`enabled`。
  二进制 SHA-256 与 [首次部署](2026-10-14-raspberry-pi.md) 一致。
- LibreOffice `25.2.3.2`，Writer/Calc/Impress 包版本 `4:25.2.3-2+deb13u8`。
- Poppler `25.03.0-5+deb13u4`。
- Noto CJK `1:20240730+repack1-1`，Liberation `1:2.1.5-3`。
  `fc-match` 能匹配 Noto Sans CJK SC 和 Noto Serif CJK SC。

## 检查结果

- 在设备上运行既有 ARM64 PDF 测试程序的 `TestOfficeWithRealConverter`，
  使用真实 `/usr/bin/soffice`、`pdfinfo` 和 `pdftoppm`。
  DOC/DOCX、XLS/XLSX、PPT/PPTX、ODT/ODS/ODP、RTF、TXT、CSV 共 12 项全部通过；
  页数、栅格化和独立 PDF 解析均正确，无跳过项。
- 正式服务的 `deploy/verify-preview.py --office` 通过全部 20 个合成夹具：
  PDF、7 个图片用例及上述 12 个 Office/文本用例。
  覆盖上传、首尾页原稿预览、默认倒序排版预览和准备结果，不调用打印提交接口。
- DOC/DOCX、PPT/PPTX、ODT/ODP 的夹具各为 2 页；表格、文本、RTF 各为 1 页。
  本轮 Office 接口的上传到排版预览完整检查约 4.0–7.2 秒/文件。
  同时运行独立转换测试，时间只记录这组小型夹具的观测值，不作为性能保证。
- 使用 `pdftotext` 对本次会话的 12 个转换 PDF 检查预期文字。
  11 个带中文夹具的关键词完整；RTF 英文夹具文字完整。
- 目视检查 Word、Excel、PPT、TXT 和 CSV 的设备生成页面，中文可读，
  没有发现方块缺字、文字遮挡或截断。PPT 排版预览首先显示第 2 页，符合倒序设置。
- `pdffonts` 确认抽查的 Word/Excel/PPT PDF 已嵌入 CJK 字体。
  实际回退为 Noto 字体：Word 使用 CJK HK，Excel/PPT 使用 CJK SC；
  未安装原文档的全部商业字体，不宣称与原应用排版完全一致。
- PrintBridge 数据库打印任务总数为 **0**，本轮会话产生 20 个合成文档。
  检查前后 CUPS 历史作业数均为 **27**，没有新增实体作业。

## 设备状态与边界

CUPS 报告 G2810 为暂停状态，原因是 `Unplugged or turned off`，当前无活动作业。
本轮保持队列状态，文档转换和预览在此状态下正常工作。
服务没有新增 warning/error 级日志。

本轮只更新部署验证记录和路线图，因此没有重跑与本次环境变化无关的 Go 全量检查；
此前本机 vet/race 和设备虚拟打印结果见首次部署记录。
未重新操作微信模拟器，也没有手机扫码、实际出纸或手动翻纸校准结论。
复杂表格、特殊字体、公式、图表和具体用户 Office 文档仍应在打印前检查预览。
