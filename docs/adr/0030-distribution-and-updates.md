# ADR-0030: 分发方式与更新

- 状态：Accepted（分发识别和便携手动更新）；Pending（安装包、安装器升级）
- 日期：2026-10-15
- 取代：ADR-0029 的便携 ZIP 自动整目录替换方案

同一应用版本使用一个不可变 annotated tag，包类型不编码进版本号。
`resona-package.json` 记录 schema、product、platform、version、distribution。
当前 Windows ZIP、macOS .app 包标记 portable；Core 校验元数据后解释更新能力，
GUI 不按操作系统或文件名推断。元数据缺失、未知或与当前构建不符时关闭更新能力。

portable 仅手动检查 Ed25519 签名清单。发现严格更新的正式版本且具备当前平台
portable 资产后，提供对应版本的源仓库 Release 页。链接来自已验证的清单，
并限定为本仓库对应版本页面。用户自行下载替换，不在应用内下载、解压、安装或
退出更新。旧 Core 复制成 helper 的 ZIP 替换器及 CLI 入口删除；旧 IPC 安装命令
保留明确拒绝，不让旧 GUI 意外执行。检查只有一个有界 worker，无缓存写入和闲置轮询。

installer 是另一种已知包类型，必须由真实安装器负责升级，而非把 ZIP 改标记。
在安装器产物、分发维度的签名资产、下载校验、退出交接、失败恢复和重启验收完成
前，不提供安装能力。当前不产出 installer 包，其元数据被识别后明确报告后端未接通。
今后两种包在同一个 Release 中作为独立资产发布，不新增版本 tag。

偏好、书签、身份与凭据仍位于产品目录外，更新行为不修改它们。
发布使用 `tsukiyoz/resona`，不另外创建发布仓库。签名密钥通过发布 Secret，
客户端只内置公钥和公开 feed；平台签名与 Apple 公证仍是独立要求。
