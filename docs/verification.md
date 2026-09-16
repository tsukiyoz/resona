# 当前验证记录

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
