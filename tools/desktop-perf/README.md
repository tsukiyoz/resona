# Windows 进程性能采集与对比

独立命令行工具，比较 TeamSpeak、Resona 或其他指定进程组。运行不需要 Go、Node、Python、MSYS2，也不安装服务或修改被测应用。普通桌面进程通常不需要管理员权限；权限拒绝时会报错，不会漏算。

## 最简单的用法

从 GitHub Actions 的 **Desktop performance tools** 工作流下载 `desktop-perf-win-x64-<commit>`，完整解压到可写目录。该工作流独立于客户端构建，只需要 Go；本地交叉编译也可以生成同一工具。

1. 打开 TeamSpeak 和 Resona，让双方进入同样的测试场景。
2. 双击 `compare.cmd`。进程表有 PID、父 PID、线程数和 EXE 名称。
3. 填写 TeamSpeak 主进程 PID，再填写 `resona-desktop.exe` 的 PID。默认会计入已有子进程，包括 `resona-core.exe`，无需再填一次。
4. 填写场景名称，例如 `offline-minimized`、`connected-listening` 或 `voice-vad-noise-medium`。不要填写密码或服务器地址。
5. 有 10 秒准备时间，随后每秒采样一次，共两分钟。最小化场景请将两个客户端都最小化；工具不会恢复窗口、截屏或操作麦克风。
6. 完成后同目录出现 `perf-日期时间/` 和 `perf-日期时间-report/`。前者含原始 `samples.csv` 和 `capture.json`，后者含 `report.md` 与 `report.json`。

程序窗口保留结果，按任意键关闭。如果出现错误，请先看错误内容；不完整采集的元数据会标为 `failed`，分析器拒绝将其作为完成结果。Ctrl+C 会停止采集并尽量保留错误状态；强制结束时可能留下 `collecting`，同样拒绝分析。

## 自定义采集

在 PowerShell 中运行，PID 数字仅是示例，必须替换为本次运行的值：

```powershell
.\desktop-perf.exe list
.\desktop-perf.exe collect --group teamspeak=1234 --group resona=5678 --duration 120s --warmup 10s --interval 1s --scenario offline-minimized --out capture-01
.\desktop-perf.exe analyze --input capture-01/samples.csv --baseline teamspeak --out report-01
```

`--group` 可重复使用，也可显式指定多个进程，例如 `--group resona=5678,5679 --children=false`。请检查采集开始时打印的进程名单和 `capture.json`；辅助进程不一定是主进程后代，例如某些共享浏览器或系统服务，必须人工确认归属，不能仅凭相同 EXE 名把其他应用的进程全部计入。

默认在准备时间结束后锁定已有进程树，每次采样检查成员变化。新观察到的子进程、成员退出、读数失败都会令本次采集失败，避免把少算进程误判为性能改善。持有进程句柄并记录创建时间，原进程退出后不会采集复用同一 PID 的新进程。进程快照之间出生又退出的短命辅助进程仍可能漏过，因此本工具适合稳定常驻负载，不适合精确测冷启动和频繁创建进程的工作负载。

至少采集一个完整间隔，间隔不低于 250ms；默认 1s，通常不建议缩短。支持最大 24h，长时间数据会占用磁盘和分析内存。准备时间不超过 1h。输出目录必须不存在，避免覆盖历史结果。工具不主动上传数据；本地目录权限按 Windows 当前目录 ACL 继承。

## 报告读法

报告输出每组和每个进程的指标，包含均值、P95、最小值、采样峰值，以及相对于 `--baseline` 的绝对差和百分比差。负数表示测得的使用量更低，不自动等于更好；例如 I/O 少也可能因为业务没有正常工作。基准为零时百分比显示 `N/A`。

| 报告字段 | 含义 | 注意 |
| --- | --- | --- |
| `cpu_one_core` | CPU 时间增量 / 墙钟时间 | 100% 是一个逻辑核，可以超过 100%；不等于任务管理器的整机归一化百分比 |
| `private_commit` | 私有提交内存，MiB | 各进程相加不会重复算共享页，但并非实际驻留 RAM，更不是 macOS 物理占用 |
| `working_set_sum` | 工作集之和，MiB | 包含驻留的私有及共享页，多个进程可能重复计算共享页 |
| `io_read/write/other` | 进程 I/O 累积字节数增量，KiB/s | 包含文件、管道、设备等活动，不是专门的网络流量或物理磁盘吞吐 |
| `handles`、`threads` | 句柄数、线程数 | 数量和末尾变化可辅助定位问题，不能仅凭上涨认定泄漏 |

