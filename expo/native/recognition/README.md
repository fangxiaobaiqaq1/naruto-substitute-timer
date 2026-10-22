# iOS recognition adapter (v0.3.0)

## Source/build contract

Compile in the **broadcast extension**, not the React Native bridge target:

- `RecognitionBridge.mm`, `VisionAccounts.mm`, `TimerSession.cpp` in this directory.
- Every `android/app/src/main/cpp/src/**/*.cpp` (currently 45 files); **exclude `jni.cpp`**.
- Header search: this directory and `android/app/src/main/cpp/include`, including its vendored `third_party/nlohmann/json.hpp`.
- C++17, exceptions/RTTI enabled, `-O3 -fno-fast-math -ffp-contract=off`.
- Objective-C++ files use ARC. Link libc++, Foundation, CoreVideo, CoreImage, CoreGraphics, ImageIO, **Vision**. CommonCrypto SHA-256 is provided by the system SDK.
- Swift bridging header imports `RecognitionBridge.h`. Existing Swift selectors are unchanged.
- Recommended deployment target: iOS 16+ (public accurate Simplified Chinese Vision OCR; the adapter checks language availability and reports failures).

The supplied CMake file also builds the portable core/tests on Linux/macOS without Apple recognition frameworks. An iOS CMake invocation must set `CMAKE_SYSTEM_NAME=iOS`; the Expo plugin can instead register the above sources directly with Xcode.

## Resource root

Use original files under a plain directory/folder resource, **not an asset catalog**. Disable PNG recompression (`COMPRESS_PNG_FILES=NO`); avatar integrity checks hash the original encoded bytes.

| Repository source | Extension resource path |
|---|---|
| `assets/templates/manifest.json` and `*.png` | `templates/` |
| `assets/avatars/index.json` and `*.png` | `avatars/` |
| `internal/ninja/templates/*.png`, excluding `*.source.png` | `ninja_templates/` |

`game/` is not read by native code. **Do not bundle the ONNX model in the extension:** iOS uses Vision for account text. The Android OCR C++ files remain in the build because shared preprocessing/evidence code references their portable helpers; no model is registered or loaded.

The loader reads only manifest-referenced scene PNGs, avatar-index entries, and non-source ninja PNGs. ImageIO decodes one image at a time into temporary RGBA; AssetStore retains only grayscale and alpha bits. Current assets: 32 scene images (0.8035 MiB luma), 242 avatars (1.3084 MiB), 18 ninja templates (0.0544 MiB), plus small masks/catalogue/prepared-template caches. It strips ICC/gamma chunks as Android does, verifies original avatar SHA-256, checks dimensions/budgets before allocating, and never resizes reference templates. Quartz's premultiply/unpremultiply can round semi-transparent edge RGB; opaque interiors are unaffected. Apple pixel/score validation remains necessary.

One live engine per extension process is enforced because the shared core has process-global account names and an immutable avatar catalogue. Another instance may reuse the same root after deallocation; changing roots requires a new process.

## Settings / state

`configure` is a partial update. Settings:

- `playerSide: auto|left|right`, `showBothSides`, `dual` (legacy manual estimate, only a resolved opponent), `backgroundFPS: 1..10`.
- `playerNames: string[]`, deduplicated, at most 20 names of 40 Unicode code points. If absent, legacy `playerName` supplies one name.
- `ninjaQuery`: explicit full opponent variant. It affects a resolved opponent only, never player-side inference.
- `rememberSide`: persistence policy belongs to the host. Explicit live manual choices remain valid even when false; host must not restore them in a new run when false.
- `layoutPreset: auto|camp|duel`.
- `calibration: null | {profile: camp|duel, leftBeads?, rightBeads?, leftAvatar?, rightAvatar?}`. Each region is `{x,y,width,height}` normalized to the entire oriented frame. Missing boxes stay automatic. Shared calibration size/shape validation rejects invalid updates atomically.
- `roiScale`, `roiOffsetX/Y`: legacy sliders translate/scale the shared default bead-box geometry, not a cropped toy detector. Explicit boxes take precedence. At identity slider values, automatic HUD anchoring stays enabled.

Results retain four pinned absolute deadlines, match/counts, detectedPlayerSide, message, frames/lastFrameAtMs. Additions: `leftNinja/rightNinja`, `leftSubstitutions/rightSubstitutions`, `scene`, `latencyMs`, `frameWidth/frameHeight` (also width/height), `playerName/opponentName`, `ocrStatus/ocrError`, and normalized `geometry.sides.left/right.beadBox/avatarBox` plus pixel bead centres. No frame pixels cross this API. Screenshot-based calibration is a separate host concern.

## Recognition and timing

