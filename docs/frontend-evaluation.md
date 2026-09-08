# 桌面 UI 与资源预算评估

日期：2026-09-09。完成 macOS 第一轮常驻基线；不是最终框架迁移验收。

更正：首轮采样在最小化后读取界面，工具将窗口恢复，原“最小化约 63 次/秒”结论无效。诊断构建已证明 GPUI 真正最小化会停止显示时钟。修正后的两次离线采样及原因见 [唤醒调查](wakeup-investigation.md)。

## 开发收敛

新 GUI 功能集中在 `desktop/` GPUI；`frontend/` Wails/React 冻结为可构建的比较与回退基线，不再要求功能同步。共享 Go 核心改动保持基线能够构建；只有阻断构建、基线测试或必要安全修复才修改旧 GUI。现在不删除 Wails，也不扩大到第三套框架。选择基于当前功能已向 GPUI 集中、减少重复维护，以及本轮原生内存优势，不能解释为 GPUI 在所有资源指标胜出。

最终删除 Wails 前，需要 Windows 同负载通话与游戏帧时间测量、主要输入/权限/退出工作流验收。GPUI 最小化高唤醒调查已确认为测量工具恢复窗口所致，不再作为已知产品缺陷。若后续指标不达标，依据热点决定修复原生或回退；不继续两套平行补功能。维护边界见 ADR-0015。

## 本轮结果

物理占用为所有归属进程 `ri_phys_footprint` 之和，单位 MiB；CPU 为用户态与内核态 CPU 时间增量 / 墙钟时间，100% 表示一个逻辑核。唤醒为 `ri_interrupt_wkups` 的增量，不是帧率或 GPU 使用率。

| 场景 | Wails 总物理占用 | GPUI + Go 总物理占用 | Wails CPU | GPUI CPU | Wails / GPUI 唤醒每秒 |
| --- | ---: | ---: | ---: | ---: | ---: |
| 离线静置，30.1 秒 | 101.58 | 63.76 | 0.0181% | 0.0156% | 46.92 / 60.40 |
| 本地预览，最小化状态无效，60.2 秒 | 97.58 | 74.13 | 0.0195% | 0.0388% | 38.34 / 63.01 |
| 离线、真正最小化，修正复测 30.1 秒 | 104.06 | 48.30 | 0.0210% | 0.00065% | 51.03 / 0.30 |

- 预览为同一核心提供的 3 个频道、1 位本人、0 条消息，均未连接网络、未开启音频。原生语音控件存在但未启用，Wails 未提供同等音频设置。
- 预览物理占用区间：Wails 96.94–99.55 MiB；GPUI 74.13–74.13 MiB。原生平均少约 23.45 MiB（24.0%）；离线少约 37.82 MiB（37.2%）。固定区间来自内核账本读数，不表示没有任何分配。
- CPU 都很低；预览 GPUI 数值更高，但本轮不是长期重复实验，不能推广成稳定的倍数差距。Go 核心在该预览窗口 CPU 增量为零，约 7.36 MiB；原生主进程约 66.77 MiB，高唤醒集中在 GUI。
- 结果支持“GPUI 在本机简单常驻场景省内存”，不支持“全面更省电”或“游戏 GPU 干扰更低”。没有用 RSS 简单相加作物理占用结论，也没有将共享 WindowServer 成本归入某个客户端。

## 环境与方法

Apple M2、16 GiB、macOS 26.5.1 (25F80)、arm64。两套来自当前 `feature/tsukiyo/client-20260908_gpui-voice` 工作区，基于 ad5190a 且包含未提交功能，不是该提交的纯净构建。Wails v2.15.0 production、关闭 devtools；GPUI release（gpui 0.2.2 / gpui-component 0.5.1）。保留平时其他应用运行，没有清缓存、重启或独占机器控制。

Wails 统计主进程和本次启动产生的 WebContent、GPU、Networking 三个辅助进程；GPUI 统计 GUI 和直接启动的 Go 核心。每秒一次，同一窗口同时采样双方。预览 Wails 独立复制包的主程序 SHA-256 与原构建一致；复制用于绕开自动化缓存旧应用标识，没有改代码或配置。

首轮预览 PID：Wails 3675 / 3705 / 3706 / 3707，GPUI 128 / 179。WebKit 并非主进程的直接子进程：以启动前后差集识别，并在关闭评测包后复核四个进程全部消失。首轮离线 Wails PID 为 88731 / 88758 / 88759 / 88760，GPUI 相同。离线遮挡未严格统一；预览虽点击最小化，但随后的界面读取将窗口恢复，不能用于最小化结论。修正复测 PID 与严格操作次序见唤醒调查。

程序 SHA-256：

```text
Wails: ccc64aa60d8fbbd53f58323bde7b28e21b4e8355746522016331e4baff0b9720
GPUI:  d29a287c5ef3025b7dec848f2bb95e5deb87ba1061fba8539b14f1fffccc59ed
Core:  e4f0c70c5bf01a2054375c3f42703d911859b602a52ba80f0457bc3af4b48afd
```

原始 CSV 在本地被 Git 忽略的 `build/bin/performance-20260909/`，没有将本地测试数据或进程快照加入 Git。

## 复现

先分别构建前端及 Wails production、Go 核心和 GPUI release，使用同一源码和等价场景。通过系统进程列表确认当次 PID 与 WebKit 归属，不能复用历史 PID。进入场景后等待稳定，不在采样窗口操作界面。

```sh
clang -O2 -Wall -Wextra tools/desktop-metrics.c -o /tmp/resona-desktop-metrics
/tmp/resona-desktop-metrics 60 /tmp/desktop.csv WAILS_PID WEB_PID GPU_PID NET_PID GUI_PID CORE_PID
node tools/summarize-desktop-metrics.mjs /tmp/desktop.csv wails=WAILS_PID,WEB_PID,GPU_PID,NET_PID gpui=GUI_PID,CORE_PID
```

PID 占位符替换成数字。工具使用 macOS `proc_pid_rusage(RUSAGE_INFO_V4)`，任一进程不可读即失败，不静默漏算。物理占用是进程账本归属口径，不是全系统内存差，也不包含所有系统服务和 GPU 驱动共享资源。CPU 用累计时间差，避免 `ps %cpu` 的历史平均污染当前窗口。

## 尚需完成

每场景只采一个窗口，没有冷启动统计、重复启动方差、500 条消息/大频道树/图标压力、连续消息、长时间泄漏、GPU/功耗采样或 TS3 官方客户端对照。默认窗口尺寸不同（Wails 1280×820，GPUI 约 1200×768 逻辑像素）；最小化减少可见绘制差异，不能推断相同可见面积下的渲染性能。

下一轮优先：

1. 最小化唤醒原因已查清；后续需要时评估可见静置窗口的显示时钟成本，不将时钟回调次数当作实际重绘次数。
2. Windows release 等价频道/音频负载，计入 WebView2 或 Go 核心；记录后台 CPU、总占用、GPU 和游戏帧时间分位数。
3. 大频道与聊天数据、中文输入、全局 PTT、权限提示、取消和退出回收。真实 TS3 动作只在 Resona 专用测试频道。

高频快照中的图标已改为独立引用与按需资源读取；Wails 仍每秒取工作区，GPUI 为变化通知。比较的是当前两套客户端实现，而非隔离所有设计差异的纯框架微基准。音频实时路径在 Go，不经 GUI JSON 传递。
