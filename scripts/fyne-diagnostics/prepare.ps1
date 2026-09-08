param(
    [string]$OutputDirectory = (Join-Path $PSScriptRoot "..\..\bin\fyne-diagnostics")
)

$ErrorActionPreference = "Stop"
$projectDirectory = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot "..\.."))
$outputPath = [IO.Path]::GetFullPath($OutputDirectory)
$utf8 = New-Object System.Text.UTF8Encoding($false)

function Replace-Once([string]$Source, [string]$Before, [string]$After, [string]$Name) {
    $count = [regex]::Matches($Source, [regex]::Escape($Before)).Count
    if ($count -ne 1) {
        throw "Fyne diagnostics patch '$Name' expected one anchor; found $count. Review the toolkit changes before building."
    }
    return $Source.Replace($Before, $After)
}

Push-Location $projectDirectory
try {
    $moduleJSON = & go list -m -json fyne.io/fyne/v2
    if ($LASTEXITCODE -ne 0) { throw "Cannot resolve the Fyne module." }
    $module = ($moduleJSON -join "`n") | ConvertFrom-Json
    if ($module.Version -ne "v2.6.3" -or $module.Replace) {
        throw "The diagnostics renderer overlay supports unmodified fyne.io/fyne/v2 v2.6.3 only. Review the overlay for this module version/replacement."
    }
    # Go 1.26 rejects overlays directly below GOMODCACHE. Use a directory alias
    # and a disposable modfile; all actual source writes stay in $outputPath.
    [IO.Directory]::CreateDirectory($outputPath) | Out-Null
    $moduleAlias = Join-Path $outputPath "fyne"
    if (Test-Path -LiteralPath $moduleAlias) {
        $aliasItem = Get-Item -LiteralPath $moduleAlias
        if ($aliasItem.LinkType -ne "Junction" -or
            [IO.Path]::GetFullPath([string]$aliasItem.Target) -ne [IO.Path]::GetFullPath($module.Dir)) {
            throw "The existing Fyne alias is not the expected directory junction: $moduleAlias"
        }
    } else {
        New-Item -ItemType Junction -Path $moduleAlias -Target $module.Dir | Out-Null
    }
    $modfilePath = Join-Path $outputPath "diagnostics.mod"
    [IO.File]::WriteAllText($modfilePath, [IO.File]::ReadAllText((Join-Path $projectDirectory "go.mod")), $utf8)
    [IO.File]::WriteAllText((Join-Path $outputPath "diagnostics.sum"), [IO.File]::ReadAllText((Join-Path $projectDirectory "go.sum")), $utf8)
    & go mod edit "-modfile=$modfilePath" "-replace=fyne.io/fyne/v2=$moduleAlias"
    if ($LASTEXITCODE -ne 0) { throw "Cannot prepare the diagnostics modfile." }

    $driverPath = Join-Path $moduleAlias "internal\driver\glfw"
    $loopPath = Join-Path $driverPath "loop.go"
    $windowPath = Join-Path $driverPath "window.go"
    $loopSource = [IO.File]::ReadAllText($loopPath).Replace("`r`n", "`n")
    $windowSource = [IO.File]::ReadAllText($windowPath).Replace("`r`n", "`n")

    $loopSource = Replace-Once $loopSource `
        "func (d *gLDriver) repaintWindow(w *window) bool {`n" `
        ("func (d *gLDriver) repaintWindow(w *window) bool {`n" +
         "`t// Snapshot the revision-specific callback before any rendering begins.`n" +
         "`tonFrameDraw := timerDiagnosticsFrameCallback(w)`n" +
         "`tvar drawStarted time.Time`n" +
         "`tif onFrameDraw != nil {`n`t`tdrawStarted = time.Now()`n`t}`n") `
        "draw start"
    $loopSource = Replace-Once $loopSource `
        "`tif view != nil && visible {`n`t`tview.SwapBuffers()`n`t}" `
        ("`tif view != nil && visible {`n`t`tview.SwapBuffers()`n" +
         "`t`tif onFrameDraw != nil {`n`t`t`tonFrameDraw(drawStarted, time.Now())`n`t`t}`n`t}") `
        "visible buffer submission"
    $windowSource = Replace-Once $windowSource `
        "func (w *window) destroy(d *gLDriver) {`n" `
        ("func (w *window) destroy(d *gLDriver) {`n" +
         "`ttimerDiagnosticsFrameCallbacks.Delete(w)`n") `
        "window callback cleanup"

    # A leading underscore keeps generated toolkit files out of `go test ./...`.
    $sourcePath = Join-Path $outputPath "_source"
    [IO.Directory]::CreateDirectory($sourcePath) | Out-Null
    $generatedLoop = Join-Path $sourcePath "loop.go"
    $generatedWindow = Join-Path $sourcePath "window.go"
    $generatedHook = Join-Path $sourcePath "timer_diagnostics_frame.go"
    [IO.File]::WriteAllText($generatedLoop, $loopSource, $utf8)
    [IO.File]::WriteAllText($generatedWindow, $windowSource, $utf8)
    [IO.File]::WriteAllText($generatedHook, [IO.File]::ReadAllText((Join-Path $PSScriptRoot "frame_hook.go.txt")), $utf8)
    & gofmt -w $generatedLoop $generatedWindow $generatedHook
    if ($LASTEXITCODE -ne 0) { throw "Cannot format the Fyne overlay sources." }

    $replace = [ordered]@{}
    $replace[$loopPath] = $generatedLoop
    $replace[$windowPath] = $generatedWindow
    $replace[(Join-Path $driverPath "timer_diagnostics_frame.go")] = $generatedHook
    $overlayPath = Join-Path $outputPath "overlay.json"
    [IO.File]::WriteAllText($overlayPath, (@{ Replace = $replace } | ConvertTo-Json -Depth 5), $utf8)
    Write-Output $overlayPath
} finally {
    Pop-Location
}
