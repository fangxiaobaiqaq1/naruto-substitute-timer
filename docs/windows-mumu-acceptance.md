# Windows / MuMu acceptance checklist

This checklist is intentionally executable on the target host. Linux cross-build
checks do **not** validate the MuMu SDK, Fyne/CGO renderer, or runtime capture.

## Prerequisites

- Windows x64, Go 1.26.1, Windows PowerShell, and the x64 C/C++ toolchain required by Fyne with `CGO_ENABLED=1`. Use an x64 Developer PowerShell or an MSYS2 UCRT64 environment whose compiler, `pkg-config`, headers, and Windows SDK libraries are on `PATH`; do not use `CGO_ENABLED=0` as a substitute.
- A running MuMu instance with `com.tencent.KiHan` installed.
- MuMu SDK DLL discoverable from the selected MuMu installation.
- Optional: GStreamer if replaying the supplied MP4 evidence.

## Build and identify artifacts

From the repository root in a Windows PowerShell:

```powershell
$env:TIMER_VERSION = 'acceptance-local'
cmd /c build.bat
if ($LASTEXITCODE -ne 0) { throw 'build.bat failed' }
Get-ChildItem bin\*.exe | Get-FileHash -Algorithm SHA256
```

Confirm that `bin\timer.exe`, `bin\timer-gui.exe`, `bin\timer-app.exe`,
`bin\timer-app-debug.exe`, and `bin\timer-lab.exe` exist. Inspect PE machine
architecture with a Windows PE tool available on the build host (for example
`dumpbin /headers bin\timer-app.exe`).

## Focused recognition tests

```powershell
go test ./internal/ninja -run 'TestReviewedTargetTitlesRecognizeWithoutGameplayInference|TestObitoCurrentTemplateRejectsPartialVersion|TestSasukeXiayinNameAndBoundedGeometryHint|TestItachiHyakusenMuMuVideoTitles'
go test ./internal/engine -run TestConfiguredLayoutMapsMuMuReferenceAcrossResolutions
```

The Itachi video test needs extracted PNG frames under `$env:NARUTO_VIDEO_FRAMES`:

```powershell
$env:NARUTO_VIDEO_FRAMES = 'C:\temp\naruto-video-frames'
# Create the two child directories matching the MP4 basenames, then extract
# one PNG per second with the locally installed GStreamer tooling.
go test ./internal/ninja -run TestItachiHyakusenMuMuVideoTitles -count=1
```

## MuMu runtime acceptance

1. Start MuMu and the game; select the exact installation/instance in the app
   settings when multiple instances are present.
2. Use the capture probe and confirm SDK connection, display selection, and a
   non-empty current screenshot.
3. `capture.timeoutMs` remains the synchronous observation deadline. Runtime
   MuMu gives the helper a separate 15-second hard transaction ceiling
   (startup, DLL/SDK, capture, PNG, protocol, close/reap), but a caller that
   reaches `capture.timeoutMs` immediately receives a held timeout while the
   helper is cancelled and reaped. Confirm a normally slow helper that still
   completes within the observation deadline produces a current frame. Then
   deliberately cause one SDK capture to stall (for example by pausing the
   emulator while the app is capturing). Confirm the app reports a timeout for
   that observation without blocking the UI for 15 seconds, does not overlap a
   new helper, and resumes with a current valid frame after reap and recovery.
   Do not accept a frame completed by the timed-out helper as evidence. In
   `capture.jsonl`, verify `source_revision` plus non-sensitive helper
   request/deadline/outcome/reap and origin/terminal fields classify the
   initiating timeout and later `busy`
   attempts (`busy`, `timeout`, `helper_launch`, `helper_protocol`,
   `sdk_worker`, or `success`) without paths, tokens, or image data. Close the
   app while another forced stall is active; confirm its helper terminates
   before the app exits and no helper remains in Task Manager.
4. At 1280x720, 1600x900, and 1920x1080, confirm the HUD is not marked
   `unsupported-resolution` and bean centers follow the game content area.
5. In live fights, verify:
   - 百战鼬: right title is recognized and its energy-row four beans are read.
   - 神驹佑祥斑 / 木叶创立柱间: six current beans and color states.
   - 九喇嘛连结水门: title and energy-row geometry.
   - 十尾带土 / 侠隐佐助: title recovery after a visible current-frame move and
     unknown state during a genuinely obscured title.
6. Enable the optional three-minute diagnostic replay during a representative
   fight, then stop diagnostics. Confirm `replay/manifest.json` has schema 2,
   complete `raw_path`/`annotated_path` pairs only, and that each annotation
   describes its own frame rather than a later UI state. Export the small replay
   ZIP and use “Open folder”; confirm the support ZIP contains no game images
   unless its explicit privacy option was selected.
7. Record an SDK diagnostic bundle for any mismatch; do not treat an old
   identity or old bean state as validation. For unstable 十尾带土 detection,
   retain original-resolution consecutive frames plus title/avatar scores and
   purple-bean diagnostics; do not lower global thresholds based on lossy
   replay JPEGs alone.
