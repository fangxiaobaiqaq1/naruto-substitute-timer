# 替身冷却计时器 · Android 原生版 架构契约（ARCHITECTURE.md）

本文件是所有 porter 共同遵守的契约。头文件 / Kotlin 桩里的声明是权威 API；本文件说明归属、
依赖、编码、线程和资源映射。改动共享契约（`include/nt/*.h` 中不属于自己的、`NativeCore.kt`、
`AnalysisResult`、`Session`/`Settings` 公开 API）时只做**最小的增量修改**，并在交付说明里写明。

- 包名 `com.narutotimer`，minSdk 31 / targetSdk 34 / compileSdk 35，AGP 8.7.3，Kotlin 2.0.21，
  Gradle 8.11.1（wrapper 已生成），NDK 27.2.12479018，CMake 3.22.1，ABI x86_64 + arm64-v8a。
- 无 AndroidX、无第三方 Kotlin 依赖（纯 framework API）。C++ 只依赖 vendored nlohmann/json。
- **忠实移植原则（继承本来的思想）**：算法、阈值、ROI、常量、模板名、判定顺序逐行对应 Go；
  保留 Go 里解释"为什么"的中文注释。只删掉 Windows/桌面/OCR(onnx, hudtext)/更新/诊断录制。
  测试暂不写。

---

## 1. 目录

```
android/
  settings.gradle.kts  build.gradle.kts  gradle.properties  gradlew(.bat)  gradle/wrapper/*
  app/build.gradle.kts                 # externalNativeBuild + syncRecognitionAssets（§5）
  app/src/main/AndroidManifest.xml
  app/src/main/res/{values/strings.xml, xml/accessibility_service_config.xml, drawable/ic_launcher.xml}
  app/src/main/cpp/
    CMakeLists.txt                     # libnarutocore.so = src/**/*.cpp + jni.cpp
    jni.cpp                            # architect
    include/nt/*.h                     # 每个 Go 包一个头文件（§3）
    include/third_party/nlohmann/json.hpp
    src/<module>/*.cpp                 # 每个 Go 文件一个 .cpp
  app/src/main/java/com/narutotimer/
    TimerApp.kt Settings.kt NativeCore.kt AssetLoader.kt      # architect
    tracker/{SideClock,Session,NinjaRules}.kt                 # kt-tracker
    capture/{ProjectionCaptureService,ScreenshotCapture,FrameLoop}.kt   # kt-capture-service
    service/TimerAccessibilityService.kt                      # kt-capture-service
    ui/ProjectionPermissionActivity.kt                        # kt-capture-service
    overlay/{OverlayController,MiniTimerView,OverlaySettingsView}.kt # kt-overlay-ui
    ui/MainActivity.kt                                        # kt-overlay-ui
```

## 2. 模块与归属

每个 `.cpp` 桩顶部写了 `Owner`、`Port of` 和要实现的函数清单。只编辑自己拥有的文件。

### cpp-match-detect
| Go | 目标 |
|---|---|
| internal/match/match.go | src/match/match.cpp |
| internal/match/prepared.go | src/match/prepared.cpp |
| internal/detect/beads.go | src/detect/beads.cpp |
| internal/detect/chrome.go | src/detect/chrome.cpp |
| internal/detect/content.go | src/detect/content.cpp |
| internal/detect/screen.go | src/detect/screen.cpp |
| internal/layout/transform.go | src/layout/transform.cpp |

- 头文件：`nt/match.h`、`nt/detect.h`、`nt/layout.h`（本模块拥有）。
- 公开 API：`match::NCC::Match`、`PrepareNCC`、`PreparedNCC::at`、`newGrayPrefix`、`coarseStep/refineStep`、
  `ToGray(RGBA*)`、`ToGray(Gray*)`、`ToGrayAsset(AssetImage)`、`ScaleGray`、`CropRGBA`；
  `detect::ParseMode/ComputeContentArea/ResolveContentArea/ContentArea::Map/DefaultBeads/DuelBeads/Layout/
  Classify/IsGold/ApplyIncrementRule/IsLegalPrefix/IsPossiblePrefix/CountLight/ClassifyScreen/GuessTopChrome/StripChrome`、
  三个 `ColorRange` 常量；`layout::New` + `Transform` 方法。
