param([string]$Bitrates = "32000,48000", [int]$Repeats = 3)
$ErrorActionPreference = "Stop"
Push-Location $PSScriptRoot
try {
    $stamp = Get-Date -Format "yyyyMMdd-HHmmss-fff"
    foreach ($model in @("regular", "little")) {
        & ".\resona-chain-$model.exe" -input ".\chain-input\manifest.json" -bitrates $Bitrates -repeats $Repeats -out ".\chain-$stamp-$model"
        if ($LASTEXITCODE -ne 0) { throw "Chain test failed for $model (exit $LASTEXITCODE)." }
    }
    Write-Host "Completed. Results: chain-$stamp-regular and chain-$stamp-little"
} finally { Pop-Location }
