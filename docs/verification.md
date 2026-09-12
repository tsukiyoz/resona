# 验证记录

## 2026-09-12 Noise UDP 实验

- Go 全核心回归通过：`go test ./internal/... ./cmd/resona-server`。Noise、原生适配器、凭据边界的竞态测试通过；原生多客户端场景同时覆盖 QUIC 与 Noise。
- 固定依赖的 `go test github.com/flynn/noise` 通过，包括库自带握手与协议向量；这不是独立安全审计。
- Noise 故障测试验证：控制数据和 ACK 各丢失一次仍完整且不重复交付；认证前篡改计数器不推进重放窗口；乱序可接收、重复和旧会话包被丢弃；篡改关闭类型不能关闭会话；nonce 预算耗尽不回绕；读 deadline 可唤醒；取消握手及错误公钥拒绝；应用密码错误拒绝。
- 修复回环测试发现的关闭顺序问题：先尝试认证 Close，再关闭 UDP；关闭设 1 秒 socket 看门狗并回收维护任务。丢失 Close 仍需对端空闲超时，不声称可靠通知。
- Rust 19 项测试通过；macOS core / debug app 打包通过；无 CGO 的 server 在 macOS 本机构建及 Windows/Linux amd64 交叉构建通过。Windows 实机未验收。
- CLI 实测 `--init-key` 输出公钥、重复初始化报 file exists、再次启动公钥一致、仅监听回环临时端口、Ctrl+C 退出。私钥放在被忽略的 build/bin 测试目录，没有进入 Git。
- macOS 实际打开 Noise 表单，确认三种协议、缺公钥禁止保存、公钥粘贴显示、切换 QUIC 时不复制到证书指纹、取消不保存书签。GUI 未连接用户 TS3 服务器。Windows IME、最小窗口全流程、真实双机设备音频仍待验收。
- 尚无性能结论、独立安全审计、自动换钥或语音拥塞适应。控制 stop-and-wait 的 WAN 吞吐、长连接、丢包突发和容量需继续测量，见 ADR-0019。

## 2026-09-11 原生 QUIC 服务端实验

- `go test ./internal/... ./cmd/resona-server` 通过；监听回环端口的测试需在允许本机网络监听的环境执行。
- `go test -race ./internal/client ./internal/protocol/... ./internal/nativewire ./internal/server ./cmd/resona-server` 通过；真实 QUIC 多客户端覆盖 TLS/指纹/错误密码、频道文字、频道隔离、gopus 合成帧收发与解码、旧频道 epoch 丢弃、成员退出、ID 不复用及服务端关闭。另覆盖凭据目标变更与证书不覆盖。
- Rust 19 项测试通过；已有 `block 0.1.6` future-incompatibility 警告仍在。
- `CGO_ENABLED=0` 服务端 macOS 本机构建、Windows amd64 / Linux amd64 交叉构建通过；交叉构建不等同于对应平台运行验收。
- macOS core 构建及独立 debug 测试包 `desktop/dist/Resona-Native-Test.app` 打包通过。实际打开新增服务器表单，确认旧默认 TS3、可选 Resona QUIC、原生专属指纹字段以及缩小窗口后的完整显示；Escape 可取消表单，未保存测试书签、未连接用户 TS3 服务器。
- 待验收：完整 GUI 原生登录/断开与设备音频、Windows 输入法及最小窗口、双机 WAN 丢包/延迟、长连接、CPU/内存/带宽容量压测。未将实验实现标记为公网生产就绪。

## 2026-09-10 发布入口、服务器菜单与钥匙串授权

- release通过编译条件隐藏本地预览入口，离线引导不再提预览；debug保留。书签右键菜单绑定其自身ID，编辑/删除替代固定按钮，F2提供键盘编辑入口。沿用原表单验证和删除确认。
- 共享图标改为近黑底灰白四条，重新生成9尺寸Windows ICO，macOS打包生成ICNS，原生品牌图同源更新。
- macOS Has加上SDK的非交互认证策略，不返回密码正文；受保护条目交给显式Get授权，不自动重复读取。定向测试检查两次查询中只有Get允许认证。`go test ./internal/...`通过；随机临时条目的原生Keychain创建/静默查询/读取/更新空密码/删除测试通过并清理。未读取用户密码，也未代替用户操作授权弹窗；原有条目的实际弹窗次数仍需用户连接复验。
- Rust 15项测试、release macOS打包及Info.plist校验通过；原生实测正式版无预览，展开/收起栏右键菜单、编辑、Escape取消和F2均正常，未保存修改或发起网络连接。Windows图标资源已生成，Windows实机显示仍待验证。现有上游block 0.1.6 future-incompatibility提示不属于本次变更。

## 2026-09-10 两级服务器导航与退出清理

