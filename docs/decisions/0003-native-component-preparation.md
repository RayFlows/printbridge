# ADR 0003：小程序组件准备与运行目录检查

日期：2026-10-14。状态：采纳。

## 问题

`npm ci` 把 TDesign 安装到 `node_modules`，微信页面则从小程序根目录内的
`miniprogram_npm` 解析组件。原 `setup` 只创建配置，原测试也只检查安装目录，
导致检查通过后仍出现 `t-input` 找不到组件、模拟器无法启动。

## 决策

`npm run setup` 同时准备本地配置与 TDesign 运行文件。TDesign 当前固定版本已经发布完整的
原生 `miniprogram_dist`，包含内嵌 npm 依赖；准备脚本按包的 `miniprogram` 字段读取整个目录，
原样复制到 `src/miniprogram_npm/tdesign-miniprogram`，附上包许可证。
不修改包源码，不自行编译第三方 JavaScript，也不把该脚本扩展为通用 npm 打包器。

生成目录仍被 Git 忽略。重复准备会替换这个已校验位于项目内的生成目录，避免旧版本文件残留；
已有的项目配置、AppID 和开发者工具偏好不会被覆盖。
以后增加没有原生发布目录的依赖时，再引入微信官方 npm 构建流程。

CI 从干净安装执行同一个 `setup`。页面测试检查实际运行目录中的 JS、JSON、WXML、WXSS，
准备流程测试覆盖内嵌依赖、许可、重复执行、保留本地配置和缺少依赖时明确失败。
这些检查不替代微信模拟器及手机验收。
