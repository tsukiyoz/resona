# 当前验证记录

## 2026-10-23 Rust 客户端迁移收尾

删除旧 Go client/audio、客户端协议适配、专用 config/credentials/diagnostics 和
不再使用的 iconcache；SpeexDSP C 源码归 native/speexdsp，四个提示音归
client/assets/sounds。Go server、nativewire/noiseudp/nativeidentity、发布工具与
独立 gopus 互通/基准参考保留。没有改变协议格式、DSP 算法、偏好或版本文件。

- Rust 客户端 51 项单元及 Go 1.26.0 下串行 27 项真实互通通过，包含实际原生
  DSP、编解码双向参考、配置/身份继承、账号替换/撤销、资源观察、分页聊天、
  注册/切频道语音隔离、丢 ACK、取消与重连。新增四个同时发言者互收及跨服务
  重启的账号 UID、owner、频道 CRUD/权限和聊天持久化检查。
- 原 Go 完整客户端测试随实现删除；相应行为由上述正式 Rust 客户端互通以及
  Go server 的账号/权限/KV/聊天留存回归负责，不保留第二套客户端只为跑测试。
- 试听机器人改用正式 Rust Session、Factory 和 Opus。三个单元、真实回环
  音源解码/频道隔离、原生六机器人及 AGC 三档轮换/退出检查通过；明确要求
  地址/公钥并写 PCM 的 capture 测试仍为 ignored，不读取个人麦克风或钥匙串。
- GC 压测改为测试内最小协议端。四人单频道 50 帧收到全部 150 次转发；八人
  双频道并发重连的 race 检查通过，稳定成员收到全部预期 750 次转发。
  修复测量包装器未透传身份 channel binding 的问题，验证换连接仍拒绝旧证明。
  这些短 smoke 数据不是性能对比，也不用于声称语言迁移降低了占用。
- Go 全部 internal 测试、server/accounts/nativewire/noiseudp/nativeidentity 的
  race 与统一 make lint 通过。Go mod tidy 移除 malgo 和 go-keychain。
- 独立 3A 基准三个测试通过；缺失 Meson/Ninja 时使用 build/audio3a 下隔离
  Python 环境补齐。客户端与试听工具严格 Clippy 通过；桌面仅放行既有
  collapsible_if、items_after_test_module、field_reassign_with_default 三类警告。
- 桌面 56 项单元、清理/签名/部署脚本检查与 macOS release 打包、深度签名
  校验通过。Windows/Linux CI 已同步路径和 Rust 试听工具检查；Windows
  原生凭据检查由现有 Rust 测试负责，仍待新分支远端构建和实际听测。
- build 中两份旧临时 Go 源码引用了已删除的客户端包，会被模块扫描误收录，
  已原地保留为 .go.retired；构建产物和本地测试数据不入 Git。
- 实现依次提交 15a44e9（共享源码）、427d1fe（Rust 试听工具）、cad391e
  （测试覆盖迁移）、dd9e821（删除旧客户端）。最终 macOS 应用和配套服务器均
  从 dd9e821、dirty=false 构建；应用版本仍为 v0.2.2，位于
  desktop/dist/Resona.app。release 试听工具使用原有 60.4 秒音源短跑，六个
  机器人正常启动、两秒自动退出，未开启麦克风或连接远端。
- tsukiyo 已部署 resona-server:v0.2.2-migration-review.20261023，运行提交为
  dd9e821、dirty=false。上传 SHA-256 一致；停服备份 data/access 至私有目录
  /opt/resona/backups/resona-server.HF6CnI，并保留旧容器用于回滚。运行中、
  重启次数 0，管理页 HTTP 200、公网 RN06 认证握手成功；保持公网 UDP 9988
  和回环 TCP 9989，不进入活动频道。没有合并、推送或发布新 tag。

## 2026-10-23 按需分页与频道音频解耦完成检查

进入频道只取最新至多 20 条 / 48 KiB；滚轮或滚动条上翻再取旧页，不自动拉空
历史。实时消息独立接收；列表按消息 ID 和行内像素偏移保留阅读位置，缓存达到
500 条 / 512 KiB 后停止旧页加载。覆盖层、退出和频道切换会阻止旧手势发起请求。

- Go 全部 internal 测试、server/nativewire 的 race 检查与 `make lint` 通过。
  分页回归覆盖跨频道序号空洞、字节上限、淘汰后空页、实时边界、过期请求拒绝，
  以及已有 Stream 启用 direct read 时保留自定义留存配置和原消息。
- Rust 客户端 51 项单元、桌面 56 项单元通过；新增旧页与实时并发、回到同频道
  后拒绝旧游标的真实 Go/NATS 互通，完整 25 项互通在 Go 1.26.0 下串行通过。
  同时运行竞态检测及编译时，既有 ACK 丢失语音测试曾低于接收门槛；整组串行复测
  通过。尚不能据此确定负载是否为唯一原因，未放宽测试阈值。
- 客户端严格 Clippy 通过；桌面仅放行已记录的三类既有警告后通过。
  产品流程只读复审通过，含设置覆盖层与延后滚动回调的范围隔离。
- 音频修复分两次提交：`54e7fdb`（恢复与身份/菜单修正）、`9453e0d`
  （设备与路由解耦）。注册沿用本机音频偏好，不上传为账号配置；昵称同步到服务器。
- 分页实现提交 `2694984`。macOS `v0.2.1-dev` release 构建、打包和深度签名
  校验通过；`--version-json` 确认该提交且 dirty=false，应用为
  `desktop/dist/Resona.app`。临时验收后缀被现有打包版本校验拒绝后，使用标准
  `-dev` 构建，未更改版本文件。
- 配套服务器已部署至 tsukiyo，镜像 `resona-server:v0.2.1-voice-review.20261023`，
  同为 `2694984`、dirty=false。上传 SHA-256 一致；停服备份完整 data/access，
  保留旧容器和私有配置。新版运行中、重启次数 0，管理页 HTTP 200；继续使用公网
  UDP 9988 和回环 TCP 9989，未进入活动频道或操作其他用户。
- 本轮 Windows 真实设备反复切频道、在线注册及滚轮/滚动条仍待维护者验收；
  单元、互通与代码复审不能代替实机听测。

## 2026-10-23 音频设备与频道路由解耦

同连接切频道、注册/登录保留设备和发送 AEC/ANS；采集代次拒绝旧输入，播放/
监听由回调按写入边界丢弃旧 PCM。断开、设备变更及退出仍释放设备。

- 客户端 audio 单测 49 项通过，新增设备打开计数、代次切换、PTT 和退出覆盖。
- Go 1.26.0 的 Rust/Go 互通 24 项通过，包含注册、往返频道及双向语音。
- C 回调 ASan/UBSan 检查通过，含 100 次旧/新 PCM 边界、半帧与环回。
- 客户端严格 Clippy 通过；产品流程只读复审通过。
- 尚未进行本轮 Windows/macOS 真实设备听测，不据此声称公网失声已完全排除。

## 2026-10-23 切频道与账号注册后的语音恢复

维护者已用上一轮 Windows 新包与朋友验收，确认双人/多人卡顿改善；随后报告
切走再返回频道、游客注册后偶发收发同时失声。本轮继续使用原修复分支，未提交、
合并、推送或发布版本。

- 用阻塞第二次设备打开的可控回归复现：pipeline 已接收新频道/身份范围，但
  打开期间任意资源 revision 增长都会丢弃新设备；下一轮范围相同不再打开，
  形成 active 但无设备的状态。修复前回归超时，修复后通过。
- 新设备按音频范围判断有效性，普通成员或聊天刷新不额外重开；真正过期的
  设备仍丢弃，并在最新路由更新时补开。所有新设备均初始化当前 capture epoch。
  回归覆盖注册身份代次、频道切换、打开期间快速切回、返回原频道、收听开关、
  配置保持和 PTT 释放；同时断言过期设备零写入、普通刷新不多开设备。
- 真实本机 Go/NATS/SQLite 互通覆盖注册前后双向语音、服务端静音/收听状态保持、
  旧身份帧拒绝和返回原频道。产品 Core 测试比较注册前后全部音频配置字段，
  包含非默认设备、增益和发言设置；没有读取真实钥匙串或打开麦克风。
- 权限与身份展示共用角色映射，游客不再显示未知角色；设置栏目改为“身份”。
  聊天使用整行右键复制，取消悬停按钮与占位。独立产品审查修正多行菜单的
  祖先 ID，确认菜单 Enter 走菜单确认、不会发送底层输入框。
- 客户端 49 项单元、CI Go 1.26.0 下串行 24 项互通、桌面 55 项单测通过；
  客户端严格 Clippy 通过。本机 Go 1.27.1 下既有 ACK 丢包测试曾出现 1/40、
  30/40 接收，CI 工具链下单独和整组均通过；尚不能据此归因 Go 运行时。
  桌面严格 Clippy 被既有 collapsible_if、items_after_test_module、
  field_reassign_with_default 阻断；仅放行这三类后的 warnings-as-errors 检查通过。
- `make build-desktop` 的 macOS release 编译、打包及深度签名校验通过，最新应用
  为 `desktop/dist/Resona.app`；没有更改版本号或打 tag。
- 明确记录注册没有账号级音频配置同步，沿用本机偏好；历史订阅默认补齐
  每频道至多 200 条，内部 4 条/48 KiB 批次不是总加载上限。未改服务器、协议
  或历史策略，也没有为本轮中断预发服务或进入其他用户频道。

实际 Windows 音频设备反复切频道/在线注册，以及多消息右键、复制/取消、浅深
主题与窄窗口交互仍需实机验收；自动回归及只读审查不代替此项。

## 2026-10-22 Windows v0.2.1 多人语音卡顿回归

分支 `fix/tsukiyo/audio-20261022_voice-stutter`，基于 main c3bf883。维护者报告
两个/三个 Windows v0.2.1 客户端即使关闭全部 3A 仍严重断续；尚无该次实机诊断。

- 在旧 Rust worker 上复现：48 kHz 声卡持续消费、worker 每 16ms 运行一次，
  相对 `now + 20ms` 截止时间使实际混音间隔变成 32ms；10 秒模拟中，启动后
  100ms 以外缺少 177984 个输出采样帧，约 3.71 秒。此数是模拟结果，不是公网测量。
- 改为按声卡输出环剩余采样补帧，每次最多两帧；低于 40ms 时补充，补充后
  少于 60ms。回调负责唤醒，20ms 等待只兜底设备故障检查，不承担播放时钟。
  不增加线程、全局高精度定时器、忙等、逐包日志或周期磁盘写入。
- 使用真实 Opus 编解码，在全部 3A 关闭时覆盖一个/两个远端说话者、7/16/31ms
  worker 间隔；每组模拟 10 秒，预热后输出断粮为零、语音实际写到设备、
  接收无丢弃，输出积压不超过三帧。模拟不代替 Windows WASAPI 或游戏负载验收。
- 复现并修复丢失结束标记后的恢复问题：停顿时抖动序号持续推进，使新语音被当成
  迟到帧拒绝；超过既有 120ms PLC 窗口后重新积累播放窗口，原地复用存储。
- 原生 C callback/ring 检查通过，含半帧输出周期、余量读取与反复回绕。
- 真实本机 Go 服务端启用嵌入式 JetStream，两个 Rust 客户端各连续发送 100 个
  Opus 帧，第三个同时收听并提交聊天；两路可持续接收和解码，聊天正常完成。
  该回归要求每路至少收到 95 帧；不能据此排除公网、设备或服务器负载问题。