- 依赖：nt/image.h、nt/assets.h。
- 要点：NCC 的整数前缀和、`uint32` 点积分段（maxDotRun）、浮点公式与运算顺序必须与 Go 相同；
  `ToGrayAsset` 走 Go 通用路径（NRGBA 预乘 16 位 → >>8 → 整数亮度，见 image.h `GoLumaFromNRGBA`）。

### cpp-rgb
| Go | 目标 |
|---|---|
| internal/engine/rgb/rgb.go | src/rgb/rgb.cpp |
| …/special.go | src/rgb/special.cpp |
| …/blue_body.go, blue_glint.go, body_contrast.go, dark_glint.go, gold_body.go, gold_glint.go, purple_glint.go, purple_halo.go, purple_lobes.go, warm_glint.go, xiayin_palette.go | src/rgb/同名.cpp |

- 头文件：`nt/rgb.h`（公开：`rgb::Engine`）、`nt/rgb_internal.h`（本模块私有，可自由修改）。
- 公开 API：`rgb::Engine(layout)` / `rgb::Engine(layout, vision)`、`Prefer`、`AnalyzeAt`（实现 `engine::Engine`
  与 `engine::LayoutPreferrer`）。`AnalyzeAt(nullptr, …)` 必须返回 `Name == "rgb"`（NewGated 用它取名）。
- 依赖：match、detect、engine（`ConfiguredLayout` 用 `dynamic_cast` 识别，等价 Go 类型断言）、ninja（Reader/Tracker/AvatarTracker/Palette）、
  nt/parallel.h（parallelSides）。
- 要点：Go 的 `parallelSides`（camp‖duel、left‖right）用 `nt::ParallelInvoke`；每个并行体只写自己的槽。

### cpp-scene
| Go | 目标 |
|---|---|
| internal/scene/scene.go | src/scene/scene.cpp |

- 头文件：`nt/scene.h`（本模块拥有）。
- 公开 API：`scene::Load(cfg, err)`、`scene::NewGate(cfg, description, err)`、`Catalog`（`engine::Gate` +
  `engine::FightSupportGate`：`DecideAt`、`SupportsFight`、`ScoreAll`）、`ColorGate`。
- 资源：`AssetStore::Text("templates/manifest.json")`；模板/掩膜 `AssetStore::Image("templates/" + file)`，
  灰度一律 `match::ToGrayAsset`。Android 永远走 Go 的「内置清单」分支（`cfg.Scene.Manifest` 只用于判断是否为默认值）。
- 依赖：match、detect、engine（GateDecision 等类型）、config、nt/parallel.h（forEach）。
- 要点：Go map 迭代无序——凡是结果依赖迭代顺序的地方，保持 Go 的实际语义（通常是求最大值/计数，与顺序无关）；
  若 Go 结果确实依赖顺序，按 manifest 模板顺序处理并在注释说明。

### cpp-ninja
| Go | 目标 |
|---|---|
| internal/ninja/rules.go | src/ninja/rules.cpp |
| internal/ninja/reader.go | src/ninja/reader.cpp |
| internal/ninja/tracker.go | src/ninja/tracker.cpp |
| internal/ninja/avatar.go | src/ninja/avatar.cpp |

- 头文件：`nt/ninja.h`（公开）、`nt/ninja_internal.h`（本模块私有，可自由修改）。
- 公开 API：常量名（`Hashirama`…）、`DualCooldown`、`ShortLabel`、`Palette`、`Readout`、`Reader::NewReader/Read/ResolveEvidence`、
  `Tracker::Read/ReadWithAvatar`、`AvatarTracker::Read`、`NameRegion`、`AvatarRegion`、`LoadEmbeddedAvatarCatalog`、`AvatarStats`。
- 资源：`ninja_templates/<file>.png`（Go `templates/<file>.png`）、`ninja_templates/itachi_hyakusen_portrait.png`、
  `avatars/index.json` + `avatars/<id>.png`，完整性校验对照文本 `avatars/<id>.png.sha256`（Kotlin 计算原始 PNG 字节的 SHA-256）。
