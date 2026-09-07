# TeamSpeak 3 协议候选评估

评估日期：2026-09-07。

## 结论

**建议仅将 `HoneyBBQ/teamspeak-go` 作为实验适配器候选，尚未引入依赖；暂缓将其作为完整语音 GUI 或长期运行客户端的已验证基础。** 本轮范围仅为 M0，候选评估不代表已选定协议实现。它实现普通 TS3 客户端 UDP 连接路径，提供文字、频道查询、身份和原始 Opus 发送接口。然而，上层客户端直接丢弃收到的语音包，不能据此承诺双向语音。

本次先进行了有限源码评估；随后用户提供测试服务器，独立探针完成一次握手连接，频道查询收到 2568 后退出，详见 [验证记录](verification.md)。没有执行音乐播放、语音接收、官方客户端互通或断线重连测试，也未运行上游测试套件、量化覆盖率或核验 CI 成功记录。下文的“有测试”只表示检查到了相应测试源码。

## 版本与证据范围

- 上游：[HoneyBBQ/teamspeak-go](https://github.com/HoneyBBQ/teamspeak-go)。
- 实际下载的 HEAD：`a334def898f4d9c518a1434a9b9e9f889dcae954`。
- Git 提交时间：`2026-08-24T22:24:57+08:00`。提交说明为依赖更新 PR 的合并，不能据此推断协议功能近期完成验证。
- [`go.mod`](https://github.com/HoneyBBQ/teamspeak-go/blob/a334def898f4d9c518a1434a9b9e9f889dcae954/go.mod#L1) 声明模块路径 `github.com/honeybbq/teamspeak-go`、`go 1.26.0`。导入应使用模块声明中的小写路径；本评估未选择发布标签。
- 深入检查六个主要文件：`client.go`、`commands.go`、`api.go`、`crypto/crypt.go`、`transport/handler.go`、`integration_test.go`；另抽查相关单元测试片段及检索公开接口。
- 证据等级：固定提交源码可确认接口与直接控制流；测试源码可确认测试意图和断言；真实服务端只完成验证记录中的有限探针。以下判断严格区分三者。

## 能力与缺口

### 接收语音：目前是明确阻断项

底层 [`PacketHandler.handlePacketQueue`](https://github.com/HoneyBBQ/teamspeak-go/blob/a334def898f4d9c518a1434a9b9e9f889dcae954/transport/handler.go#L344) 将非命令包交给 `OnPacket`，但 [`Client.handlePacket`](https://github.com/HoneyBBQ/teamspeak-go/blob/a334def898f4d9c518a1434a9b9e9f889dcae954/commands.go#L112) 对 `PacketTypeVoice` 和 `PacketTypeVoiceWhisper` 直接返回。`Client` 的 handler 是私有字段；现有事件中间件不会绕过该返回点提供语音。

公共客户端层尚缺语音事件出口。完整语音 GUI 需要补充带发送者、codec、序号和数据生命周期约定的接收接口，以及 Opus 解码、每个说话者的抖动缓冲、丢包处理、混音和播放。可向上游贡献接口或维护明确的补丁，避免业务代码依赖私有字段。

### 发送语音：有原始帧接口，没有完整音频流水线

[`Client.SendVoice`](https://github.com/HoneyBBQ/teamspeak-go/blob/a334def898f4d9c518a1434a9b9e9f889dcae954/api.go#L51) 接受已编码的原始 Opus 帧，注释中 codec `4` 为 voice、`5` 为 music。它不接收 MP3 文件，也不负责采集、解码音乐文件、Opus 编码或按帧时钟发送。

[`SendVoicePacket`](https://github.com/HoneyBBQ/teamspeak-go/blob/a334def898f4d9c518a1434a9b9e9f889dcae954/transport/handler.go#L876) 构造序号、codec 和 payload，始终设置 `PacketFlagUnencrypted` 并使用 `FakeSignature`。这能证明现有发送路径的选择，不能证明它适配要求语音加密的频道。发送方法未检查连接状态，最终直接访问 `h.conn.Write`，连接前调用存在 nil 连接风险。

Resona 应在适配边界限制有效连接状态、帧大小与 codec，并由独立音频模块管理输入、编码和发送时序。发送成功只表示本地写入成功，不表示其他客户端确实听到声音。

### Identity：可序列化，持久化由 Resona 负责

[`crypto.Identity`](https://github.com/HoneyBBQ/teamspeak-go/blob/a334def898f4d9c518a1434a9b9e9f889dcae954/crypto/crypt.go#L45) 包含 P-256 私钥和安全级别搜索 offset；`String()`、`IdentityFromString()` 提供私钥标量 Base64 与 offset 的往返序列化。`PublicKeyBase64()` 与 `GetUidFromPublicKey()` 可计算 UID。重建连接时 [`resetForConnectLocked`](https://github.com/HoneyBBQ/teamspeak-go/blob/a334def898f4d9c518a1434a9b9e9f889dcae954/client.go#L208) 复用原 identity。

这不是自动落盘，也未在本次范围内证明能直接导入官方客户端导出的 identity 文件。Resona 应显式保存和加载身份文件，首次创建后重用，限制文件权限并避免记录包含私钥的序列化值。安全级别提升接口 `UpgradeToLevel` 支持 context；`GenerateIdentity` 的搜索循环没有 context，初始化流程应避免无界等待。

### 连接与断线：有事件，失败和重试需要适配层约束

[`Client.Connect`](https://github.com/HoneyBBQ/teamspeak-go/blob/a334def898f4d9c518a1434a9b9e9f889dcae954/client.go#L159) 先设置 `StatusConnecting`，再返回底层连接结果。如果地址解析或 UDP 建连在后台循环启动前失败，这条路径没有将状态恢复为 disconnected；直接再次调用 Connect 会被拒绝。适配层应在失败后清理，优先为每次重试创建新 Client，并复用持久化 identity。

[`Disconnect` 与 `handleConnectionClosed`](https://github.com/HoneyBBQ/teamspeak-go/blob/a334def898f4d9c518a1434a9b9e9f889dcae954/client.go#L179) 会更新状态并发出断线回调，底层接收循环也有关闭通知。不能因此推断已有可靠自动重连策略。Resona 仍需明确连接超时、退避、用户主动退出、被踢出、旧连接迟到事件和正在等待命令的处理。回调以 goroutine 调用，业务事件应集中进入有序状态处理流程。

### 命令与文字：结果语义必须收紧

[`commandTracker.collect`](https://github.com/HoneyBBQ/teamspeak-go/blob/a334def898f4d9c518a1434a9b9e9f889dcae954/commands.go#L60) 将没有直接关联标识的数据行归入最大的 pending return_code。若同时存在多个请求，前一条响应行可能被记到后一条请求；仅给完成消息添加 return_code 并不能消除该风险。当前实验适配器应串行执行查询命令。

[`SendTextMessage`](https://github.com/HoneyBBQ/teamspeak-go/blob/a334def898f4d9c518a1434a9b9e9f889dcae954/api.go#L16) 使用 `SendCommandNoWait`，返回 nil 不表示服务器接受或对方收到。需要确认发送结果的业务应通过适配器使用带服务端响应的命令路径，或将状态准确标记为“已提交”。

## 测试实际证明了什么

- [`integration_test.go`](https://github.com/HoneyBBQ/teamspeak-go/blob/a334def898f4d9c518a1434a9b9e9f889dcae954/integration_test.go#L1) 受 `integration` build tag 控制；`TEAMSPEAK_ADDR` 未设置会跳过。部分查询权限错误也会跳过，因此默认 `go test ./...` 或未检查 skip 的集成测试不能作为真实互通通过证据。
- 集成用例包含连接、主动断开、用户和频道列表、默认频道、两客户端私聊通知及 poke。主动断开测试主要验证回调触发，未覆盖断网后可靠恢复；其对 `Disconnect` 返回错误仅记录为 non-fatal。
- [`transport/handler_test.go`](https://github.com/HoneyBBQ/teamspeak-go/blob/a334def898f4d9c518a1434a9b9e9f889dcae954/transport/handler_test.go#L424) 的语音发送测试检查包头、codec 和递增序号，使用任意字节样本。它们不证明 Opus 帧可被真实 TS3 客户端解码，也不验证频道加密策略或听感。
- [`crypto/crypto_test.go`](https://github.com/HoneyBBQ/teamspeak-go/blob/a334def898f4d9c518a1434a9b9e9f889dcae954/crypto/crypto_test.go#L141) 存在 identity 往返、安全级别和升级取消测试；这不同于跨进程持久化后保持真实服务器 UID/权限的验收。
- 检索到的集成用例没有语音收发、音频连续播放、网络抖动、被踢后状态恢复或并发查询响应隔离验收。本次没有执行这些测试，也没有宣称上游整体覆盖率。

## 采用前的验证 Gate

1. **协议基础 Gate：** 固定上述提交并验证 Go 1.26 工具链可构建；在授权的普通 TS3 服务器上以普通客户端身份连接、进入指定频道、收发双向文字并获取服务端错误。记录服务器版本、权限、客户端版本及所有 skipped 用例。
2. **身份与生命周期 Gate：** 重启 Resona 后保持相同 UID；身份文件不可读、连接失败、握手超时、被踢、网络中断和重连都能进入明确状态；没有残留连接、重复回调或无界重试。
3. **发送音频 Gate：** 向官方 TS3 客户端发送确定的短音频，验证实际解码、时钟节奏、停止/恢复、codec 4/5 和语音加密频道。未通过时只开放文字实验功能，不把 SendVoice 命名为完整播放器。
4. **接收音频 Gate：** 首先补齐公共接收接口，然后验证普通语音和 whisper 的发送者归属、Opus 解码、多说话者混音、丢包/乱序与播放。通过前不得承诺完整 GUI 语音客户端。
5. **并发与运行 Gate：** 将命令串行化或修复关联逻辑后做交错响应验证；执行与适配器相关的 race 检查，并在断线重试和持续收发场景检查资源回收。音频压力与服务端实现留作独立后续评估。

Resona 的业务模型、GUI 和 identity 存储接口应独立于候选库，协议包类型留在 adapter 内。当前结论不扩展到 TS3 兼容服务端可行性，也不代表对上游密码学实现进行了完整安全审计。
