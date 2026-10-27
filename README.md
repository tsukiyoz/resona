# Resona / 共鸣

面向游戏连麦的桌面语音应用与服务端。Rust GPUI 桌面调用进程内 Rust 客户端库；服务端使用 Go 转发语音。唯一产品网络协议为 Noise UDP，控制消息使用 Protobuf，音频使用 Opus。

支持服务器书签、系统密码存储、频道聊天、多人语音、单人收听音量、麦克风增益、按键/持续/阈值发言、回声消除、降噪和在线本地试听。所有者可认领服务器、管理频道及设置频道音质。当前仍为实验版本，客户端与服务端需配套升级。

## 构建

需要 Go 1.26（服务端与互通测试）、Rust 和原生 C/C++ 编译工具，以及 CMake、pkg-config、Meson、Ninja。macOS 需要 Xcode Command Line Tools；Windows 运行根目录 `build-windows.cmd`，详见 [Windows 构建](docs/windows-build.md)。

```sh
make                 # 默认构建全部
make build           # 桌面 + 服务端
make build-core      # Rust 客户端库（不是独立进程）
make build-desktop   # 进程内核心 + 桌面；macOS 生成 Resona.app
make build-server    # 纯 Go 服务端（不需要 C 编译器）
make deploy          # 生成 Linux 部署包，不上传、不重启服务器
make dev
make clean           # 删除 build/bin/、build/deploy/、desktop/dist/
make clean-all       # 额外删除 desktop/target/，下次 Rust 构建会更慢
make help
```

Makefile 面向 macOS/Linux。macOS 桌面产物为 `desktop/dist/Resona.app`；Linux 桌面和原生处理库放在 `build/bin/`，服务端为 `build/bin/resona-server`。Windows 继续使用 `build-windows.cmd`，启动 `resona-desktop.exe` 并保留同目录的 `resona_webrtc_apm.dll`。没有 `resona-core` 子进程或 NDJSON IPC，不需要 Node.js 或 WebView。

`make core` 保留为 `make build-core` 的别名，库输出在 `client/target/release/`。根目录 `VERSION` 是基础版本来源，Go/Rust 构建元数据共同使用它；分支包可使用 `-dev` 后缀。桌面与客户端 Cargo 版本须与文件一致；服务端支持如 `GOOS=linux GOARCH=amd64 make build-server` 的交叉编译，桌面按本机平台构建。

直接执行 `./desktop/scripts/package-macos.sh` 会构建固定提交的官方 Opus 静态库、原生 3A 动态库与桌面。第一次构建需要下载依赖；以后复用 `build/native/` 与 Cargo 缓存。先退出旧应用，再清理/打包并打开新应用。

macOS 本机构建自动使用唯一有效的代码签名证书；存在多个证书时须通过
`RESONA_CODESIGN_IDENTITY` 指定证书 SHA-1。没有证书时仍生成 ad-hoc 开发包，
重建后可能重复触发钥匙串授权；CI 只使用显式配置的签名身份。签名不等于 Apple
公证。更换签名身份或证书续期后仍需重新验证授权。旧 ad-hoc 钥匙串条目可能需要
在稳定签名包中重新保存，不自动清除或放宽权限。

`make deploy` 从 `VERSION` 读取版本，在 `build/deploy/` 生成静态 Linux 服务端、Dockerfile、升级脚本及压缩包。通用源码在 [deploy/](deploy/README.md)，支持 amd64/arm64，默认 amd64；不依赖本机 Docker，不执行远端操作。

`clean` 不删除 `desktop/assets/` 中的图标、旧的 `build/deploy-*`、`build/` 下其他目录中的性能报告、用户配置或全局 Go/Cargo 下载缓存。不要把需要保留的数据放在 `build/bin/`、新的 `build/deploy/` 或 `desktop/dist/`。不要在同一次调用中混用 `clean` 和构建目标；按顺序分别执行。

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
- 音频采集、处理、编解码与混音由 Rust 客户端专用 worker 拥有，复用原生 Opus/SpeexDSP/WebRTC/RNNoise；服务端不做编解码。
- 调试本地预览不联网；麦克风试听不向频道发送。服务器 JetStream 保存有限近期历史，客户端显示缓存最多 500 条 / 512 KiB。
- `client/` 是唯一客户端核心；试听机器人复用 Rust 会话与 DSP。Go 只保留服务端、共享协议/传输、发布工具及独立编解码基准参考。
- 暂未选择项目开源许可证，不将本仓库描述为已完成开源授权的 SDK。

[服务端运行](docs/server.md) · [文档索引](docs/README.md) · [桌面说明](desktop/README.md)
