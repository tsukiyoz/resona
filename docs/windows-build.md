# Windows x64 构建

## 最省事：下载云端构建

打开仓库 **Actions -> Windows build**，选择成功的构建，在 **Artifacts** 下载 `Resona-win-x64-<commit>`，解压后再解压其中的 `Resona-win-x64.zip`，运行 `resona-desktop.exe`。不需要在自己电脑安装 Go、Rust 或 GCC。也可点击 **Run workflow** 手动构建指定分支；主分支相关代码变化会自动构建。只有 workflow 已推送且运行成功后才会出现产物；构建成功不代表麦克风、显卡和游戏后台已实机验收。

## 本地单命令

性能采集器采用独立的 [Desktop performance tools 工作流](https://github.com/tsukiyoz/resona/actions/workflows/desktop-perf.yml)，不包含在应用压缩包中。选择该工作流最近一次成功运行，在 Artifacts 下载 `desktop-perf-win-x64-<commit>`，解压到可写目录后双击 `compare.cmd`。工具无需开发环境，具体采集步骤见 [工具说明](../tools/desktop-perf/README.md)。应用和工具的构建提交号可以不同；无需等待每次应用更新重新编译未改动的工具。

工具链安装一次后，在仓库根目录运行：

```powershell
.\build-windows.cmd
```

脚本自动初始化 VS 环境、选择 MSVC 与 MINGW64 Rust、设置 CGO/GCC、执行 Go/Rust 测试、构建桌面/Core/可选 WebRTC 音频库并生成 `desktop/dist/Resona-win-x64.zip`。不必手动打开 Developer PowerShell、设置 CC 或切换工具链。MSYS2 自定义路径可用 `build-windows.cmd -Msys2Root D:\msys64`；本地快速构建可加 `-SkipTests`，CI 默认执行测试。缺少工具时会报告具体项目；脚本不会擅自安装 Visual Studio 或修改系统环境。

WebRTC 的 Abseil 对象文件名较长，MinGW 归档工具需要短构建目录。CI 使用 `RUNNER_TEMP/3a`，本地默认 `build/3a`；完整路径必须不含空格且不超过 30 字符，否则请传入例如 `build-windows.cmd -NativeTargetDirectory C:\resona-3a` 的可写短路径。该目录保留 Cargo 构建缓存，位于仓库外时不由 `make clean` 清理。

Windows 图标由 `desktop/build.rs` 编译资源 ID 1，GPUI 原生窗口和 EXE 使用同一资源。打包时验证 GUI 子系统及 16/32/48/256 像素图标可加载。原图为 `build/appicon.png`；修改后从仓库根目录运行 `go run ./tools/appicon build/appicon.png desktop/assets/resona.ico` 更新各尺寸，不需在 Windows 安装图像工具。

CI 另启用 `RESONA_CREDENTIAL_INTEGRATION=1`，对随机生成的独立 Credential Manager 条目执行写入/读取/更新/删除测试，不使用用户书签。macOS 交叉编译不能替代该原生测试；系统桌面的任务栏/Alt+Tab 图标及密码重启后连接仍需 Windows 验收。

云端与本地共用同一个脚本。当前方案使用 Windows 原生构建机，未提供 Linux Docker 交叉构建：Rust MSVC 和 Windows SDK 仍需 Windows 工具链，容器不会自动消除这部分要求。

构建当前 GPUI 客户端，不需要 Node.js、npm、Wails 或 WebView2。请在 Windows 本机执行，下面所有项目命令均从仓库根目录运行。当前 macOS 已验证；Windows 本机构建、设备、后台按键及游戏负载仍待实测，不将脚本存在等同于 Windows 验收通过。

## 一次性准备

1. 安装 Git 与 Go 1.26 或更新版本（go.mod 要求 1.26.0）。
2. 安装 Visual Studio 2022 或更新版本的 Build Tools，选择“使用 C++ 的桌面开发”、MSVC x64/x86 工具、Windows SDK 和 CMake 工具。Rust 官方 MSVC 工具链使用这套编译/链接环境，见 [Microsoft Rust 环境说明](https://learn.microsoft.com/en-us/windows/dev-environment/rust/setup)。
3. 用 rustup 安装当前 stable Rust；脚本自行准备桌面所需 MSVC 及可选音频库所需 GNU 工具链。
4. 安装 [MSYS2](https://www.msys2.org/)，在 **MSYS2 UCRT64** 终端安装 Go CGO 的 GCC 和 WebRTC 音频库所需 MINGW64 工具：

```sh
pacman -Syu
# 若升级要求关闭终端，重新打开 UCRT64 后继续。
pacman -S --needed mingw-w64-ucrt-x86_64-gcc mingw-w64-x86_64-gcc mingw-w64-x86_64-clang mingw-w64-x86_64-meson mingw-w64-x86_64-ninja mingw-w64-x86_64-pkgconf mingw-w64-x86_64-python
```

GCC 包与 UCRT64 用法见 [MinGW-w64 官方说明](https://www.mingw-w64.org/getting-started/msys2/)。桌面为 MSVC Rust，Go Core 使用 UCRT64 CGO，可选 WebRTC 音频库使用 MINGW64 Rust 并只通过 C ABI 与 Core 交互。

## 拉取与构建

在 PowerShell 进入已有仓库。切分支前保留你在 Windows 上的本地修改；构建直接运行上方一键脚本。

```powershell
git switch main
git pull --ff-only origin main
.\build-windows.cmd
.\desktop\dist\Resona-win-x64\resona-desktop.exe
```

仅对上述本次脚本进程使用 ExecutionPolicy Bypass，不修改系统执行策略。若企业策略禁止执行脚本，按组织允许的方式运行，不修改企业策略。

输出目录：`desktop/dist/Resona-win-x64/`。保留 `resona-desktop.exe`、`resona-core.exe` 和 `resona_webrtc_apm.dll` 在同一目录；GUI 自动启动核心。首次 Rust 构建需要下载和编译较多依赖。

## 运行与问题定位

- Windows 使用 ACL，Go 的 `0600/0700` 模式位不代表 Windows 的访问隔离；当前文件继承所在用户目录的 ACL。权限位断言仅在 Unix 平台执行，Windows 仍运行持久化、并发身份加载及缓存行为测试。身份文件在发布前同步内容，Windows 跳过不支持的目录同步，因此不承诺断电时目录元数据的持久化。
- 一键脚本还会收集本机 GCC 运行库与许可文件；旧 package-windows.ps1 只打包两个 exe。目标机可能仍需 Microsoft Visual C++ 2015-2022 x64 Redistributable。尚未完成干净 Windows 机器的免安装验收；如报告缺少运行库，应检查 exe 的 DLL 依赖，不从不明网站下载 DLL。
- `gcc not found`：核对 UCRT64 安装路径与 CC。`link.exe`/Windows SDK 错误：确认使用 VS Developer PowerShell 和 MSVC Rust，且已经清除 CC/CXX 的 GCC 覆盖。不要将 MSYS2 的 `usr/bin` 放到 MSVC 链接器前面。
- 更新显卡驱动；当前 GPUI Windows 后端使用 Direct3D 11。首次先测试离线界面和本地麦克风回放，按系统提示授权麦克风。
- 连接后自动收听、麦克风默认静音。真实服务器验证只在 Resona 专用测试频道进行；不能把默认登录频道当作测试频道。
- 问题报告请保留 `go version`、`rustc -V`、完整构建错误与触发步骤，避免附带服务器密码或身份密钥。