- Rust 客户端 48 项单元和原有 23 项 Go 互通、桌面 55 项单测以及严格 Clippy
  检查通过；新增 JetStream/双发言互通单独通过。macOS release 构建与签名
  校验通过，应用路径为 `desktop/dist/Resona.app`。未改变服务端或网络协议。

Windows 实机双人/三人长听、切后台和游戏负载仍待新版 Windows 包复测。
维护者已授权提交并推送修复分支、触发 Windows 构建供验收；未合并主分支或
发布新版本，也没有操作远端活动频道。

## 2026-10-22 服务器本地账号

从 main 的 6863b97 创建 feature/tsukiyo/identity-20261022_server-accounts。
SQLite 保存独立账号 UID、加盐 Argon2id 密码与 Token 摘要；KV 保留角色与管理资源。
按维护者要求不迁移旧身份，旧设备权限与凭据一次性清空，频道和服务器设置保留。

- `go test ./internal/... ./cmd/resona-server`、账号/服务器/原生协议的 race 检查与
  `make lint` 通过。覆盖注册/重开、独立 UID、随机盐、密码重置、Token 撤销与轮换、
  旧会话替换、游客权限、账号昵称同步、管理后台密码登录和会话撤销。
- Rust 音频 feature 的 45 项单元、23 项 Go 互通和桌面 55 项测试通过；含恢复故障
  回归：Token 登录成功后中断首次频道恢复，第二次使用已轮换的新 Token
  恢复账号及原频道。测试故障 peer 只存在于互通 fixture，不进入产品。
- 严格 Clippy、格式检查、CGO 关闭的 server 构建和本机签名 release 桌面包通过。
  产品复核修正了旧凭据抢回连接、临时繁忙终止恢复、恢复期间 Token 轮换顺序、
  登录后昵称草稿和 Token 导出迟到回调等问题。
- macOS GPUI 使用独立回环服务器实窗验证离线提示、游客注册、回车提交、UID/角色
  显示、昵称保存同步频道及底栏、退出断开、重新作为游客连接、账号密码登录恢复昵称。
  未访问远端活动频道，未修改系统钥匙串；账号密码与 Token 不进入本地偏好或诊断。

需 client/server 配套升级；账号只属于当前服务器。Windows 原生窗口、多人长时间
使用、正式部署备份与等负载性能仍待验收。本轮不发布版本、不推送或部署远端。

## 2026-10-22 单服务器管理后台

从 origin/main 的 778506a 创建 feature/tsukiyo/server-20261022_admin-console，按
Casbin/KV 角色、管理认证、频道与事件接口、成员/封禁/凭据、在线设置、内嵌页面
六个模块分别提交。未变更协议帧格式、Noise 握手或音频处理，不操作线上服务器。

- `go test ./internal/... ./cmd/resona-server` 通过；权限矩阵、KV 重启、CAS 冲突、
  CSRF/Origin/Host、会话过期/撤销、原生频道能力、成员实例、默认/非空频道保护、
  设置保存及 retention 生效有回归覆盖。新增真实 Noise 客户端测试验证封禁关闭会话、
  同身份禁止重新连接、解除后可连接。
- `go test -race ./internal/server ./internal/protocol/native ./cmd/resona-server`
  与 `make lint` 通过。初次 lint 被临时验收 harness 中的两个 main 阻断，给临时文件
  添加 build ignore 后通过；临时数据、脚本、截图及凭据仅在已忽略的 build/bin 下。
- Rust client 全量 21 项单元和 19 项 Go 互通通过；随后新增的管理关闭原因单测也通过，
  验证 3/4/5 不重连、普通 0 可重连。Rust 格式和 Git 差异检查通过。
- 本机 Chrome/Playwright 使用独立回环服务和两个真实测试成员通过：登录/退出、
  频道创建/编辑/删除/改序、409 保留草稿、成员移动/踢出/封禁/解除、角色委派与
  admin 限权、凭据创建/撤销、服务器设置保存、SSE 更新与撤权清空、跨栏目草稿、
  页面恢复重新订阅、保存等待中退出、首个快照等待期间注销、401 后重新登录。
  登录/注销竞态修复经请求拦截重现场景验证，未宣称取消会撤销已提交操作。
- 1280×900 深浅主题和 390×844 窄窗口截图检查通过，无页面横向溢出，窄窗口保留
  退出；图标本地内嵌，页面无运行时 CDN/Node 依赖。独立产品复核发现的退出、
  草稿、恢复订阅、冲突完整值和登录代次问题已修复并复测。
- `CGO_ENABLED=0 make build-server` 和 Linux amd64 纯 Go 交叉编译通过。

尚未验证公网 HTTPS 反向代理、Windows 浏览器和真实游戏负载。没有进行线上部署、
合并、推送或版本发布；管理端默认关闭，启用与凭据步骤见 server.md。

## 2026-10-21 Rust 音频工作循环

- 单独音频 owner 管理设备、编解码、抖动与混音；回调只读写有界 PCM 环并唤醒。
  本地监听不进入网络；后台不发布周期电平；没有设备时 worker 无限等待事件。
- 32 项单元与 19 项真实 Go 回环测试通过，含实际产品 ABI 2 音频库。新增覆盖
  音量相乘、坏包恢复、AGC 错误旁路及显式重试、发送成功才亮灯、旧频道帧拒发、
  队列满拒绝不改变已受理操作、候选失败保留旧设备、PTT 代次、打开期间取消与换会话。
  随后增加路由打开期间退出和仅离线路由清理回归；退出前重查持久取消条件，避免
  原生调用消耗唤醒 token 后误入无限等待。
- 设备打开由音频线程执行；迟到完成按操作及会话/频道代次拒绝，退出最多等待三秒。
  若 OS 驱动仍阻塞，线程保留数据直至原生清理结束；不强行释放回调使用的内存。
- 本轮设备用 fake 验证所有权，未打开真实麦克风；GPUI 仍使用 Go Core。提示音、
  产品操作投影及最终进程内接入后继续做实机验收，尚不代表完整迁移或性能收益。

## 2026-10-21 Rust 音频基础

- 官方 libopus 1.6.1 与 gopus 分别编码的十帧合成音，由两种解码器解码；
  帧长和有限值通过，两方向波形 NRMSE 均低于 5%。此项不是人声质量评估。
- Rust 发送 Speex AEC/ANS、接收 Speex AGC 和实际产品 ABI 2 库的 AEC3、NS、
  AGC2、RNNoise 均通过固定帧调用测试；配置顺序/参数拒绝及逐阶段有限值检查通过。
- 无设备的 miniaudio 回调测试在 ASan/UBSan 下通过：有界溢出、100 次环回、
  PTT epoch 隔离、部分帧重置、输出欠载静音及本地监听不进入 AEC 参考。
- 完整 Rust 音频 feature 下 19 项单元和 19 项 Go 回环测试通过；没有打开或
  枚举系统音频设备。新增三平台 CI 编译此 feature，但尚未推送运行。
- 设备/Opus/DSP 已建立 Rust 所有权；完整实时 worker、混音与 GPUI 尚未切换。
  Windows 原生链接、驱动取消、真实收听和游戏资源消耗仍待验收。

## 2026-10-21 Rust 会话、配置与凭据迁移

- 持久 identity seed、schema 1 书签、旧格式备份及未知/损坏文件写入保护已迁移。
  macOS/Windows 沿用原凭据键与 JSON 内容；原生查询/更新代码经只读复核，测试仅
  使用 mock，未读取或改动真实钥匙串。Windows 原生 API 编译及系统提示仍待实机。
- Rust 14 项单元测试与 18 项真实 Go 回环测试通过，覆盖身份继承、密码拒绝、
  频道/聊天/语音隔离、前后台资源范围、退出清空和取消拨号。控制 ACK 丢失期间
  继续接收语音，资源范围快速变更串行合并，不取消半帧写入。
- 恢复单次共用 8 秒期限，前三次短退避后 25–30 秒；恢复保持麦克风静音，原频道
  不可用单独告知。最终失败释放会话密码，立即重试不会积存到下一次退避。
- locked/offline Clippy 全 targets、warnings-as-errors 与格式检查通过。
  产品桌面尚未接入此层；音频、产品投影、诊断/更新及打包后续迁移。未测量收益。

## 2026-10-21 Rust Client 迁移第一阶段

- 新增独立 `client/` crate，与现有 Go Server 保持 RN06、Noise NK 和共享 Protobuf 契约；产品桌面仍使用完整 Go Core，尚未切换或删除 IPC。
- Rust 8 项单元测试及 12 项真实 Go 回环互通测试通过。覆盖双向控制/语音、握手和 ACK 丢包、背压、取消及超时、部分帧保留、释放关闭、身份签名、登录拒绝，以及真实服务端内嵌 JetStream 聊天收发。
- 后台换钥由 Go 参考密文向量、旧密钥宽限/重放测试及 Rust 双端真实 UDP 同时换钥验证；未以长时间 Go/Rust 运行替代上述分项测试。
- `go test ./internal/...`、Rust locked/offline Clippy（所有 targets，warnings 视为错误）、Rust 格式检查及临时目录构建清理测试通过。产品流程和协议生命周期复审已完成，并修正缓存读忽略取消/期限和持续收包可能饿死语音发送的问题。
- 新增独立 Linux、Windows、macOS 互通 CI；尚未推送触发。未做真实设备、GUI、公网长时或性能测量，不能据此声称常驻内存已经降低。本轮不改生产 NATS 数据目录、不部署远端、不更新发布版本或 tag。

## 2026-09-17 主题应用图标

- 以原有音柱轮廓生成浅/深 PNG 和 ICO；静态 `desktop/assets/appicon.png`、Windows resource 1 与 SVG 默认白色，resource 2 为深色运行时图标。运行时 PNG 为 256px，应用内及原生图标对象缓存，切换不写安装包、不增加定时器。
- macOS 构建及桌面 40 项测试通过，实窗验证启动按系统浅色显示、深色预览及取消恢复。AppKit Dock 更新接入相同事件，未通过自动化独立截图 Dock，也未改动系统外观设置。Windows 大小图标使用独立拥有的 HICON，随 UI 线程退出回收；Windows 编译与固定任务栏/高 DPI 仍待 Actions 和实机验证。
- 独立调试验证包 `desktop/dist/Resona-Theme.app` 已生成，ICNS 转换、plist 和 ad-hoc 签名完整性检查通过；原 `Resona.app` 未替换。此包未使用发行证书，不作为正式发布产物。

## 2026-09-17 设置分栏、处理器元数据与连接日志

- 设备与声音分栏，统一图标槽位；12px 信息图标显示 Core 提供的后端说明。菜单按 Core 的阶段/处理项/后端元数据渲染，诊断探测成功会刷新能力；新安装默认 Speex 接收 AGC，已保存关闭的偏好不改。
- 产品流程复核修正了探测不刷新能力、应用等待时设备菜单仍可点击的问题，并补充未应用草稿导致麦克风测试禁用的提示。
- `go test ./internal/...`、桌面 40 项测试、本机 debug 构建通过。`TestNativeChatVoiceIsolationAndShutdown` 在 `-race` 下通过，观察到每个已认证会话对应的连接/断开日志，含远端 IP/端口、昵称、会话 ID 和时长；日志在锁外，语音广播不记录逐包日志。
- macOS 临时应用验证默认窗口及 760×600 下的设备/声音导航、侧栏图标、信息提示、AEC3/AGC2 菜单、滚动和固定确认区、跨页草稿、取消与诊断探测。未连接远端服务器或启用设备采集。
- 固定版本 gofumpt 调用通过；`make format GOFUMPT=gofumpt` 在临时仓库副本完整执行 Go 和两个 Rust 工程格式化。本次未将整仓既有格式差异混入功能改动。
- 本轮未重新打 release 安装包，Windows 实机、多人听感及游戏负载仍需分别验证；界面检查不代表性能测量。

