param(
    [string]$Msys2Root = "C:\msys64",
    [switch]$SkipTests
)

$ErrorActionPreference = "Stop"
Set-StrictMode -Version Latest
if ([Environment]::OSVersion.Platform -ne [PlatformID]::Win32NT) {
    throw "This build requires Windows x64. Use the Windows build GitHub Action on other systems."
}
$RepoDirectory = (Resolve-Path "$PSScriptRoot\..\..").Path

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
    Invoke-Checked go @("build", "-o", "build/bin/resona-core.exe", "./cmd/resona-core")

    Remove-Item Env:CC, Env:CXX -ErrorAction SilentlyContinue
    if (-not $SkipTests) {
        Invoke-Checked cargo @("test", "--locked", "--manifest-path", "desktop/Cargo.toml", "--target", "x86_64-pc-windows-msvc")
    }
    & "$PSScriptRoot\package-windows.ps1"

    $Package = Join-Path $RepoDirectory "desktop\dist\Resona-win-x64"
    # Include the GCC runtime candidates. These are needed only if the core's
    # native libraries link them dynamically; copying is local to the package.
    foreach ($Dll in @("libgcc_s_seh-1.dll", "libwinpthread-1.dll", "libstdc++-6.dll")) {
        $Source = Join-Path $Msys2Root "ucrt64\bin\$Dll"
        if (Test-Path $Source) { Copy-Item $Source $Package }
    }
    $Licenses = Join-Path $Package "licenses"
    New-Item -ItemType Directory -Force $Licenses | Out-Null
    Copy-Item "$RepoDirectory\internal\audio\speexdsp\COPYING" "$Licenses\SpeexDSP.txt"
    foreach ($Name in @("gcc-libs", "libwinpthread", "winpthreads", "gcc")) {
        $Source = Join-Path $Msys2Root "ucrt64\share\licenses\$Name"
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
