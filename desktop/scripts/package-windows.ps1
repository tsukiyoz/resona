param(
    [string]$CoreBinary = "$PSScriptRoot\..\..\build\bin\resona-core.exe",
    [string]$OutputDirectory = "$PSScriptRoot\..\dist\Resona-win-x64"
)

$ErrorActionPreference = "Stop"
$DesktopDirectory = (Resolve-Path "$PSScriptRoot\..").Path
$DistDirectory = [IO.Path]::GetFullPath((Join-Path $DesktopDirectory "dist"))
$OutputDirectory = [IO.Path]::GetFullPath($OutputDirectory)
if (-not $OutputDirectory.StartsWith($DistDirectory + [IO.Path]::DirectorySeparatorChar, [StringComparison]::OrdinalIgnoreCase)) {
    throw "OutputDirectory must be below $DistDirectory"
}
if (-not (Test-Path $CoreBinary -PathType Leaf)) {
    throw "resona-core.exe is missing: $CoreBinary"
}
if ($env:CGO_ENABLED -ne "1") {
    throw "Set CGO_ENABLED=1 and build resona-core with a working C compiler before packaging voice support."
}

cargo build --locked --manifest-path "$DesktopDirectory\Cargo.toml" --release --target x86_64-pc-windows-msvc
if ($LASTEXITCODE -ne 0) { throw "cargo build failed with exit code $LASTEXITCODE" }
$DesktopBinary = "$DesktopDirectory\target\x86_64-pc-windows-msvc\release\resona-desktop.exe"
# The PE subsystem must be GUI; hiding the core alone still opens a console.
$Reader = [IO.BinaryReader]::new([IO.File]::OpenRead($DesktopBinary))
try {
    $Reader.BaseStream.Position = 0x3c
    $PeOffset = $Reader.ReadInt32()
    $Reader.BaseStream.Position = $PeOffset
    if ($Reader.ReadUInt32() -ne 0x00004550) { throw "Invalid desktop PE header" }
    $Reader.BaseStream.Position = $PeOffset + 24 + 68
    if ($Reader.ReadUInt16() -ne 2) { throw "Desktop binary must use the Windows GUI subsystem" }
} finally { $Reader.Dispose() }
if (-not ("Resona.IconResourceCheck" -as [type])) {
    Add-Type -TypeDefinition @'
using System;
using System.Runtime.InteropServices;
namespace Resona {
    public static class IconResourceCheck {
        [DllImport("kernel32.dll", CharSet = CharSet.Unicode, SetLastError = true)]
        public static extern IntPtr LoadLibraryEx(string path, IntPtr file, uint flags);
        [DllImport("user32.dll", CharSet = CharSet.Unicode, SetLastError = true)]
        public static extern IntPtr LoadImage(IntPtr module, IntPtr name, uint type, int width, int height, uint flags);
        [DllImport("user32.dll")]
        public static extern bool DestroyIcon(IntPtr icon);
        [DllImport("kernel32.dll")]
        public static extern bool FreeLibrary(IntPtr module);
    }
}
'@
}
# Load as data without executing the application, using GPUI's resource ID.
$Module = [Resona.IconResourceCheck]::LoadLibraryEx($DesktopBinary, [IntPtr]::Zero, 2)
if ($Module -eq [IntPtr]::Zero) { throw "Cannot load desktop icon resources" }
try {
    foreach ($Size in @(16, 32, 48, 256)) {
        $Icon = [Resona.IconResourceCheck]::LoadImage($Module, [IntPtr]1, 1, $Size, $Size, 0)
        if ($Icon -eq [IntPtr]::Zero) { throw "Desktop application icon resource 1 is missing or invalid at size $Size" }
        [Resona.IconResourceCheck]::DestroyIcon($Icon) | Out-Null
    }
} finally { [Resona.IconResourceCheck]::FreeLibrary($Module) | Out-Null }
if ((Test-Path $OutputDirectory) -and -not (Test-Path "$OutputDirectory\.resona-package" -PathType Leaf)) {
    throw "Refusing to replace an unrecognized output directory: $OutputDirectory"
}
if (Test-Path $OutputDirectory) { Remove-Item $OutputDirectory -Recurse -Force }
New-Item $OutputDirectory -ItemType Directory | Out-Null
New-Item "$OutputDirectory\.resona-package" -ItemType File | Out-Null
Copy-Item "$DesktopDirectory\target\x86_64-pc-windows-msvc\release\resona-desktop.exe" "$OutputDirectory\resona-desktop.exe"
Copy-Item $CoreBinary "$OutputDirectory\resona-core.exe"
Write-Output $OutputDirectory
