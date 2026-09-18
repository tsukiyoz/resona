# 3A 实现与候选方案审查

日期：2026-09-18。结论针对当前实现和本机合成音试听，不是跨设备音质排名。

## 当前实现

- 发送 AEC → ANS → 输入增益 → Opus；接收 Opus → 每成员独立 AGC → 个人/全局增益 → 混音限幅。界面选项来自 Core 能力响应。
- SpeexDSP 中等 ANS 配置为噪声抑制 -20 dB；WebRTC 中等映射 Moderate，当前 bundled 源码对应 k12dB。这些是算法控制参数，不保证实际全频段固定衰减。相同档位文字不代表同等强度，更不能直接推论算法排名。
- 已检查 `processing_cgo.go`、`speexdsp/processor.go`、`native/webrtc-apm/src/lib.rs`：Speex 使用 int16 PCM，WebRTC 使用浮点 PCM；20ms 帧拆成两个 10ms；AEC/NS/AGC2 分别建立独立配置，没有发现 NS 档位或处理器类型接错。
- Speex 接收 AGC 当前目标 8192、最大 +18dB、上升 6dB/s、下降 40dB/s，不启用其 ANS。AGC2 设置数字自适应、初始增益 0dB、余量 5dB、最大 +18dB，关闭硬件音量控制；上游自带噪声电平估计及增益限制。两者目标、适应策略不同，可能解释主观稳定性差异，尚未证实具体原因。
- `receive_processing.go` 跳过 PLC 和极低电平帧；这意味着低电平门槛前后的处理可能不连续，且算法没有持续看到静音。需要语音尾音/停顿恢复的量化对比，不能把它当作已验证的最佳策略。
- AEC 使用本应用实际输出作为参考，包含收听音量；参考靠有界队列对齐，没有独立设备时钟/延迟校准，PTT 非发送期采集不持续喂给处理链。扬声器、蓝牙和时钟漂移仍须专项验证。带噪 TTS 不构成 AEC 测试。
- 默认改为 AEC SpeexDSP、ANS SpeexDSP、AGC WebRTC AGC2，是当前用户偏好选择，不声明 CPU 或真人音质已全面验证。旧保存配置不强制覆盖，WebRTC 缺失明确报不可用，不静默降级。

## 业界与候选

- [WebRTC APM](https://webrtc.googlesource.com/src/+/refs/heads/main/api/audio/audio_processing.h) 将常见 3A 用于采集处理；AGC2 数字处理用于 Resona 每成员接收链是产品选择，不意味着整个 APM 都应放到接收端。
- [OBS 实现](https://github.com/obsproject/obs-studio/blob/master/plugins/obs-filters/noise-suppress-filter.c) 提供 Speex 与 RNNoise，可见保留多后端是成熟做法，但不能以其选项证明某后端适合所有输入。
- [RNNoise](https://github.com/xiph/rnnoise) 将传统 DSP 与神经网络结合，建议作为下一项 ANS 候选，用 CPU 跑同源真人/键盘/风扇/游戏串音及喊叫测试，记录帧耗时 p95/p99、分配和新增延迟。
- [nnnoiseless](https://github.com/jneem/nnnoiseless) 是 Rust 的 RNNoise 移植候选；必须核对模型与上游版本，不能假定听感和最新版 RNNoise 相同，Rust 本身也不保证更快。
- [DeepFilterNet](https://github.com/Rikorose/DeepFilterNet) 提供 48kHz 实时语音增强及 Rust 路径，可作为第二候选。模型大小、STFT/lookahead 延迟和 CPU 开销需实测，不直接设为游戏默认。
- [Oopz 官方帮助](https://help.oopz.cn/42f9/f5ae) 明确有 AI 智能降噪模型，并提醒声卡降噪叠加可能不兼容。该公开说明未披露模型结构、具体算法或可复现性能，不能确认“自研”范围或照搬实现。

## 下一步验证

同源对比 Speex 中/高、WebRTC High/VeryHigh，固定接收 AGC 与手动增益，分开听噪声残留、人声损伤和停顿泵动。补真人样本与静音/轻声边界，再验证 AEC 回声路径和参考对齐。按需评测 RNNoise；暂不引入新产品依赖、GPU 后端或新处理线程。
