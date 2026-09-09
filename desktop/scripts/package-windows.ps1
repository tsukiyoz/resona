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
if ((Test-Path $OutputDirectory) -and -not (Test-Path "$OutputDirectory\.resona-package" -PathType Leaf)) {
    throw "Refusing to replace an unrecognized output directory: $OutputDirectory"
}
if (Test-Path $OutputDirectory) { Remove-Item $OutputDirectory -Recurse -Force }
New-Item $OutputDirectory -ItemType Directory | Out-Null
New-Item "$OutputDirectory\.resona-package" -ItemType File | Out-Null
Copy-Item "$DesktopDirectory\target\x86_64-pc-windows-msvc\release\resona-desktop.exe" "$OutputDirectory\resona-desktop.exe"
Copy-Item $CoreBinary "$OutputDirectory\resona-core.exe"
Write-Output $OutputDirectory
