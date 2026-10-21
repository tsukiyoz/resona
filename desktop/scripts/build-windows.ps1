param(
    [string]$Msys2Root = "C:\msys64",
    [ValidatePattern('^[A-Za-z0-9.+_-]+$')]
    [string]$Version,
    [string]$NativeTargetDirectory,
    [switch]$SkipTests
)

$ErrorActionPreference = "Stop"
Set-StrictMode -Version Latest
if ([Environment]::OSVersion.Platform -ne [PlatformID]::Win32NT) {
    throw "This build requires Windows x64. Use the Windows build GitHub Action on other systems."
}
$RepoDirectory = (Resolve-Path "$PSScriptRoot\..\..").Path
$BaseVersion = (Get-Content -Raw "$RepoDirectory\VERSION").Trim()
if ($BaseVersion -notmatch '^\d+\.\d+\.\d+$') { throw 'Invalid root VERSION' }
if (-not $Version) { $Version = "v$BaseVersion" }

function Invoke-Checked {
    param([string]$Program, [string[]]$Arguments)
    & $Program @Arguments
    if ($LASTEXITCODE -ne 0) { throw "$Program failed with exit code $LASTEXITCODE" }
}

$Gcc = Join-Path $Msys2Root "ucrt64\bin\gcc.exe"
if (-not (Test-Path $Gcc -PathType Leaf)) {
    throw "UCRT64 GCC missing at $Gcc. Use GitHub Actions without local tools, or install MSYS2 GCC; see docs/windows-build.md."
}
foreach ($Tool in @("go", "rustup", "cargo")) {
    if (-not (Get-Command $Tool -ErrorAction SilentlyContinue)) {
        throw "$Tool is missing. Use GitHub Actions without local tools, or see docs/windows-build.md."
    }
}
$VsWhere = Join-Path ${env:ProgramFiles(x86)} "Microsoft Visual Studio\Installer\vswhere.exe"
if (-not (Test-Path $VsWhere)) { throw "Visual Studio C++ Build Tools are missing. See docs/windows-build.md." }
$VsRoot = & $VsWhere -latest -products '*' -requires Microsoft.VisualStudio.Component.VC.Tools.x86.x64 -property installationPath
if ($LASTEXITCODE -ne 0 -or -not $VsRoot) { throw "No Visual Studio installation with x64 C++ tools found." }