- 移除顶部书签条，改为172/52px服务器栏和220/44px频道栏；两栏独立收起并保存偏好，添加/预览入口固定，书签列表独立滚动。产品流程只读复核完成，验收规则见 `docs/product.md`。
- macOS 原生窗口实测：三个已有书签同时可见，第三个可以选择且自动展开频道栏；未连接书签不借用原会话频道。两栏四种展开组合可操作，900×600内容区下未发现导航、聊天输入与语音工具栏重叠。没有修改已有书签或发起真实服务器连接。
- 原生本地预览进入、退出通过，退出后频道与成员列表清空；离线书签恢复连接入口。真实断开保留频道快照的UI条件由新增回归覆盖，未在本轮重新进行网络连接测试。
- `cargo test --offline --manifest-path desktop/Cargo.toml` 15项通过，包含跨服务器频道隔离、预览隔离和离线旧快照不隐藏重连入口。release macOS打包和Info.plist检查通过；现有上游block 0.1.6 future-incompatibility提示仍在。
- Windows实机、几十个长名称书签滚动到底，以及最小窗口下资料栏同时展开，仍待验收；当前截图验证不替代这些场景。

## 2026-09-10 macOS 应用图标

修复打包遗漏：从共享 `build/appicon.png` 生成16至1024像素的标准iconset，再由macOS iconutil生成 `Contents/Resources/Resona.icns`，Info.plist增加CFBundleIconFile。release打包与plutil校验通过。iconutil在沙箱内报Invalid Iconset，获准在正常系统环境重跑成功。用户随后明确授权退出测试连接，已正常退出、重新打包并启动正式Resona.app；Dock观察工具超时，因此图标资源与引用已验证，但未宣称完成Dock截图验收。

## 2026-09-10 GPUI 黑色界面与 Wails 退役

在 `feature/tsukiyo/perf-20260909_windows-comparison` 实施用户确认的黑色原型：顶栏书签、频道树、聊天、按需资料、固定72px语音工具栏及分栏设置。Go IPC、音频配置队列与关闭清理保持原路径。删除 Wails 产品源码与依赖，HTML 设计工具移至独立 npm 包，见 ADR-0016。

- `go test ./...` 通过，含 audio/client/desktopipc/protocol 及性能工具；首次沙箱内图标 TCP 测试无法监听，在获准沙箱外重跑通过。
- Rust 14 项测试通过，release macOS 打包通过。现有上游 `block 0.1.6` 仍有 future-incompatibility 提示，本轮未升级上游依赖。
- 实际 macOS 应用检查默认窗口与900×600内容区：顶栏、频道、消息输入、固定底栏、语音设置均在窗口内；本地预览消息按Enter发送成功。关闭设置与Cmd-Q正常退出，随后进程检查未发现测试bundle的GUI/Go核心残留。
- 产品代码复核修正了长主题挤压聊天、重复消息确认框样式误改，补充查看对象独立标记；原生品牌位图复用缓存，避免每帧创建图像。
- 独立 `docs/ui-prototype` npm安装、构建通过，不再依赖已删除的frontend。完整Go检查无需生成HTML资源。

本轮没有进入真实TS3频道，未重新验证真实设备处理、在线网络失败、Windows UI或游戏负载。半透明表面采用原生静态颜色、边框与阴影，不声称等同网页实时背景模糊。版本及tag未变，没有创建commit或push。

## 0.0.2 功能筹备（2026-09-10，未发版）

本轮与 `tools/desktop-perf` 的 Windows 采集/CSV 分析工作处于同一功能分支。UI 整体优化随后单独推进；当前版本号仍为 0.0.1，未提交、推送或打 tag。

- 用户报告四人频道中一个成员长期无声。合成回归确认：三个合法 10ms Opus 帧会留下不足 20ms 的 PCM；旧清理条件要求 PCM 为空，导致过期说话者及序号状态不回收，较小的新序号被一直拒绝。有效回归在修复前失败，移除残留 PCM 对超时回收的阻止后通过；追加三名远端发言者同时恢复的有声输出检查通过。没有现场接收记录，不能认定该缺陷就是用户昨天事故的根因。
- 最终 `go test -race ./internal/...` 通过；随后加强的音频定向回归通过。覆盖在线测试本机回放、远端混音、零网络发送、原音量保留、静音/耳聋/停用意图恢复、启动取消、失败只恢复静音输出、停止与切频道竞争、启动中退出及单个说话者恢复。使用内存音频设备和传输，没有连接用户服务器，没有录音文件。
- `cargo test --locked --offline --manifest-path desktop/Cargo.toml` 14 项通过。仍有原有依赖 `block v0.1.6` 的 Rust future-incompatibility 告警。
- Windows 凭据测试成功交叉编译为 amd64 EXE；原生 Credential Manager 测试已加入 Windows CI，仅创建随机独立测试条目。尚未在 Windows 运行本次代码，不将交叉编译当作原生测试通过。
- Windows 图标从已有应用 PNG 生成 9 个尺寸的 ICO，资源 ID 1 与 GPUI 0.2.2 窗口加载实现一致。打包增加 GUI 子系统与原生图标加载检查；EXE/任务栏/Alt+Tab 的实机视觉检查待 Windows 完成。
- CGO Go 核心构建及 macOS release 打包通过，验收包为 `desktop/dist/Resona-review.app`。实际进程路径确认运行的是该新包；原生窗口打开书签表单，空名称按 Enter 出现核心校验错误，未写入书签；取消表单、音频设置布局、Cmd-Q 退出通过。未测试本轮真实设备在线回放、Windows IME 候选确认或真实密码登录。
- 产品流程复查已纳入 `docs/product.md`，包含等待、失败、取消、退出和待验收边界。Windows 多人频道听测、系统凭据重启读取、按键释放与 UI 整体优化仍未验收。

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

