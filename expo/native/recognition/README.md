# iOS recognition adapter — manual side selection

The user chooses **left or right** (default left). iOS does not recognize account names or infer the player's side. There is no Vision backend, account-template import, learned-opponent catalogue, or OCR model loading. Ninja/avatar, bean, scene and cooldown recognition remain automatic.

## Native build

Compile in the broadcast extension:

- `RecognitionBridge.mm` and `TimerSession.cpp` from this directory.
- All `android/app/src/main/cpp/src/**/*.cpp`; exclude `jni.cpp` and test files.
- Header search paths: this directory and `android/app/src/main/cpp/include`, including its vendored `third_party/` headers.
- C++17, exceptions/RTTI, `-O3 -fno-fast-math -ffp-contract=off`; ARC for Objective-C++.
- Link libc++, Foundation, CoreVideo, CoreImage, CoreGraphics and ImageIO. CommonCrypto SHA-256 is provided by the system SDK. **No Vision framework is required.**
- Swift bridging header imports `RecognitionBridge.h`. The current `analyze(_:orientation:capturedAtMs:capturedUptime:)` selector is preserved.

The complete portable shared source set is retained for linkage dependencies. This does not enable its OCR runtime: the pipeline's text wrapper is disabled, no account names/templates are supplied, and no model loader is registered. Do not remove individual shared OCR/identity translation units without resolving their shared linkage dependencies.

The CMake file builds the portable core/tests on Linux/macOS. Set `CMAKE_SYSTEM_NAME=iOS` to include the Apple bridge. The host/plugin must remove references to the deleted `VisionAccounts` and `IdentityTemplates` sources; those host files are outside this directory's ownership.

## Bundled assets

Copy original files as plain folder resources, not asset catalogs. Set `COMPRESS_PNG_FILES=NO`: avatar checksums verify original encoded bytes.

| Repository source | Resource path |
|---|---|
| `assets/templates/manifest.json` and `*.png` | `templates/` |
| `assets/avatars/index.json` and `*.png` | `avatars/` |
| `internal/ninja/templates/*.png`, excluding `*.source.png` | `ninja_templates/` |

Neither `game/`, account glyph images nor `ocr/recognizer.onnx` is needed. The loader decodes only manifest-referenced scene images, indexed avatars and non-source ninja templates. It validates image dimensions/budgets and original avatar hashes, strips colour-management chunks as Android does, and converts ImageIO/Quartz pixels to straight RGBA before registration. AssetStore retains grayscale and alpha masks, not RGBA copies. The generic PNG decoder now lives directly in `RecognitionBridge.mm`; no template-import API is involved.

Current luma footprint: approximately 0.80 MiB scene templates, 1.31 MiB avatars and 0.05 MiB ninja templates, plus masks and prepared caches. Frames are uniformly downsampled to <=1280 pixels wide; 1280×720 RGBA is 3.52 MiB. Intermediate CI caching and the shared parallel worker pool are disabled; scratch is reused and released on pause. Physical-device peak memory still requires measurement against the extension's actual Jetsam limit. Quartz alpha-edge rounding and NV12 colour parity also need device validation.

## Settings and output

`configure` accepts partial updates:

- `playerSide: left|right`, default left. The ObjC bridge rejects unsupported modes without changing settings. The standalone C++ session normalizes invalid modes to left.
- `showBothSides`, `dual` (manual alternate estimate for the opposite side only), `backgroundFPS: 1..10`.
- `ninjaQuery`: full opponent variant override, applied only opposite the manually selected player side.
- `rememberSide`: host persistence policy; live manual selection works independently of it.
- `layoutPreset: auto|camp|duel`: automatic **layout** selection is unrelated to player-side selection.
- `calibration: null | {profile: camp|duel, leftBeads?, rightBeads?, leftAvatar?, rightAvatar?}`. Boxes are `{x,y,width,height}` fractions of the whole oriented frame. Missing regions remain automatic; shared size/shape limits are enforced. Structural comparison avoids rebuilding on identical settings snapshots.
- Legacy `roiScale` and `roiOffsetX/Y` transform shared calibrated bead geometry. Explicit boxes take precedence. Default slider values preserve automatic HUD anchoring.

State contains four pinned deadlines, match/counts, ninja names, substitution totals, scene, latency, frame dimensions, frame count/time, message/error and normalized geometry. It does **not** expose detected player side, account names, OCR status or template counters. Selecting/switching sides changes only opponent interpretation; neither side's confirmed events/deadlines is reset.

## Capture and timing guarantees

The host must use a serial recognition worker and drop arrivals while busy rather than accumulate a backlog. `capturedUptime` is acquisition-time **CLOCK_MONOTONIC** seconds, paired with `capturedAtMs` epoch milliseconds. The bridge checks age using that same clock, rejects invalid/replayed/older-than-two-second deliveries, and maps acquisition time into the core epoch; worker delay never becomes the event timestamp. The existing Apple clock/sysctl fixes are unaffected.

TimerSession retains confirmed single-bead-drop detection, two-frame confirmation, first-candidate deadline pinning, 250 ms recovery/immediate-reuse handling, dynamic 4/6-slot geometry, exact Mei/Hanzo 15/10-second rules, scene/round transitions and calibration resync. Multi-bead drops do not invent substitutes. Unknown frames interrupt pending votes; duplicates can maintain continuity but never vote. Capture pause/failure/source changes discard visual baselines without stopping confirmed clocks.

## Runnable checks

From `expo/`:

```sh
bash native/recognition/tests/run.sh
```

This builds all portable shared sources and runs TimerSession and matching-kernel regressions. Manual-selection tests cover default left, switching both directions, opposite-side dual/variant overrides, unchanged event deadlines/counts and ignored shared side hints. Existing cooldown/scene/recovery tests remain.

On Darwin the same script also compiles and runs `PixelOrientationTest.mm`, checking an asymmetric BGRA image under all eight EXIF orientations. Production retains the same `PixelConversion.hpp` helper and one-time row-order probe. This Apple-framework test cannot execute on the Linux development host; CI/device validation remains required.