# The cmd entry runs this in a child process; scoped overrides are also restored
# when the PowerShell entry is invoked directly.
$OriginalEnvironment = @{}
Get-ChildItem Env: | ForEach-Object { $OriginalEnvironment[$_.Name] = $_.Value }
Push-Location $RepoDirectory
try {
    Import-Module (Join-Path $VsRoot "Common7\Tools\Microsoft.VisualStudio.DevShell.dll")
    Enter-VsDevShell -VsInstallPath $VsRoot -SkipAutomaticLocation -DevCmdArguments '-arch=x64 -host_arch=x64'
    $VsPath = $env:Path
    $env:RUSTUP_TOOLCHAIN = "stable-x86_64-pc-windows-msvc"
    Invoke-Checked rustup @("toolchain", "install", $env:RUSTUP_TOOLCHAIN, "--profile", "minimal")
    $env:CGO_ENABLED = "1"
    $env:GOOS = "windows"
    $env:GOARCH = "amd64"
    $env:CC = $Gcc
    $env:CXX = Join-Path $Msys2Root "ucrt64\bin\g++.exe"
    $env:Path = (Join-Path $Msys2Root "ucrt64\bin") + ";" + $env:Path
    Invoke-Checked go @("version")
    if (-not $SkipTests) { Invoke-Checked go @("test", "./internal/...") }
    New-Item -ItemType Directory -Force "build\bin" | Out-Null
    $BuildTime = [DateTime]::UtcNow.ToString("yyyy-MM-ddTHH:mm:ssZ")
    $env:RESONA_VERSION = $Version
    $env:RESONA_BUILD_TIME = $BuildTime

    $Mingw = Join-Path $Msys2Root "mingw64\bin"
    $MingwGcc = Join-Path $Mingw "gcc.exe"
    $MingwMeson = Join-Path $Mingw "meson.exe"
    if (-not (Test-Path $MingwGcc) -or -not (Test-Path $MingwMeson)) {
        throw "MINGW64 GCC, Clang, Meson, Ninja and pkgconf are required for WebRTC; see docs/windows-build.md."
    }
    $env:Path = "$Mingw;$(Join-Path $Msys2Root 'usr\bin');$env:Path"
    $env:RUSTUP_TOOLCHAIN = "stable-x86_64-pc-windows-gnu"
    Invoke-Checked rustup @("toolchain", "install", $env:RUSTUP_TOOLCHAIN, "--profile", "minimal", "--component", "llvm-tools-preview")
    $env:CC = $MingwGcc
    $env:CXX = Join-Path $Mingw "g++.exe"
    $env:LIBCLANG_PATH = $Mingw
    $env:BINDGEN_EXTRA_CLANG_ARGS = "--target=x86_64-w64-windows-gnu"
    $NativeHeader = $RepoDirectory.Replace('\', '/') + "/native/webrtc-apm/windows-static.h"
    $env:CFLAGS = "-include $NativeHeader"
    $env:CXXFLAGS = $env:CFLAGS
    $env:CXXSTDLIB = "static=stdc++"
    $CxxLibrary = (& $env:CXX '-print-file-name=libstdc++.a').Trim()
    if ($LASTEXITCODE -ne 0) { throw "Failed to locate the static MINGW64 C++ runtime" }
    if (-not (Test-Path $CxxLibrary)) { throw "Static MINGW64 C++ runtime is missing: $CxxLibrary" }
    $CxxLibraryDirectory = [IO.Path]::GetDirectoryName($CxxLibrary)
    $env:RUSTFLAGS = "-C target-feature=+crt-static -C link-arg=-static -L native=$CxxLibraryDirectory"
    $env:PKG_CONFIG_LIBDIR = Join-Path $env:TEMP "resona-empty-pkg-config"
    New-Item -ItemType Directory -Force $env:PKG_CONFIG_LIBDIR | Out-Null
    # MinGW's archive tools need short object paths for the bundled Abseil build.
    if (-not $NativeTargetDirectory) {
        if ($env:RUNNER_TEMP) {
            $NativeTargetDirectory = Join-Path $env:RUNNER_TEMP "3a"
        } else {
            $NativeTargetDirectory = Join-Path $RepoDirectory "build\3a"
        }
    }
    $ApmTarget = [IO.Path]::GetFullPath($NativeTargetDirectory)
    if ($ApmTarget.Length -gt 30 -or $ApmTarget -match '\s') {
        throw "WebRTC requires a short build path without spaces. Pass -NativeTargetDirectory with a writable path such as C:\resona-3a (at most 30 characters): $ApmTarget"
    }
    $env:CARGO_TARGET_DIR = $ApmTarget
    if (-not $env:RNNOISE_SOURCE) {
        $env:RNNOISE_SOURCE = ($RepoDirectory + "/build/native/rnnoise").Replace('\', '/')
    }
    Invoke-Checked bash @("native/rnnoise/prepare.sh")
    Invoke-Checked cargo @("test", "--locked", "--release", "--manifest-path", "native/webrtc-apm/Cargo.toml")
    Invoke-Checked cargo @("build", "--locked", "--release", "--manifest-path", "native/webrtc-apm/Cargo.toml")
    $Apm = Join-Path $env:CARGO_TARGET_DIR "release\resona_webrtc_apm.dll"
    if (-not (Test-Path $Apm)) { throw "WebRTC audio DLL missing: $Apm" }
    Copy-Item $Apm "build\bin\resona_webrtc_apm.dll"
    $env:RESONA_WEBRTC_PLUGIN_DIR = Join-Path $RepoDirectory "build\bin"
    # Restore the MSVC desktop compiler environment after building the isolated C ABI DLL.
    foreach ($Key in @("CC", "CXX", "LIBCLANG_PATH", "BINDGEN_EXTRA_CLANG_ARGS", "CFLAGS", "CXXFLAGS", "CXXSTDLIB", "RUSTFLAGS", "PKG_CONFIG_LIBDIR", "CARGO_TARGET_DIR")) {
        Remove-Item "Env:$Key" -ErrorAction SilentlyContinue
    }
    $env:Path = (Join-Path $Msys2Root "ucrt64\bin") + ";" + $VsPath
    $env:RUSTUP_TOOLCHAIN = "stable-x86_64-pc-windows-msvc"
    Remove-Item Env:CC, Env:CXX -ErrorAction SilentlyContinue
    Invoke-Checked bash @("native/opus/build.sh")
    $env:PKG_CONFIG = Join-Path $Mingw 'pkg-config.exe'
    $env:PKG_CONFIG_PATH = (Join-Path $RepoDirectory 'build/native/opus-install/lib/pkgconfig').Replace('\','/')
    $env:OPUS_STATIC = '1'
    $env:RESONA_TEST_NATIVE_AUDIO = '1'
    if (-not $SkipTests) {
        Invoke-Checked cargo @("test", "--locked", "--manifest-path", "client/Cargo.toml", "--features", "audio")
        Invoke-Checked cargo @("test", "--locked", "--manifest-path", "desktop/Cargo.toml", "--target", "x86_64-pc-windows-msvc")
    }
    $Artifacts = & cargo test --locked --manifest-path client/Cargo.toml --features audio --no-run --message-format=json
    if ($LASTEXITCODE -ne 0) { throw 'Cannot build Rust native audio smoke test' }
    $SmokeBinary = ($Artifacts | ForEach-Object { $_ | ConvertFrom-Json } | Where-Object { $_.reason -eq 'compiler-artifact' -and $_.target.name -eq 'resona_client' -and $_.profile.test } | Select-Object -Last 1).executable
    if (-not $SmokeBinary) { throw 'Rust audio smoke test executable missing' }
    $BuildPath = $env:Path
    try {
        $env:Path = "$env:WINDIR\System32;$env:WINDIR"
        Invoke-Checked $SmokeBinary @('audio::dsp::tests::product_external_library_runs_all_stages_when_requested', '--exact', '--nocapture')
    } finally { $env:Path = $BuildPath }
    & "$PSScriptRoot\package-windows.ps1" -Version $Version

    $Package = Join-Path $RepoDirectory "desktop\dist\Resona-win-x64"
    Copy-Item "build\bin\resona_webrtc_apm.dll" $Package
    $Licenses = Join-Path $Package "licenses"
    New-Item -ItemType Directory -Force $Licenses | Out-Null
    Copy-Item "$RepoDirectory\internal\audio\speexdsp\COPYING" "$Licenses\SpeexDSP.txt"
    Copy-Item "$RepoDirectory\desktop\vendor\gpui\LICENSE-APACHE" "$Licenses\GPUI.txt"
    Copy-Item "$RepoDirectory\build\native\opus-source\COPYING" "$Licenses\Opus.txt"
    $MiniAudio = Get-Content -Raw "$RepoDirectory\native\client-audio\miniaudio.h"
    [IO.File]::WriteAllText("$Licenses\miniaudio.txt", $MiniAudio.Substring($MiniAudio.LastIndexOf('ALTERNATIVE 1 - Public Domain')), [Text.UTF8Encoding]::new($false))
    $env:RUSTUP_TOOLCHAIN = "stable-x86_64-pc-windows-gnu"
    $env:Path = "$Mingw;$(Join-Path $Msys2Root 'usr\bin');$env:Path"
    Invoke-Checked (Join-Path $Mingw "python.exe") @("native/webrtc-apm/licenses.py", "native/webrtc-apm/Cargo.toml", $Licenses, $ApmTarget)
    foreach ($Name in @("gcc-libs", "libwinpthread", "winpthreads", "gcc")) {
        $Source = Join-Path $Msys2Root "mingw64\share\licenses\$Name"
        if (Test-Path $Source) { Copy-Item $Source $Licenses -Recurse -Force }
    }
    $Archive = Join-Path $RepoDirectory "desktop\dist\Resona-win-x64.zip"
    Compress-Archive -Path "$Package\*" -DestinationPath $Archive -Force
    Write-Host "Built: $Archive"
    Write-Host "Run: $Package\resona-desktop.exe"
} finally {
    Pop-Location
    Get-ChildItem Env: | Where-Object { -not $OriginalEnvironment.ContainsKey($_.Name) } | ForEach-Object { Remove-Item "Env:$($_.Name)" }
    foreach ($Name in $OriginalEnvironment.Keys) { Set-Item "Env:$Name" $OriginalEnvironment[$Name] }
}
