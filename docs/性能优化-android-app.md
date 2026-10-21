# 安卓 App 性能优化（采集 → 分析 → 悬浮窗）

基线 v0.25 @ `3256514`。只改 Kotlin（`android/app/src/main/java/**`）；C++ 识别核心、Go、`android/ARCHITECTURE.md` 未动。
目标：采集 → 分析 → 悬浮窗这条链少用 CPU / 电 / 内存；识别结果除「帧节奏」外不变。

## 改动

| 提交 | 内容 | 行为变化 |
|---|---|---|
| `841749d` | 帧节奏：按 `session.pollIntervalMs()` 限速分析 | **有**：非对局只分析 ≤2.5 帧/秒（同 Windows） |
| `397bffe` | 无障碍截屏兜底：复用暂存缓冲 | 无（逐字节相同） |
| `21210ed` | 录屏逐张取帧（`acquireNextImage`） | 无（仍是最新帧胜出） |
| `41c95d4` | 资源只加载 native 会读的部分 | 无（native 读到的内容逐字节相同）；诊断计数变为「图片 292 · 文本 2」 |
| `53161bc` | 悬浮窗：对局外改为推送刷新 | 无（显示内容相同，非对局更新更早出现） |

### 1. 帧节奏（`capture/FrameLoop.kt`）

ARCHITECTURE §6 写的是「下一帧最早在 本帧开始 + session.pollIntervalMs()」（Go `nextCaptureWait`），但 FrameLoop
从不等待，`Session.pollIntervalMs()` 无人调用：大厅、VS、桌面等非对局画面按录屏帧率（60–120 Hz）满速分析。现在：

- 分析线程取帧前等到 `上一帧开始 + pollIntervalMs`（对局 16 ms，否则 400 ms）；间隔在 `onFrame` 之后读取，
  所以第一帧对局画面立即切到 16 ms。
- 等待期间新到的帧照旧替换单槽 `pending`（最新帧胜出，旧帧立即 close，计入「丢弃」）；deadline 之前 `offer`
  不再唤醒分析线程（否则每帧白醒一次）。
- 分析慢于间隔时不等待；deadline 之后到的帧立即开始。**不加** Go 的 1 ms 下限：帧是推送来的，不会空转，下限只会加延迟。
- 有校准取帧请求（`requestSnapshot`）时，它接受的那一帧跳过等待，校准不变慢。

行为变化（有意，同 Windows）：非对局时最多每 400 ms 分析一帧，进入对局最晚约 400 ms 后才被看到（`enterFightFrames=1`，
看到第一帧即进入对局并切到 16 ms）。对局中 60 Hz 源帧间隔 16.7 ms > 16 ms，基本不受影响（唤醒抖动下至多亚毫秒）；90/120 Hz 时分析上限约 62 帧/秒，
被分析的帧最多多等一个源帧（≤8–11 ms）。离开对局仍需 10 帧结束场景（此时仍按 16 ms）。诊断里的「丢弃」在非对局时会快速增长（按节奏跳过的帧）。

### 2. 无障碍截屏兜底（`FrameLoop.analyzeViaBitmap`）

只在 JNI 锁不住 HardwareBuffer 的设备上走（本模拟器不走）。原来每帧 `IntArray(w*h)` + `allocateDirect(w*h*4)`
（2400×1080 时约 20 MB Java 堆垃圾）再逐像素 `put`。现在 `copyPixelsToBuffer` 进一个复用的 direct buffer；
alpha 全为 255 的行原样保留（预乘与非预乘相同），其余行或非紧凑 / 非 sRGB 位图按原来的 `getPixels` 换序重写，
所以字节与原实现完全相同。缓冲在 `stop()` 或一帧 HardwareBuffer 分析成功后释放。
验证：4 帧真实 1080×2400 截屏 + 预乘/非预乘/Display-P3 合成位图逐字节相等；转换 270–610 ms → 20–26 ms（JIT 预热后）。

### 3. 录屏取帧（`capture/ProjectionCaptureService.kt`）

`acquireLatestImage` 在「分析中 1 张 + pending 1 张 = 2 张」时再取 1 张后还会试第 4 张（maxImages=3），
ImageReader 每帧打一条 `Unable to acquire a buffer item` 警告（模拟器对局约 10 条/秒，挤掉有用日志、白耗 logd）。
现在 `acquireNextImage` 循环到队列空，每张都 `offer`（FrameLoop 替换时关闭旧帧），同一时刻最多持有 3 张，仍是最新帧胜出。
**maxImages 保持 3**：分析中 + pending + 正在取的最新帧正好 3 张；改成 2 会在分析忙时取不到最新帧（静止画面上最后一帧可能永远不分析）。

### 4. 资源加载（`AssetLoader.kt`）

