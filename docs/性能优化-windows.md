# Windows 计时器性能优化（v0.25）

目标：在识别结果逐字节不变的前提下，让 Windows 版（Go 引擎 + timer-app）尽量快、尽量省内存。
基线为 v0.24beta `3256514`。所有改动都只改变“怎么算”，不改变“算什么”：
冷扫描/头像全扫描/OCR 的节奏、分辨率与缩放、归约顺序与并列规则全部保持原样。

## 结果一览

测量机：Intel Xeon E5-2680 v2（Ivy Bridge，无 AVX2，走 SSE2 内核），共享主机、负载较重，
`taskset -c 0-3`、`GOMAXPROCS=4`，基线与新版交替运行 6 轮取中位数（benchstat，全部 p=0.002）。
AVX2 机器（绝大多数 Windows 用户）上 NCC 内核还会再快一截，但本机无法实测。

| 指标 | 基线 | 优化后 | 变化 |
|---|---|---|---|
| 每帧耗时，21 个场景几何平均 | 38.3 ms | 7.35 ms | −81%（5.2×） |
| 每帧耗时，1080p 对局（synthetic-duel） | 25.1 ms | 5.17 ms | −79% |
| 每帧耗时，1080p MuMu、标记被遮（mumu-1，最坏路径） | 134 ms | 18.9 ms | −86% |
| 每帧耗时，2880×1800 对局（img005） | 63.0 ms | 10.7 ms | −83% |
| 每帧耗时，2560×1600 手动校准（两侧尺度不同） | 319 ms | 37.5 ms | −88% |
| 每帧分配字节，几何平均 | 650 KiB | 435 KiB | −33% |
| 每帧分配次数，几何平均 | 218 | 185 | −15% |
| 引擎构建后常驻堆 | 9.7 MiB | 5.9 MiB | −39% |
| 1080p 对局 200 帧后：常驻堆 / 峰值 RSS（仅引擎+1 帧） | 24.0 / 71.0 MiB | 17.2 / 43.8 MiB | −28% / −38% |
| 同上，按应用建模（采集缓冲 4→2 帧，GOGC 100→50） | 47.7 / 107 MiB | 25.1 / 47.3 MiB | −47% / −56% |
| 2880×1800 应用建模 常驻 / 峰值 RSS | 101 / 204 MiB | 52.9 / 89.9 MiB | −48% / −56% |
| 2560×1600 校准 应用建模 常驻 / 峰值 RSS | 79.7 / 243 MiB | 45.7 / 85.6 MiB | −43% / −65% |
| timer-app.exe 体积 | 81.0 MB | 60.8 MB | −25% |
| 一致性 `run-parity.sh go` | — | 与 golden-go.txt 逐字节相同 | ✔ |

单项微基准（同一台机器）：NCC 每位置 4.0 µs → 0.95 µs（头像 102×102 菱形）、9.9 µs → 2.2 µs（300×40 标题）；
ToGray 26 万像素 约 1.0 ms → 0.32 ms；MuMu 1080p 采集后处理 4.4 ms → 3.2 ms 且少 8 MiB；
PrintWindow 1080p 像素转换 22 ms → 3.8 ms；OCR 助手每次启动少算约 57 MB 的 SHA-256（本机约 0.8 s，改为约 7 ms 的字节比较）。

## 改了什么

1. **NCC SIMD 内核**（`internal/match/kernel_amd64.s`）。模板按掩码连续段打包，一次汇编调用对所有段
   同时累加 Σimg·tpl、Σimg、Σimg²（不再需要前缀和数组）。SSE2（amd64 基线，GOAMD64=v1 即可）与 AVX2
   （`golang.org/x/sys/cpu` 运行时检测）两版；32 位通道在可能溢出前加宽到 64 位，尾部用掩码向量读，
   仅在读不越界时才这样做，否则逐字节。都是整数，结果与原纯 Go 路径完全相等；纯 Go 路径保留为
   回退（非 amd64 或 `-tags purego`）。Match 再跳过精扫窗口里先前已算过的网格点（严格 `>` 下重算不可能改变结果）。
2. **模板缓存按尺寸存**。头像模板以 (条目, 宽, 高) 为键、按字节上限 8 MiB 做 LRU（原来按单一 scale 缓存，
   两侧校准尺度不同时每帧重建，且上限 2484 个模板约 24 MiB 从未触发）；名字模板按 scale 保留最近 4 套；
   场景续接微调的 ±2 px 变体、身份账号名模板也缓存。模板是 (源, 尺寸) 的纯函数，缓存不改变结果。
3. **场景帧内共享**。`Gated.AnalyzeAt` 用 `BeginFrame/EndFrame` 包住一帧：DecideAt 已全 ROI 扫过的模板分数和灰度视图，
   StartFight/SupportsFight/ScoreAll 直接复用（“标记被遮但豆完整”的最坏路径原来每帧把全部模板扫两遍）。