- 依赖：match、nt/assets.h、nt/json.h、nt/utf8.h、nt/parallel.h（Go parallelFor）。
- 线程：`Reader::mu`、`AvatarCatalog::mu` 复刻 Go 的锁；Tracker/AvatarTracker 每个只被一个并行体使用。
- 删除：`AvatarOptions` / `LoadAvatarCatalog`（外部开发目录）——Android 只有内置图鉴。

### cpp-identity
| Go | 目标 |
|---|---|
| internal/identity/identity.go | src/identity/identity.cpp |
| internal/identity/glyph_mask.go | src/identity/glyph_mask.cpp |

- 头文件：`nt/identity.h`（本模块拥有）。
- 公开 API：`SetMineNames`、`MineNames`、`LoadBook`、`Guesser(cfg)`、`Read`、`ReadFight`、`NormalizeName`、
  `TakePendingOppCrop`（供 jni.cpp）。
- 资源：AssetStore 前缀 `identity/`（直接子文件 = Go AssetDir）、`identity/seen/`、`identity/fight/`；
  只取 png/jpg/jpeg（不区分大小写）。目录扫描顺序 = AssetStore::List 的字典序（Go os.ReadDir 也是按文件名排序）。
- `saveOppCrop`：不写文件；节流 8 秒后把裁剪放入单槽（只保留最新）。Kotlin `AssetLoader.drainOppCrop` 取走、
  编码 PNG 写 `filesDir/identity/seen/opp-<yyyyMMdd-HHmmss>.png` 并注册回 AssetStore。Go 里它是 `go saveOppCrop(...)`，
  这里同步执行（只是一次裁剪拷贝）。
- 依赖：match、detect、config、nt/assets.h、nt/utf8.h、nt/time.h。

### cpp-gated-frame
| Go | 目标 |
|---|---|
| internal/engine/layout.go | src/engine/layout.cpp |
| internal/engine/configured_layout.go | src/engine/configured_layout.cpp |
| internal/engine/gated.go | src/engine/gated.cpp |
| internal/engine/factory/factory.go | src/engine/factory.cpp |
| internal/frame/frame.go（fillFromEngine 的 Status/Slots）+ internal/frame/source.go（AnalyzeImage、frameFingerprint、mixHash、dedupedAnalysis、boundedProvider 的 Sequence/Duplicate） | src/frame/frame.cpp |

- 头文件：`nt/engine.h`、`nt/factory.h`、`nt/frame.h`（本模块拥有）。
- 公开 API：`engine::DefaultLayout`、`ConfiguredLayout`、`Gated`、`NewGated/NewGatedWithGuess`、`pixelHash`、
  `hasBilateralHUD`、`readyBeads`；`factory::FromApp/DefaultConfig/New`；`frame::Pipeline`（jni 唯一入口）、
  `frameFingerprint`、`AnalyzeImage`、`DedupedAnalysis`、`MethodName`。
- `factory::New` = `NewGatedWithGuess(scene::NewGate(cfg.App), rgb::Engine(ConfiguredLayout(mode, cfg.App.Layout), cfg.App.Vision), adapt(identity::Guesser(cfg.App)))`。
  hudtext/OCR 包装（Go `NewLive`）不移植；`Result.TextStatus/TextError` 恒空。
- `Pipeline::Analyze(img, started, capturedAt, method)` = `DedupedAnalysis::analyze` → 交付判定（成功帧 `Sequence++`，
  `Duplicate = havePrevious && fingerprint == previous`）。`Frame::Reused` 标记命中去重缓存。
- 哈希：`nt/hash.h`（进程内 wyhash 替代 maphash，只需等值语义）。`pixelHash` 只哈希可见像素行（stride 填充不是观测）。
- 依赖：所有 C++ 模块。

### kt-tracker
| Go | 目标 |
|---|---|
| internal/app/timer.go（SideClock、DisplayTenths、FormatCD、NextTenthDelay） | tracker/SideClock.kt |
| internal/ui/run.go 状态机部分（captureOnce 的帧处理、observeRoundOpening、observeBeads、commitBeads、applyFight、applyScene、maybeAutoSide、updateHUD、statusLine、visibleReady、sceneName、refreshClockAt/refreshOverlay 的文本/颜色、nextTenthWait、poll、oppRemaining、detectedText、dualClockText、opponentNinja、clockColor、splitNames），internal/ui/ninja_display.go，internal/ui/settings.go 侧别规则，internal/ui/mini.go 的 setShowBothSides/setMiniMode/effectiveOpacity，internal/app/observation.go，internal/ui/theme.go 颜色 | tracker/Session.kt |
| internal/ninja/rules.go（DualCooldown、normalize、ShortLabel、名字常量、冷却时长） | tracker/NinjaRules.kt |

