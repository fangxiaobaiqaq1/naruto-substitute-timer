# Android 识别核心（native C++）性能优化

范围：`android/app/src/main/cpp/**`（识别核心）与 `android/tools/hostcheck.*`。目标是「极致的快速和轻内存占用」，
**结果逐字节不变**：节拍（cadence）、降采样、归约顺序与并列规则、浮点求值（`-fno-fast-math -ffp-contract=off`）
一律不动；所有加速都是「同一组精确整数 / 同一串 double 运算，换一种算法组织」。

## 1. 改了什么

| 提交 | 内容 |
|---|---|
| `2a9429d` | `hostcheck --bench N [--perturb] [--serial]`：一个 `frame::Pipeline`、每帧 +16 ms，输出每张图与总体 p50/p90、每帧 `operator new` 次数/字节、资源注册/引擎构建/校准重建/预热后的内存（C++ 存活堆、malloc、VmRSS/VmHWM）以及结果哈希。 |
| `256c372` | **NCC 点积 SIMD 内核**（`src/match/ncc_dot.h`）：SSE2（x86-64 基线）、AVX2（运行时 `__builtin_cpu_supports`）、NEON（arm64 基线 `umull`+`uadalp`）、DotProd（`udot`，`getauxval(AT_HWCAP)&HWCAP_ASIMDDP` 运行时检测）。尾部用「从 n-16 重叠读 + 掩码清零已算字节」，绝不越界读。整段 run ≤ `maxDotRun`，和 < 2^32，任何加法顺序都精确。模板只存掩膜内像素（紧凑）；前缀和改 uint32（行宽 ≤ maxDotRun 时精确，更宽走 `atDirect`）。 |
| `c98d5fb` | 头像模板缓存改为按 (条目, w, h) 键的 LRU（不再「换一个 scale 就全清」——校准后左右两侧 scale 不同时每次调用都在重建）；名字条模板按 scale 缓存最近 4 组；同一搜索区的前缀和共享（`Query::Prefix`：头像一次 matchEntries 只建一次，名字粗定位一次）；场景续命模板 ±2px 细化的 24 个尺寸变体每个几何只缩放/准备一次；`ScaleGray` 每列/每行权重只算一次（同一串 double 运算）；`Gray/RGBA::NewUninit` 去掉会被整块覆盖的清零。 |
| `dcc5d2b` | 粗→细扫描里已经打过分的位置不再重复（同值不可能通过严格 `>`）；`ScoreAll`（StartFight）模板并行。 |
| `a015c98` | **资源内存**：`AssetStore::AddImage` 当场算出 Go 语义亮度（原 `ToGrayAsset`）与 alpha≥128 位图，丢掉 RGBA（4 B/px → 1 B/px + 1 bit）。`ToGrayAsset` 返回共享视图；头像条目共享亮度，全分辨率脸部掩膜改为缓存未命中时现算；删除从未被读取的 16² thumb。引擎构建/校准重建不再从 RGBA 重算。 |
| `7430bd0` | `ToGray` 用 `((x>>3)*33555)>>22 == x/1000`（对全部 x ≤ 255500 穷举验证）去掉除法，可向量化（arm64 生成 `ld4`+`umull`）；头像模板缓存加「2048 次调用未用即淘汰」+ 8 MiB 上限；超大前缀和缓冲不常驻每个线程。 |
| `35d988a` | 线程池：池线程里的嵌套 `ParallelFor` 也能用空闲池线程（之前一律串行，左右两侧/camp-duel 并行时有一侧的头像打分整段串行）；线程上限不变（≤3 helper + 调用线程）；`ParallelFor` 改为模板 + 类型擦除引用，不再每次构造 `std::function`。 |
| `70880b9` | 帧指纹：各行哈希分 4 段并行算，再按行序 `MixHash`（值不变）。 |
| `0153a5e` | 大扫描（位置数 × 模板像素 ≥ 3M）把位置行分给线程池算进缓冲，再在调用线程按原扫描顺序、原严格 `>` 归约。 |
| `14723a4` | 几何变化时场景模板并行准备；大搜索区前缀和按行带并行；账号名识别共享前缀和。 |
| `589084b` | 超过 512 KiB 的前缀和缓冲来自一个只保留 1 个备用块的进程级池，而不是每个池线程各留一份。 |

