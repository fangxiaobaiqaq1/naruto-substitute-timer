param([Parameter(Mandatory=$true)][string]$Version,[Parameter(Mandatory=$true)][string]$ReleaseDirectory,[string]$Compiler=$env:INNO_ISCC)
$ErrorActionPreference='Stop'
if ($Version -notmatch '^v?\d+\.\d+\.\d+$') { throw 'Use a release version such as v0.2.0.' }
$projectRoot=[IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..\..'))
$releasePath=[IO.Path]::GetFullPath($ReleaseDirectory)
if (-not $Compiler) { $found=Get-Command ISCC.exe -ErrorAction SilentlyContinue; if ($found) {$Compiler=$found.Source} }
if (-not $Compiler -or -not (Test-Path -LiteralPath $Compiler)) {throw 'Set INNO_ISCC to the full path of the Inno Setup 6 compiler.'}
if (-not (Test-Path -LiteralPath (Join-Path $releasePath 'timer-app.exe'))) {throw 'ReleaseDirectory must contain timer-app.exe and licenses/.'}
& $Compiler "/DAppVersion=$Version" "/DSourceRoot=$projectRoot" "/DReleaseDir=$releasePath" (Join-Path $PSScriptRoot 'timer.iss')
if ($LASTEXITCODE -ne 0) {throw 'Setup build failed.'}