- The shared `frame::Pipeline` performs scene gating, configured layout, 4/6-slot topology, special ninja/palette/avatar rules and image-template identity. No Android/JNI dependency or substitute pixel heuristic exists in the adapter.
- `VisionAccounts` replaces **only the account OCR backend**, using public on-device accurate Chinese/English Vision requests on title crops. It reuses shared `NameLayout`, `BuildRows`, `AccountSegment`, `Consensus`, `Evidence`, `ObserveBound`, `ValueState` and `DecideSide`.
- OCR requires two agreeing image treatments, then two distinct acquisition timestamps; glyph proofs/signatures are rechecked on subsequent pixels. Same-name accounts on both sides remain unresolved; exactly one verified other account can identify the opposite player side. Failed/stale readings never guess a side. Vision has no public character dictionary, so matching is conservative/strict instead of Android's unsupported-symbol wildcard rule.
- Vision is synchronous on the dedicated recognition worker, **not ReplayKit's delivery queue**. The host must keep at most one latest pending sample (never queue a backlog), retain its pixel buffer, and preserve capture time. OCR is skipped after side resolution, for manual side, and on duplicate acquisitions; failed OCR backs off five seconds.
- TimerSession preserves the WIP deadline contract, adds Android's 250 ms recovery/immediate-reuse evidence, source/topology resync, cadence EMA, verified round-opening reset, VS inherited baseline, consecutive end-scene evidence, confirmed identity, and opponent overrides. Candidates never enable special rules. Only confirmed one-bead drops start a timer; multi-drops/wipes never invent events. Two observations minimum; first candidate time pins deadlines. Both normal 15 s and exact Mei/Hanzo 10 s projections derive from one confirmed event.
- Unknown frames clear pending votes but retain a recent baseline only inside the cadence limit. Capture failure/pause/geometry change breaks the baseline, not existing deadlines. Duplicate pixels maintain continuity but never add votes. A heartbeat cannot create an observation.

## Tests and memory findings

From `expo/`:

```sh
bash native/recognition/tests/run.sh
```

This compiles all portable shared sources and runs TimerSession assertions. On Darwin it additionally builds/runs `PixelOrientationTest.mm`: asymmetric top-down BGRA colours through Core Image, all eight EXIF orientations. Production uses the same `PixelConversion.hpp` helper and probes actual bitmap row order once, rather than guessing an unconditional Y flip. **The Apple test has not run on the Linux development host.**

Optional Linux probes:

```sh
cmake -S expo/native/recognition -B /tmp/nt-ios-core -DCMAKE_BUILD_TYPE=Release
cmake --build /tmp/nt-ios-core --target ocr_memory_probe pipeline_memory_probe -j4
/tmp/nt-ios-core/ocr_memory_probe internal/ocr/neuraldata/recognizer.onnx 1600
/tmp/nt-ios-core/pipeline_memory_probe "$PWD" 图片_002_01.png
```

Measured Linux max RSS, serial core pool (not an iOS footprint guarantee):

- Shared model weights: 10.2633 MiB; encoded model: 10.355 MiB.
- Actual two concurrent OCR rows, widths 320/640/1280/1600: 25.2/25.9/35.9/40.8 MiB max RSS.
- Full shared pipeline + assets + real 1280×720 image + `ReadAccounts`: **57.4 MiB RSS**. Scene was fight/duel and real account text was read. This ruled out simply enabling the Android model inside a 50 MiB ReplayKit extension.
- iOS therefore does not load ONNX. Frames are downsampled uniformly to <=1280 width (<=3.52 MiB RGBA at 16:9), CI intermediate caching is disabled, large frame scratch is reused, the shared worker pool is disabled, and Vision sees only small title crops sequentially. Pause clears CI/scratch.

**Not yet proven:** Vision/framework/IOSurface peak process footprint, Chinese OCR speed/accuracy, NV12 colour parity and orientation on physical iPhones, and transparent PNG edge score parity. Vision is a public offline backend, not a promise that allocations run out-of-process or fit the extension. Measure `phys_footprint`/Instruments on oldest supported devices during cold startup, first OCR, repeated uncertain names, all scene transitions, 4/6-bead characters, pauses and calibration rebuild. Ship only after the extension stays below its real Jetsam limit. Never describe Linux host tests as a device-recognition validation.

## Remaining feature boundary

Imported glyph templates and persistent learned-opponent images are **not yet exposed** by this adapter. The shared identity matcher already supports them, but AssetStore has no per-key/prefix deletion, so clearing/replacing a user catalogue without disturbing globally cached base assets needs a small shared API addition (parent approval required). No Android shared source was modified. The single shared pending opponent crop is bounded but is not persisted by iOS. Account-name changes reset delivery/OCR evidence immediately and update the shared name catalogue.
