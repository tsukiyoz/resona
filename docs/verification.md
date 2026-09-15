# 当前验证记录

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
