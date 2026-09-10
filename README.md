# Resona / 共鸣

以 Go 为核心的桌面语音客户端与服务端项目，TeamSpeak 3 是第一个兼容协议。当前唯一产品界面为 Rust GPUI `desktop/`，通过私有管道与 Go `resona-core` 通信。产品构建不需要 Node.js、WebView 或 Wails。

界面采用近黑色、轻磨砂表面、频道树、聊天主区、按需资料与固定语音工具栏。支持书签、系统密码存储、频道文字、Opus 语音、按键/持续/语音检测、音频处理与本地麦克风试听。Windows 实机和游戏同负载验收仍在进行，详情见 [验证记录](docs/verification.md)。

## 开发与构建

需要 Go 1.26、Rust 及原生编译工具；macOS 需要 Xcode Command Line Tools。Windows 使用仓库根目录的 `build-windows.cmd`，首次工具安装和构建说明见 [Windows 构建](docs/windows-build.md)。

在仓库根目录启动开发版：

```sh
make dev
```

macOS 发布构建：

```sh
make core
./desktop/scripts/package-macos.sh
```

应用位于 `desktop/dist/Resona.app`，双击打开。普通 Rust release 二进制用 `make build` 构建；分发时优先使用打包脚本，它会带上 Go 核心。Windows 包含 `resona-desktop.exe` 与辅助进程 `resona-core.exe`，启动前者。

## 验证

```sh
go test ./...
cargo test --manifest-path desktop/Cargo.toml
make test-protocol
```

完整 Go 测试不再需要预先构建 HTML 资源。真实服务器测试只在 Resona 创建的专用测试频道进行。

HTML 风格原型是独立设计工具，不是第二套产品 GUI：

```sh
cd docs/ui-prototype
npm ci
npm run build
```

生成的 `mineradio.html` 可直接打开，只包含示例数据。Wails 已退役，历史基线及迁移理由见 [ADR-0016](docs/adr/0016-gpui-desktop-ui-and-wails-retirement.md)。

## 数据与边界

- 书签与身份位于 `os.UserConfigDir()/resona/`。密码由 macOS Keychain 或 Windows Credential Manager 保存，不进入书签 JSON。
- 语音音频留在 Go 核心，界面只传控制与状态，不逐帧传 PCM。
- 本地预览不连接 TS3；麦克风试听只回放到本机，不向频道发送。
- 频道消息仅在本次会话内存保留，跨频道最多 500 条；未确认消息不会自动重发。
- 自定义图标使用有限内存与磁盘缓存，详见 [架构](docs/architecture.md)。
- 暂未选择开源许可证，不将本仓库描述为已完成开源授权的 SDK。

更多资料见 [文档索引](docs/README.md)与 [原生桌面说明](desktop/README.md)。