- 公开 API（其它 Kotlin 模块调用，签名已在桩中固定）：
  - `Session(settings, playerNamesSink)`：`onFrame(AnalysisResult)`（分析线程）、`pollIntervalMs()`、
    `nextTickDelayNanos(now)`、`overlayState(now): OverlayState`、`fighting`、`side`、`revision`、
    `swapSide()`、`lockSide(side)`、`selectSideMode(mode)`、`setRememberSide(b)`、
    `applySettings(names, ninjaQuery, sideMode, remember)`、`setShowBothSides(b)`、`setMiniMode(b)`、`resyncObservation()`。
  - `OverlayState`（不可变数据类）、`TimerColors`。
  - `SideClock` 方法与 Go 一一对应（时间 = `System.nanoTime()` 纳秒，0 = 零时刻）。
  - `NinjaRules.dualCooldown/normalize/shortLabel`。
- 依赖：`AnalysisResult`/`FrameBead`（NativeCore.kt）、`Settings`。
- 帧时间：用 `AnalysisResult.capturedAtNanos`（Go f.CapturedAt），不是分析完成时间。

### kt-capture-service
- 文件：capture/ProjectionCaptureService.kt、capture/ScreenshotCapture.kt、capture/FrameLoop.kt、
  service/TimerAccessibilityService.kt、ui/ProjectionPermissionActivity.kt。
- Go 来源（思想）：internal/ui/run.go `loop()`/`nextCaptureWait`、internal/frame/source.go `boundedProvider`（单 worker、不排队、超时丢弃）。
- 公开 API：`FrameLoop.start/stop/running/offer/offerFailure/stats`、`CapturedFrame`、
  `ProjectionCaptureService.start/stop/running`、`ScreenshotCapture.start/stop/running`、
  `TimerAccessibilityService.instance/isEnabled/startScreenshotFallback/stopScreenshotFallback`、`ProjectionPermissionActivity.launch`。
- 依赖：NativeCore、TimerApp（session/settings/nativeStatus）、OverlayController（由无障碍服务创建）、AssetLoader.drainOppCrop。

### kt-overlay-ui
- 文件：overlay/OverlayController.kt、overlay/MiniTimerView.kt、ui/MainActivity.kt。
- Go 来源：internal/ui/run.go `overlayContent/applyBothSideVisibility/applyMiniVisibility/applyPendingClock/clockLoop`，
  internal/ui/mini.go（迷你模式、控制按钮、拖动），internal/ui/settings.go `openSettings`（设置项），internal/ui/theme.go。
- 公开 API：`OverlayController(service, session, settings).show/hide/destroy/isShowing`、
  `MiniTimerView.render(OverlayState)` + `Listener`、`OverlaySettingsView`（悬浮窗内分组设置、输入和滑杆）。
- 依赖：Session/OverlayState（kt-tracker）、Settings、TimerApp、TimerAccessibilityService、ProjectionPermissionActivity、ProjectionCaptureService。

### architect（已完成）
jni.cpp、NativeCore.kt、AssetLoader.kt、Settings.kt、TimerApp.kt、`nt/{image,assets,hash,json,time,result,config,parallel,utf8}.h`、
src/core/{assets,parallel}.cpp、src/config/config.cpp、Gradle/Manifest/res/CMake。

---

## 3. C++ 公共约定（include/nt）

