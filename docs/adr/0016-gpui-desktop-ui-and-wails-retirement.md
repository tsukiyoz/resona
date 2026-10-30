# ADR-0016: GPUI 界面迁移与 Wails 退役

- 日期：2026-09-10
- 状态：Accepted，用户明确要求在当前分支实施
- 决定：只维护 GPUI 产品界面

用户完成多轮原型比较，确认近黑色、轻磨砂、开放聊天、频道树、按需资料及紧凑语音底栏，并要求应用至 GPUI，同时移除旧 frontend。此决定用于结束两套产品 GUI 的维护，不是 Windows 游戏负载性能验收结论。

此决策落地时，产品入口为 `desktop/` 与 `cmd/resona-core`。删除 `frontend/` 产品源码、根目录 Wails 入口/桥接及专属测试、Wails 配置和 Go 依赖。共享 Go 核心、协议、设备与凭据行为不变，Node/React 不是产品构建依赖。

2026-10-23 更新：Rust 客户端迁移完成后，桌面仅通过进程内 `client/` 工作，
旧 Go Core 入口已移除（ADR-0032）。已退役的 `docs/ui-prototype/` 最后说明文件
一并删除，原型保留在 Git 历史中；当前界面与交互以 `desktop/` 和产品文档为准。

2026-10-30 更新：桌面统一依赖 `gpui-kit` 0.8.1，使用默认 GPUI 后端（固定
`gpui-pre` 0.3.10）、Base、组件及资源导出，不启用 GPUI Fast。Kit 负责初始化及
窗口 Root；Resona 保留业务视图、主题、输入语义、资源订阅与有界退出流程。
新版多行输入采用 `Textarea`，主题修改经 `Theme::update` 同步组件及 Base tokens。
旧完整 GPUI vendor 退役；仍必要的 Windows 标题栏与 block 补丁见 vendor 记录。
框架升级不代表性能结论，Windows 流程及等负载性能仍需原生验收。

历史 Wails 基线可以从本次修改前的提交 `78f55563d54f2f7b6c6d3dd571ea1dbe8e1f2d82` 取回。历史测量和 ADR 不删除；Windows 同负载语音/游戏与主要流程验收仍待进行。若出现性能回退，先用现有进程性能工具定位，不声称 GPUI 语言或体积保证低占用。

GPUI 复用已有命令、异步状态和清理路径。资料默认收起，关闭取消旧请求；底栏保留启用/停用、静音、耳聋、音量、设备设置与连接取消/断开。磨砂以静态半透明表面、内高光和边框实现，暂不引入常驻背景模糊或动画。实际效果与 HTML 的 backdrop-filter 不等价，需原生视觉验收。