在客户端开发分支接入固定协议快照，保留许可证，并增加同步命令观察接口和已知 client ID 保护。GUI 通过 Wails 绑定使用真实 Go 会话服务，浏览器环境仍明确禁用真实连接。该版本已提交为 `0031455`；分支后续按用户约定改名为 `feature/tsukiyo/client-20260907_ts3-session`。

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

## GUI 频道切换切片

当前源码已开放无密码频道切换，真实所在频道仍完全由服务端事件决定。验证结果：

- 根 `go test -race ./...` 通过；命令响应与自身通知两种顺序、权限错误映射、密码标记、取消、状态隔离通过。
- 固定上游模块 `go test -race ./...` 通过，新增 context 命令等待、取消清理、重复/迟到响应与结构化错误测试。
- 前端构建与 10 条 Playwright 测试通过，包括原有 6 条和新的成功/拒绝/迟到响应/断开操作。
- 1440x900 和 390x844 的切换中、手机侧栏与权限错误截图检查通过，未发现溢出或遮挡；截图使用测试 bridge，不是真实服务端界面。
- macOS Wails 绑定、编译、打包、自签名通过。窗口工具仍不能选择新的应用标识，原生完整点击流程未宣称通过。

真实验证创建了一个 Resona 专用临时频道，创建请求成功并收到创建和本人进入通知。驻留夹具只保持该频道可用，随后 `TestLiveDedicatedChannelMove` 通过真实 `client.Service` 发起频道切换，验证命令成功响应、本人所在频道和 `switchingChannelID` 清空，最后主动断开。没有进入既有业务频道或测试默认频道权限；默认频道仅可能出现在普通登录引导阶段。

```text
dedicated channel move acknowledged and observed through client service; disconnected
testChannelRemoved=true
```

夹具已优雅退出，之后仅通过登录推送核对专用频道 ID 和名称均已消失。没有留下测试频道或驻留连接，没有发送消息或音频、操作他人或修改服务器权限。

复现时先自行创建并保持专用临时频道，显式设置 `RESONA_TS_TEST_CHANNEL_ID`、`RESONA_TS_TEST_CHANNEL_NAME`（必须以 `Resona Test ` 开头）、`RESONA_TS_IDENTITY`，以及原有地址/密码环境变量。集成测试要求初始频道列表中的 ID、名称完全匹配，才会尝试移动；它不创建或删除频道资源。

```sh
go test -race -tags=integration ./internal/protocol/ts3 -run '^TestLiveDedicatedChannelMove$' -v -count=1 -timeout=50s
```

## 本地提示音切片

用户手动体验上一版桌面应用后反馈可接受，并提出当前频道成员进出和本人退出的提示音需求。本轮增加三种原创短音调、提示音开关、音量和试听。此处本人退出指断开服务器；关闭整个应用不等待音效播放。

| 检查 | 结果 |
| --- | --- |
| 根 Go race | `go test -race ./...` 通过；包括客户端通知的初始静默、快速进出、当前频道过滤、64 条容量、快照隔离和断开去重 |
| 协议事件 | 当前频道加入/离开、重复事件、订阅快照、本人移动、其他频道、未知原因和自身退出原因通过；review 后补充传输断线清空旧瞬时事件，协议包 race 再次通过 |
| 上游批量通知 | 嵌套模块 `go test -race ./...` 通过；初始批量成员共享字段、移动/离开、显式覆盖、字段隔离和转义回归通过 |
| 前端 | TypeScript/Vite 构建通过；原有 10 条和新增首批 4 条 Playwright 用例一同通过，追加生命周期用例后 5 条音频用例再次全部通过 |
| 真实媒体资源 | 三份 WAV 经真实 Web Audio 解码并启动播放节点；PCM 检查均非静音、无削波、起止为零，时长约 0.39 / 0.39 / 0.55 秒 |
| 视觉 | 设置页 1440x1000 与 390x844 截图目视检查通过，控制项无横向溢出或遮挡 |
| 原生包 | Wails 绑定生成、编译、打包、自签名通过，更新 `build/bin/resona.app` |

前端覆盖初始队列静默、重复轮询只播放一次、快速加入再离开、禁用期间不重播、过期与其他频道事件丢弃、断开提示、音量持久化、异步加载时关闭提示及页面退出资源释放。媒体验证证明资源可解码及播放调用成功，不代表已在原生 WebView 中听取实际扬声器输出。

