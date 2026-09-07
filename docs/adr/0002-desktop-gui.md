# ADR-0002：Wails v2 + React/TypeScript GUI

- 日期：2026-09-07
- 状态：Accepted

## 背景

用户明确 GUI 优先，希望更美观、支持未来插件，同时核心维护栈是 Go。之前的 TUI 方向已纠正。

## 决定

使用 Wails v2.15.0 的原生 WebView 外壳，React/TypeScript 负责界面，Go 负责持久化、会话以及未来协议与音频。初期验证 macOS arm64。

Wails v3 官方仍标为 beta，当前采用 v2 减少框架变动。前端 bridge 集中封装，业务核心不得依赖 Wails，方便后续升级外壳。

正式应用通过 Wails 绑定通信；浏览器预览仅作为开发路径。不建立为了本地 GUI 而存在的独立 HTTP 服务。实时音频不经过 JSON 桥接逐帧传输。

## 替代方案

- Fyne：可保持界面 Go 实现，但本项目优先考虑 Web UI 的布局和视觉定制空间。
- Electron：可实现类似界面，但本项目已有 Go 核心且不需要捆绑完整浏览器运行时作为首选。
- TUI：不符合已修正的产品目标。

## 代价

维护 Go 与 TypeScript 两套工具链，依赖平台 WebView 行为及原生编译环境。仅通过浏览器检查不代表桌面绑定和打包完成。

## 重新评估条件

Wails 生命周期、音频/热键系统集成或跨平台 WebView 差异阻碍日常客户端体验时，重新评估框架或适配策略。

依据：[Wails 安装与版本信息](https://wails.io/docs/gettingstarted/installation/)、[项目布局](https://wails.io/docs/gettingstarted/firstproject/)。