| 头文件 | 内容 |
|---|---|
| `nt/image.h` | `Point`/`Rect`（Go image 语义：Intersect 空返回 ZR、Inset、In、Eq 与 `==` 区分）、`RGBA`/`Gray` 视图（Pix 指向 Rect.Min，Stride 字节，SubImage 共享像素保留绝对坐标，`New` 零初始化，`Clone`）、Go 亮度/预乘 helper |
| `nt/assets.h` | `AssetStore::Instance()`：`AddImage(path,w,h,rgba,stride)`、`AddText`、`Image(path)`、`Text(path)`、`Has`、`List(prefix)`（字典序、递归）、`Revision()`；`AssetImage`（非预乘 RGBA8，`View()`、`RGBA16()`） |
| `nt/result.h` | `engine::Side`、`engine::BeadInfo`、`engine::Result`（字段与 Go 完全一致） |
| `nt/config.h` | `config::Default()`（识别核心用到的字段，逐值对应 Go） |
| `nt/time.h` | `TimeNs`/`DurationNs`（CLOCK_MONOTONIC ns，0 = 零时刻）、`NowNs()`、`SecondsOf` |
| `nt/hash.h` | `HashBytes/HashString/MixHash/ProcessHashSeed`（maphash 替身） |
| `nt/json.h` | nlohmann::json 别名 + `JsonNumber/JsonInt/JsonBool/JsonString`（缺失给默认值，等价 Go 零值） |
| `nt/parallel.h` | `ParallelFor(n, fn)`、`ParallelInvoke(a, b)`、`SetParallelEnabled`（常驻池，调用线程参与，嵌套安全） |
| `nt/utf8.h` | rune 语义：`DecodeRune`、`AppendRune`、`Runes`、`FromRunes`、`RuneCount`、`IsSpace`（unicode.IsSpace）、`TrimSpace` |

Go → C++ 翻译规则：
- `*image.RGBA` 参数 → `const nt::RGBA*`（nullptr == nil）；返回值按值（默认构造 == nil / 空图）。
- `(T, error)` → `bool f(..., T& out, std::string* err)` 或 `std::shared_ptr<T>`（nullptr == 错误/nil），见各头文件注释。
- `(T, bool)` → `std::pair<T,bool>` 或 `bool f(..., T& out)`。
- `time.Time` → `TimeNs`；`t.IsZero()` → `t == 0`；`time.Now()` 只在 Go 本身调用 `time.Now()` 的位置使用 `NowNs()`。
- 数值：`int(x)` 截断 → `static_cast<int>`；`math.Round` → `std::round`（同为远离零）；整数除法/取模语义相同；
  `uint8(...)` 的回绕显式写出；禁止 fast-math，`-ffp-contract=off`（不融合 FMA）。
- Go `string` 是字节串：`len`/下标按字节，`range` 按 rune → 用 `nt/utf8.h`。
- Go `sync.Mutex`：Engine 级（Gated、rgb::Engine、scene 的 prepareMu）可省略（单分析线程）；帧内并行共享的结构
  （ninja Reader/AvatarCatalog、scene hints）保留锁。
- 每个 .cpp 必须能在主机上通过：`g++ -std=c++17 -O2 -Wall -fsyntax-only -I app/src/main/cpp/include <file>`（jni.cpp 除外）。
- 异常：核心可以抛（例如 bad_alloc），jni.cpp 在边界全部捕获；核心逻辑不要用异常表达 Go error。

---

## 4. JNI 契约（jni.cpp ↔ NativeCore.kt）

方法通过 `RegisterNatives` 绑定到 `com.narutotimer.NativeCore` 的 `@JvmStatic external`：

| 方法 | 签名 | 说明 |
|---|---|---|
| `nativeInit()` | `()Z` | 清空 AssetStore、释放引擎 |
| `nativeAddImage(path, w, h, rgba, rowStride)` | `(String,int,int,ByteBuffer,int)Z` | direct ByteBuffer，非预乘 RGBA8；会复制 |
| `nativeAddText(path, text)` | `(String,String)Z` | UTF-16 → 严格 UTF-8（非 modified UTF-8） |
| `nativeFinishAssets()` | `()Z` | `config::Default()` + playerNames → `frame::Pipeline`（engine 构建）；失败看 `nativeLastError` |
| `nativeLastError()` | `()String` | |
| `nativeSetPlayerNames(names)` | `([String)V` | → `identity::SetMineNames`（Guesser 按 revision 重载名册） |
| `nativeAnalyzeHardwareBuffer(hb, capturedAtNanos, method, out)` | `(HardwareBuffer,long,int,ByteBuffer)I` | `AHardwareBuffer_lock(CPU_READ_OFTEN)` 零拷贝，格式须为 RGBA_8888/RGBX_8888 |
| `nativeAnalyzeBuffer(pixels, w, h, rowStride, capturedAtNanos, method, out)` | `(ByteBuffer,int,int,int,long,int,ByteBuffer)I` | direct RGBA8 |
| `nativeTakeOppCrop()` | `()[I` | `[w, h, argb...]` 或 null |
| `nativeResetDelivery()` / `nativeRelease()` / `nativeResultHeaderBytes()` | | |