## 2026-09-16 语音工作区与设置草稿

- 频道/成员成为主要工作区，资料按选中内容展开；底部实时文字可折叠、拖高，发送按钮固定尺寸。客户端显示缓存增加 512 KiB 字节上限，重连成功清空旧文字。
- 设置统一草稿；取消丢弃，关闭确认，实际音频配置完成并核对后才保存。纯提示音/快捷键修改不重建设备；退出清理待确认状态，迟到回复不保存草稿。产品体验子代理进行了初审和完成复审。
- `go test ./internal/...`、`go test -race ./internal/client ./internal/audio ./internal/desktopipc` 通过；新增停用语音偏好测试后 client 竞态回归再次通过。Rust 30 项测试通过，macOS release 编译和打包通过。
- macOS 实窗与临时回环服务器验证：频道/成员资料、成功换频道清空、发送与长文本换行、链接/复制入口、折叠/拖高、1197px 与约 1039px 宽度下的布局、设置取消/关闭确认、离线和在线应用增益后回读。测试音量已恢复原 +6 dB。
- 临时独立成员发送 26 条广播，验证停留底部自动跟随、上翻阅读时新消息不抢位置、点击新消息回到底部、折叠后继续接收与提示。测试成员、服务器和本次书签已清理；没有连接远端活动频道。
- 最新应用为 `desktop/dist/Resona.app`。Windows 设备/游戏负载、输入法组合键及长时间多人听测仍需实机验收；未将本次功能验证当作 CPU/GPU 性能测量。

## 2026-09-15 可复用部署包

- `make deploy` 默认生成 linux/amd64 静态二进制及独立 Docker 构建包；`DEPLOY_ARCH=arm64` 生成另一架构，ELF 类型已核对。两种架构的生成目录和 tar.gz 均在新的 `build/deploy/` 下，旧部署目录保持不变。
- 归档清单只包含二进制、Dockerfile、受限 dockerignore、升级脚本、README、许可证、构建信息及生成标记；未复制私有配置或历史部署数据。
- 本机 OrbStack 成功构建 amd64 scratch 镜像，并在 `--network none --read-only --cap-drop ALL` 的临时容器运行 `--version`；未进行远端部署或真实容器切换。
- `go test ./deploy` 使用模拟 Docker 验证正常切换、停止/备份/运行/健康检查失败恢复、无换行 CID 清理、环境文件权限、升级锁释放、非法参数及生成目录保护。`make test-build` 验证新部署产物被清理、旧 deploy-* 与数据保留。Shell 语法及 diff 检查通过。

升级脚本只适用于文档中的已有 data/access 布局。两秒运行检查不代表完整连接/语音健康；回退容器不自动撤销数据格式变更。通用源码在 deploy/，构建包不自动上传或重启服务器。

## 2026-09-15 构建入口与清理

- `make` 默认目标为完整构建；增加独立 core/desktop/server 目标，保留 `core` 别名。macOS 打包脚本直调也通过统一 core 目标构建，避免新界面混入旧核心。
- `make test-build` 在临时目录验证 clean/clean-all 的删除边界、缓存区别、幂等，以及拒绝在同一调用中混合清理和构建；图标、部署数据、其他目录中的性能报告保留。
- 本地实际执行 `make clean` 后，`make -j2 build VERSION=v0.1.1-dev` 成功。core/server 的版本输出正确，应用包内核心与 build/bin 产物一致，图标源文件 SHA-256 不变。
- macOS 脚本从 desktop 目录直接调用、锁定依赖的 Rust 29 项测试、shell 语法和 diff 检查通过。Linux 目标依赖与 server 交叉编译命令做 dry-run 检查，未进行 Linux GUI 或 Windows 实机验证；Windows 保留既有专用脚本。

本次仅维护工具链，不修改协议、部署远端或移动已发布 tag。`clean-all` 的删除范围在临时目录验证，没有为了验证而删除本机完整 Rust 编译缓存。

## v0.1.1 发布检查

版本、Cargo 锁文件、macOS bundle 与 [发布说明](releases/v0.1.1.md)同步更新。完整 Go 竞态回归、锁定依赖的 Rust 29 项测试和 macOS release 构建用于本次发布；保持 RN05 兼容，不部署服务端。另补恢复频道被拒绝后清理会话重连信息的回归测试。

产品流程复审确认导出成功/失败均解除等待，退出后不定位文件；导出已从全局忙态排除，只禁用自身按钮。此前实窗验证范围见下方开发记录，Windows tag 构建结果以 Actions 为准。

## v0.1.0 发布检查

发布前 `go test -race ./internal/... ./cmd/... ./tools/desktop-perf` 和 Rust 29 项桌面测试通过。2026-09-15 远端已升级当前 RN05 服务，备份原有持久数据并保留旧容器，实际验证握手、临时身份登录与资源订阅；用户确认客户端连接正常。后续透明发送按钮样式通过 macOS release 编译及打包。

发布说明见 [v0.1.0](releases/v0.1.0.md)。以下开发切片记录保留当时的验证范围和产物状态，不能用来推断 Windows tag 构建已经通过。

## 2026-09-15 聊天输入框布局

正文与底部发送行共用外框，发送箭头固定 32×32px。输入改为固定高度的多行滚动模式，避免自动增高的正文在最小高度溢入按钮区域。

Rust 29 项测试通过。macOS release 编译与打包通过；隔离本地服务器实际窗口验收了默认与 64px 最小高度、多行草稿拖动后保留、Shift+Enter 换行、按钮发送成功后清空并回焦、Enter 发送一次。中文输入法确认与失败/断线流程沿用现有处理，本次未额外实测；Windows 视觉验收待完成。开发包更新至 `desktop/dist/Resona-NativeOnly.app`。

## 2026-09-15 开发切片

范围：仅保留 Noise UDP，移除过时传输入口和历史对比，补当前频道成员通知，更新服务器表单与书签拒绝逻辑。

通过：

- 完整 Go 竞态回归：`go test -race ./internal/... ./cmd/... ./tools/desktop-perf`。
- 新增配置测试后再次通过 `internal/config`、`internal/client`、`internal/protocol/native` 竞态回归。
- Rust 29 项测试通过，macOS release 编译及应用打包通过。
- 服务端 Windows amd64、`CGO_ENABLED=0` 交叉构建通过；这不是 Windows 桌面或设备实测。
- 两个入口的 `go list -deps` 与源码扫描确认只使用现有原生网络实现，根模块和独立编码基准模块的依赖锁已整理。
- 文档本地链接检查无断链，`git diff --check` 通过。

新增通知测试验证：初始列表、资料变化、订阅范围扩缩与本人移动静默；其他成员移动、实例替换、短暂加入后离开保留对应事件。旧书签测试覆盖核心连接、凭据读取前拒绝、不影响当前会话，以及混合配置加载不覆盖用户文件、旧记录可删除。

macOS 隔离验收使用临时本地服务器、临时身份与内存书签。实际窗口验证名称/地址/昵称/公钥四字段；缺公钥错误保留已输入名称和地址；Escape 取消；双击旧书签显示应用错误且不弹密码框，原连接保持在线。验收应用及嵌入服务已退出。

开发产物：`desktop/dist/Resona-NativeOnly.app` 和 `build/bin/native-only/resona-server`，两端 `--version` 为 `dev-native-only`、dirty=true。普通 `Resona.app` 和远端服务未替换。当前分支未提交、未推送、未部署。

限制：未实跑 Docker 镜像、本轮 Windows 桌面设备/游戏负载和长时间公网听测尚未完成。系统 Keychain C API 弃用提示及 Rust `block 0.1.6` 未来兼容性提示仍存在，不属于已退休网络依赖。

## 2026-09-14 资源观察基线

- Go 核心回归、相关竞态测试与 28 项 Rust 测试通过。
- 本地资源测试：30 次后台修改没有 workspace 推送，恢复一次同步拿到全部状态。
- macOS 已操作验证聊天分隔线拖动、多行草稿、窗口缩放和回车发送。
- 最小化恢复、Windows 实际设备和游戏 CPU/GPU 仍未验收。

本轮删除与逻辑修改的重新验证见上节，不直接沿用旧结果作为通过证据。
## 2026-09-15 自动重连与内存诊断（开发分支）

分支：`feature/tsukiyo/client-20260915_reconnect-diagnostics`。未提交、未推送；不更新远端服务器，不更改 RN05。

- 完整 `go test -race ./internal/... ./cmd/...` 通过；导出即时计数与最后的预分配调整后，相关 audio/client/desktopipc/diagnostics 竞态测试再次通过。
- 重连测试覆盖：短暂失败后恢复、永久认证失败停止、取消退避/连接、旧回调隔离、频道恢复中再次断网、原频道失效回退、恢复中退出；临时真实 Noise UDP 服务重启后身份保留、会话 ID 更新。
- 诊断测试覆盖：容量覆盖顺序、时间窗口淘汰、独立快照、并发记录/导出快照、争用非阻塞丢弃、字段限制、拒绝任意对象和未审核事件、主动导出前不写盘、取消清理半成品及失败后重试。
- Rust 29 项测试通过，macOS release 编译与打包通过。现有 Keychain C API 弃用及 Rust block 未来兼容性告警仍存在。
- macOS 隔离应用使用本地临时服务器、临时身份、内存书签，验证重连取消、服务重启恢复原频道及静音提示；音频设置导出成功，Finder 选中合法 JSON，再次导出包含 reconnect succeeded 与当前音频累计计数。没有采集麦克风或连接用户远端服务器。

M2 短基准：结构化事件约 336ns/op，音频计数约 11ns/op，均为 0 B/op、0 allocs/op。事件环固定存储 835632 字节（约 816KiB）；导出另有短暂快照/JSON 分配。这不是整体 CPU、Windows 游戏 1% low 或后台 GPU 测量，不能外推为零帧时间影响。

试用包：`desktop/dist/Resona-Reconnect.app`，核心版本 `dev-reconnect`；隔离验证包 `Resona-Reconnect-Check.app` 仅用于测试。Windows 长时多人语音、蓝牙/设备切换、休眠与网络切换、导出失败的实际界面仍待验收；文件失败与取消路径已做 Go 测试。

## 2026-09-16 收听增益与聊天范围

分支 `feature/tsukiyo/audio-20260916_playback-chat`，未提交。全局默认 +6 dB、单人默认 0 dB，两层 -20 至 +18 dB；专用 SetOutputGain 同步确认且不重开设备，失败不保存。产品审查发现并修复异步配置受理误当成功、试听禁用和排队静音覆盖已确认增益的问题。

