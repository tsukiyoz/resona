# ADR-0011: GPUI 原生桌面实验与 Go 子进程核心

- 日期：2026-09-08
- 状态：Accepted（实验版本，不代表性能迁移验收通过）
- 关联：ADR-0002 保留 Wails 基线；本决定增加独立原生入口。

## 背景

电竞常驻客户端要求低后台 CPU、总内存与 GPU 干扰。用户要求本轮尝试 GPUI，同时接入实际语音，覆盖 macOS 与 Windows。不能从 Rust 或非 WebView 推断性能已经优于旧客户端。

## 决定

`desktop/` 使用固定 `gpui 0.2.2` 与 `gpui-component 0.5.1`，Go 业务和 TS3 适配保留。GUI 启动同包的 `resona-core` 子进程，通过继承的 stdin/stdout 交换 NDJSON；不监听本地 HTTP 端口。stderr 用于诊断，stdout 仅含协议帧。

请求包含非零 `id`、`method` 与对象 `params`，响应回显 ID；事件包含 `event` 与 `result`。`GetCapabilities` 报告协议版本 1、平台、语音及安全密码存储能力。界面不得把不支持的凭据存储呈现为可用。

Go 服务通过容量为 1 的通知通道合并变化，IPC 仅在快照变化时发布 workspace/voice。无变化时不定时轮询。当前事件仍传完整变更快照，图标和长历史的独立资源协议留待测量后设计；不能将此阶段描述成字段级增量同步。

音频采集、Opus、抖动缓冲、混音和设备生命周期全部留在 Go；JSON 只传控制和状态。GUI 生命周期拥有子进程，关闭管道或 Shutdown 会取消服务并回收连接和音频；Rust 退出路径保留终止并等待子进程的兜底。

## 取舍与验证门槛

子进程增加序列化和独立运行时成本，但避免本轮 C ABI 内存所有权与回调线程复杂度。必须测量两个进程总成本。Wails 不删除，直到原生文字输入、主要工作流、多平台和同负载性能通过。

macOS 通过 runtime_shaders 支持没有完整 Xcode Metal 编译器的构建环境。Windows 提供本机构建路径，运行验收由用户后续完成；macOS 结果不代表 Windows 已验证。安装包必须携带核心程序和麦克风权限用途说明。

书签、频道层级与滚动、spacer、自定义图标、聊天状态、失败重试和提示音均是原生验收范围。真实语音与硬件测试证据另记 docs/verification.md。