这轮没有连接真实 TS3 服务器或创建频道。真实成员通知到桌面扬声器的完整链路尚待在 Resona 自建专用频道验收；不把模拟 bridge 或浏览器试听等同于实际互通。设计见 ADR-0007，音效可用 `node frontend/scripts/generate-notification-sounds.mjs` 重新生成。

## 连接成功提示与产品审查补全

用户指出本人进入服务器也需要提示音后，加入产品体验子 agent 审查，按操作、等待、成功、失败、取消与退出补齐验收标准，记录在 `docs/product.md`。当前设置为四项：连接服务器、断开连接、成员进入当前频道、成员离开当前频道。

连接成功通知仅在有效会话完成连接、正式转为 `connected` 时产生一次；同一会话的同步与切频道不重复通知。重新连接使用新事件 ID，异步加载的旧会话声音被丢弃。产品复查另发现试听可能占用同类等待或 400ms 限频，现将真实连接、断开反馈独立处理，保证试听不吞掉真实事件。

- 根 `go test -race ./...` 通过，覆盖成功时机、失败/取消静默、迟到连接清理、重连新通知以及原有会话行为。
- 前端构建和 12 条音频 Playwright 测试全部通过，包含新增连接、重连、掉线及 4 条试听加载/限频冲突回归。
- 四份 WAV 均通过真实 Web Audio 解码和播放调用，新增成功音约 0.49 秒，PCM 非静音、无削波、首尾为零。
- 设置页 1440x1000 和 390x844 截图检查通过；Wails 绑定生成、编译、打包与自签名通过，桌面产物已更新。

本轮没有连接真实服务器；新增成功提示在原生连接后的实际听感仍待验收。产品审查流程已加入 `AGENTS.md`，本人切频道成功反馈、逐类开关等建议记录为后续范围。

## 成员列表与服务器进入修复

用户反馈成员不可见、频道看不全、无法滚动及连接重复输密码。本轮定位到侧栏未渲染成员、窄窗口隐藏右侧名单、固定高度 grid/flex 缺少最小高度约束导致长列表截断，以及协议从未订阅其他频道成员。修复频道下成员展示和侧栏滚动，连接后异步订阅 `channelsubscribeall`，超时或拒绝单独报告成员同步受限。

服务器书签支持双击/Enter；有已保存凭据直接连接，没有则打开密码框。切服先准备目标密码再断开旧会话，准备失败保留原会话及本次输入。密码只在认证成功后存入原生 macOS 钥匙串，前端只接收保存状态；地址变化与书签删除清除关联凭据。

验证证据：

- 根 Go race 通过，覆盖凭据不进入书签或 Workspace、错误密码不覆盖、空密码重连、保存失败保持在线、取消记住和忘记密码、重命名保留、地址变化及删除清理。额外使用阻塞旧连接关闭的测试确认切服期间修改目标地址不会把旧凭据发往新地址。
- 前端 32 条用例分组通过：音频 12 条、凭据及既有工作区 15 条、长列表及成员 5 条；包含受限成员状态、准备失败保留输入、切频道定位与轮询不抢滚动。1280x720 与 390x844 的频道成员及滚动到底截图检查通过。
- 原生钥匙串随机测试项完成写入、查询、更新、删除。首次真实测试发现非空值更新为空时系统忽略零字节更新，修复为钥匙串内部版本化 JSON 后，空密码及中文、换行、引号、反斜线、NUL 的往返测试通过；临时条目已删除，没有读取既有凭据。
- 真实 TS3 测试创建并仅使用 Resona 专用临时频道，实际 `client.Service` 进入后确认成员订阅成功、本人及测试夹具成员都在该频道可见，随后断开。夹具退出后另一次登录推送检查确认专用频道 ID 和名称均已消失，无遗留测试连接或频道。
- macOS Wails 绑定生成、编译、打包与自签名通过。真实服务端结果验证 Go 连接和成员模型；不声称已完成新版原生 GUI 内的完整鼠标操作及跨进程钥匙串授权体验验收。

只验证用户授权身份可见的成员范围，不宣称全服用户不受权限限制。没有发送文字或音频、进入既有业务频道、移动他人或调整服务器权限。原生钥匙串集成测试入口为 `RESONA_KEYCHAIN_INTEGRATION=1 go test -tags=integration ./internal/credentials -run '^TestNativeKeychainRoundTrip$' -v`，只操作随机临时条目。测试频道验收增加可选 `RESONA_TS_TEST_PEER_NICKNAME`；清理核对入口为 `TestLiveDedicatedChannelRemoved`。

## 用户验收

2026-09-07，用户体验当前桌面版后确认“验收，暂无问题”，授权提交本轮频道导航、成员显示、提示音、记住密码与服务器进入流程，对应提交 `268b528`。以上自动化及真实集成结果作为本次提交的验证依据；用户反馈不代表下列尚未实现功能或跨平台兼容矩阵已通过。

## 频道文字与图标切片

本轮在上一版已验收基础上加入频道纯文本收发、发送结果与手动重试、断线只读历史、TS spacer 分隔呈现和真实自定义图标资源。自动化检查与用户手动桌面验收分别记录。