- 完整 Go internal 测试通过；最终 audio/client/desktopipc 竞态测试通过。覆盖增益成功、失败、旧会话、范围、设备不重开、静音保持、两层叠加、限幅比例与释放，以及换频道/离服清理、迟到旧频道消息过滤。
- Rust 29 项测试通过，验证整数 dB 往返、输入截断和静音保持；macOS release 打包通过。
- macOS 实窗验证全局 +6 至 +7 dB 调整，设置与底栏一致；重启恢复 +7 dB，一键重置回 +6 dB。约 1197px 和 1026px 宽窗口布局无重叠。只操作离线设置，未进入远端频道或开启麦克风。
- M2 限幅器微基准约 1974 ns/20ms 帧，0 B/op、0 allocs/op。只在已有混音路径运行，无新增空闲定时器、设备实例或音频缓冲；该结果不是 Windows 游戏性能数据。

当前试用产物 `desktop/dist/Resona.app`。Windows 多人真实听感、峰值较大时的限幅听感和游戏性能仍需实机验收；没有修改服务器协议或部署远端。

## 2026-09-16 macOS 标题栏与凭据读取

- macOS 使用 GPUI 组件的透明原生标题栏，背景与内容统一；34px 顶栏位于设置及退出遮罩之外，内容最小高度调整为 566px，窗口最小高度仍为 600px。Windows 标题栏配置不变。
- `go test ./internal/...`、credentials/client/desktopipc 竞态测试通过；Rust 30 项测试通过，`make build-desktop` 完成。传统 Keychain API 弃用和 Rust block 未来兼容性告警仍存在。
- 临时随机钥匙串条目的原生集成测试通过，覆盖读写、更新与删除；新增测试验证显式读取一次、后续缓存、拒绝不重试。未访问现有服务器密码。进程检查仅有一组 desktop/core，未发现同时运行多核心。
- macOS 新包实际启动，确认原白色标题栏替换为暗色、激活时交通灯正常显示、设置遮罩不覆盖标题栏，双击缩放后面板完整。未连接远端或开启麦克风。
- 用户原有受保护条目的连续两次提示未复现，不能宣称该问题已彻底解决；需要用户在新包中复测。ad-hoc 签名更新后的持久授权、Windows 实机表现仍不在本轮验证范围。

后续用户复测：新包首次连接，输入密码点击“允许”后仍出现第二次同文案授权。上面的读取路径调整未解决问题。已增加不含条目标识或秘密的原生调用边界内存诊断；全量 Go internal 测试及本地打包通过，待带诊断的新包复现后导出以判断实际调用来源。单元测试确认开始/结束可关联、保留数值 OSStatus、不记录错误正文。

04:00 导出及相应系统日志已定位根因：一次 `has_silent` 11ms、一次 `read` 4455ms，无诊断覆盖/丢弃；securityd 在首次访问允许后报告旧条目与新二进制的 `ACL partition mismatch`，再进行 XARA partition 授权。`codesign -dr -` 显示 core 的 designated requirement 为 cdhash，`security find-identity -v -p codesigning` 返回零个有效身份。未修改用户钥匙串 ACL 或条目，未将私有诊断文件加入 Git。问题尚未通过稳定签名修复。

## 2026-09-16 竖向窗口与缩放修正

用户暂停发布并撤销版本递增，VERSION 与 Cargo 保持 0.1.1，未提交、合并或打标签。默认窗口改为 800×900，最小 760×600。根容器直接绑定 viewport 尺寸，标题栏与主区作为直接 flex 子项分配高度，取消百分比高度包装；频道主区显式全宽，输入区保留固定高度与发送按钮尺寸。

设置面板限制为窗口内宽，内部页面与麦克风表采用弹性宽度。产品复审指出 640px 下详情、设置与底栏空间不足，本轮选择 760px 最小宽度，不引入新的移动端导航模式。

Rust 30 项测试与 macOS release 打包通过。离线实窗验证 800×900 默认、760×900 窄窗、760×600 最小窗：主区、文字输入和底栏填满可用宽度，设置语音/提示音页及取消/应用按钮完整可见，服务器栏收起后文字区同步扩展。未连接真实服务器或开启麦克风；Windows 缩放、连接后成员详情与长消息内容仍需实机验收。

## 2026-09-16 macOS Actions 构建

新增独立 macOS build，使用 macos-15 arm64 与 macos-15-intel 两个 runner；普通分支版本带 -dev，精确匹配 VERSION 的标签去掉该后缀。分别上传 Apple Silicon/Intel ZIP，保留 14 天，不创建 GitHub Release。

actionlint v1.7.12、shell 语法及 diff 检查通过。本机 arm64 独立测试包 release 打包、ad-hoc 签名及严格校验通过；修正旧 Contents 根目录标记文件导致的签名验证失败，将标记移至 Resources，同时识别旧包标记用于升级。没有替换当前运行的 Resona.app。

首次 GitHub Actions 与 Intel 构建仍待推送后验证；ad-hoc 签名不是 Developer ID 或 Apple 公证，不能将其作为跨版本钥匙串授权问题已解决的证据。

## v0.1.2 发布前检查

用户重新授权提交、合并主分支并发布新标签。VERSION、Cargo.toml/Cargo.lock 与发布说明同步为 0.1.2。完整 `go test -race ./internal/...`、Rust 30 项测试、cargo fmt 检查、shell 语法、Windows/macOS actionlint 与 diff 检查均通过；独立 macOS v0.1.2 release 包构建及严格签名验证通过。沿用本轮已记录的产品流程复审与实窗验证，不将远端 Actions 或 Windows/Intel 实机状态记作已通过。
# 2026-09-17 Windows build follow-up

- Final remote validation: Windows run 35219519290 at 0afad0f passed credential tests, core/audio tests, native WebRTC tests, packaged-library smoke checks, desktop compilation and artifact upload. macOS run 35218751010 at 1935d38 passed both arm64 and x64 tests, bundle creation, packaged-library checks and upload. The later 0afad0f change adds only a Windows linker argument; macOS behavior is unchanged. Fix-branch macOS packages are ad-hoc signed, not the trusted main/tag certificate path.
- Windows run 35218579097 reached linking after the path fix and exposed missing winmm for WebRTC timeGetTime. The native crate now uses the same trailing Windows system-library link argument as the passing 3A benchmark.

- Fix-branch run 35217174221 passed the Windows Go audio tests in 2.195 seconds and the GCC runtime query, then hit MinGW archive path limits under the user TEMP directory. Native builds now prefer RUNNER_TEMP and reject overlong local build paths with an explicit override option.
- macOS tag jobs completed core/desktop tests and native compilation, but license collection requested uncached cross-platform crates while forcing offline mode. License metadata now permits downloads while retaining the locked dependency graph. Fresh remote packaging checks are required for both platforms.

- v0.1.4 Windows tag run 35214797720 timed out in the live DSP replacement test; main run 35214797706 also exposed a truncated unquoted PowerShell GCC library query.
- Encoder control is now checked between queued frames. A deterministic test keeps capture nonempty and verifies replacement and old-processor destruction at the next frame boundary.
- The new regression and existing live DSP replacement test passed 10 repetitions under the Go race detector locally. Windows packaging validation remains pending on the fix branch; v0.1.4 is immutable.

## 2026-10-14 本地配置升级保护

分支 `feature/tsukiyo/client-20261014_config-updates`，未提交。偏好对象与书签 envelope
增加独立格式版本 1；读取旧文件只迁移内存，首次保存备份原始字节。损坏、超限、
未来版本及当前版本未知字段拒绝覆盖；恢复偏好不自动开麦、3A 全关、全程不保存。
诊断页显示保护状态并可定位配置目录；设置区分 Core 已应用与偏好已保存。

- Rust 45 项测试通过：旧链及明确关闭项、原文件读取不变、逐字节备份、备份冲突、
  未来版本/错误类型/嵌套未知字段/超限、恢复配置及乱序保存。
- `go test ./internal/...` 通过。首次沙箱运行的 UDP bind 被拒绝，在允许本机端口
  的环境复跑通过；没有使用远端服务器。
- `go test -race ./internal/config` 通过：旧数组、v1 往返、权限、Core 启动拒绝错误
  配置、Save 再校验、原文件保护与备份冲突。
- `make build-desktop` 成功，包含最新 Core、GPUI 与 WebRTC 库的 macOS release
  包完成 ad-hoc 签名和严格校验。该开发包不作为公开签名或公证完成的证据。
- 原生 CUA 验证新版离线默认 800×900 与最小 760×600：配置状态、诊断按钮、Core
  探测结果与固定页脚无重叠；Finder 成功选中配置目录。未连接服务器或开启麦克风。
  验收后退出应用。
- 产品体验初审及完成复审通过。修正旧保存失败污染新结果和诊断页内容挤压问题；
  定位按钮文案匹配系统行为。`cargo fmt`、gofumpt v0.10.0 与 diff 检查通过。

异常配置实窗未通过破坏用户真实文件来验证，保护逻辑由临时文件测试覆盖。
Windows 文件替换/目录定位/最小窗口仍待实机验证。保存前校验不提供跨进程事务；
早于本次版本保护的旧二进制不能获得未来格式保护，回滚不等于配置降级。
本节阶段完成时公开发布和自动更新尚待实现；后续进度见文末更新流程验证及 ADR-0029。

## 2026-10-14 静默资源刷新与悬停复制

同一功能分支，未提交。恢复前台或扩大资源关注范围时保留缓存界面，异步刷新，
移除同步遮罩和聊天/管理的刷新门控；会话、命令等待、对象及权限检查保留。
复制按钮使用消息行独立 hover group 和固定 28×28px 槽位。补齐期间过滤成员提示音，
连接和断开通知不再被普通资源刷新过滤。

- Rust 46 项测试通过，包含刷新期间连接通知及成员补齐过滤回归。
- desktopipc 的后台合并及资源关注范围回归通过：后台多次变化不发布普通状态，
  恢复后一次补齐。没有改变 Core 实现，没有增加空闲定时器。
- `cargo fmt`、diff 检查和 `make build-desktop` 通过；最新 macOS release 包为
  `desktop/dist/Resona.app`，ad-hoc 签名及严格验证通过。产品流程复审通过。
- 独立 debug 包离线窗口和本地预览可打开，未连接远端或开启麦克风。
  首次自动化点击触发 GPUI macOS 窗口激活回调阻塞；进程采样显示
  `window_did_change_key_status` 持锁调用 `resignKeyWindow`，同步重入回调等待同一锁。
  重启并 Raise 后可进入预览，但自动化键盘/粘贴未成功，不能将悬停、复制或真实网络
  切应用的实窗验收记作通过。临时测试进程已结束；采样文件未加入 Git。

Windows 实机、长消息换行、悬停不重排及剪贴板完整性仍待验证。

## 2026-10-14 连接与资源同步生命周期

同一功能分支，未提交。初次连接临时失败最多三次尝试，明确拒绝立即停止；
断线恢复三次快速尝试后显式显示失败并保留慢速重试。立即重试唤醒现有 worker，
退出取消拨号和退避，恢复后保持麦克风静音。资源同步独立报告状态；有缓存的
普通刷新静默，三次失败后阻止频道操作并等待手动重试，不停止健康语音。

- `go test ./internal/...` 和连接、IPC、原生协议三个包的 race 测试通过。
  覆盖 setup 期间关闭、旧回调、取消等待、三次失败、立即重试与计时器竞争、
  单资源请求、关注范围替换、断开取消和显式重试。
- 确定性 IPC 测试验证首次同步、成功和失败状态都在对应 connected workspace
  之后发送；原生控制测试验证资源回复超时、取消及明确拒绝不关闭健康传输。
