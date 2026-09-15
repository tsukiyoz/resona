# 客户端日志与 flush 调研

2026-09-15。范围为官方文档/公开实现，不推断闭源客户端内部刷新频率。

## 已核实的实践

- [Discord 官方故障报告文档](https://support.discord.com/hc/en-us/articles/1500006052822-How-to-Report-a-Bug)描述启用调试日志、复现、主动上传，以及本地日志目录。能确认支持文件诊断，不能据此确认其 batch 大小、flush 周期或默认写入量。
- [WebRTC FileRotatingLogSink](https://webrtc.googlesource.com/src/%2B/5a7e6f8ed1c1313300fb6bb48d70e056202011ed/rtc_base/log_sinks.h)，核实提交 `5a7e6f8ed1c1313300fb6bb48d70e056202011ed` 的公开头文件：提供有界滚动文件、通话会话文件和关闭缓冲选项。它是库提供的能力，不代表每个 WebRTC 产品都启用或采用相同刷新周期。
- [spdlog 官方示例](https://github.com/gabime/spdlog)同时提供异步日志、定期 flush（示例为三秒）和按需输出的内存 backtrace 环。批量文件与内存回溯都是成熟选择，三秒并非语音应用的统一标准。

## CPU 与帧时间

[Go bufio.Writer.Flush](https://pkg.go.dev/bufio#Writer.Flush)把用户态缓冲写给底层 Writer。若底层是文件，通常先进入操作系统缓存；这不同于要求持久化的 [Windows FlushFileBuffers](https://learn.microsoft.com/en-us/windows/win32/api/fileapi/nf-fileapi-flushfilebuffers)。微软明确说明在大量独立写入后逐次调用 FlushFileBuffers 可能低效。

理论上，合并多个小写入能摊薄系统调用、锁和调度开销；异步只转移工作，并不消除序列化、内存拷贝、文件系统和系统后台写回成本。格式化/分配可能比少量顺序写更显著。强制落盘的耗时还包括等待设备，不能把整个等待时间当作 CPU 消耗。

不能用平均 CPU 低推断游戏 1% low 无影响。周期集中写入、驱动和存储竞争可能产生瞬时波动，但幅度取决于日志量、设备、系统缓存和游戏负载，当前没有 Windows 同负载实测数值。

Resona 选择默认结构化内存环，热路径只计数，主动导出才格式化并写文件。不增加周期刷盘、退出刷盘或 crash dump；这减少诊断引入的周期活动，但不保证游戏帧时间绝无影响。主要代价是崩溃/退出丢失未导出的历史。
