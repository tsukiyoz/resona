# ADR-0003：TS3 客户端优先，协议库先验证后采用

- 日期：2026-09-07
- 状态：Accepted

## 背景

产品需要以普通客户端身份进入 TS3 频道并双向语音通信。原 `toqueteos/ts3` 项目只封装 ServerQuery，不能作为语音客户端底座。TS3AudioBot 的完整音乐后端不等于通用 GUI 客户端核心。

## 决定

先评估 Go 原生客户端候选 `HoneyBBQ/teamspeak-go`，TSLib 和 ReSpeak 作为协议与行为研究参考。候选库未通过 M1 验收前，不宣称可日用或完全兼容，不把依赖类型暴露给 GUI。

M0 只提供显式本地预览。正式接入的决定必须基于固定版本源码检查和真实 TS3 测试，包括语音接收；能发送 Opus 包不能推出完整音频客户端能力。

未来自有服务端需要以官方 TS3 客户端交叉验证其基础兼容性。客户端研究与服务端研发分别跟踪，不将服务端工作隐含在 UI 里程碑中。

## 代价

早期无法直接承诺语音发布时间，需要投入协议、失败恢复和 native 音频依赖验证。采用现有库后也可能需要修复或替换。

## 重新评估条件

候选库缺失关键能力或失败恢复无法满足要求时，决定补丁、fork、替代库或分阶段实现；在新的 ADR 中记录证据。

依据：[候选库](https://github.com/HoneyBBQ/teamspeak-go)、[TS3AudioBot](https://github.com/Splamy/TS3AudioBot)、[ReSpeak](https://github.com/ReSpeak/tsclientlib)。
