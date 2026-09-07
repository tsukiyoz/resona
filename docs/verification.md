# 验证记录

日期：2026-09-07。

## M0 验证范围

本轮验证对象为新建 GUI 骨架。平台：macOS 26.5.1 / Apple Silicon，Go 1.26.4、Node.js 22、Wails v2.15.0。

| 检查 | 结果 |
| --- | --- |
| Go 核心测试 | 通过：书签持久化、损坏配置保护、输入与会话边界、失败不提交状态 |
| Go race 检查 | `go test -race ./...` 通过，包含 App 初始化失败后恢复与并发书签保存 |
| 前端 TypeScript / Vite 构建 | `npm run build` 通过 |
| 浏览器主要工作流 | 4 条 Playwright 测试通过：书签 CRUD/重载、预览、外观与窄屏、损坏数据与存储写入失败恢复 |
| 桌面 / 窄屏截图检查 | 1440x900 与 390x844，浅/深色、侧栏和表单检查通过；无横向溢出或页面脚本错误 |
| macOS 原生应用构建 | Wails 编译、打包、自签名成功，产物 `build/bin/resona.app` |
| macOS 原生桥接检查 | 实际启动原生窗口，保存书签并检查 Go 配置文件落盘；进入预览、发送中文本地消息成功；无浏览器 fallback 标识 |
| npm 依赖审计 | 本轮修正 Vite / Playwright 的已报告问题后，`npm audit` 返回 0 漏洞 |

## 独立 TS3 连接探针

用户授权使用其测试服务器，凭据仅经运行时环境变量传入临时探针，不进入仓库、日志或文档。使用候选库固定提交 `a334def898f4d9c518a1434a9b9e9f889dcae954`，临时测试身份与主 GUI 配置分离。

操作顺序：以 `Resona-Verify` 普通身份连接 -> 等待握手 -> 请求频道列表 -> 遇到服务端错误后断开。未发送文字、音频或 poke，未修改服务器配置、权限或其他用户；连接过程包含库自身的握手、心跳及本人状态更新。

结果：

```text
connected=true
read channel count: server error id=2568
```

2568 对应 `0x0a08` / `ERROR_permissions_client_insufficient`，见 [官方错误枚举](https://github.com/teamspeak/ts3client-pluginsdk/blob/master/include/teamspeak/public_errors.h)。本次只证明普通客户端握手成功和该查询在当前权限下被拒绝；由于探针遇错即退出，用户列表与自身 UID 查询未执行，也没有验证服务端版本、重启后 UID 或自动重连。

源码复查发现候选库 `ListChannels()` 发送 `channellist`，当前高层接口还没有维护完整的初始频道快照和频道变更状态。下一步应研究普通客户端初始化通知与本地状态同步，不以要求用户授予管理员权限来替代协议实现。

探针是一次研究验证，不是 GUI 里的连接功能，也不构成 M1 验收通过。

## 未验证

- GUI 内真实 TS3 连接、文字通信、持久化身份认证与语音收发。独立探针的有限握手结果如上。
- Windows / Linux 原生构建与运行。
- 第三方插件加载、无界面机器人和自有服务端。

上述能力不属于本轮已完成内容。协议研究的发现见 [候选评估](protocol-evaluation.md)。
