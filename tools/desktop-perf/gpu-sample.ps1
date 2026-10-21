# Samples per-process GPU utilisation for a running desktop build.
#
# The Windows "GPU Engine" counter is machine-normalised and reported once per
# engine per second; Task Manager's GPU column is the maximum across engines.
# To compare builds it is enough, and more stable, to sum every engine that
# belongs to the process id. Dedicated GPU memory is sampled from
# "GPU Process Memory" so the two numbers describe the same window.
#
# Usage:
#   .\gpu-sample.ps1 -ProcessId 1234 -Seconds 15
#   .\gpu-sample.ps1 -ProcessName resona-desktop -Seconds 45 -Json out.json
#
# Requires the process to be running; this script never starts or stops it.

[CmdletBinding()]
param(
    [Parameter(Mandatory, ParameterSetName = 'Id')]
    [int]$ProcessId,

    [Parameter(Mandatory, ParameterSetName = 'Name')]
    [string]$ProcessName,

    [ValidateRange(3, 600)]
    [int]$Seconds = 15,

    # Optional: write the per-second samples as JSON for the report.
    [string]$Json,

    # Optional: label recorded in the output, e.g. "visible-idle".
    [string]$Label = ''
)

$ErrorActionPreference = 'Stop'

if ($PSCmdlet.ParameterSetName -eq 'Name') {
    $target = Get-Process -Name $ProcessName -ErrorAction SilentlyContinue |
        Sort-Object StartTime -Descending | Select-Object -First 1
    if (-not $target) { throw "no running process named '$ProcessName'" }
} else {
    $target = Get-Process -Id $ProcessId -ErrorAction SilentlyContinue
    if (-not $target) { throw "no running process with id $ProcessId" }
}
$ProcessId = $target.Id

# --- window state -----------------------------------------------------------
# IsIconic tells "minimised"; the foreground window tells "visible but behind".
if (-not ('ResonaPerf.Win32' -as [type])) {
    Add-Type -Namespace ResonaPerf -Name Win32 -MemberDefinition @'
[DllImport("user32.dll")] public static extern bool IsIconic(IntPtr hWnd);
[DllImport("user32.dll")] public static extern bool IsWindowVisible(IntPtr hWnd);
[DllImport("user32.dll")] public static extern IntPtr GetForegroundWindow();
'@
}
$hwnd = $target.MainWindowHandle
$windowState = if ($hwnd -eq [IntPtr]::Zero) {
    'no-window'
} elseif ([ResonaPerf.Win32]::IsIconic($hwnd)) {
    'minimized'
} elseif ([ResonaPerf.Win32]::GetForegroundWindow() -eq $hwnd) {
    'foreground'
} elseif ([ResonaPerf.Win32]::IsWindowVisible($hwnd)) {
    'visible-background'
} else {
    'hidden'
}

# --- CPU baseline -----------------------------------------------------------
$cpuBefore = $target.TotalProcessorTime
$started = Get-Date

# --- GPU sampling -----------------------------------------------------------
$gpuCounter = '\GPU Engine(*)\Utilization Percentage'
$memCounter = '\GPU Process Memory(*)\Dedicated Usage'

$gpuSamples = @()
$memSamples = @()
try {
    $sets = Get-Counter -Counter @($gpuCounter, $memCounter) -SampleInterval 1 -MaxSamples $Seconds -ErrorAction Stop
} catch {
    # Counters can be missing on remote/VM adapters; report rather than crash.
    throw "GPU performance counters unavailable: $($_.Exception.Message)"
}

$gpuFilter = "*pid_${ProcessId}_*"
$memFilter = "*pid_${ProcessId}_*"
foreach ($set in $sets) {
    $gpu = 0.0
    $mem = 0L
    foreach ($sample in $set.CounterSamples) {
        if ($sample.Path -notlike "$gpuFilter" -and $sample.Path -notlike "$memFilter") { continue }
        if ($sample.Path -like "*Utilization Percentage*") {
            $gpu += [double]$sample.CookedValue
        } elseif ($sample.Path -like "*Dedicated Usage*") {
            $value = [long]$sample.CookedValue
            if ($value -gt $mem) { $mem = $value }
        }
    }
    $gpuSamples += [math]::Round($gpu, 4)
    $memSamples += $mem
}

$target.Refresh()
if ($target.HasExited) { throw "process $ProcessId exited during sampling; discard this capture" }
$cpuAfter = $target.TotalProcessorTime
$wall = ((Get-Date) - $started).TotalSeconds
$cores = [Environment]::ProcessorCount
$cpuPercent = if ($wall -gt 0) {
    [math]::Round(($cpuAfter - $cpuBefore).TotalMilliseconds / ($wall * 1000) / $cores * 100, 3)
} else { 0 }

$summary = [pscustomobject]@{
    label        = $Label
    pid          = $ProcessId
    exe          = $target.Path
    windowState  = $windowState
    seconds      = $gpuSamples.Count
    gpuMean      = if ($gpuSamples) { [math]::Round((($gpuSamples | Measure-Object -Average).Average), 4) } else { 0 }
    gpuPeak      = if ($gpuSamples) { ($gpuSamples | Measure-Object -Maximum).Maximum } else { 0 }
    gpuNonZero   = @($gpuSamples | Where-Object { $_ -gt 0 }).Count
    gpuSeries    = ($gpuSamples -join ', ')
    dedicatedMB  = if ($memSamples) { [math]::Round((($memSamples | Measure-Object -Maximum).Maximum / 1MB), 1) } else { 0 }
    cpuPercent   = $cpuPercent
    workingSetMB = [math]::Round($target.WorkingSet64 / 1MB, 1)
}

$summary | Format-List

if ($Json) {
    $payload = [pscustomobject]@{
        summary = $summary
        gpu     = $gpuSamples
        memory  = $memSamples
    }
    $payload | ConvertTo-Json -Depth 4 | Set-Content -LiteralPath $Json -Encoding utf8
    "json written: $Json"
}
