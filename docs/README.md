# Resona 文档

当前产品为独立的 GPUI 桌面、Go 客户端核心和 Go 语音转发服务，唯一传输为 Noise UDP。

| 文档 | 内容 |
| --- | --- |
| [路线图](roadmap.md) | 当前能力与后续优先级 |
| [架构](architecture.md) | 进程边界、资源与语音路径 |
| [产品](product.md) | 工作流与体验验收标准 |
| [服务端](server.md) | 本地运行、容器与所有者认领 |
| [控制与语音协议](native-protocol.md) | Protobuf、语音包和身份 |
| [Noise 传输](noise-protocol.md) | 握手、加密、可靠控制与换钥 |
| [验收清单](native-acceptance.md) | 自动化与设备测试边界 |
| [验证记录](verification.md) | 当前开发版验证结果 |
| [Windows 构建](windows-build.md) | 工具链及打包 |
| [设计决策](adr/README.md) | 当前维护的决策 |
| [性能工具](../tools/desktop-perf/README.md) | 进程采集与 CSV 分析 |

文档只保留当前架构和仍适用的决策。用户已授权清理过时协议说明及历史对比。实现、自动化通过、设备体验与正式发布分别记录；不以本地短测推断公网或游戏性能。