没有改：jni.cpp 编码格式、任何阈值/ROI/节拍、Go 侧、Kotlin 侧、APK 构建参数。

## 2. 正确性

* **对拍**：每个提交之后都跑 `run-parity.sh cpp`，输出与 `golden-cpp.txt` `cmp` 完全一致（最终提交 `589084b` 亦然）。
* **随机等价测试** `tests/perf_kernels_test.sh`（约 260 万次检查）：全部点积内核 vs 标量（0..300 全长度、随机长度/对齐、`maxDotRun` 极值
  255²·66051）；`NCC::Match`（每个内核 × 有/无共享前缀 × 有/无 Gray × Template/Prepared，含大图并行路径）vs 原始逐像素算法，
  `double` 按位比较；`ScaleGray`/`ToGray` vs 原实现；`AssetStore` 转换与头像掩膜 vs RGBA 路径。
* **NEON/DotProd**：本机不能跑 arm64，测试用 `tests/neon_emu.h`（逐 lane 按 ACLE 语义实现所用的十几个内建函数）在 x86 上执行
  同一份内核源码；arm64 产物经 `llvm-objdump` 确认 `sumsDotProd` 主循环为双累加器 `udot v.4s, v.16b, v.16b`，`sumsNEON` 为
  `umull/umull2/uadalp`，`ToGray` 为 `ld4`+`umull`。
* **AVX2**：本机（Xeon E5-2680 v2）无 AVX2。`tests/perf_kernels_qemu.sh` 用 QEMU TCG（`-cpu Haswell`）+ Android SDK 的
  `kernel-ranchu` 启动静态测试程序：`NCC kernels on this CPU: scalar sse2 avx2 (active: avx2)`，全部检查通过。
* `tests/hud_start_test.sh`：与改动前一样跑到 3307 项检查后停在既有的 `duplicate ROI is not independent`。
* APK：`gradle -p android :app:assembleDebug --offline` 成功（arm64-v8a + x86_64）。

## 3. 测量方法（本机）

主机：Intel Xeon E5-2680 v2（Ivy Bridge，SSE4.2/AVX，**无 AVX2**），GCC 13 `-O3`，`taskset -c 4-7`（池 = 3 helper + 调用线程）。
机器同时有其它任务在跑（load 20~30），所以只比较**交替运行**的同批中位数：

```sh
HOSTCHECK_OUT=/tmp/hc bash android/tools/hostcheck.sh
taskset -c 4-7 /tmp/hc/hostcheck --bench 30 --perturb /ssd/work/perf-corpus/real          # 17 张真图，每张 30 帧
CAL=$(cat /ssd/work/perf-corpus/synthetic/calib/control-tablet-2560x1600-camp/calibration.txt)
taskset -c 4-7 /tmp/hc/hostcheck --bench 40 --perturb --calibration "$CAL" \
    /ssd/work/perf-corpus/synthetic/calib/control-tablet-2560x1600-camp/0.png              # 左右头像 scale 不同的校准
taskset -c 4-7 /tmp/hc/hostcheck --bench 120 --perturb /ssd/work/perf-corpus/real/mumu-1920x1080-1.png  # 单分辨率会话
```

`--perturb` 每帧翻转 8px 网格上 R 通道最低位（相位逐帧变化），去重与头像像素记忆都不命中，相当于实时采集；
每张图的第 1 帧（几何变化）单独记为 `first`，其余帧算 p50/p90。基线 = 原核心（`3256514`）+ 同一个 bench 工具。
两个版本在同参数下的 `results-hash` 完全相同（真图 `4657229885ca5024`，校准 `14e331f3801674a3`）。