- Rust 47 项测试通过；临时目录加入进程内唯一编号，消除并行测试命名碰撞。
  普通刷新期间保留 Core 产生的真实成员通知，bootstrap 静默由 Core 负责。
- 最终 `make build-desktop` 成功，最新 Core、GPUI 与 WebRTC 库打包至
  `desktop/dist/Resona.app`；ad-hoc 签名严格验证、gofumpt v0.10.0、Rust 格式与
  diff 检查通过。开发签名不代表公开分发签名或公证完成。
- 产品与生命周期复审完成，发现的提前回调、IPC 顺序、重试令牌、同步提交失败
  和永久拒绝分类问题已修复。没有新增空闲定时器、逐包日志或后台文件写入。

原生失败遮罩、按钮及切应用的完整实窗流程仍待验收；上一节记录的 GPUI macOS
自动化激活阻塞仍限制 CUA 验证，Windows 尚未实机验证。不将单元测试等同于实窗
和真实网络验收；本轮未连接或修改用户远端服务器。

## 2026-10-14 轻量连接状态提示

去掉连接、恢复和初次资源同步的全区灰幕；工作区上方显示紧凑的实色状态面板，
普通等待低对比，失败明确区分。透明层使用 GPUI `occlude()` 阻挡底层点击、悬停
和滚轮；取消、重试、退出按钮，服务器栏、语音工具栏与标题栏保留可用。
无新增动画、计时器或状态机改动。

Rust 47 项测试、格式和 diff 检查、最终 `make build-desktop` 与 ad-hoc 签名严格
验证通过；最新产物为 `desktop/dist/Resona.app`。产品流程与实现复核通过。
实窗点击、深浅主题和最小窗口的状态提示验收仍待完成，沿用上一节的 CUA 限制。

## 2026-10-14 更新检查与 Windows 便携安装流程

同一功能分支，未提交。关于页接入手动检查、异步下载/取消、校验结果、包目录定位
和 Windows 退出更新。没有启动或空闲检查。公开源、公钥及发布工作流未配置，
仓库选择尚待确认；没有创建远端仓库或改变源码可见性。

- `go test ./internal/... ./tools/release-manifest` 通过。沙箱的临时 TCP bind
  首次失败，经允许本机端口的环境复跑通过；没有使用远端服务器。
- `go test -race ./internal/updates ./internal/desktopipc` 通过。覆盖篡改签名、
  过期清单、版本排序/回退、HTTPS 限制、完整下载、取消/重复任务、ZIP 路径与
  重复项拒绝、包版本身份、目录替换失败回退、过期/取消安装交接信号。
- Windows 更新包测试的 `GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go test -c`
  编译通过；这不等于 Windows 原生执行或完整 cgo 产品打包验收。
- Rust 47 项测试及 `cargo fmt --check`、gofumpt v0.10.0、diff 检查通过。
- 最终 `make build-desktop` 成功，`desktop/dist/Resona.app` 包含新 UI/Core，
  ad-hoc 签名严格验证通过。沙箱首次 iconutil 失败，允许系统工具后复跑成功。
- 产品及生命周期复审通过：安装器 ready/commit/armed 后才退出，准备失败
  写 abort 并 Kill/Wait 专属 helper；helper 使用独立 cwd，重新校验后整包替换。
  打包脚本校验 Cargo/Core/包版本，签名工具与安装器再次校验产品元数据。

Windows 原生退出、占用目录、杀毒软件及失败回滚/重新启动尚待实机验证；本机
没有 PowerShell，不将源码复审当作该脚本运行成功。macOS 暂不自动安装，公共
签名/公证尚未完成。保留 `.previous` 和已交接事务缓存，不宣称断电或新版运行期
崩溃自动回退。实窗关于页沿用前述 CUA 限制，未记为验收通过。

## 2026-10-14 Go 静态检查

`make lint` 汇总 `go vet` 和固定 Staticcheck v0.8.1，覆盖根模块及
`test/controlcodec`、`test/opuscodec` 两个独立模块；统一使用 `make lint` 入口。
检查基于当前 macOS/arm64 和默认构建标签，不代表 Windows 专属实现验收。

- 最终 `make lint` 通过，没有禁用规则。修复详情请求取消函数释放路径、14 条
  Staticcheck 告警（13 条错误文案规范及 1 条 benchmark 测试比较表达式）。
- `go test ./internal/... ./tools/desktop-perf ./tools/release-manifest`、
  `go test -race ./internal/desktopipc` 和两个 benchmark 模块测试通过。
  Opus 测试按其文档链接本机已有 Rust benchmark 静态库；未重跑性能采样。
- 模块下载/编译缓存的沙箱限制经授权复跑；macOS Keychain C API 的原生编译
  弃用警告仍存在，不属于 Go 静态检查失败。diff 检查通过，未提交、未推送。

## 2026-10-15 更新发布链路、发行类型与设置入口

- 源仓库 `tsukiyoz/resona` 的更新 Secret 和两个公开 Variables 已配置，私钥
  独立备份在本机 Keychain；未创建新仓库、未发布 Release、未移动已有 tag。
- `release.yml` 复用 Windows 和两架构 macOS 构建，仅全部成功后签名并发布。
  构建固定到事件提交，发布前反复校验远端 annotated tag；已有版本不覆盖，
  先创建草稿并上传全部资产，旧版本不接管 latest。
- 便携包元数据明确标记 `distribution: portable`，Windows 与 macOS 发布 ZIP
  名称均带 `portable` 后缀，共用一个版本 tag。Core 校验包身份后返回更新能力；
  缺失、未知发行类型及尚未实现的 installer 类型均禁止更新操作。
- 便携版仅手动检查签名清单并打开对应版本发布页，没有下载或退出安装操作。
  已删除旧的便携整包替换 helper；旧 IPC 下载/安装命令显式拒绝。
- `go test ./internal/... ./tools/release-manifest` 通过。签名工具验证配置公钥
  与签名私钥匹配；发布脚本模拟覆盖完整首次发布、
  旧版本、已有 Release、缺包、签名失败、上传失败和移动 tag，均通过。
- 更新管理器测试覆盖仅请求清单、不下载包、并发拒绝、取消、失败重试、关闭、
  包类型门控及发布页地址校验；Windows/macOS ZIP 检查覆盖必需文件和包身份。
  Windows 更新包测试交叉编译通过，不代表 Windows 原生执行验收。
- `go test -race ./internal/updates ./internal/desktopipc`、`make lint`
  与调整入口前的 Rust 48 项测试通过；产品流程复核通过。
- actionlint v1.7.12、脚本语法、gofumpt、cargo fmt 和 diff 检查通过。
  GitHub 的实际多平台 tag 运行尚未触发；模拟测试不代表远端发布验收完成。
- `make build-desktop` 使用已配置的公开更新源/公钥打包成功，
  `desktop/dist/Resona.app` 的 ad-hoc 签名严格验证通过；未替代原签名版本的
  钥匙串授权验收。设置中的关于页使用 Core 返回的发布页能力，应用退出后
  忽略迟到的打开发布页响应。

尚未提供真实安装器或安装器自动升级；该工作需另行验证升级和失败恢复。
Windows 手动更新、深浅主题和 760×600 实窗尚未验收，本轮没有新增闲置轮询
或动画。本分支改动尚未提交或推送。

### 更新入口收敛到设置

按最终反馈删除顶部按钮、独立更新弹窗及其专属测试；仅保留设置中的关于页。
macOS 恢复原有空标题栏，其他平台无额外工具行。关于页只读取本地状态，
检查由按钮显式触发；切换栏目/关闭设置不取消检查，显式取消/退出仍取消。
Core 和更新生命周期未修改，不新增空闲任务。旧顶部入口实窗结果不作为
当前产品验收证据；深色、最小窗口与 Windows 实机仍待验证。
最终 Rust 47 项测试、cargo fmt、diff 检查和 macOS 发布构建及签名严格验证
通过。本机 800×900 浅色实窗确认主界面无更新按钮、设置导航可进入关于页，
“尚未检查更新”和检查按钮正确显示；打开关于页没有自动检查。

### 更新检查失败原因

总检查期限改为 10 秒；deadline/HTTP client timeout 均为 failed，只有实际
取消才是 cancelled。GUI 将 failed 显示为“检查更新失败：{安全原因}”，
主动取消为“已取消检查更新”。HTTP 404/403/429/其他响应和 DNS/TLS/连接
错误分别说明，不回显原始请求或网络错误。没有增加自动重试或空闲任务。
`go test ./internal/... ./tools/release-manifest`、更新/IPC race、`make lint`
及 Rust 47 项测试通过。新增用例验证两类期限、取消、HTTP 状态和原始错误脱敏。
本机公开更新 URL 实测 DNS/TCP/TLS 成功后 12 秒无响应；GitHub API 可访问
公开仓库，但 latest Release 返回 404。该次网络超时和缺少发布清单均有直接
证据，不将它们等同于此前用户那一次请求的确切根因。
最终 macOS 发布构建与签名严格验证通过；800×900 浅色实窗点击检查后明确
显示“检查更新失败：尚未发布更新清单（HTTP 404），请稍后重试”，按钮恢复
可检查状态。网络超时和主动取消的分类由上述本地定向/竞态用例验证。

## 2026-10-15 本地 macOS 构建签名与密码读取

用户确认双提示来自 `make build-desktop`。检查旧本地包的 core，签名为 ad-hoc，
无 Team ID，DR 只绑定 cdhash；本机沙箱外可查询到一个有效开发签名身份。
此前脚本只接受显式身份，本机默认打包因而未使用已经配置的证书。

新增身份选择器及接入 `make test-build` 的隔离检查：显式身份优先、CI 和
GITHUB_ACTIONS 无显式身份时不查询证书、零身份保留开发包路径、唯一身份自动
选择、重复指纹去重、多身份拒绝猜测、查询失败明确失败、拒绝把 `-` 当证书。
检查使用 mock，不访问系统钥匙串。shell 语法、差异检查和构建工具链检查通过。
产品/正确性子 agent 复核无阻断问题。

实际 `make build-desktop` 自动选用本机证书并完成发布模式打包及严格签名校验。
core 的 `dev.resona.core` 和应用的 `dev.resona.client` DR 均绑定证书身份，
不再绑定 cdhash；沿用原开发证书，不代表 Developer ID 或 Apple 公证。

独立临时程序 A/B 使用不同构建标记、相同标识与证书，创建仅含合成内容的条目。
A 创建成功；B 读取和另一个 A 进程读取均成功，原生读取各约 12ms，无授权提示。
B 删除返回 `errSecInvalidOwnerEdit`（-25244），随后由创建程序 A 删除成功（14ms），
已清理合成条目；没有据此更改产品删除逻辑或宣称跨路径删除已解决。

本轮未读取、删除或更改现有服务器密码、ACL 或 partition。临时程序的成功仅验证
新条目的跨构建读取连续性；用户原条目的首次迁移提示次数仍需在新版包中实测，
不保证既有双提示由签名选择调整自动合并。此次不改变连接/音频热路径或空闲任务。

## 2026-10-21：RNNoise 与有界频道历史

RNNoise regular 产品接入提交 c686be1，已通过 1b8b11c 合并本地主分支；未推送。
保留 Speex 默认与已有选择，Core 提供算法说明、GUI 菜单项和当前选项 tooltip；
原生音频测试、核心全量/相关 race、桌面测试、make lint、macOS 签名打包通过。
自动化验证过离线 RNNoise 草稿选择/取消及等级隐藏；实际鼠标悬停说明、Windows
产品包与游戏负载尚待实机验收。源码与模型固定及测量限制见 native/rnnoise/README.md。