| 验证范围 | 本轮需确认的行为 | 状态 |
| --- | --- | --- |
| Go 应用服务 | 绑定会话/频道、发送与移动互斥、状态归并、500 条历史上限、断线保留和新连接清空 | 全量 Go race 通过 |
| TS3 文字协议 | 转义、长度与错误映射、发包前频道检查、本人回显不重复、瞬时消息不被状态更新重放 | 适配器及第三方嵌套 module 全量 race 通过 |
| 前端聊天 | 输入法、原样正文、草稿保留、失败重试、未确认重试确认、断线查看与不抢滚动 | 41 条全量回归通过；补充确认入口后 11 条聊天回归通过（含新增 2 条） |
| 频道呈现与资源 | 顶层永久 spacer 归一化、分隔不可点击、真实图标解码、限额、取消与标准图标回退 | 单元及界面测试通过；真实 22 个频道图标全部加载，识别 1 个 spacer |
| 原生与真实互通 | macOS arm64 桌面构建、专用测试频道内发送/接收 | 构建及真实文字双向验证通过；官方客户端交叉验收待用户进行 |

产品复查发现的异常掉线失败页遮挡历史、未确认原稿直接 Enter 绕过重复发送确认均已修复，并有前端回归。已成功的同文消息和编辑后的新内容仍可正常发送，不进行全局正文去重。

协议复查修复了底层使用全文子串判断 `return_code=` 的问题：正文包含该字面内容时也会附带可关联的顶层回执 ID；调用者显式传入的回执参数会被拒绝。单元及真实文字验证包含该用例。

`TestLiveDedicatedChannelText` 通过实际 Go 会话服务进入 Resona 自建频道，与驻留夹具互通。样本文字含随机标记、中文、空格、换行、管道、反斜杠及 `return_code=abc`，长度跨 UDP 命令分片；对端返回全文 SHA-256 与原文一致。验证服务器接受、本地本人仅一行、收到对端回复及断开后保留两条记录。夹具只回复本频道已知测试参与者的一条指定前缀消息，不发私聊或服务器广播。

首次使用三个同身份连接的夹具被服务器以 521（同一身份连接数上限）拒绝。随后复用驻留夹具为收发对端，只保持两个连接完成验证，没有调整权限或限制。真实图标首次检查 22 个频道中 21 个加载成功，剩余一个超过原来的 256 像素输入限制；已改为最多 1024x1024 输入并按比例生成不超过 256x256 的缩略图。`TestLiveDedicatedChannelIcons` 复验通过：22 个频道使用的 21 个图标资源全部加载，识别 1 个 spacer。缩略图修复后全量 Go race 与 macOS arm64 桌面构建再次通过。

测试结束后所有测试客户端和驻留夹具已退出。退出后的两次即时清理检查仍看到了临时频道；2026-09-08 后续只读诊断在完整成员订阅成功后确认本轮创建的频道 ID 已不存在，因此未执行 `channeldelete`，也没有操作任何其他成员。该诊断连接正常退出。清理结果不依赖强制删除。

桌面 1280x1000、窄屏 390x844 的聊天和频道栏截图通过目视检查，PNG 图标真实解码、坏图及外部 URL 回退、空行和对齐分隔均正常。截图使用 mock bridge，不等于新版原生窗口已获用户验收。

协议限制：TS3 频道消息忽略 `target`，本地串行化与 dispatch 前校验不能消除服务器强制移动和处理消息之间的竞态。服务器接受不表示成员已读；发送后超时或取消不证明没有送达，重试可能重复。真实验证仍只在 Resona 自建专用测试频道进行，不能通过移动其他用户或修改权限构造测试。

## 图标缓存与界面外观更新（2026-09-08）

GUI 改为紧凑石墨深色布局，保留浅色、服务器书签管理、连接与消息流程。默认主题使用 `resona.theme.v2`，不清理已有声音、书签或凭据偏好。频道详情可收起，进入窄屏时自动收起，手动展开后支持 Escape 关闭并恢复焦点。

图标缓存按内存、磁盘、网络依次读取。全量 `go test -race ./...` 通过；离线缓存测试确认内存复用、重新创建实例后的磁盘命中、固定有效期、键隔离、损坏重取、数量与字节限额、取消、不相关文件保护、不可写目录降级、符号链接保护和并发原子发布。下载次数由注入函数计数，测试文件只写临时目录，不依赖用户服务器。

最终淘汰计数修正后，缓存与 TS3 包 race 再次通过；图标 worker 链路确认同 Connector 重连、新实例磁盘复用均不会重新下载，更换服务器 UID 会重新获取。产品子任务完成最终布局和流程复查，未发现阻断项。浅色截图改为禁用过渡动画并验证最终颜色，避免将主题切换中间帧误认为终态。macOS arm64 桌面编译与打包通过。