对照 C++ 源码确认 native 只读：`templates/manifest.json` 及其引用的模板（scene.cpp `readGray`）、
`avatars/index.json` + `avatars/<id>.png` + `avatars/<id>.png.sha256`（avatar.cpp）、`ninja_templates/*`（reader.cpp）、`identity/**`。

- SHA-256 只算 `avatars/*.png`（少算 53 个，模板/忍者名/用户图的 `.sha256` 从无人读）。
- 不再加载 `game/*.json`（vision_catalog 669 KB + substitutes 172 KB，及其 UTF-16 副本和 native 副本）；文件仍在 APK 里。
- 模板只加载 manifest 里出现的 `.png`（遍历所有字符串值）：跳过 `lobby_back`、`raw_camp_key`、`raw_lobby_labels`（264 KB RGBA）。
  manifest 读不出时退回全量加载。
- 读文件、去色彩块、像素拷贝用复用缓冲，加载完即释放（原来 512 KB 像素缓冲常驻、每个文件都有 `readBytes`/`ByteArrayOutputStream` 垃圾）。
- `AssetManager.list` 只对目录调用（原来每个文件一次）；日志拆出 decode / engine 耗时。
- 未跳过 `ninja_templates/{sakura,shino,tayuya}.png`（reader.cpp 不用，共 15 KB）：清单硬编码在 C++，Kotlin 抄一份将来容易漏。

验证：临时给每次 `nativeAddImage`/`nativeAddText` 打摘要，新旧加载器对比：新的 536 项与旧的逐字节相同，缺的恰是上面列出的那些。

### 5. 悬浮窗刷新（`overlay/OverlayController.kt`、`tracker/Session.kt`）

原来无论什么场景都每 33 ms 轮询 `session.revision`。现在 `Session` 每次 `bump()` 后调用可选的 `revisionListener`
（在改动线程、持锁调用，只 post）。非对局且设置面板关闭时，悬浮窗只在时钟 deadline（空闲 200 ms）醒来，
revision 变化立即 post 刷新；「先打开推送、再复读 revision」的 volatile 握手避免漏掉并发的变化。
对局中（结果最多 60 Hz）和设置面板打开时仍 33 ms 轮询合并刷新。

## 测量

环境：模拟器 ui34（Android 14 x86_64，1080×2400，swiftshader），`taskset -c 12-17`；宿主同时跑别的重任务（load 5–30），
数据波动大，只看相对值。debug APK + 临时 `NtStats` 日志（每 10 s 一行已分析/丢弃/平均耗时，未提交）。
CPU = 60 s 内 `/proc/<pid>/task/*/stat` utime+stime 增量（% 单核）；内存 = `dumpsys meminfo` App Summary。
「替身」画面：真实对局帧 `图片_011_01.png` 做成 1280×576、60 fps 的 H.264 视频（中间一个移动色块，保证每帧像素变化），
Gallery 横屏全屏播放；「非对局动画」= 同一帧高斯模糊 10 px（不会被识别为对局）+ 移动色块。

| 场景（中位数） | 改前 | 改后 |
|---|---|---|
| 启动：资源加载（5 次） | 490 ms | 308 ms（decode 280 + engine 28） |
| 启动后约 20 s（仅无障碍服务）：PSS / Java 堆 / Native 堆 | 55.3 / 25.1 / 19.6 MB | 36.5 / 7.6 / 18.3 MB |
| 非对局动画，CPU（5 次，含 2 次交替对比） | 55.7 %（48–70） | 45.2 %（40–52） |
| 　同上，已分析帧/秒 | 2.2（2.0–9.2） | 1.8（≤2.5） |
| 桌面静止，CPU / 主线程 | 1.4 % / 0.9 % | 0.9 % / 0.4 % |
| 对局（3 次），CPU | 52.4 % | 54.8 %（噪声内，设计上不变） |
| 对局中 ImageReader 警告 / 75 s | 732 条 | 0 |
| 录屏中 PSS（非对局 / 桌面 / 对局） | 80.4 / 78.7 / 80.4 MB | 78.8 / 76.7 / 79.0 MB |

非对局降幅小于帧率降幅，是因为模拟器上一帧完整分析要 250–450 ms，改前本来也只能分析 2–9 帧/秒，且连续分析时
大量相同画面被 native 去重（指纹相同直接复用结果），单帧平均更便宜。帧更便宜时节奏的效果更明显：在同一构建上只切换
节奏（两者都用下文「遗留 1」的 1280 宽采集，交替各 2 次），非对局 CPU 72.8 / 69.0 % → 46.9 / 42.1 %，分析帧 6.2 / 21.5 → 2.5 帧/秒。
真机若能跟上 60 Hz 源帧，非对局的分析量约降为 1/24（未在真机验证）。

## 复现

