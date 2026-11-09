# ADR-0030: 分发方式与更新

- 状态：Accepted；Windows 安装/重启仍须实机验收，Authenticode 延后
- 日期：2026-10-15
- 取代：ADR-0029 的便携 ZIP 自动整目录替换方案

同一应用版本使用一个不可变 annotated tag，包类型不编码进版本号。
`resona-package.json` 记录 schema、product、platform、version、distribution。
当前 Windows ZIP、macOS .app 包标记 portable；Core 校验元数据后解释更新能力，
GUI 不按操作系统或文件名推断。元数据缺失、未知或与当前构建不符时关闭更新能力。

portable 仅手动检查 schema 1 的 Ed25519 签名清单。发现严格更新的正式版本且具备当前平台
portable 资产后，提供对应版本的源仓库 Release 页。链接来自已验证的清单，
并限定为本仓库对应版本页面。用户自行下载替换，不在应用内下载、解压、安装或
退出更新。旧 Core 复制成 helper 的 ZIP 替换器及 CLI 入口删除。便携版拒绝安装命令。
检查只有一个有界 worker，无缓存写入和闲置轮询。

Windows installer 使用 Velopack Rust/CLI 1.2.161，上游提交
`92d6a1c91716729d449034df5c50307dcce39493`。核对了 locator、startup hooks、
`wait_exit_then_apply_updates`、打包参数及 nupkg 布局；不是只根据 README 选型。
仅真实 Resona 安装目录、匹配版本且非 Velopack portable 布局才启用安装能力。
Velopack 负责当前用户安装、快捷方式、文件替换及重启；不再维护自己的 PowerShell 替换器。
首次只支持完整包，不实现 delta 或后台自动下载；启动关闭 SDK 自动应用和文件日志。

安装版使用独立 `manifest-windows-installer.json`（schema 2，仅 windows/amd64 installer），
旧 `manifest.json` 的 schema 1 portable 语义保持不变。签名校验后显式下载，限制大小、
时限及 SHA-256，用户确认重启后重新校验缓存，将带文件所有权的 handoff 移出 core，
等实际 client close 完成后才启动 helper 等待当前 PID 退出，GUI 随后退出。
更新不使用普通退出的七秒强制退出兜底，应用级退出也等待同一清理流程。
取消/失败不会授予 ready 能力；启动 helper 失败恢复当前版本运行时并保持断开，
用户可重新连接或重新下载。文件替换失败由
Velopack 显示恢复交互，不承诺新版逻辑有 bug 时自动回滚。

两种 Windows 包在同一 Release 发布，不新增 tag。当前明确输出 unsigned 安装器，
用户已决定本期延后 Authenticode；Ed25519 不是 Windows 发布者签名。

偏好、书签、身份与凭据仍位于产品目录外，更新行为不修改它们。
发布使用 `tsukiyoz/resona`，不另外创建发布仓库。签名密钥通过发布 Secret，
客户端只内置公钥和公开 feed；平台签名与 Apple 公证仍是独立要求。
