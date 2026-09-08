# Fyne renderer diagnostic boundary

`prepare.ps1` generates a Go build overlay for **Fyne v2.6.3** under
`bin/fyne-diagnostics`. It neither edits nor copies the entire dependency module.
Go 1.26 disallows overlays directly under the module cache, so the script creates
a Windows directory junction to the cached Fyne directory and a disposable
`diagnostics.mod`/`diagnostics.sum` pair with a local replacement pointing to that
alias. Only generated replacement files are written; the cached source and the
project's `go.mod`/`go.sum` are untouched.
The script checks the module version and requires each source patch anchor to
match exactly once. Dependency upgrades require review of this small overlay.

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/fyne-diagnostics/prepare.ps1
go build -modfile bin/fyne-diagnostics/diagnostics.mod -overlay bin/fyne-diagnostics/overlay.json -o bin/timer-app-debug.exe ./cmd/timer-app
```

The application discovers the additional method with an optional structural
interface, so ordinary `go build` and `go test` also work without the overlay:

```go
type frameDrawObserver interface {
    SetFrameDrawCallback(func(started, completed time.Time))
}
```

Install or replace the callback from the UI goroutine after applying a new
content revision. Capture that revision's valid frame and event IDs in the
closure. Fyne snapshots the closure before `repaintWindow` does its render work,
then invokes it after a visible window's `SwapBuffers` returns. The UI goroutine
does not process another `fyne.Do` callback in the middle of that render.

Callbacks are persistent: the application must consume each revision only once
and must not attribute a new sample to unchanged/invalid data. Set the callback
to `nil` to stop observing; window destruction also removes it. The callback
must be short and must not wait on the UI goroutine. Missing overlay capability,
hidden windows, a missing native viewport, or data that was superseded before
rendering must not produce a complete end-to-end sample.

`started` is captured before canvas texture/layout/render work. `completed` is
captured immediately after native buffer submission. This is a real renderer
boundary; it is **not physical display latency** (scan-out or compositor
presentation). `Refresh`, widget setters, `fyne.Do`, and callback function
durations are not equivalent boundaries.

The overlay changes only `internal/driver/glfw/loop.go` (draw boundaries) and
`window.go` (callback cleanup), and adds `timer_diagnostics_frame.go` (optional
interface implementation). In builds without it the application must report
the native draw boundary and the full end-to-end percentile series as
unavailable, while still reporting the earlier capture/analysis/UI stages.

An opt-in native smoke check drives three synthetic frames through real buffer
swaps and saves the associated raw PNGs and report. Its timings are not game
performance measurements:

```powershell
go build -modfile bin/fyne-diagnostics/diagnostics.mod -overlay bin/fyne-diagnostics/overlay.json -o bin/diagnostics-native-smoke.exe ./scripts/fyne-diagnostics/smoke
.\bin\diagnostics-native-smoke.exe -out debug/native-draw-verification
```
