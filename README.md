# Resona / 共鸣

以 Go 为核心的桌面语音客户端，先兼容 TeamSpeak 3，逐步发展自己的扩展、机器人和服务端生态。

当前进入 **M1 只读 TS3 接入实验**。桌面版可从书签连接服务器，显示服务器推送的频道和可见成员，并主动断开；保留独立的本地预览。真实文字发送、频道切换、语音和第三方插件尚未开放，不能替代日用 TS3 客户端。

## 开发

环境：Go 1.26、Node.js 22、npm、Wails v2.15.0，以及操作系统的原生编译依赖。macOS 需要 Xcode Command Line Tools；Linux 和 Windows 的要求见 [Wails 安装文档](https://wails.io/docs/gettingstarted/installation/)。

```sh
go install github.com/wailsapp/wails/v2/cmd/wails@v2.15.0
cd frontend
npm ci
cd ..
wails dev
```

桌面正式构建：

```sh
wails build
```

产物位于 `build/bin`。桌面开发和构建均使用本机 WebView，不在正式应用中启动公共 HTTP API。

浏览器预览只用于前端开发：

```sh
cd frontend
npm run dev
```

访问终端打印的本地 URL。浏览器预览使用浏览器本地存储，和桌面 Go 配置互相独立；界面会持续标记预览状态。

## 验证

```sh
go test ./internal/...
cd frontend
npm run build
```

完整 `go test ./...` 前先构建前端，以满足桌面入口的静态资源嵌入。原生构建和测试需要目标平台的编译环境。

固定第三方协议快照作为嵌套 module，需要另执行 `make test-protocol`。真实服务器的只读集成测试默认关闭，运行方式和凭据环境变量见验证记录。

浏览器回归测试（在 `frontend` 目录）：

```sh
npx playwright install chromium
npm run test:e2e
```

本轮已完成的检查与真实连接探针结果见 [验证记录](docs/verification.md)。

## 数据与边界

- 桌面书签保存至 `os.UserConfigDir()/resona/servers.json`，不包含密码或身份私钥。
- 首次连接创建 `os.UserConfigDir()/resona/identity.key`，权限 0600，后续连接复用同一身份。请自行备份该私钥；损坏文件会报错，不能自动覆盖。服务器密码只用于本次连接。
- 真实连接目前支持域名/IP、显式端口与 TS3 SRV；不支持 MyTeamSpeak 昵称解析、TSDNS、自动重连或身份选择。初始身份安全等级为 8。
- 浏览器预览不能真实连接 TS3；请运行桌面应用。成员数量只代表服务器向本连接提供的可见成员。
- 本地预览不打开 TS3 网络连接，不代表协议兼容性验证。
- 主题等界面偏好保存在 WebView / 浏览器的本地存储。
- 暂未选择开源许可证；在许可证确定前，不把本仓库描述为已完成开源授权的 SDK。

## 文档

从 [文档索引](docs/README.md) 开始，包含产品范围、推进顺序、架构、功能设计、协议评估和 ADR。
