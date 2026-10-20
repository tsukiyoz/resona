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

## 2026-10-20 评估工具补充

`test/audio3a` 新增独立 `quality` 入口，复用已有锁定版本的 Speex、WebRTC、
RNNoise regular/little 与 nnnoiseless。以同一 clean/noise PCM 和已标注语音范围
按 SNR 混音，统一峰值余量；ANS-only 和固定 AGC2 两组分别比较。额外对干净语音
执行各处理器，记录失真代理值及原始输出；匿名试听只匹配语音投影增益，不用于
判断 AGC 的真实音量。输入来源、哈希、模型和每次测量均保留。

当前公开 smoke 样本是真人英语读音（VOiCES，16k 上采样），噪声为合成粉红噪声。
它验证评估工具能运行，并支持初步观察开销和静音段残留；不代表中文/full-band、
键盘、风扇、游戏串音或陌生说话人的效果。没有验证模型训练集排除关系。
短片段也不证明 AGC 长时间适应性。AEC、Opus/网络/设备链、内存与游戏帧时间
均不在本次测量范围内。

SI-SDR 是波形参考指标，不是听感评分；滤波、相位变化也可能降低它。不能因为
WebRTC 分数低就认定实现错误，或因为神经算法静音更干净就忽略人声损伤。
相关对齐只服务指标计算，不是新增算法延迟测量。RMS 采用 -120dBFS 计算下限，
极低残留的抑制值受此下限约束。选型仍需结合匿名听感、CPU 和真实场景验证。
本次不改变产品默认值或增加产品音频依赖。

### 第二轮：中文与录制噪声

首轮用户在粉红噪声 5/15dB 两组均偏好 RNNoise little；nnnoiseless 的人声边缘
被反馈有不舒服的噪声感。该反馈是一次非标准主观试听，不作为 MOS 分数。
第二轮使用四位 THCHS-30 中文说话人与 MS-SNSD 录制键盘、空调、背景交谈，
另加输入音量阶跃和极轻输入压力场景。后两者是调幅模拟，不是真实轻声或喊叫。
来源、版本、哈希和许可证由 `quality-round2.py` 固定并随试听包保留。

参考样本先做低频检查：原选 C21_517 的极低频能量明显影响波形投影与音量匹配，
改用 C31_736 并重跑整轮，未对选定人声额外高通。背景交谈作为困难对照，
普通 ANS 不保证区分目标人声。新版匿名包将 regular/little 与其余四个对照统一
匹配音量，只含 ANS，各场景分别打乱 A-F；AGC2 组合的原始输出单独保留。
复现命令见 `test/audio3a/README.md`，测量证据见 `test/audio3a/VERIFICATION.md`。
本轮仍不覆盖设备、Opus/网络、full-band 游戏录音、AEC 或游戏帧时间。

本机复测 300 行计时与配套参考指标后，RNNoise little 仍是优先接入候选：
单核实时流 CPU 中位约 2.45%，regular 约 5.99%；键盘与空调组的 SI-SDR 改善
接近，停顿残留明显低于当前 Speex -20dB / WebRTC Moderate 档位。该优势不推广
到所有噪声：背景交谈组 little 的 SI-SDR 改善为 -0.32dB，且首段语音投影衰减
4.11dB，必须重点试听人声损伤。结合新版匿名听感决定接入优先级和后续验证；
不能用静音段趋近计算下限的抑制值推断人声自然度。

第二轮维护者匿名反馈已收集：键盘与音量阶跃两组仅 RNNoise regular/little
明显有效；空调组 Speex 与两款 RNNoise 均可接受。背景交谈组 regular 略优于
little 的人声自然度，但两者都有声音变化与少量泄漏，均只勉强接受。
nnnoiseless 多次出现人声边缘或背景声音异常，当前 WebRTC Moderate 未满足
键盘抑制需求。极轻输入组所有降噪器均无明显底噪，但未单独评价轻声完整性。
这是一位听者对有限样本的定性反馈，不是 MOS 或统计排名，也不外推到更强档位。

后续优先验证 RNNoise little 的真实链路接入，regular 作为质量可选候选，Speex
保留低开销用途；暂不优先接入当前 nnnoiseless。下一步应测采集、ANS、Opus、
每用户 AGC2 串联与 Windows 等价负载，检查起音、尾音、音量突变及背景人声，
而非仅重复离线静音抑制排名。完成这些验证后再确定产品默认值。

### 编解码与接收 AGC2 串联

独立 Go 工具已使用产品实际的纯 Go `gopus v0.1.1`（48k 单声道、20ms、
VoIP、complexity 5、32/48kbit/s）串联发送 ANS 和独立接收 AGC2；Speex 对照
改为与 core 相同的 20ms。沿用第二轮同源输入，顺序完成 180 行测量。
完整链单核实时折算 CPU 中位 little 3.48%、regular 7.20%、Speex 1.33%。
这包含测试适配器和校验开销，不是整机占用或游戏帧时间。

键盘场景 AGC2 会抬高残余底噪，但 neural 两项仍明显低于当前 Speex 对照。
音量阶跃第一段轻声未立即拉齐，不能承诺瞬时一致响度；少量输出恰好满幅，
无超过满幅的值。原始测试存在墙钟尖峰，定向复测未复现，原因尚未确定。
三阶段音频保留真实增益，统一防削波余量，不逐条响度匹配。
详见 `test/audio3a/VERIFICATION.md`。链路听感、Windows/Linux 新版 CI、
设备采集和游戏等价负载仍待验收，尚未把 RNNoise 接入产品或改默认值。