CPU 和 I/O 的 P95 是按持续时间加权的**采样间隔平均速率**，不是帧时间或请求延迟。内存、句柄、线程均值用相邻采样值的梯形时间加权；P95 用两端各占半个间隔的权重。`End-start` 是末值减初值。采样间隔之间的瞬时峰值可能漏掉，多个进程也不是在完全同一纳秒读取；批次耗时超过间隔 25% 时报告会提示。

报告不输出 Windows 的唤醒、上下文切换、GPU、瓦数、音频延迟或游戏 FPS。它们不能用进程 I/O 或线程数替代，需要后续专门的 ETW/WPR、GPU 或帧时间测量。也不把 Windows 和 macOS 不同内存接口混成一个跨系统排名。

## 分析既有 macOS CSV

分析器可在 Windows、macOS、Linux 运行。继续使用仓库原有 `tools/desktop-metrics.c` 采集 macOS；指定原 CSV 中的进程归属：

```sh
go run ./tools/desktop-perf analyze --input /tmp/mac.csv --group teamspeak=1234 --group resona=5678,5679 --baseline teamspeak --out /tmp/mac-report
```

额外的未选中进程被忽略，所有指定 PID 必须存在。旧格式没有进程创建时间或采集完成标记，报告会明确提示无法验证 PID 复用及是否完整结束。保留原采集记录作为旁证。

Windows CSV 自带进程组，不再接受 `--group`。请把 Windows CSV 与同目录 `capture.json` 一起保留；存在元数据时必须标记完成，并且样本数、进程组和创建时间须匹配。仅有 CSV 也可分析，但报告明确标记无法验证完成情况。

可用 `--skip 10` 排除开头 10 秒，按实际时间戳选取后续完整样本，不插值。所有进程必须具有相同的样本时间序列；缺样、重复时间戳、计数器倒退和非有限数字会被拒绝，而不会补零或合并不同时间窗。

## 可重复的比较流程

- 分开测离线、连接仅收听、实际讲话和高负载；每个场景单独采集文件。
- 确认真实连接状态、相同频道与人数、采样率、编解码器和降噪设置。不要把本地预览当作真实网络负载。
- 固定窗口显示或最小化状态。测试期间不读取窗口截图，以免工具恢复最小化窗口。
- 记录版本、CPU 架构、输入输出设备和场景；使用 release 构建，计入辅助进程。
- 每个场景至少做三次独立采集，并交换启动顺序；短时间很低的 CPU 数值不要只看倍数。
- 两者同时运行适合静置对比。语音互测可能产生设备竞争和回声反馈，需相同录音输入、独立可控场景；进程资源结果不等于音质或游戏体验结论。

工具只读取进程，不会自动连接 TS3 或进入频道。涉及用户服务器的测试仍须遵守仓库要求，只在 Resona 专用测试频道进行。

## 开发与验证

本工具复用根模块已有的 `golang.org/x/sys/windows`，不导入客户端、Wails 或音频包，不需要 CGO：

```powershell
$env:CGO_ENABLED = '0'
go test ./tools/desktop-perf
go build -trimpath -o desktop-perf.exe ./tools/desktop-perf
```

在 macOS/Linux 交叉构建：

```sh
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -o build/bin/desktop-perf.exe ./tools/desktop-perf
```

分析器测试覆盖进程组求和、非均匀时间加权、百分比零基准、CSV 损坏/缺样/身份变更、元数据匹配、输出防覆盖和旧 macOS CSV。Windows 专用测试读取真实进程计数，并启动临时测试子进程完成采集到报告的往返，验证退出和取消状态。CI 运行这些 Windows 测试，不把交叉编译等同于原生运行通过。

指标来源：[GetProcessTimes](https://learn.microsoft.com/en-us/windows/win32/api/processthreadsapi/nf-processthreadsapi-getprocesstimes)、[PROCESS_MEMORY_COUNTERS_EX](https://learn.microsoft.com/en-us/windows/win32/api/psapi/ns-psapi-process_memory_counters_ex)、[GetProcessIoCounters](https://learn.microsoft.com/en-us/windows/win32/api/winbase/nf-winbase-getprocessiocounters)。