前端构建与 44 条 E2E 回归通过，覆盖原有连接、密码、成员、消息和提示音流程及新增布局场景。1280x900、1440x1000、390x844 的 mock 连接态、设置和弹窗截图完成目视检查；这些结果验证界面，不代表真实服务器或原生窗口的用户验收。本轮未连接用户 TS3 服务器。

低资源常驻目标已加入产品与架构约束。Rust 原生 UI 候选与同负载评测方案见 [桌面 UI 评估](frontend-evaluation.md)；尚未实测框架间 CPU、内存、GPU 或功耗差异，不能声称当前已优于 TS3。

## 未验证

后续真实测试约束：用户要求仅在 Resona 自建的专用测试频道进行测试，不再进入既有业务频道。上文普通聊天频道的探针发生于该约束提出之前。需要额外权限时必须说明具体拒绝结果并等待授权，不调整服务器权限或其他用户。凭据与专用测试频道的运行时状态不进入仓库。

- 新版 GUI 内完整真实连接点击流程；持久身份与真实 Go 适配器结果如上。
- 官方客户端与新版桌面 GUI 的文字交叉验收、密码频道切换、语音收发、长连接、真实被踢/断网场景及自动重连。专用频道内真实 Go 应用服务的文字往返和频道切换已验证，不代表完整频道权限矩阵。
- 服务端版本兼容矩阵、高于 8 的身份安全等级要求与官方身份导入。
- Windows / Linux 原生构建与运行。
- 第三方插件加载、无界面机器人和自有服务端。

上述能力不属于本轮已完成内容。协议研究的发现见 [候选评估](protocol-evaluation.md)。
# GPUI 与语音开发验证（2026-09-08，待用户验收）

- `go test -race ./internal/...` 通过，覆盖音频队列、客户端生命周期、TS3 适配与原生 IPC；设备等待取消后 Busy 清理的追加并发测试通过。
- `go build -o build/bin/resona-core ./cmd/resona-core` 通过。IPC 管道关闭与 Shutdown 测试确认服务退出；无逐帧音频 JSON。
- 本机 macOS arm64 实际设备测试：`RESONA_AUDIO_HARDWARE=1 go test ./internal/audio -run '^TestHardware' -count=1 -v -timeout 45s` 两项通过（设备生命周期 4.50s，Engine 静音/取消静音/耳聋 0.79s）。使用 MacBook Pro 默认麦克风与扬声器；输出短测试音、采集有回调，关闭后回调停止；静音无编码发送、取消静音产生 Opus、耳聋后发送停止。测试传输为内存 fake，不向服务器发送采集音频，不生成录音文件。
- 明确选择枚举所得输入/输出 ID 的设备测试发现并修复 CGO 未固定指针崩溃；通过 `runtime.Pinner` 在原生初始化期间固定 ID。追加真实设备测试通过（2.27s，总计 3.085s），与空 ID 的系统默认路径分别验证。
- gopus v0.1.1 对照本机官方 libopus 1.6.1 的交叉测试通过：codec 4 单声道、codec 5 立体声均覆盖 gopus 编码 -> libopus 解码与反向组合。可复现入口为 `go run -tags opus_oracle ./tools/opus-oracle`，需要本机 pkg-config 与 libopus 开发库；这不是 TS3 官方客户端听测。
- 最终音频并发测试覆盖 64 个发言者上限、非致命解码错误后仍处理致命发送错误、旧实例状态隔离，以及每次缺失序列只补 960 样本 / 20ms 的 PLC。嵌套 TS3 module 的 `go test -race ./...` 六个包通过。
- 真实服务器双向测试 `TestLiveVoiceRoundTrip` 已通过（9.31s，Go 包总计 10.966s）：两个客户端在自建临时频道各接收并解码至少 10 个真实服务器转发的非静音 Opus Voice 帧；关闭两连接后重新登录确认临时频道已删除。仅使用授权身份和运行时凭据，不保存音频。
- 该测试曾发现新建频道推送缺少加密属性；显式设置属性被拒绝（2568），未继续重试该权限操作。最终通过读取当前频道信息补全并缓存，遇到防洪 524 时只退避重试一次，未要求提升权限。测试验证的是两个 Resona 客户端通过真实 TS3 服务端互通，官方客户端交叉听测仍独立待验收。
- macOS 原生 GUI 实测：连接测试书签并进入自建临时频道 78；启用语音时默认静音，夹具向 GUI 转发 25 帧低音量测试音，取消静音后夹具实际收到并解码 1678 帧麦克风音频，耳聋后发送停止。停用语音后文字会话仍连接；本轮未完成停用后的 GUI 实际文字发送。关闭窗口后 GUI 与核心进程退出，夹具结束后通过重新登录确认临时频道已消失。没有保存录音，临时测试书签已删除，原有书签保留。
- 最终调试包原生交互通过：本地预览消息发送后清空输入、中文中间光标位置 Enter 完整发送、Shift+Enter 换行不发送、分频道草稿保留、书签删除，以及空名称错误显示在表单内部。真实连接时检查到频道图标、成员、滚动列表与 spacer 呈现正常。
- Rust 最终测试 3/3 通过，Go/Rust 共用 JSON 契约覆盖 `channelID` 等缩写字段，避免静默反序列化为空。macOS release 打包通过，包含 arm64 GUI 与最终 Go 核心；Info.plist 含麦克风用途说明，核心文件哈希与独立构建一致。实际启动 `desktop/dist/Resona.app` 并进入本地预览通过，启动时未自动连接或启用麦克风。
- Windows 构建脚本与音频后端已实现，但本轮未在 Windows 编译或运行。首次安装的麦克风拒绝/重新授权、官方客户端交叉听测及同负载性能比较仍待完成；当前机器测试没有出现首次授权弹窗，不把已有授权环境当作权限全流程验证。语音首版尚不包含 PTT、VAD、回声消除与降噪。
- 真实加密频道尚未验证：测试身份不能显式设置频道加密属性；加密发送的报文标志、加密/解密与序号绕回已通过单元测试，不据此声称真实加密频道验收通过。