随后在 feature/tsukiyo/server-20261021_nats-chat 实现 ADR-0031，尚未提交或部署。
用户确认默认每频道 200 条、全流逻辑 64 MiB、7 天；嵌入 JetStream，无外部 NATS
端口。go list -deps ./cmd/resona-core 确认客户端没有 nats-io 依赖。

本地 Go 测试验证条数/字节/过期淘汰、重启后保留身份/顺序/时间、调小配置恢复、
历史连续转实时、本人去重、频道隔离、消费者退出、首次/重连 admission 消息增量、
聊天超时保持语音连接、IPC 读取/关闭不被重试阻塞。增加独立四槽聊天发送队列，
验证历史背压不占关键控制容量且可取消，写循环优先发送 Welcome/控制回复。
频道 epoch 传到 GUI，A→B→A 的旧失败不能覆盖新加载，历史不增加未读提示。
产品体验只读复核后修复了两项超时/IPC 阻塞问题和迟到回复隔离问题。

go test ./internal/... ./cmd/...、相关 race、48 项锁定 Rust 桌面测试、make lint、
go test ./deploy 通过。make build 完成本机 core/server 与 macOS release 包，
严格签名校验通过；make deploy 验证 Linux amd64 纯 Go 部署包和许可证。
构建另补 build/native 忽略规则，缓存源码/模型不进入 Git。

握手为 RN06，需 client/server 同步升级。没有更改 tsukiyo 服务或操作其他用户。
尚未做聊天加载/失败的完整实窗、Windows、长期磁盘/运行内存及等负载性能验收；
不据集成测试宣称 JetStream 比原直接广播更省资源。64 MiB 不是物理目录配额。

## 2026-10-21：Rust 客户端产品迁移

按传输互通、会话与配置、音频、产品 Core、GPUI 接入逐步提交，见 ADR-0032。
产品不再启动 Go helper；删除 cmd/resona-core 和 internal/desktopipc。
Go client/audio 参考实现保留给机器人、基准和服务端互通测试，不进入桌面包。
服务端和内嵌 JetStream 未更换；RN06 与已有配置、身份、密码格式保持一致。

固定静态 Opus 1.6.1 并加载实际 C ABI 2 的全套处理后，44 项 Rust 单元、
20 项真实 Go 回环互通和 50 项桌面测试通过。覆盖重放/篡改/换钥、背压与取消、
重连/资源/聊天、凭据单次保存、逐用户音频链、试听停止回执及命令意图覆盖。
go test ./internal/... ./cmd/...、make lint、make test-build、Rust Clippy 全音频
all-targets 严格警告、格式和 shell 语法检查通过。测试使用临时数据、假凭据和
无硬件设备；未读取用户密码、连接线上服务器或启动实际麦克风。

make build-desktop 完成 Apple Silicon release 包，严格深度签名校验通过；
应用与 APM 库只依赖系统动态库，Opus/Speex 静态链接。运行进程只有桌面本身，
没有 resona-core。实窗验证离线书签、关于中的 Rust 构建信息、诊断与 15ms 核心
探测成功。其后一次窗口激活卡住，线程采样发现 GPUI 0.2.2 持锁调用
resignKeyWindow 后同步回调重锁；Core、音频和提示音线程正常等待。已终止本轮
离线测试实例；此时不能将后续交互及正常退出计为通过，需独立依赖补丁验证。

Windows CI 增加原生 Credential API 合成条目测试，以及无编译器 PATH 的 Rust
音频 DLL 加载检查；macOS CI 校验 Rust 包及签名。Windows、Intel macOS、真实
设备多人长听、游戏负载和等负载性能测量尚未执行；不据语言切换宣称资源下降。

### GPUI 焦点重入修复后复测

固定 vendored gpui 0.2.2，仅在 resignKeyWindow 前释放窗口状态锁，来源、许可、
包校验和及移除条件见 desktop/vendor/gpui/RESONA-PATCH.md。只读复核确认没有
异步间隔、同步回调可重新取锁，整个桌面依赖图共用此补丁实例。
50 项桌面测试和 Rust 格式检查再次通过；release 打包和严格签名校验通过。
上游 shaders.metal 原有的七处空格/tab 缩进原样保留，项目改动差异检查通过。

实际离线实窗重新执行设置打开、关于、诊断、核心探测（4ms）、关闭设置，均正常
响应，原卡住动作未复现。Cmd-Q 后进程退出；重新启动并点击系统关闭按钮后再次
验证进程退出，无 resona-core。没有进入服务器、打开实际音频设备或修改用户设置。
这些结果只覆盖本机离线交互与退出，不替代连接中退出、真实驱动或 Windows 验收。

## 2026-10-21：服务端 Cobra、KV 管理资源与单进程诊断

Rust 客户端迁移已通过 8a096bb 合入本地 main；随后从 main 创建
feature/tsukiyo/server-20261021_kv-resources。本轮未推送、打 tag 或部署。

Cobra 子命令覆盖 serve、identity init、owner init/reset-claim、version，保留旧部署
标志兼容。所有 Go internal 包均有独立 doc.go。频道、owner 与 claim 摘要迁入
共享嵌入式 JetStream 的文件 KV，以一个有界文档的 CAS 保持原子性；旧 JSON 仅在
首次建桶时导入，原文件保留。一个服务端 watcher 驱动现有兴趣范围内的资源推送，
没有逐连接 KV consumer、轮询定时器或语音包日志。未来 HTTP 入口仍需走已授权的
管理方法；本轮没有开放 HTTP 管理接口。

go test ./internal/... ./cmd/resona-server ./deploy、make lint 和相关 server/native/CLI
race 测试通过。补充验证了 CAS 冲突重试上限、排队取消、重启持久化、导入失败恢复、
损坏或删除状态拒绝启动、按兴趣推送、拒绝修改后的投影更新，以及关闭时回收 watcher。
KV/磁盘等待不持有语音路由锁；这些正确性测试不代表已完成并发性能测量。

50 项桌面测试、Rust 客户端 21 项单元与 18 项 Go 回环互通测试通过。诊断页移除
Core 探测，自动加载能力信息；导出等待、失败和重试在本页显示，退出后迟到回执
不打开文件管理器。关于页保留实际构建版本标记。产品体验只读复核无剩余阻断。

make build-desktop build-server 完成本机 release 构建及严格签名校验，独立验收包为
desktop/dist/Resona-KV-Review.app，未替换或操作用户运行中的应用。本轮未做新版本
实窗、真实音频设备、Windows、游戏负载或长期资源验收，也未操作线上服务和凭据。

### 授权远端 KV Review 部署

随后经用户授权，将当前未提交工作树构建为 Linux amd64 Docker 镜像
resona-server:v0.1.5-kv-review.20261021，并部署到 tsukiyo。上传包 SHA-256 校验一致；
停止旧服务后备份完整 data/access，保留旧容器，沿用原身份、密码环境与 9988/UDP。
镜像版本明确保留 dirty=true，本次不是发布或 Git 推送。

启动日志确认监听成功，文件 KV_RESONA_RESOURCES 已创建；检查时容器运行且重启次数
为零。备份目录/归档权限为 0700/0600。此次检查仅覆盖部署、存储初始化与监听，
尚不代表客户端握手、旧频道/Owner 显示、实时同步或语音验收通过；由用户使用独立
Resona-KV-Review.app 继续验证。跨格式回退需同时恢复停服时的数据备份。

## 2026-10-21：频道持久顺序与拖放

用户完成 KV Review 基本验收后，上一版提交为 5ebd3a8，未推送。由该提交创建
feature/tsukiyo/channel-20261021_drag-order，排序改动暂未提交、合并或部署。

频道顺序仍使用 KV 数组，新增独立 default_id，频道记录 v1 读取时规范化为 v2，
首次成功修改保存新格式。新增 owner-only ReorderChannel(id,before_id) 和排序能力
字段；同一 CAS 管理入口及共享 watcher 推送，不添加轮询或改变语音路由。旧 server
不提供新能力时，桌面不发送排序命令。跨格式回退仍需恢复匹配的数据备份。

实际 JetStream 测试覆盖上下/首末排序、权限、失效 ID、存储失败、占用频道可排序、
重启后顺序与 ID 分配、v1 KV 升级首次写入，以及默认频道移动后的标记和删除保护。
Rust 与真实 Go 服务端互通验证多客户端收到新顺序、普通成员拒绝、默认频道移至
末尾后新登录仍进入原频道，以及排序不改变本人频道/epoch/音质。

go test ./internal/... ./cmd/resona-server ./deploy、相关 server/nativewire/native
race、make lint、Rust 客户端 Clippy 通过。客户端 21 项单元及 19 项互通测试、
桌面 52 项测试通过；后者新增位置/no-op 解析和跨书签/会话/权限/源对象隔离。
产品只读复核修复了 pending 期间仍可从顶部打开频道管理的问题；共享入口与按钮
均已阻止该操作。回执不覆盖权威列表、旧会话/退出错误隔离等流程复核无剩余阻断。

make build-desktop build-server 完成本机 release 构建与严格签名检查，独立验收包为
desktop/dist/Resona-Channel-Review.app。最终 pending 入口修复后重跑桌面测试并重新
打包。未操作用户运行窗口或再次更新线上服务器；真实鼠标插入线命中、预览位置、
滚轮滚动、Escape/无效区域释放、拖动后点击和 Windows 实机仍需验收，不据纯函数
单测宣称这些实窗动作已通过。游戏负载和长期资源测量未执行。

### 授权远端排序版部署与基本验收

随后经用户授权，将排序工作树构建为 Linux amd64 镜像
resona-server:v0.1.5-channel-review.20261021 并部署到 tsukiyo。上传包 SHA-256
校验通过；停服后备份完整 data/access 并保留旧容器，沿用原密钥、密码环境和
9988/UDP。启动日志确认监听成功，复查容器持续运行且重启次数为零，私钥摘要未变；
备份目录为 0700，环境及数据归档为 0600，归档可完整读取。镜像标记 dirty=true，
属于验收构建，不是正式版本发布。

用户随后反馈基本使用无明显问题，并授权提交、合并 main 和推送；这不替代上述
逐项拖放边界、Windows、游戏负载及长期验收。

## 2026-10-21：Rust 音频 CI 检查修复

Actions 39594487460 在 main 的 a910e75 失败：macOS/Linux 使用 Rust 1.99.1，
新增 chunks_exact_to_as_chunks lint 被严格警告检查拒绝；Linux 还拒绝未使用的
凭据服务常量。Windows 在 Rust build.rs 中找不到写死路径的 pkg-config.exe，
尚未进入 Rust lint 或互通测试。本机原工具链为 1.96.0，不能据其通过推断新版
Clippy 或其他平台通过。

修复五处固定大小分块为数组分块，保持余项忽略与音频处理顺序；凭据常量仅编译
到支持的 macOS/Windows。Windows 工作流显式安装 MSYS2 pkgconf，使用 action
输出的安装目录，并检查可执行文件存在后写入后续步骤环境，保留严格警告检查。

