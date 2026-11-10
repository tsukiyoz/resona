param(
    [Parameter(Mandatory)][ValidatePattern('^v\d+\.\d+\.\d+(-dev)?$')][string]$Version,
    [Parameter(Mandatory)][string]$Vpk
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest
$Root = (Resolve-Path "$PSScriptRoot\..\..").Path
$Dist = Join-Path $Root 'desktop\dist'
$Source = Join-Path $Dist 'Resona-win-x64'
$Stage = Join-Path $Dist 'Resona-installer-stage'
$Output = Join-Path $Dist 'installer'
foreach ($Name in @('resona-desktop.exe', 'resona_webrtc_apm.dll', 'resona-package.json', '.resona-package')) {
    if (-not (Test-Path (Join-Path $Source $Name) -PathType Leaf)) { throw "Missing Windows payload: $Name" }
}
$Info = Get-Content -Raw (Join-Path $Source 'resona-package.json') | ConvertFrom-Json
if ($Info.version -ne $Version -or $Info.distribution -ne 'portable') { throw 'Windows package metadata mismatch' }
# Only replace this script's marked staging/output directories.
foreach ($Directory in @($Stage, $Output)) {
    if (Test-Path $Directory) {
        if (-not (Test-Path (Join-Path $Directory '.resona-installer-build'))) { throw "Unrecognized output directory: $Directory" }
        Remove-Item $Directory -Force -Recurse
    }
    New-Item $Directory -ItemType Directory | Out-Null
    New-Item (Join-Path $Directory '.resona-installer-build') -ItemType File | Out-Null
}
Copy-Item "$Source\*" $Stage -Recurse -Force
$Info.distribution = 'installer'
[IO.File]::WriteAllText((Join-Path $Stage 'resona-package.json'), ($Info | ConvertTo-Json -Compress), [Text.UTF8Encoding]::new($false))
$Arguments = @('pack', '--packId', 'Resona', '--packVersion', $Version.TrimStart('v'),
    '--packDir', $Stage, '--mainExe', 'resona-desktop.exe', '--packTitle', 'Resona',
    '--packAuthors', 'tsukiyoz', '--runtime', 'win-x64', '--outputDir', $Output,
    '--icon', (Join-Path $Root 'desktop\assets\resona.ico'), '--delta', 'None', '--noPortable', '--skip-updates', '--yes')
& $Vpk @Arguments
if ($LASTEXITCODE -ne 0) { throw 'Velopack packaging failed' }
$Setups = @(Get-ChildItem $Output -Filter '*Setup.exe' -File)
$Packages = @(Get-ChildItem $Output -Filter '*-full.nupkg' -File)
if ($Setups.Count -ne 1 -or $Packages.Count -ne 1) { throw 'Expected exactly one installer and one full update package' }
Move-Item $Setups[0].FullName (Join-Path $Output 'Resona-Setup-unsigned.exe')
Move-Item $Packages[0].FullName (Join-Path $Output 'Resona-full.nupkg')
# Validate the payload before it can reach the release signing job.
Push-Location $Root
try {
    go run ./tools/validate-installer -package (Join-Path $Output 'Resona-full.nupkg') -version $Version
    if ($LASTEXITCODE -ne 0) { throw 'Installer package validation failed' }
} finally { Pop-Location }
Write-Warning 'Windows Authenticode is not configured: this installer is UNSIGNED.'
Write-Host "Installer: $Output\Resona-Setup-unsigned.exe"