4. **ToGray SSE2**：4 像素一组，`(299R+587G+114B+500)/1000` 用精确倒数乘法（对全部 2^24 颜色穷举验证）。
5. **采集链路**。MuMu SDK 直接写入环形槽，一次原地“翻转+不透明+逐行哈希”替代暂存缓冲→拷贝→alpha→指纹三遍；
   逐行哈希折叠出的指纹与原 `frameFingerprint` 完全相等（测试覆盖）。采集环 3→2：每次只有一帧在途，
   所有消费者都同步拷贝（回放/诊断、校准快照、截图、OCR 条带，身份截图改为先同步裁剪再异步写盘）。
   PrintWindow：DIB 段按窗口尺寸复用（原代码在位图仍选入 DC 时调用 DeleteObject，按 Win32 文档会失败，位图可能泄漏），
   每次清零以保持“新 DIB 全零”的语义；BGRA→RGBA 按字转换、写入采集环。未加去重。
6. **内存**：头像目录不再保留 3.0 MB PNG 副本和未用的 16×16 缩略图；场景模板同一文件只解码一次；
   未用的名字条（来源图、普通名）不再嵌入；timer-app 默认 `GOGC=50`（环境变量 GOGC 仍优先）。
   GOGC 实测（3 帧缓冲、600 帧、最坏路径）：GC CPU 100→50 每帧约 +0.07 ms（<1%），峰值 RSS 77→62 MiB；
   25 时再省约 8 MiB 但 GC 次数翻倍。没用 `SetMemoryLimit`：高分辨率下常驻堆可能接近上限，会让 GC 空转。
7. **每帧杂项**：豆标签查表、调试行仅在调试开启时格式化、OCR 名字区布局按配置缓存。
8. **OCR 助手**：内置 DLL 摘要预先算好（测试保证与文件一致，缓存目录名不变），已解压文件用字节比较校验。
   行为不变（无空闲停止）。
9. **体积**：build.bat 加 `-trimpath -ldflags "-s -w"`，并固定 `GOAMD64=v1`（v3 会融合 FMA，可能改变浮点结果）；
   发布流程调用 build.bat，因此同样生效。

没做的：常驻 goroutine 池（每帧约 20 次 `go`，约 20 µs，测不出收益）。

## 复现

```sh
export PATH=/usr/local/go/bin:$PATH
# 每帧基准（需要 /ssd/work/perf-corpus，或 NARUTO_PERF_CORPUS 指向语料）
taskset -c 0-3 env GOMAXPROCS=4 go test ./internal/engine/factory -run '^$' \
  -bench '^BenchmarkFrames$' -benchtime 96x -count 6 -benchmem > new.txt
benchstat base.txt new.txt
# 内存足迹（新进程；RING=2 模拟采集环，FRAMES 可调）
NARUTO_FOOTPRINT=1 NARUTO_FOOTPRINT_CASES=synthetic-duel-1920x1080 NARUTO_FOOTPRINT_RING=2 GOGC=50 \
  go test ./internal/engine/factory -run '^TestFrameFootprint$' -count 1 -v
# 一致性
taskset -c 0-5 /ssd/work/perf-corpus/run-parity.sh go . /tmp/parity.txt && cmp /tmp/parity.txt /ssd/work/perf-corpus/golden-go.txt
# AVX2 路径（无 AVX2 的机器用 qemu-user 模拟 Haswell）
go test -c -o match.test ./internal/match && (cd internal/match && qemu-x86_64-static -cpu Haswell-noTSX ../../match.test)
```

每帧基准每个场景一个引擎，帧间隔 16 ms（含 100 ms 冷扫描和 500 ms 头像全扫描），在 4 份“绿色最低位
1/36 晶格翻转”的副本间轮换，使像素备忘录/去重不会命中。微基准：`BenchmarkKernels`、`BenchmarkToGray`
（match）、`BenchmarkCapturePostprocess1080p`（mumu）、`BenchmarkBGRAToRGBA1080p`（win）。

## 验证

- `run-parity.sh go`：每个里程碑和最终版都与 `golden-go.txt` 逐字节相同（SSE2 路径）；在 qemu Haswell（AVX2 路径）下
  跑真实帧 17 张、`--seq` 与 3 组合成序列，同样逐段一致。
- 新测试：内核与纯 Go 求和随机/穷举等价（各种段长、对齐、缓冲末尾、饱和值溢出、PROT_NONE 守护页防越读）；
  Match 内核/可移植路径逐位相等；跳过已算网格点与不跳过逐位相等；帧内共享与不共享逐项相等；
  ToGray 全颜色穷举；原地翻转与指纹折叠；BGRA 转换；身份模板缓存；OCR 摘要表。
- `go test ./internal/... -count=1` 的失败集合与基线完全相同（82 个：缺少私有 inbox/ 图片或 config.json 的测试，
  以及基线即失败的 4 个 diagnostics 回放测试）；internal/ocr 与 internal/win 新增了可在 Linux 运行的测试。

## 剩余事项与风险

- AVX2 内核只在 qemu 中验证了正确性，真机速度未测；Windows 专用代码（SDK 直写环槽、DIB 复用、PrintWindow 环）
  只做了交叉编译与 vet，需在 Windows 上回归一次 MuMu 与 PrintWindow 采集。
- 采集环 2 帧依赖“一帧在途、消费者同步拷贝”的约定；以后新增异步读帧的代码必须先拷贝。
  timer-lab bench 的 `last` 帧本来（3 帧时）就可能被覆盖，只用于 OCR 计时，影响不变。
- 仍可做：AVX2 版 ToGray；头像掩码改位图（约 −1.5 MiB）；场景灰度视图复用缓冲（每帧少约 1 MB 垃圾）。