本机 Rust 1.96.0 完整 audio 特性 Clippy（all-targets、-D warnings）、44 项单元
及 21 项真实 Go/Rust 回环互通测试通过；格式、差异、工作流 YAML 和各 Bash
步骤语法检查通过。测试使用临时服务和假设备，不访问线上服务或用户凭据。
首次沙箱测试因禁止绑定 UDP 失败，允许回环网络后完整通过。尝试安装与 CI 相同
的 1.99.1 因下载缓慢取消，默认工具链未更改；新版本 Clippy、Linux 和 Windows
执行仍待远端 CI 验证。本轮修复尚未提交、推送或重新触发 Actions。

## 2026-10-22：桌面重绘分支修正

在 fix/tsukiyo/desktop-20261021_gpu-idle 上保留有界事件批处理与按需帧追踪，
将额外的 JSON 快照、字段反射和差异列表改为比较实际接受的 VoiceState。
迟到代次/操作先过滤；隐藏电平只更新模型，前台设备试听电平变化立即重绘。
音频层已有约 100ms 发布间隔，因此删除 UI 的第二层节流，避免最终电平被丢弃，
不增加空闲定时器。重新激活窗口时显示最新状态。

55 项桌面测试通过，覆盖隐藏/可见电平、稳定的最终电平、忽略未知计数字段、
静音/忙碌/错误/发言者/操作变化及既有迟到操作验证。产品只读复核确认回执、
PTT、设置确认/取消、错误和退出路径无新的阻断问题，未操作用户运行窗口。
严格 Clippy 检查被既有 14 处 collapsible_if、items_after_test_module、
field_reassign_with_default 风格告警阻断；仅允许这三类后其余严格检查通过。
不将此结果描述为完整 Clippy 无告警。

性能工具工作流补入 GPU 脚本及 PowerShell 语法检查；采样结束刷新进程信息，
进程中途退出时拒绝结果。文档说明脚本入口、窗口状态采样时点与 CPU 统计口径。
工作流 YAML、Rust 格式与差异检查通过。本机没有 PowerShell/Windows GPU 环境，
脚本实际运行与等效工作负载的 GPU 降幅仍待 Windows 验收。

make build-desktop 完成 macOS release 构建，独立验收包为
desktop/dist/Resona-GPU-Review.app，严格签名验证通过。首次沙箱打包在系统
iconutil 生成图标时失败；经允许在沙箱外重跑后完成。未启动该包或替换运行中的
用户应用；实际设备试听、焦点/最小化切换仍待实窗验收。

### Windows 原生标题栏主题

同分支补齐 theme::apply 的 Windows DWM 深浅模式设置，启动、预览、应用、取消及
系统变化都复用同一入口。GPUI 0.2.2 在系统主题事件中原本先通知应用再覆盖标题栏；
最小补丁改为释放状态借用后，先设置系统默认再通知观察者，使显式偏好最后生效。
不改变系统 appearance 读数，不缓存跳过标题栏设置，保留跟随系统和原生窗口操作。
补丁来源及移除条件记录在 desktop/vendor/gpui/RESONA-PATCH.md。

55 项 macOS 桌面测试、格式与差异检查通过。复核中确认 Windows 0.61.3 的 BOOL
需从 windows::core 导入，已修正；最终只读流程复核无剩余阻断发现。本机只有
macOS Rust target，未验证 Windows 编译和实际标题栏效果，不能用 macOS 结果
替代。Windows 待验收：反向系统主题下启动已存主题；三模式预览/应用/取消；系统
主题变化时手动模式保持与 System 跟随；最大化、原生按钮和退出。

macOS cargo build --locked --release 通过；沿用上述三类既有风格告警的允许项后，
其余严格 Clippy 检查通过。未重新打包或操作运行窗口，Windows 专属代码仍待 CI。

### Windows 标题栏编译回归

Actions 39644671446 在 acba870 的 Windows 桌面测试编译阶段失败，E0308 指向
theme.rs 的 window.window_handle()。该写法优先调用 GPUI 的固有方法，返回
AnyWindowHandle，而非 raw-window-handle trait 的 Result。改为与现有应用图标
代码一致的 HasWindowHandle::window_handle(window)，显式选择原生句柄接口。
仅修正方法解析，不改变主题或窗口生命周期。原提交的 Rust core 与性能工具
Actions 已成功；Windows 修正仍须重跑原生构建确认。

修正后 55 项本地桌面测试、Rust 格式及差异检查通过；此结果不覆盖 Windows
专属代码，最终编译/打包以新提交的 Windows Actions 为准。

## 2026-10-22：用户页与 Unicode

用户页改为最多 400px、受可用宽度约束的独立纵向表单，滚动包装器不再吞掉
内部间距；登录/注册为主入口，Token 登录为次级入口。注册显式提交昵称，昵称
框回车按当前身份选择注册或修改资料；IME 组合期间不提交。账号回复的提示按
会话和页面代次隔离，迟到回复仍同步权威账号状态，但不覆盖后来打开的表单。
正常用户页隐藏偏好底栏，其他页草稿触发的放弃确认仍保留可见入口。

Go 全量 internal 与 server CLI 测试、accounts/server race、make lint 通过。
Rust 客户端 45 项单测及 23 项 Go/Rust 回环互通通过：覆盖注册中文昵称、改名后
再次登录、中文/肤色/ZWJ/旗帜 emoji 和组合附加符号的精确实时传输及近期历史。
桌面 55 项测试通过；Clippy 仅允许此前已有的三类风格告警，其余按 -D warnings
检查通过。没有新增字体、emoji 库、选择器或后台任务，保留既有 UTF-8 与长度限制。

macOS release 实窗确认紧凑登录/注册页、账号注册、中文昵称保存、成员列表同步，
以及组合 emoji 消息输入、发送与服务端回显。760×600 窗口下深浅色布局正常；
其他设置存在未应用草稿时，在用户页关闭仍显示放弃确认，放弃后关闭并恢复原主题。
最终签名 release 包包含该关闭修正，严格签名验证通过。原生自动化的 typeText 未注入非
ASCII 文本，改用粘贴完成实测；粘贴工具虽报等待超时，窗口确认内容已正确插入。
Windows 字体/IME、实际系统字符面板和游戏等负载性能尚未验收。

预发 server 已部署身份体系分支，保留频道、历史和私有连接配置，升级前完整备份
data/access；旧设备权限按约定一次性取消，新 owner 认领码仅保存在私有文件。
新版容器运行、版本及管理页 HTTP 200 已确认，管理端口仍仅绑定回环地址。

### 注册反馈与声音页布局

本地注册校验明确提示用户名、昵称及密码规则；密码要求仍为 12–256 UTF-8 字节，
不降低服务端策略。校验在清空秘密和异步请求前执行，失败保留表单；UI 与 Core
复用 AccountRequest::validate。登录、注册和改密码不再共用误导性的长度提示。
取消登录/注册标签，在同一表单底部用次级文字切换，标题随模式更新，注册成功
显示服务端确认的账号资料。Token 模式聚焦可见输入，切换仍清秘密并隔离旧回复。

声音页将滚动包装器与内容 flex/gap 分开，激活按钮可换行，分组间距统一；
不改变音频处理与草稿应用逻辑。独立本地 server、760×600 macOS release 实窗
确认登录/注册切换、11 字节短密码报错且输入保留、修正后回车注册成功、资料页及
退出账号断开；声音页自动开麦选项与模式按钮不再相贴。最终包签名验证通过。
客户端 46 项单测、23 项 Go/Rust 互通、桌面 55 项测试通过，沿用上文三类既有
Clippy 告警允许项后其余严格检查通过。最终焦点微调经 release 编译及只读复核。
本轮服务端无新增修改，已确认 tsukiyo 配套镜像运行且管理页 HTTP 200，不重复部署。
Windows 实窗与本轮深色页面效果仍待验收；没有新增依赖、后台任务或更改用户音频偏好。

### 8 字节密码与聊天表情选择器

按后续反馈将注册、新密码及离线重置的下限统一改为 8 UTF-8 字节，上限仍为
256 字节。Go 真实 SQLite 测试覆盖 7 字节拒绝、8 字节注册/登录/改密/重置；
Rust 表单测试覆盖同一边界及多字节密码。保留密码散列、盐和并发限额。

发送按钮左侧增加本地 64 个常用 emoji 的分类面板，使用现有 Popover 和系统
字体，无新依赖或后台任务。按 session、频道和 chat epoch 隔离选择范围；
只插入光标或替换选区，不自动发送。工具栏遮挡底层 Input 的鼠标命中，避免
点击选择器时将光标移到末尾；表情按钮固定 28×28，分类内容独立于滚动包装器。

Go internal 全量、accounts race、make lint 通过；客户端 46 项单测及 23 项
Go/Rust 互通、桌面 55 项测试通过。严格 Clippy 沿用既有三类风格告警允许项。
最终 macOS 签名 release 实窗在 760×600 下确认：8 列完整显示、面板滚动、
选区替换、光标中间插入、点击发送及真实服务端回显、Esc 关闭后继续输入、
外部点击和切频道关闭面板。产品流程只读复核通过。

预发 tsukiyo 已升级 v0.1.5-identity.20261022.2，升级前备份 data/access，
保留账号和连接配置；容器 running、重启次数 0、版本和后台 HTTP 200 已确认。
管理 TCP 9989 仍只绑定回环，语音 UDP 9988 映射不变，未重置 owner。
Windows、深色选择器实窗、原生 IME 与完整键盘遍历仍待人工验收。

### 跨平台 CI 修复与重连测试稳定性

修复分支将账号数据库绝对路径编码为无 authority 的 file URI，避免 Windows
盘符被 SQLite 当作主机名；Windows 不再以 POSIX mode 位判断文件权限。账号
密码校验按严格 Clippy 建议合并条件，未修改校验规则。

重连拨号前仍在状态锁内消费竞速到达的立即重试信号；删除拨号后的冗余清理，
因为 RetryConnection 在拨号期间拒绝请求。原 50/75/10ms 墙钟窗口测试改为
testing/synctest 虚拟时间及通道握手，精确覆盖定时器先到、点击随后到；另以
完整 Service 验证等待时可唤醒、拨号时拒绝、失败后的下一轮仍遵守退避时间。
这两项测试在本机 race 模式连续 100 次通过，无需真实网络或音频设备。
Go internal 全量、accounts/client 完整 race、make lint 和客户端 audio/all-targets
严格 Clippy（不豁免告警）通过；跨平台构建将按新提交重新运行。

首次重跑发现本机 Go 1.27 的 synctest.Sleep 不在项目 Go 1.26 标准库中；改为
等价的 time.Sleep + synctest.Wait，仍只推进 bubble 内的虚拟时间。使用
GOTOOLCHAIN=go1.26.0 重跑 internal 全量和两项定向 race（100 次）均通过，
随后再次推送触发跨平台构建。

### 本地账号登录保持与续期（2026-10-23）

沿用服务端已有的 30 天轮换 token，不增加认证协议或后台续期定时器。
本地 token 按服务器书签、地址和固定公钥隔离，保存在 macOS 钥匙串或 Windows
凭据管理器；正常退出保留，显式退出账号、删除书签或改变连接目标时清除。
普通配置、诊断和公开状态不保存 token。独立有界 FIFO 串行执行凭据操作，
账号获准后系统存储失败只显示警告，未响应的系统授权不阻塞控制、语音或退出。

