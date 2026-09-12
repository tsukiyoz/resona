# 架构决策记录

| 编号 | 决策 | 状态 |
| --- | --- | --- |
| [0001](0001-monorepo.md) | 单仓库与单 Go module 起步 | Accepted |
| [0002](0002-desktop-gui.md) | Wails v2 + React/TypeScript GUI | Accepted |
| [0003](0003-ts3-compatibility.md) | TS3 客户端优先，协议库先验证后采用 | Accepted |
| [0004](0004-extensions.md) | 内置扩展先行，外部插件契约延后稳定 | Accepted |
| [0005](0005-read-only-ts3-session.md) | 固定协议快照、只读 TS3 会话和本地身份 | Accepted |
| [0006](0006-channel-navigation.md) | 服务器确认驱动的频道切换与取消 | Accepted |
| [0007](0007-local-notification-sounds.md) | 协议事件驱动的本地提示音 | Accepted |
| [0008](0008-server-navigation-and-credentials.md) | 成员可见范围、服务器进入与系统钥匙串凭据 | Accepted |
| [0009](0009-channel-text-and-presentation.md) | 协议适配边界、频道文字与真实频道呈现 | Accepted |
| [0010](0010-local-icon-cache.md) | 有限内存、本地图标缓存与网络回退 | Accepted |
| [0011](0011-native-gpui.md) | GPUI 原生实验与 Go 子进程核心 | Accepted（实验） |
| [0012](0012-voice-engine.md) | Go 语音与显式设备生命周期 | Accepted |
| [0013](0013-resource-references-and-details.md) | 独立图标引用与按需详情 | Accepted |
| [0014](0014-audio-controls-and-shutdown.md) | 激活与音频处理、本地试听、有界退出 | Accepted（设备及平台验收待完成） |
| [0015](0015-gui-maintenance-convergence.md) | GUI 开发收敛与冻结 Wails 基线 | Accepted（可逆维护策略） |
| [0018](0018-native-quic.md) | 原生 QUIC 服务端与可选客户端适配器 | Accepted（实验） |
| [0019](0019-noise-udp.md) | Noise UDP 主实验与显式 QUIC 对照 | Accepted（实验） |

状态可为 Proposed、Accepted、Superseded、Rejected。新增重大决定使用递增编号，注明日期和替代关系。
