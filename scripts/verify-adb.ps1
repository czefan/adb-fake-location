#requires -Version 7.0
param(
    [string]$Serial = "",
    [ValidateSet("check", "test", "clean")]
    [string]$Action = "check",
    [ValidateRange(-90, 90)]
    [double]$Lat = 39.9042,
    [ValidateRange(-180, 180)]
    [double]$Lng = 116.4074,
    [ValidateRange(1, 600)]
    [int]$DurationSec = 10,
    [string]$AdbPath = "adb",
    [string]$BaseUrl = "http://127.0.0.1:8080"
)

$ErrorActionPreference = "Stop"
$script:TargetSerial = $Serial

function Invoke-AdbRead {
    param([string[]]$AdbArgs, [switch]$List)
    $processInfo = [Diagnostics.ProcessStartInfo]::new()
    $processInfo.FileName = $AdbPath
    $processInfo.UseShellExecute = $false
    $processInfo.CreateNoWindow = $true
    $processInfo.RedirectStandardOutput = $true
    $processInfo.RedirectStandardError = $true
    if (-not $List -and $script:TargetSerial) {
        $processInfo.ArgumentList.Add("-s")
        $processInfo.ArgumentList.Add($script:TargetSerial)
    }
    foreach ($argument in $AdbArgs) { $processInfo.ArgumentList.Add($argument) }
    $process = [Diagnostics.Process]::new()
    $process.StartInfo = $processInfo
    try {
        $null = $process.Start()
        $stdoutTask = $process.StandardOutput.ReadToEndAsync()
        $stderrTask = $process.StandardError.ReadToEndAsync()
        if (-not $process.WaitForExit(8000)) {
            $process.Kill($true)
            throw "ADB 命令超时"
        }
        $stdout = $stdoutTask.GetAwaiter().GetResult().Trim()
        $stderr = $stderrTask.GetAwaiter().GetResult().Trim()
        $validHelp = ($AdbArgs -join ' ') -eq 'shell cmd location help' -and
            $process.ExitCode -eq 255 -and $stderr -eq '' -and
            $stdout.StartsWith('Location service commands:') -and
            $stdout -notmatch 'Error:|Exception|Permission Denial|Unknown command'
        foreach ($command in @('providers', 'add-test-provider', 'set-test-provider-enabled', 'set-test-provider-location', 'remove-test-provider')) {
            if (-not $stdout.Contains($command)) { $validHelp = $false }
        }
        if ($process.ExitCode -ne 0 -and -not $validHelp) { throw "ADB 失败 ($($process.ExitCode)): $stderr $stdout" }
        return $stdout
    } finally { $process.Dispose() }
}

function Check-Device {
    Write-Host (Invoke-AdbRead -AdbArgs @("version") -List)
    $output = Invoke-AdbRead -AdbArgs @("devices", "-l") -List
    Write-Host $output
    $devices = @($output -split "\r?\n" | Where-Object { $_ -match "^\S+\s+(device|unauthorized|offline|recovery|sideload)\b" })
    if (-not $script:TargetSerial) {
        if ($devices.Count -ne 1) { throw "请连接唯一目标设备，或使用 -Serial 明确指定（离线设备也计入）。" }
        $script:TargetSerial = ($devices[0] -split "\s+")[0]
    }
    $selected = @($devices | Where-Object { ($_ -split "\s+")[0] -eq $script:TargetSerial })
    if ($selected.Count -ne 1 -or ($selected[0] -split "\s+")[1] -ne "device") {
        throw "目标设备未就绪，请检查连接和手机调试授权。"
    }
    $sdk = Invoke-AdbRead -AdbArgs @("shell", "getprop", "ro.build.version.sdk")
    Write-Host "SDK: $sdk"
    $helpText = Invoke-AdbRead -AdbArgs @("shell", "cmd", "location", "help")
    foreach ($command in @("providers", "add-test-provider", "set-test-provider-enabled", "set-test-provider-location", "remove-test-provider")) {
        if (-not $helpText.Contains($command)) { throw "设备缺少命令: $command" }
    }
    $uid = Invoke-AdbRead -AdbArgs @("shell", "id", "-u")
    if ($uid -notmatch "^\d+$") { throw "无法识别 ADB shell UID" }
    Write-Host "shell UID: $uid"
    Write-Host (Invoke-AdbRead -AdbArgs @("shell", "cmd", "appops", "get", $uid, "android:mock_location"))
    Write-Host "只读检查通过，尚未修改手机定位。"
}

# Mutating operations reuse the Go backend's locking, journal and cleanup.
# Run the revised tool first; do not keep a separate copy of mutation logic here.
function Invoke-LocalApi {
    param([string]$Path, [object]$Body)
    $parameters = @{
        Uri = "$($BaseUrl.TrimEnd('/'))$Path"
        TimeoutSec = 40
        Headers = @{ "X-Session-Token" = $script:SessionToken }
    }
    if ($null -ne $Body) {
        $parameters.Method = "Post"
        $parameters.ContentType = "application/json"
        $parameters.Body = $Body | ConvertTo-Json -Compress
    }
    $response = Invoke-RestMethod @parameters
    if ($response.success -eq $false) { throw $response.message }
    return $response
}

if ($Action -eq "check") { Check-Device; return }

$localUri = [Uri]$BaseUrl
if (-not $localUri.IsLoopback -or $localUri.Scheme -ne "http" -or $localUri.UserInfo -ne "") {
    throw "BaseUrl 必须是本地回环 HTTP 地址"
}
$script:SessionToken = ""
$bootstrap = Invoke-LocalApi -Path "/api/bootstrap"
if (-not $bootstrap.token) { throw "请先启动修订版工具；旧 demo 不支持安全会话接口。" }
$script:SessionToken = $bootstrap.token
$status = Invoke-LocalApi -Path "/api/status"

if ($Action -eq "clean") {
    if ($status.state -eq "模拟中") { throw "会话仍在运行，请在网页点击停止模拟。" }
    Write-Host (Invoke-LocalApi -Path "/api/recover" -Body @{}).message
    return
}
if ($status.state -ne "就绪") { throw "工具当前状态为 $($status.state)：$($status.error_message)" }
if ($Serial -and $status.device.serial -ne $Serial) { throw "运行中的工具未选中 $Serial，请使用 --serial 重启工具。" }
if ([double]::IsNaN($Lat) -or [double]::IsNaN($Lng)) { throw "坐标不能为 NaN" }

$testStarted = $false
try {
    Write-Host (Invoke-LocalApi -Path "/api/start" -Body @{
        latitude = $Lat; longitude = $Lng; coordinate_system = "WGS-84"
    }).message
    $testStarted = $true
    Write-Host "通过工具的单一定位任务持续注入 $DurationSec 秒；可在目标应用观察结果。"
    for ($second = 0; $second -lt $DurationSec; $second++) {
        Start-Sleep -Seconds 1
        $status = Invoke-LocalApi -Path "/api/status"
        if ($status.state -ne "模拟中") { throw "测试中断: $($status.error_message)" }
    }
} finally {
    if ($testStarted) {
        Write-Host (Invoke-LocalApi -Path "/api/stop" -Body @{}).message
    } else {
        Write-Warning "测试未成功启动；请检查网页状态，若为待清理则执行恢复。"
    }
}
