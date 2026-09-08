$ErrorActionPreference = "Stop"
Set-Location $PSScriptRoot\..

$bin = Join-Path (Get-Location) "bin"
$env:PATH = "$bin;" + $env:PATH
$env:NARUTO_DEBUG = "1"
$env:CGO_ENABLED = "1"

if (-not (Test-Path "$bin\timer-app-debug.exe")) {
    & .\build.bat
    if ($LASTEXITCODE -ne 0) { throw "构建失败" }
}

& "$bin\timer-app-debug.exe"