Go 1.26 独立本地服务端互通验证覆盖：全新 Core 重建后的身份恢复及 token 轮换、
改密后的恢复、退出账号后的游客连接、失效凭据清除、拒绝读取/写入的显式反馈、
存储阻塞期间继续聊天与断开、删除书签后不被迟到写入复活，以及并发凭据查询时
有界退出。最终客户端 51 项单测、30 项互通测试及 audio/all-targets 严格 Clippy
通过；桌面 56 项单测通过，最后生命周期调整由完整客户端测试和桌面 release
构建覆盖。cargo fmt 与 git diff --check 通过，产品流程只读复核无阻塞问题。

本机 desktop/dist/Resona.app 已重新构建并通过严格签名校验，维护者简验反馈无明显
问题。真实 macOS 授权各分支及 Windows 重启恢复、原生凭据仍待完整验收。
tsukiyo 当前服务端与 main 服务端代码一致，本轮仅改客户端，
未重复重启；容器版本 v0.2.2-migration-review.20261023、管理页 HTTP 200 已确认。

发布准备：版本及客户端、桌面、试听机器人的相关锁文件同步为 v0.2.3；删除
退役 ui-prototype 最后说明，保留架构决策和验证记录。最终版本重新通过 51 项
客户端单测、30 项串行真实互通和 56 项桌面单测；默认配置及 audio/all-targets
严格 Clippy、格式检查、更新清单签名/发布保护测试通过。尚未响应的系统授权
仍不能保证凭据落盘；不将简验等同于 Windows 游戏负载或所有平台授权验收。
最终 v0.2.3 macOS release 重新打包完成，Info.plist 与深度严格签名校验通过。

### 试听机器人新版 Clippy 修复（2026-10-23）

main 18987bc 的 Windows/macOS 打包成功，但 Rust client core 三平台流水线
均在试听机器人严格 Clippy 阶段失败。CI Rust 1.99 新增
chunks_exact_to_as_chunks 检查，本机 Rust 1.96 尚未报告；将两处固定大小切片
分组改用 as_chunks，直接复制固定数组，保留输入对齐、长度和样本校验。
本机机器人严格 Clippy、格式检查、3 项单测及 2 项真实 UDP 互通通过，
包含原生 DSP 试听源和三个 AGC 说话人的轮播；显式导出采样测试仍按设计忽略。
修复提交 04c9650 的 [三平台 Actions](https://github.com/tsukiyoz/resona/actions/runs/39891660029)
已全部通过：Linux 3m6s、macOS 2m52s、Windows 6m10s，包含客户端、真实互通与
试听机器人检查；发布包、Windows 原生凭据及实际音频设备验收仍分别记录。

### 发言状态可辨识性（2026-10-23）

分支 feature/tsukiyo/desktop-20261023_speaking-indicator 将成员列表与底部本人
昵称旁的活动指示统一为固定槽位中的灰/蓝圆点，发言时提亮昵称；静音斜线优先，
长昵称截断。沿用实际发送/播放与核心约 300ms 释放，没有新音频任务、定时器或
动画。已知远端静音及本机个人静音不保留旧活动高亮，退出时两处停止活动显示。

桌面 release 单测 58 项通过，包含静音状态拒绝残留活动及原有会话/频道/语音状态
回归；产品流程代码审查未发现阻塞问题。make build-desktop 完成，Info.plist 和
应用深度严格签名校验通过；git diff --check 通过。发言指示阶段仅修改桌面显示，
未改协议、服务端或频道/历史加载流程；后续加载改动见下一节。真实窗口深浅主题、最小宽度与多人发言/PTT
以及 Windows 实机仍待维护者验收，不据此宣称游戏性能测量完成。

### 频道进入与首批历史呈现（2026-10-23）

沿用同一分支，网络与语音继续异步运行；首次关注资源就绪后一次呈现频道、详情
与文字区域，首批历史在已有有界缓存中收齐后一次投影，不逐条刷新。不增加正文
缓存，不改变 20 条分页、实时去重或频道隔离。加载仅在前台超过 300ms 后提示，
采用一次性可取消任务；范围改变、完成、失败、后台和退出撤销，错误立即呈现。
已有资源的普通刷新仍保持静默，历史失败不阻断语音或退出。

客户端 audio/release 单测 54 项、桌面 release 单测 59 项通过；独立本地 Go 服务端
串行互通 30 项通过，覆盖频道切换、历史分页与实时合并、资源关注、多人语音及
凭据恢复。并行互通初跑 29 项通过，失效 token 清理断言失败：连接失败状态可先于
异步凭据队列完成删除；串行重跑通过。此处记录该时序敏感测试，未将串行成功等同于
已经修复凭据完成通知，也未改动账号生命周期。产品流程代码复核未发现阻塞问题。
客户端 audio/all-targets 严格 Clippy 通过。make build-desktop 完成，新版
desktop/dist/Resona.app 的 Info.plist 与应用深度严格签名校验通过；本轮无服务端
或协议改动，未重启预发服务。git diff --check 通过。
真实窗口的快慢网络、错误与取消路径，以及 Windows 实机仍待维护者验收。

### 服务器连接入口收敛（2026-10-23）

同一分支继续将连接呈现合并到服务器页中央原按钮：准备音频偏好、读取凭据、
握手重试与必要资源同步统一为“正在连接…”，去掉顶部覆盖卡、次数及重复的
取消入口。必要资源就绪才挂载频道、详情与文字；失败在原处提供原因和处理入口，
普通资源刷新保持静默。在线断线后的慢速自动恢复仍保留，连续失败在入口明确
提示连接中断，允许重新连接或取消。语音、协议及核心重试策略没有改动。

准备请求和连接回执按服务器与 connect_revision 过滤；取消废弃代次，已开始的
核心连接沿用断开清理。迟到的成功/失败不能再打开密码框，过期请求不继续禁用
操作或压低发言指示，回执仍走公共重绘收尾。不新增连接状态机、缓存或轮询，
移除资源延迟提示任务，只保留频道历史已有的前台一次性延迟提示。

最终桌面 release 单测 61 项通过，覆盖入口在准备、连接、恢复、资源等待/失败、
可用及静默刷新间的选择、其他书签隔离、取消代次与旧请求不阻挡操作；既有优先
断开和旧排队意图失效测试同时通过。产品流程复核提出的迟到偏好回复漏重绘已修复，
再次复核无新问题。真实窗口排版、长错误、慢网络和 Windows 实机仍待维护者验收。
make build-desktop 完成，最新版 desktop/dist/Resona.app 的 Info.plist 和深度严格
签名校验通过；cargo fmt 与 git diff --check 通过。未提交或推送，未重启预发服务。

### 资源兜底按无更新时间计时（2026-10-23）

将固定的同步完成后五分钟改为连续五分钟无有效资源更新才同步。复用既有生命周期
通知与已校验的资源快照：当前前台兴趣范围的递增 revision 重置截止时间，聊天、
凭据状态和重复 revision 不重置；不增加观察任务、订阅或轮询。主动同步成功也
重新计时；会话或兴趣变化清空旧时钟，后台、失败和同步进行中不额外启动兜底。
定时唤醒先检查最新快照，避免已收到推送时仍发起多余请求。

客户端 audio/release 单测 56 项通过，新增确定性时间边界检查覆盖有效推送续期、
重复/旧 revision、到期时新推送续期、同版本同步成功以及后台/错误范围/会话替换。
临时本地 Go 服务端的资源观察互通通过，覆盖前后台兴趣切换及断开清理；只读
流程复核无阻塞问题。未通过真实五分钟墙钟等待或游戏负载测量推断性能收益。
客户端 audio/all-targets 严格 Clippy、cargo fmt 与 git diff --check 通过；
make build-desktop 完成，新版应用 Info.plist 与深度严格签名校验通过。

### macOS 构建告警修复（2026-10-28）

分支 `fix/tsukiyo/build-20261028_warnings` 保持产品版本 0.2.3。Opus 1.6.1
在 Darwin ARM 没有运行时 CPU 检测，默认 MAY_HAVE_NEON 却要求该检测；Apple
Silicon 构建改为固定 NEON，并补齐相应头文件声明宏，保留 SIMD 源码和硬化选项。
首次完整重编译发现只关闭 MAY_HAVE_NEON 会缺失函数声明，补齐宏后重编译通过。
按 CMake 目标选择，不将构建主机架构强加给显式 Intel 目标。make test-build 增加
八组配置检查，覆盖 ARM64、Intel、显式架构、其他平台及关闭 intrinsics 的情况。

GPUI grid 浮点字面量明确为 f32。桌面依赖图以本地源码补丁修正 block 0.1.6 的
uninhabited_static/隐式 C ABI，以及 proc-macro-error2 2.0.1 的私有 extern crate
公开重导出；没有屏蔽 lint、修改 Cargo registry 或升级依赖版本。原始包校验和、
来源、许可与补丁范围记录于 desktop/vendor/README.md。

最终 make build 成功，Opus 不再输出 ERRORRuntime 提示，Rust 无原有浮点、ABI
和未来不兼容告警；macOS 应用 Info.plist、深度严格签名校验及 Go server 构建通过。
客户端 audio/release 单测 56 项、桌面 release 单测 61 项、新增 macOS Block
捕获/复制/克隆调用测试 1 项、make test-build、cargo fmt --check 和
git diff --check 通过。未做 Windows/Intel 原生编译、实机连麦或性能测量，未部署
预发服务器，未提交或推送。

### Actions 按需构建（2026-10-30）

分支推送与 PR 不再自动触发桌面、Rust 客户端和性能工具工作流；所有工作流保留
workflow_dispatch。版本 tag 仍由 release.yml 验证版本并调用 Windows/macOS
打包，原有发布清单签名和发布前校验不变。macOS 仅 tag 发布或手动 main/tag
构建导入签名身份，手动普通分支使用 ad-hoc 签名。贡献与下载说明已同步。

actionlint v1.7.9 检查所有工作流通过；YAML 解析及触发器检查确认只有发布工作流
具有 push（仅 v* tag），两个平台仍可被发布工作流调用，所有工作流可手动启动。
make test-build 与 git diff --check 通过。本次未触发远端构建或创建 tag。

### 聊天消息日期（2026-10-30）

分支 fix/tsukiyo/desktop-20261030_chat-timestamps 从合并构建修复和按需 Actions
后的本地 main 创建。最终交互按客户端本地日期分组：已加载首条及日期变化处显示
固定 28px 的日期分隔区，中央 YYYY-MM-DD、两侧各 32px 细线；每条消息常驻
HH:mm:ss、昵称、正文，58px 时间列及 4px 间距，保留淡色行悬停。整个 item 右侧
预留 20px，避开 16px 覆盖式滚动条。日期线位于消息 hover/右键区域之外。
非法或空时间显示“时间未知”，日期分组为“日期未知”。列表仍按消息索引，比较
前驱日期边界以失效受影响行高，并补偿可见锚点内日期线的高度差，不新增同步
请求、业务状态或后台任务。产品复核未发现代码层面阻塞问题。

桌面 release 单测 65 项、macOS Block 兼容测试 1 项通过；时区测试覆盖跨天、
跨年、显式偏移、小数秒和非法输入；日期分组及滚动测试覆盖同日历史插入、日期线
移除/恢复、日期线内锚点钳零及前驱时间修正。cargo fmt --check 与 git diff --check
通过。按天分组版本 make build-desktop 在沙箱外完成，Info.plist 与深度严格签名
校验通过，产物为 desktop/dist/Resona.app。维护者已反馈新版实窗验收无问题，
授权提交并合入本地 main；未单独记录最小窗口/深浅主题/多链接组合或 Windows
验收。本次无服务端变更，未部署预发服务器，未推送或打 tag。