## 4. 结果（基线 → 现在）

每帧耗时（ms，5 轮交替中位数，load≈26 时测得；较空闲时现在版本真图约 p50 3.4 / p90 14 / mean 7）：

| 场景 | p50 | p90 | mean | 首帧 p50 |
|---|---|---|---|---|
| 17 张真图 ×30 帧 | 27.6 → **8.1** | 171.8 → **37.7** | 63.3 → **16.2** | 174.6 → 44.7 |
| 校准 2560×1600（左右 scale 不同） | 293.9 → **48.7** | 320.8 → **63.9** | 301.7 → **51.5** | 701 → 194 |
| 单分辨率 1920×1080 ×120 帧 | 116.8 → **22.8** | 125.8 → **24.2** | 120.1 → **23.4** | 435 → 78 |
| 单分辨率 2880×1800 ×120 帧 | 81.6 → **12.3** | 90.4 → **17.1** | 85.4 → **14.0** | 445 → 105 |
| 真图单线程（`--serial`，总 CPU） | 42~50 → **17.7** | 138~203 → **81** | 75~94 → **36** | — |

内存（C++ 存活堆；`warm` 为跑完后、无帧缓冲时）：

| 时刻 | 基线 | 现在 |
|---|---|---|
| 资源注册后（295 图 + 文本） | 10.01 MiB | **3.60 MiB** |
| 引擎构建后（含头像图鉴） | 14.65 MiB | **4.33 MiB** |
| 校准重建（`Pipeline::Rebuild`）耗时 | 5.5~6.7 ms | **0.4~0.7 ms** |
| 预热后：1920×1080 会话 | 29.85 MiB | **13.29 MiB** |
| 预热后：2880×1800 会话 | 42.57 MiB | **16.90 MiB** |
| 预热后：17 种分辨率轮换 | 39.22 MiB | 24.40 MiB（含 ≤8 MiB 头像模板 LRU） |

每帧分配次数约 297 → 267；分配字节因大前缀缓冲按需借用而上升（真图约 0.7 → 2.3 MiB/帧，glibc 下不可见）。

## 5. x86_64 主机数字 vs arm64 预期

* 本机只跑到 SSE2 内核（每 16 字节约 10 条指令）。点积占剩余单线程时间约 70%。
* x86_64 模拟器（MuMu/雷电等，宿主几乎都有 AVX2）走 AVX2 内核：每 16 字节 4 条指令，长 run 预计点积再快约 2 倍。
* arm64：无 DotProd 的旧核（A53/A73 等）走 NEON（`umull`+`uadalp`，每 16 字节 4 条运算指令，约为原自动向量化的 1.5 倍）；
  ARMv8.2+ 带 DotProd 的手机走 `udot`（每 16 字节 1 条），点积部分预计比 NEON 再快 2~3 倍。
* 并行改进（嵌套 ParallelFor、大扫描分带）与内存改进与架构无关；线程上限仍是 3 个 helper。
* 无法在本机测 Android 上 scudo 的大块分配成本；所以 >512 KiB 的前缀缓冲留 1 个备用块，避免每帧 mmap/缺页。

## 6. 还能做的

* 帧指纹仍是标量 wyhash（已分 4 段并行）；换 SIMD 哈希可再省约 1~2%（需要重新论证碰撞率，未做）。
* 短 run（<16 字节，占 26%）仍有固定开销；给 Gray 分配尾部填充后可用掩码整块读，需改 Gray 不变式。
* StartFight 的 `ScoreAll` 与同帧 DecideAt 冷扫描对非续命模板重复打分（同帧结果可复用，需要帧作用域 API）。
* 账号名识别（配置了玩家名时）每次重新缩放名字模板，可按尺寸缓存。
* 在真机上跑 `hostcheck --bench`（NDK 交叉编译）得到 arm64 实测数字；对拍语料没有覆盖账号名识别路径。
