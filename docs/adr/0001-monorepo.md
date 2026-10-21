# ADR-0001：单仓库

状态：Accepted，2026-10-21 更新当前布局。

Go 服务端及协议在根 Go module 内，Rust 客户端核心为 client/Cargo.toml，GPUI 为 desktop/Cargo.toml。产品入口为 cmd/resona-server 和 desktop，通过同一提交构建配套两端；客户端不启动 Go 子进程。具体迁移决策见 ADR-0032。

未承诺外部稳定性的 Go 实现放 internal。先稳定设备、生命周期及嵌入契约，再为真实 SDK 消费者拆 module；不按平台名称提前拆仓库。独立团队或发布节奏形成后重新评估。