`method`：0 = `android-media-projection`，1 = `android-accessibility-screenshot`，2 = `android-buffer`（进入帧指纹）。
`capturedAtNanos` ≤ 0 时 native 用 `NowNs()`；它同时作为 Go 的 `started` 与 `capturedAt`。

**返回值**：`>0` = 写入字节数；`0` = 未就绪/错误（`nativeLastError`）；`<0` = 输出缓冲太小，需要 `-r` 字节
（NativeCore 自动扩容重试一次）。

**结果编码 v1（小端，写入调用方预分配的 direct ByteBuffer，零 JNI 对象分配）**

| 偏移 | 类型 | 字段 |
|---|---|---|
| 0 | i32 | magic `0x4E545231`（'NTR1'） |
| 4 | i32 | 总字节数 |
| 8 | i32 | flags：bit0 Fighting，bit1 Uncertain，bit2 RoundOpening，bit3 Hold，bit4 HasErr，bit5 Duplicate，bit6 Reused |
| 12 | i32 | LeftSlots |
| 16 | i32 | RightSlots |
| 20 | i32 | Width |
| 24 | i32 | Height |
| 28 | i32 | beadCount |
| 32 | i64 | Sequence |
| 40 | i64 | CapturedAt (ns) |
| 48 | i64 | AnalysisStarted (ns，0 = 去重跳过) |
| 56 | i64 | AnalyzedAt (ns) |
| 64 | f64 | GateScore |
| 72 | 24×beadCount | 每颗：i32 X, i32 Y, i32 labelCode（`(side<<8)\|n`，side 0=L 1=R；-1 = 空），i32 flags（bit0 Lit，bit1 Gold，bit2 Unknown），f64 Conf |
| … | 13×(i32 len + UTF-8) | Name, LayoutProfile, Scene, LeftNinja, RightNinja, LeftNinjaCandidate, RightNinjaCandidate, PlayerSide, PlayerName, OppName, Status, Err, CaptureMethod |

Kotlin `NativeCore.Decoder` 每线程复用缓冲，字节相同的字符串复用上一帧的 `String`；解码为不可变的
`AnalysisResult`（engine.Result 全字段 + frame.Frame 元数据）。`AnalysisResult.frameBeads` = Go `fillFromEngine` 的
`frame.Bead` 换算；`slots(left, fallback)` = Go `Frame.Slots`。采集失败用 `AnalysisResult.failure(...)`。

全局锁：jni.cpp 的 `g_mu` 串行化 `nativeFinishAssets/nativeAnalyze*/nativeSetPlayerNames/nativeTakeOppCrop`，
所以识别核心只在一个线程上被调用（帧内并行另见 nt/parallel.h）。

---

## 5. 资源映射

Gradle 任务 `syncRecognitionAssets`（app/build.gradle.kts）把仓库资源同步到生成目录
`app/build/generated/...`，并通过 `variant.sources.assets.addGeneratedSourceDirectory` 加入 APK assets
（不写入 `src/main/assets`，不污染源码树）。key = APK assets 内的相对路径：

| 仓库 | APK assets / AssetStore key |
|---|---|
| `assets/templates/*.png`, `assets/templates/manifest.json` | `templates/…` |
| `assets/avatars/*.png`, `assets/avatars/index.json` | `avatars/…` |
| `assets/game/*.json` | `game/…`（当前识别链不用，保留） |
| `internal/ninja/templates/*.png`（排除 `*.source.png`） | `ninja_templates/…` |
| 用户：`filesDir/identity/**/*.{png,jpg,jpeg}` | `identity/<相对路径>` |

