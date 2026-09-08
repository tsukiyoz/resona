# macOS 唤醒来源调查

日期：2026-09-09。结论：原先约 60 次/秒来自可见窗口的 CVDisplayLink，不是 Go/IPC 轮询或最小化时钟泄漏。首轮测试最小化后调用界面读取工具，工具恢复了窗口；相关“最小化”标签无效，已更正评估与 ADR。

## 证据链

1. 原生进程 sample 捕获 `CVDisplayLink::runIOThread -> waitUntil` 与 `performIO`。stdin/stdout/stderr 线程分别阻塞在通道或管道读取。同期 Go 核心 CPU、唤醒增量均为零。
2. 固定 gpui 0.2.2 的 `platform/mac/window.rs`：`start_display_link` 检查 `NSWindowOcclusionStateVisible`；`window_did_change_occlusion_state` 不可见时调用 stop。`display_link.rs` 的回调仅将帧请求合并投递到主队列。
3. 在 `/tmp` 复制固定依赖与桌面源码，仅加入启停和每 120 次 step 的状态日志，独立构建临时程序。没有修改 registry 源码、项目依赖或正式应用包。
4. 诊断程序点击最小化后出现 `stop` 与 `visible=false mini=true`，不再出现 step；调用 `getAXState()` 后立刻变为 `visible=true mini=false` 并恢复 step。确认观察工具改变了被测状态，而非依据 60Hz 数字猜测根因。

日志节选：

```text
WAKE step visible=true mini=false
# 点击最小化
WAKE stop
WAKE start visible=false mini=true
# 调用界面读取工具后
WAKE start visible=true mini=false
WAKE step visible=true mini=false
```

不可见的 start 日志记录的是进入函数，随后的可见性检查立即返回，不意味着启动成功。

GPUI 的 `window.rs` 帧回调只在 dirty/force_render 时 draw，或 needs_present 时 present；静置的显示时钟回调不等于完整重绘。当前未逐帧统计 GPU 提交，不能把时钟频率当作实际 FPS 或功耗。

## 修正复测

使用原 release 包，不是诊断构建。双方离线、0 条消息、音频关闭；点击各自最小化按钮后，不再对窗口调用 getApp/getAXState/getScreenshot，使用独立进程采样器。环境与程序哈希沿用 frontend-evaluation.md。GUI PID 33201，核心 33202；Wails 主进程 33563，WebKit GPU/Networking/WebContent 为 33588/33589/33590。

| 30 秒采样窗口 | GPUI + Go 唤醒/秒 | Wails 全进程唤醒/秒 | GPUI 物理 MiB | Wails 物理 MiB |
| --- | ---: | ---: | ---: | ---: |
| 第一次，30.108 秒 | 0.332 | 52.976 | 48.30 | 104.84 |
| 确认窗口，30.120 秒 | 0.299 | 51.029 | 48.30 | 104.06 |

第一个窗口开始附近恢复了原 release 构建（缓存构建约 1 秒）；第二个窗口没有构建/栈采样任务。两次为同一次启动的不同时间窗，不是独立启动样本。确认窗口 CPU：原生合计 0.000653%，Wails 合计 0.021023%，单核 100% 口径。很低的短窗 CPU 读数不适合推广为长期倍数或电池续航结论。

## 两条路径的区别

| 项目 | GPUI | Wails/React |
| --- | --- | --- |
| 业务状态 | Go 变化通知，私有管道推送 | App.tsx 的 refresh 完成后 setTimeout 1000ms，再 GetWorkspace |
| 可见窗口时钟 | CVDisplayLink 随显示刷新触发帧检查 | WebKit 管理 DOM、定时器和合成 |
| 真正最小化 | GPUI 停显示时钟；无状态变化时 Go/IPC 等待 | 应用层没有 visibility 判断停止轮询；WebKit 自行调度/节流 |
| 后台活动归属 | 本轮 GUI 约 0.3 次/秒，核心零 | 主程序、WebContent、Networking、GPU 各有活动 |

Wails 栈采样捕获 WebContent 的 `WebCore::DOMTimer::fired` 和主进程的 `WKWebView evaluateJavaScript`，与工作区轮询/桥接链路一致。Networking 栈以事件等待为主。不能将它的所有唤醒精确归于某个定时器，也不能说每秒一次轮询只产生一次进程唤醒：请求、回复及 WebKit 多进程通信可能唤醒多个线程。本轮没有通过关闭轮询做严格因果拆分，不声称已定位 WebKit 内部每一次唤醒。

`ri_interrupt_wkups` 是内核归属给进程的唤醒计数，不是机器从睡眠唤醒次数、CPU 使用率或渲染帧数。接口背景可参阅 Apple [task_power_info_data_t](https://developer.apple.com/documentation/kernel/task_power_info_data_t)。

## 交付与边界

原始 CSV 和调用栈在被 Git 忽略的 `build/bin/performance-20260909/`：`resona-wake-baseline.csv`（与栈采样重叠，仅诊断用）、`resona-wake-actually-minimized.csv`、`resona-wake-minimized-confirm.csv` 以及四个 `*-sample.txt`。临时诊断程序已关闭，原依赖 release 构建已恢复；本轮没有更改产品运行逻辑。

继续将新功能集中 GPUI 的方向不变。已排除“最小化固定 60Hz 唤醒”的产品缺陷；后续重点仍是 Windows、真实语音和游戏帧时间，而非为错误的最小化结论打补丁。可见但不在前台与真正最小化不同，前者显示时钟仍可能运行，后续资源验收必须分别记录。