## 麦克风输入增益（2026-09-11）

同日追加成员右键菜单：Rust 18项测试通过，原生release构建通过。无网络模拟窗口验证A由100调到110、120失败时关闭菜单后仍显示A的错误、随后静音成功清除错误；B独立选150再重置100，A仍为110且静音。右键及Escape不打开资料，自己音量操作禁用。菜单外层使用会话/成员/实例组合ID，避免组件库固定context-menu状态在成员间冲突。Windows菜单操作尚待实机验证。性能工具继续采用独立Action，应用包不附带工具；已补明确下载入口，工具测试与Windows交叉构建通过，打包工作流未变更。

- `go test ./internal/...`通过；新增完整编码/VAD用例后，`go test -race ./internal/audio ./internal/client ./internal/desktopipc`通过。验证0/100/200%与限幅、Opus解码后约两倍幅值、增益不改变VAD判定、两种试听热更新不重开设备或发送网络音频、PTT保持、在线测试恢复配置保留最新增益、忙碌与失败不改已确认值。
- `cargo test --offline --manifest-path desktop/Cargo.toml`17项通过，覆盖跨语言字段、旧偏好缺省100、显式0往返和后台保存顺序。Go核心与macOS release构建成功，包内两份二进制哈希与构建产物一致。
- 原生应用使用真实Go核心、保持离线且未开启麦克风：点击100→110%，退出重开仍为110%，恢复100%；最小900×600内容窗口设置控件完整可达。最终保持100%。设置错误就近显示，产品体验复核完成。
- 本轮未做真实麦克风声学听测、真实服务器发送对比或Windows实机验证。合成PCM和实际Opus编解码测试不代表真实设备声音质量。依赖`block 0.1.6`仍有既有未来兼容性警告。

## 单成员本地收听控制（2026-09-11）

- 本人右键菜单修复：本人行完全不挂载收听菜单，保留左键资料。Rust 19项测试及macOS release打包通过，无网络原生夹具确认本人右键无菜单、本人资料无音量控件、远端成员仍显示菜单。原有提交路径及核心拒绝本人调整的校验未改；本轮未重新完成远端菜单音量提交全流程，也未在Windows或debug本地预览实机验收。
- 同日追加右侧音量整数输入：Rust 19项测试及macOS release打包通过。无网络原生夹具验证250回车归一为200、-10归一为0、非法文本就地提示、失败恢复已确认值、失焦撤销草稿、切换成员隔离草稿，以及137%提交保留静音。超长整数、空值和非法格式由单元测试覆盖。输入法组合输入已有防误提交检查，但真实IME候选确认和Windows交互仍待实机验收；本轮未改造应用通知系统。
- `go test ./internal/...` 全部通过；`go test -race ./internal/audio ./internal/client ./internal/protocol/ts3 ./internal/desktopipc` 通过。覆盖单/双声道PCM中A的0/100/200%增益与静音不影响B、参数表并发发布、成员实例隔离、会话和参数验证、设备配置不被重复调用。
- `cargo test --offline --manifest-path desktop/Cargo.toml` 16项通过，包含Go/Rust共享JSON契约及同ID同昵称成员替换时关闭旧资料。Go核心及macOS release应用构建通过。依赖`block 0.1.6`仍有既有Rust未来兼容性警告。
- 独立macOS测试包使用临时管道夹具，窗口明确显示`UI fixture - NO NETWORK`，未连接服务器或打开音频设备。原生点击验证：资料权限拒绝时仍可调节A到110%、静音保留音量、B保持100%未静音、重置100%保持静音、解除静音，以及最小900×600内容窗口控件完整可达。夹具与测试包不纳入Git。
- 产品体验复核确认等待提示和错误绑定具体成员。真实网络多成员听测、Windows实机体验和长期性能测量尚未进行，模拟界面与PCM单测不能替代这些验收。

## GPUI 验收修复：Opus 错误与说话指示（2026-09-08）

用户报告 `Opus 解码失败：gopus：packet too short`，具体触发时机不确定，现场包没有采样。本轮确认两个实现缺陷：带完整头部但无音频的结束包被丢弃，且单个畸形音频包会永久写入可见错误。不能据此断言用户现场包就是结束包。

