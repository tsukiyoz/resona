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

## M1 只读连接切片

在 `codex/ts3-session` 分支接入固定协议快照，保留许可证，并增加同步命令观察接口和已知 client ID 保护。GUI 通过 Wails 绑定使用真实 Go 会话服务，浏览器环境仍明确禁用真实连接。

| 检查 | 结果 |
| --- | --- |
| 根 Go race | `go test -race ./...` 通过，覆盖异步连接、取消、旧回调隔离、主动关闭、服务端 Closed 清理、只读边界 |
| 适配器回归 | 身份并发创建/重载、损坏文件保留、初始化乱序、自身信息延迟、频道移出不误断线、登录后非致命错误通过 |
| 固定上游快照 | 在 `third_party/teamspeak-go` 执行 `go test -race ./...` 通过；包含 observer 保序/参数隔离、相似昵称不能覆盖已认证 client ID 的回归 |
| 前端 | TypeScript/Vite 构建通过，Playwright 6/6 通过；包含注入 desktop bridge 的连接、断开、失败和密码不落盘 |
| 连接界面视觉检查 | 注入测试 bridge，1440x900 与 390x844 的密码弹窗/连接态均无横向溢出，频道树、标题、详情与输入区无重叠；这是界面验证，不是真实网络截图 |
| macOS 构建 | 最终 Wails 编译、绑定生成、打包与自签名通过 |
| 新版原生窗口交互 | 尚未完成本轮自动操作验收：应用已启动，但窗口工具仍缓存旧 `com.wails.resona`，无法选择新的 `io.github.tsukiyoz.resona`；不把浏览器 mock 当作原生真连接证据 |

真实测试使用独立、持久的 `Resona-Verify` 身份，两次只读连接均通过。第二次在收紧 ready 条件后不再通过 sleep 等待本人信息：Connect 返回时已有完整初始频道列表与本人频道信息。

```text
connected; channels=28 visible_users=1
own channel and persistent identity verified
disconnected
```

两次运行复用同一私钥，并比对从落盘私钥重新计算的 UID。这里验证的是本地身份复用与同一身份的重复握手，没有主动查询服务端 UID/权限，也没有测量长连接资源曲线。成员数量是当时本连接可见范围，不是全服在线人数。

网络操作仅含协议连接、心跳、本人输入/输出静音状态和主动断开。没有主动频道/用户查询、全频道订阅、文字、音频、poke、频道移动或管理操作。服务器原配置与其他用户状态未修改。

复现入口为 `internal/protocol/ts3/integration_test.go`：在运行时设置 `RESONA_TS_ADDRESS`、`RESONA_TS_PASSWORD` 和可选的 `RESONA_TS_IDENTITY` 后执行下述命令。仅使用获准测试的服务器；未设置地址时用例会 skip，skip 不代表互通成功。

```sh
go test -race -tags=integration ./internal/protocol/ts3 -run '^TestLiveReadOnlySession$' -v -count=1 -timeout=45s
```

## 正常用户授权后的验证

用户为驻留的 `Resona-Verify` 身份手动赋予正常用户权限后，临时探针复用同一身份重新登录。初始 `notifycliententerview` 显示自身 `client_servergroups=7`；推送的自身 UID 与本地私钥计算出的 UID 一致。用户组名称来自用户确认，不是仅由数字组 ID 推断。

随后以协议库 `ClientMove` 仅移动验证客户端自身，两次验证得到一致结果：

- 进入一个无密码的普通聊天频道成功，同时收到命令成功响应和自身 `notifyclientmoved` 事件。
- 尝试返回登录时的默认频道被服务端拒绝，错误码为 2568（权限不足）；不能据此把之前成功的频道进入判为失败，也不应将普通用户权限理解为所有频道均可进入。
- 每次测试结束主动断开；未发送文字、音频或 poke，未移动其他用户，未修改频道、权限或服务器配置。临时探针未继续保持在线。

这证明授权在同身份重登后保留，且该身份具备已测试普通频道的加入能力。之前主动 `channellist` 查询失败与频道加入权限需要分别判断；本轮未重新执行该查询，不宣称查询权限已修复。

已将获授权的身份保存为本机桌面默认 `os.UserConfigDir()/resona/identity.key`，使用拒绝覆盖的复制并校验内容相同，文件权限 0600。私钥不进入仓库。GUI 后续连接会复用此身份；当前 GUI 频道切换仍未开放，协议探针成功不代表界面功能已经实现。

## 未验证

后续真实测试约束：用户要求仅在 Resona 自建的专用测试频道进行测试，不再进入既有业务频道。上文普通聊天频道的探针发生于该约束提出之前。需要额外权限时必须说明具体拒绝结果并等待授权，不调整服务器权限或其他用户。凭据与专用测试频道的运行时状态不进入仓库。

- 新版 GUI 内完整真实连接点击流程；持久身份与真实 Go 适配器结果如上。
- 真实文字通信、GUI 频道切换、语音收发、长连接、真实被踢/断网场景及自动重连。单个普通频道加入的协议验证如上，不代表完整频道权限矩阵；相关状态机单元测试不等同于全部真实故障验收。
- 服务端版本兼容矩阵、高于 8 的身份安全等级要求与官方身份导入。
- Windows / Linux 原生构建与运行。
- 第三方插件加载、无界面机器人和自有服务端。

上述能力不属于本轮已完成内容。协议研究的发现见 [候选评估](protocol-evaluation.md)。