```bash
echo "sdk.dir=$HOME/Android/Sdk" > android/local.properties
export JAVA_HOME=~/.local/opt/jdk-17.0.20.1+1 PATH=$JAVA_HOME/bin:~/.local/opt/gradle-8.11.1/bin:$PATH
gradle -p android :app:assembleDebug --offline --console=plain
taskset -c 12-17 ~/Android/Sdk/emulator/emulator -avd ui34 -no-window -no-audio -gpu swiftshader_indirect -no-snapshot &
adb root; adb install -r -g android/app/build/outputs/apk/debug/app-debug.apk
# 视频：python 逐帧写 1280x576 RGB（底图 + 移动色块）→
#   gst-launch-1.0 fdsrc ! rawvideoparse width=1280 height=576 format=rgb framerate=60/1 ! videoconvert !
#   video/x-raw,format=I420 ! x264enc speed-preset=ultrafast ! video/x-h264,profile=baseline ! mp4mux ! filesink location=fight.mp4
adb push fight.mp4 /sdcard/Movies/   # 再 MEDIA_SCANNER_SCAN_FILE，content query 取 _id
# 冷启动：force-stop 后重新写 enabled_accessibility_services 拉起进程，看 logcat AssetLoader 的 "assets: … ms"
# 录屏：MainActivity「悬浮计时 启动」(923,669)、「录屏识别 开始」(923,842)，HOME；PROJECT_MEDIA 已 allow 时免确认
adb shell pm clear com.android.gallery3d   # 去掉「继续播放」对话框
adb shell settings put system user_rotation 1
adb shell am start -a android.intent.action.VIEW -d content://media/external/video/media/<id> -t video/mp4 \
  -n com.android.gallery3d/.app.MovieActivity
P=$(adb shell pidof com.narutotimer)   # 60 s 前后各取一次：
adb shell "for t in /proc/$P/task/*; do echo \$(cat \$t/comm) \$(cut -d' ' -f14,15 \$t/stat); done"
adb shell dumpsys meminfo com.narutotimer
```

注意：非对局场景要在新进程里先放（看过对局后只有大厅/结算才会离开对局）；`uiautomator dump` 会重绑无障碍、关掉悬浮窗；
宿主负载会改变单帧耗时，新旧 APK 要交替跑。结束后 `adb shell settings put system user_rotation 0`、`adb emu kill`。

## 遗留

1. **Android 14+ 录屏其实是原生分辨率**（最大的一项，需要决定）。`onCapturedContentResize` 在开始录屏后立刻回调
   （以及旋转时），`resizeCapture` 直接用内容尺寸，VirtualDisplay/ImageReader 变成 2400×1080（真机 3200×1440 等），
   与文档「安卓录屏 1280 宽」不符。按长边 ≤1280 缩放（约 6 行，见下）后模拟器对局：单帧 260–460 ms → 51–82 ms，
   PSS 77.8 → 68.2 MB、Native 堆 49.0 → 40.4 MB（真机另省约 3×7.5 MB 图形缓冲）。但它改变对局的识别输入：
   hostcheck 对 12 张真实帧（原图 vs 1280 宽双线性）场景、布局、8 颗豆全部一致，头像名有 2 张不同（003 原图识别出
   右「春野樱[百豪]」、1280 宽没有；004 反之多识别出左「佩恩·天道[超]」）。按「对局行为不变」本次没有提交。
   ```kotlin
   // resizeCapture(newWidth, newHeight)：
   val longer = max(newWidth, newHeight).coerceAtLeast(1).toLong()
   val target = minOf(longer, CAPTURE_WIDTH.toLong())
   val w = (newWidth.toLong() * target / longer).toInt().coerceAtLeast(1)
   val hh = (newHeight.toLong() * target / longer).toInt().coerceAtLeast(1)
   ```
2. 竖屏非游戏画面（在别的 App 里）按 1080×2400 分析，模拟器上一帧约 1.8 s；节奏之后仍是单核满载。
   native 对竖屏/非游戏画面尽早退出，或上面的缩放，都能解决。
3. 非对局限速后，录屏线程仍按源帧率醒来取帧/关帧（约 1% CPU），VirtualDisplay 也仍在合成；按节奏摘挂 Surface
   可省 GPU，但静止画面重挂后可能不再出帧，风险大，未做。
4. 截屏兜底仍每帧 `Bitmap.copy`（GPU 回读）；只影响锁不住 HardwareBuffer 的设备。
5. `NativeCore` 解码每帧约 1 KB 垃圾（AnalysisResult、BeadInfo、ArrayList），HeapTaskDaemon 不在 CPU 前列，未改。
6. `ARCHITECTURE.md` §5 的资源表仍写 `game/` 会进 AssetStore（本次不改该文件）。
7. 只在模拟器上测过；需要真机（60/90/120 Hz、中低端机）复测非对局 CPU、进入对局延迟和电量。
