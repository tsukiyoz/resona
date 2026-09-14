# ADR-0001：单仓库

状态：Accepted，2026-09-15 更新当前布局。

客户端核心、服务端和协议在一个 Go module 内；Rust GPUI 使用 desktop/Cargo.toml。入口为 cmd/resona-core、cmd/resona-server 和 desktop，产品通过同一提交构建配套两端。

未承诺外部稳定性的 Go 实现放 internal。先稳定设备、生命周期及嵌入契约，再为真实 SDK 消费者拆 module；不按平台名称提前拆仓库。独立团队或发布节奏形成后重新评估。