`AssetLoader.loadAll`（后台线程，TimerApp.onCreate 触发）：
1. `nativeInit()`；
2. 每个 PNG：读原始字节 → 注册文本 `<path>.sha256`（小写 hex，供 avatars/index.json 校验）→ 剥掉 iCCP/sRGB/gAMA/cHRM
   （Go image/png 不做色彩管理）→ `BitmapFactory`（`inPremultiplied=false`, ARGB_8888, `inScaled=false`）→
   `copyPixelsToBuffer`（内存序 R,G,B,A）→ `nativeAddImage`；
3. 每个 .json：UTF-8 文本 → `nativeAddText`；
4. 用户 identity 图片；`setPlayerNames`；`nativeFinishAssets()`。READY 之前 FrameLoop 丢弃所有帧。

C++ 从不解码 PNG。图鉴/模板全部是 8 位 PNG（灰度 / RGB / RGBA，无调色板、无 16 位），因此
BitmapFactory 的非预乘输出与 Go 解码逐字节一致；灰度转换用 `match::ToGrayAsset` 复刻 Go 预乘路径。
（用户 JPEG 账号名图的 IDCT 可能与 Go 有 ±1 差异，可接受。）

---

## 6. 线程模型

```
MediaProjection VirtualDisplay(1280×720) ─▶ ImageReader(RGBA_8888, maxImages=3)
   HandlerThread "nt-capture": acquireLatestImage → image.hardwareBuffer
        │  FrameLoop.offer(CapturedFrame)          ← 单槽 latest-frame-wins：被替换的旧帧立即 close()
        ▼
   单一分析线程 "nt-analysis"
        NativeCore.analyzeHardwareBuffer(hb, capturedAt, METHOD_PROJECTION)   // 零拷贝；native 全局锁
          └ native 帧内并行：nt::ParallelFor（≤3 个常驻池线程 + 本线程）
        TimerApp.session.onFrame(result)       // Session 内部锁；状态原子发布（revision++）
        AssetLoader.drainOppCrop(context)
        节奏：下一帧最早在 本帧开始 + session.pollIntervalMs()（Go nextCaptureWait，至少 1ms）
        ▼
   主线程（OverlayController）
        Handler 节拍：state = session.overlayState(now) → MiniTimerView.render(state)
        下一拍 = session.nextTickDelayNanos(now)（读秒时 4~110ms 对齐 0.1 秒，空闲 200ms）；revision 未变不重绘
```

- 无录屏时：`TimerAccessibilityService` 启用 `ScreenshotCapture`（takeScreenshot，约 3fps），同样 `FrameLoop.offer`；
  HardwareBuffer 不可 CPU 映射时改走 `Bitmap.wrapHardwareBuffer → copy → analyzeBuffer(METHOD_SCREENSHOT)`。
- 采集源切换/几何变化：native 指纹与 Gated 自动重同步；Kotlin 侧调用 `NativeCore.nativeResetDelivery()` 与
  `session.resyncObservation()`（Go ObservationSpace 变化）。
- 悬浮窗：`TYPE_ACCESSIBILITY_OVERLAY`（无障碍服务的 WindowManager），无需 SYSTEM_ALERT_WINDOW。
- 设置：从计时条操作栏直接打开第三个 `TYPE_ACCESSIBILITY_OVERLAY` 窗口；面板覆盖安全显示区域，
  保存、保存并退出、输入法、内容滚动、返回键和录屏授权都在悬浮窗生命周期内处理，不启动 `MainActivity`。
- Android 14：先 `startForeground(..., FOREGROUND_SERVICE_TYPE_MEDIA_PROJECTION)` 再 `getMediaProjection`；
  `createVirtualDisplay` 前必须 `registerCallback`；授权 Intent 只能用一次。

---

## 7. 构建与检查

```
export JAVA_HOME=$(ls -d ~/.local/opt/jdk-17*) ANDROID_HOME=~/Android/Sdk
cd android && ./gradlew assembleDebug
# 主机语法检查（每个 porter 交付前）：
g++ -std=c++17 -O2 -Wall -fsyntax-only -I app/src/main/cpp/include app/src/main/cpp/src/<module>/<file>.cpp
```

CMake：`-O3 -fno-fast-math -ffp-contract=off -fvisibility=hidden`，x86_64 加 `-msse4.1 -msse4.2 -mpopcnt`，
链接 android / nativewindow / jnigraphics / log，只导出 `JNI_OnLoad`。在所有模块实现之前 native 链接会因未定义符号失败，这是预期的。