- 结束包按序进入 jitter，短发言也排完尾部 PCM 后停止补偿；合法单字节 Opus 保留。畸形包按发送者在 2 秒窗口内连续 3 次才显示诊断，恢复只清除对应发送者的解码错误；诊断最多跟踪 64 个发送者，相同错误不重复推送。错误包含编码、发送者 ID 与帧长度，不保存音频内容。
- 回归通过：根 `go test ./...`，音频与 TS3 / client / desktopipc 的 race 测试，以及嵌套 TS3 模块全部 race 测试。覆盖结束包顺序与短发言尾音、20ms PLC、合法单字节帧、按发送者错误恢复、活动超时、快照隔离和故障后的迟到状态。
- Rust `cargo check` 与 6 项测试通过，覆盖共享 JSON 契约、缺省活动字段、本人/远端来源和状态清理。原生窗口实测发现父容器颜色没有传入 SVG，改为直接设置图标颜色并再次构建验证。
- 真实 GUI 最终测试使用自建临时频道 80，夹具转发 500 帧低音量音频：频道树与右侧成员列表对应图标同时亮绿，停止后恢复灰色，自身静音图标保持灰色，无红框。随后明确取消静音，麦克风图标实际亮起，夹具收到并解码 1408 帧；耳聋后熄灭，断开后成员清空。音频仅在内存中处理，没有生成录音文件。300ms 释放时间由测试时钟验证，窗口截图仅验证亮/灭结果。
- 第一轮临时频道 79 与最终频道 80 均在退出后通过只读登录确认消失；临时 GUI 书签删除，原有书签保留。GUI 与核心退出后进程检查无残留。两轮网络测试均限于 Resona 自建频道，默认频道仅用于正常登录引导。
- macOS debug/release 包重新构建并核对内置核心一致。Windows 本轮仍未运行；真实用户触发包及官方客户端交叉听测仍需后续复现，本轮不能宣称已覆盖所有 Opus 互通情形。
## Docker Noise server (2026-09-12)

- Built `resona-server:local` with Compose on macOS/OrbStack (Linux arm64)
  and published only `127.0.0.1:9988/udp`. The build context was approximately
  51 KB and excludes local identities, configuration and build artifacts.
- `TestDockerNoiseSmoke` passed with `-race` against the running container:
  two real Noise clients connected, exchanged a channel message and moved
  channels. After `docker compose up -d --force-recreate`, the same public key
  remained valid and the smoke test passed again.
- Runtime UID/GID is `10001:10001`; the persisted private key has mode `600`.
  Compose configuration validation and entrypoint shell syntax checks passed.
- This verifies local container packaging and connectivity, not Windows Docker,
  WAN behavior, load capacity or microphone audio quality. See [server.md](server.md)
  for startup, persistent identity and optional LAN binding.
## QUIC / Noise local performance baseline (2026-09-12)

An opt-in Darwin benchmark now exercises identical native server/client paths
through counting UDP relays. Three alternating rounds of 4-member/1-sender and
16-member/4-sender workloads completed without a race detector. See
[transport-performance.md](transport-performance.md) for measurements, exact
scope and reproduction. CPU is aggregate harness CPU, not server-only; allocation
rates are not RSS. QUIC missed 4 of 90,000 expected deliveries in the larger
scenario, with no attribution established. Noise delivered all expected frames.
This baseline does not validate WAN congestion behavior, capacity or audio quality.
## Independent server GC investigation (2026-09-12)

- Added opt-in Darwin subprocess diagnostics, a paired-run script and structured
  summarizer. Ordinary core tests pass; short baseline and reconnect harness
  checks pass with race detection. Scoring runs exclude race/profiling overhead.
- Three alternating 10-second default/GC-off pairs at 64 clients completed,
  120,000/120,000 deliveries per run. All final GC-off intervals recorded zero
  collections. Tail latency did not consistently improve when GC was disabled.
- A 30-minute default-GC run with four rooms, sixteen senders and 900 listener
  reconnects completed. GC pause p99 bucket upper bound was 0.328 ms, worst bucket
  upper bound 14.68 ms; forwarding max was 218.96 ms. Stable recipients missed
  8,873/21,240,000 expected deliveries (0.0418%); cause remains unassigned.
- Allocation profiling points primarily to per-packet contexts/timers/callbacks,
  not solely byte buffers. No production pooling, GC tuning or protocol changes
  were made. See [server-gc.md](server-gc.md) for scope, raw artifact locations,
  exclusions and the distinction between test completion and latency acceptance.
## v0.0.2 release checks (2026-09-12)

`go test -race ./internal/... ./cmd/resona-server` passed. Desktop Cargo tests
passed (19 tests), and the native macOS release bundle built successfully with
version 0.0.2, its current Go core and icon/license resources. The existing
`block 0.1.6` future-compatibility warning remains. Windows CI build and subsequent
Windows native-protocol user acceptance are separate from these local checks.
