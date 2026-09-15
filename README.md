# Resona / 共鸣

面向游戏连麦的桌面语音应用与服务端。Rust GPUI 桌面通过私有管道控制 Go 核心；服务端使用 Go 转发语音。唯一网络协议为 Noise UDP，控制消息使用 Protobuf，音频使用 Opus。

支持服务器书签、系统密码存储、频道聊天、多人语音、单人收听音量、麦克风增益、按键/持续/阈值发言、回声消除、降噪和在线本地试听。所有者可认领服务器、管理频道及设置频道音质。当前仍为实验版本，客户端与服务端需配套升级。

## 构建

需要 Go 1.26、Rust 和原生 C 编译工具。macOS 需要 Xcode Command Line Tools；Windows 运行根目录 `build-windows.cmd`，详见 [Windows 构建](docs/windows-build.md)。

```sh
make                 # 默认构建全部
make build           # 核心 + 桌面 + 服务端
make build-core      # Go 音频核心（需要 C 编译器）
make build-desktop   # 核心 + 桌面；macOS 生成 Resona.app
make build-server    # 纯 Go 服务端（不需要 C 编译器）
make dev
make clean           # 删除 build/bin/、desktop/dist/，保留编译缓存
make clean-all       # 额外删除 desktop/target/，下次 Rust 构建会更慢
make help
```

Makefile 面向 macOS/Linux。macOS 桌面产物为 `desktop/dist/Resona.app`；Linux 桌面与核心一起放在 `build/bin/`。核心和服务端分别为 `build/bin/resona-core`、`build/bin/resona-server`。Windows 继续使用 `build-windows.cmd` 配置 MSVC/UCRT64，启动 `resona-desktop.exe` 并保留同目录的 `resona-core.exe`。产品不需要 Node.js 或 WebView。

`make core` 保留为 `make build-core` 的别名。`make build VERSION=v0.1.1` 可设置 Go 二进制版本元数据；默认 `dev`，不会自动改 Cargo 或发布版本。服务端支持如 `GOOS=linux GOARCH=amd64 make build-server` 的交叉编译；桌面按本机平台构建。

直接执行 `./desktop/scripts/package-macos.sh` 也会通过 `make build-core` 构建当前核心。`make build-desktop` 复用其刚构建的核心，避免重复编译。只有显式设置 `RESONA_CORE_BINARY` 才会复用指定外部核心，需自行确保版本配套。先退出旧进程，再清理/打包并打开新应用。

`clean` 不删除 `build/appicon.png`、`build/deploy-*`、`build/` 下其他目录中的性能报告、用户配置或全局 Go/Cargo 下载缓存。不要把需要保留的数据放在 `build/bin/` 或 `desktop/dist/`。不要在同一次调用中混用 `clean` 和构建目标；按顺序分别执行。

## 测试

```sh
make test-native
go test ./internal/... ./cmd/... ./tools/desktop-perf
```

默认测试使用本机临时服务器，不访问用户公网服务。设备听测和 Windows 游戏负载验收独立进行。

## 数据与边界

- 书签与原生身份位于系统用户配置目录的 `resona/`。密码仅存 macOS Keychain 或 Windows Credential Manager。
- 添加服务器需要地址和可信渠道获得的 X25519 公钥，不自动信任未知公钥。
- 不支持的旧书签保留供删除或重新编辑，不能发起连接。
- 音频采集、处理、编解码与混音均在 Go 核心；服务端不做编解码，GUI 不传逐帧 PCM。
- 本地预览不联网；麦克风试听不向频道发送。聊天历史为本次连接最多 500 条内存记录。
- 暂未选择项目开源许可证，不将本仓库描述为已完成开源授权的 SDK。

[服务端运行](docs/server.md) · [文档索引](docs/README.md) · [桌面说明](desktop/README.md)
