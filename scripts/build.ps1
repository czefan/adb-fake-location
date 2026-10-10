param([string]$Output = "")

$ErrorActionPreference = "Stop"
$projectRoot = Split-Path -Parent $PSScriptRoot
if ($Output -eq "") { $Output = Join-Path $projectRoot 'dist\adb-fake-location.exe' }
$outputPath = [IO.Path]::GetFullPath($Output)
New-Item -ItemType Directory -Path (Split-Path -Parent $outputPath) -Force | Out-Null
Push-Location $projectRoot
try {
    & go build -trimpath -o $outputPath ./cmd/adb-fake-location
    if ($LASTEXITCODE -ne 0) { throw "Go build failed ($LASTEXITCODE)" }
    Write-Output "已构建：$outputPath"
} finally { Pop-Location }
