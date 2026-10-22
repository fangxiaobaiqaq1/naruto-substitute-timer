# NarutoCapture (iOS 16.4+)

Local Expo module, real ReplayKit Broadcast Upload extension and native sample-buffer PiP. No App Groups or paid-team entitlement; no private overlay API, automatic recording consent, silent audio or JS background timer.

## Runtime contract

`NarutoCapture` exposes `getState`, `prepare`, `startPictureInPicture`, `stop`, `setSettings`, `onState`, and its same-name Expo view. The view contains the visible `AVSampleBufferDisplayLayer` preview and genuine `RPSystemBroadcastPickerView`; it never programmatically taps or bypasses the system picker. The picker discovers the installed upload extension's actual bundle ID, including after re-signing.

Additional methods:

- `pickCalibrationImage(): Promise<{uri,width,height}>`: Files image picker; scoped file, local cache copy, <=64 MiB / 100 MP; no Photos permission and no image over IPC.
- `importCalibration(): Promise<string>`: Files JSON picker; streamed UTF-8 input <=64 KiB.
- `shareDocument(name,text): Promise<void>`: native share sheet for temporary file, <=1 MiB, sanitized filename. The temporary export is removed when sharing completes.

Settings include the existing controls plus `ninjaQuery`, `rememberSide`, up to 20 `playerNames` (40 characters each), and normalized calibration rects. New diagnostics include `pipPossible`, `pipPending`, `stale`, `renderedFrames`, ninja names, substitution counts, scene, frame dimensions, OCR/errors, `latencyMs` (recognition only), and `deliveryLatencyMs` (capture-to-host).

## Lifecycle / security

1. Prepare while foreground. Binding failure/absent extension is a real error.
2. Start PiP with its preview visible and wait for `pipActive`, not just `pipPending`.
3. Use the system picker and explicitly consent to recording. The upload extension connects to `127.0.0.1:17861`.
4. Switch to the game only after PiP is active and the broadcast is connected.

Only newline-framed JSON metadata goes over the loopback-bound listener (16 KiB bound, version, per-connection UUID and increasing sequence checks). A broadcast UUID distinguishes reconnects from a fresh recording. This prevents stale/accidental replay; **it is not cryptographic authentication against a malicious co-resident app**. Screen and audio samples never leave the extension. Audio samples are ignored.

Recognition runs synchronously before ReplayKit releases each video sample, throttled to 1–10 FPS. Pause/dropout invalidates visual continuity, not confirmed timers. The native host renders fresh PiP sample buffers independently of JS; audio session is playback/moviePlayback with mixWithOthers, but plays no audio. Confirmed epoch deadlines are pinned once to a shared CLOCK_MONOTONIC domain, so wall-clock jumps and repeated uncertain frames cannot reset them. Explicit engine round resets/new broadcast sessions can clear timers.

No frame for 3 seconds makes recognition stale; no protocol packet for 6 seconds disconnects the host. The extension ends recording after an 8-second missing-pong timeout (10-second initial handshake/reconnect allowance). Closing PiP, audio interruption, or leaving the app without active PiP stops capture honestly. An already-confirmed countdown can continue with an explicit stale warning after transport loss.

## Build

Use `./plugins/withReplayKit` in app.json. Optional `extensionBundleIdentifier` must be below the app bundle ID. The plugin stages sources/assets into generated `ios/NarutoBroadcast`, creates the `NarutoBroadcast` extension target, explicit app target dependency and PlugIns copy phase, C++17 / ObjC++ / Swift bridging header / required Apple frameworks, `APPLICATION_EXTENSION_API_ONLY=YES`, `SKIP_INSTALL=YES`, and iOS 16.4. It adds `UIBackgroundModes=audio` and EAS extension credential metadata; it does not require App Groups or disable signing globally.

The repo must include `android/app/src/main/cpp/{include,src}`, `expo/native/recognition`, `assets/{templates,avatars,game}`, `internal/ninja/templates`, and `internal/ocr/neuraldata/recognizer.onnx` with licenses. No JNI/Android framework is built. PNG assets are copied byte-for-byte to preserve hashes. The product is `NarutoBroadcast.appex/RecognitionAssets/{templates,avatars,game,ninja_templates,ocr}`.

Autolinking explicitly selects the single root `NarutoCapture.podspec`. `legacy/*.disabled` preserves older ignored WIP, excluded from compilation; the runtime is `ios/CaptureHost.swift` plus its protocol, renderer, document helper and Expo module.

Checks: `node modules/naruto-capture/checks/check.cjs` from expo/ verifies fresh-template dependencies, idempotent target/phase generation, flags, asset hashes and pbxproj round-trip. On macOS it also compiles/runs the Foundation-only Swift protocol/deadline tests. A full macOS Release arm64 unsigned app build, signing/install test, and physical-device ReplayKit/PiP/background/interrupt/memory test remain necessary; Linux prebuild checks cannot establish iOS compile or device success.
