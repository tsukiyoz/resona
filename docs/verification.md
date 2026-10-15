# 当前验证记录

## 2026-09-17 主题应用图标

- 以原有音柱轮廓生成浅/深 PNG 和 ICO；静态 `build/appicon.png`、Windows resource 1 与 SVG 默认白色，resource 2 为深色运行时图标。运行时 PNG 为 256px，应用内及原生图标对象缓存，切换不写安装包、不增加定时器。
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
