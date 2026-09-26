# Windows 10 + MuMu 实机缺陷修复交接单

> **用途**：给维护者和测试者在真实 Windows 10 + MuMu 环境中复现、修复和验收。
> 本文只记录代码审阅所得的缺陷、兼容性风险、环境阻塞和待验证项；不等同于 Windows、MuMu 或游戏运行通过。
>
> **规范依据**：Windows 目标机的准备条件、构建命令、聚焦测试和运行验收步骤以 [`docs/windows-mumu-acceptance.md`](windows-mumu-acceptance.md) 为准。本交接单不替代该清单。

## 1. 范围、证据边界和当前环境

### 1.1 本次范围

- 只对现有源码和仓库文档做了有界的读取/检索；本次交付只新增本文件。
- 以下“已确认”表示可由当前代码路径直接确认的实现问题，不表示已经在 Windows 或 MuMu 上运行复现。
- 以下“未验证”表示需要目标环境证据；不得用 Linux 交叉编译、合成输入或历史截图替代 Windows/MuMu 证据。
- 本次没有修改应用代码，没有安装 VM/ISO/MuMu/游戏，没有运行项目构建或测试，也没有声称 Windows x64、CGo、MuMu、模拟器或实时采集已通过。

### 1.2 代码证据

- provider 选择：`internal/ui/capture_settings.go` 的 `captureProviderLabels`、`captureConfigForLeidianProbe` 和“测试当前选择”分支。
- 诊断 UI：`internal/ui/diagnostics.go` 的 `diagnosticState`、MuMu SDK probe 按钮和 `exportSupportBundle`。
- 采集调度：`internal/frame/source.go` 的 `newSelectableSnapshotter`、`ContextClient` 分支和 `boundedProviderContextWithTimeout`。
- 雷电实现：`internal/capture/leidian/client_windows.go` 的 `Client.Capture`、`runCommand` 和 `ProbeContext`。
- MuMu 候选与加载：`internal/capture/mumu/diagnostics_windows.go` 的 `FindDLLCandidates`/`Probe`，以及 `internal/capture/mumu/mumu_windows.go` 的 `FindDLL`/`Open`。
- 支持包：`internal/support/bundle_windows.go` 的 `BundleOptions`、MuMu `Inventory`/`Probe` 导出路径。
- 适配设计说明：[`docs/leidian-adapter.md`](leidian-adapter.md)；其中“整屏截图、包仅用于校验”的说明是 D3 的判定边界。

### 1.3 当前环境和设置阻塞

[`docs/linux-kvm-mumu-setup.md`](linux-kvm-mumu-setup.md) 的记录显示：rootless `qemu:///session` 可用；尚无 VM、ISO、Windows guest、MuMu 或游戏。预定目标为 `win10-mumu-test`，磁盘为 `/ssd/win10-mumu-test.qcow2`，计划 4 vCPU、4096 MiB、80 GiB；这些是后续合法介质到位后的准备参数，不是已创建资源。

该记录的唯一明确环境阻塞是 Windows 10 安装介质：两条当时提供的官方 CDN 候选均返回 HTTP 404，未创建 ISO 或虚拟机。因此这属于测试环境准备阻塞，不是应用缺陷，也不能据此推断 MuMu 或应用失败。不要使用第三方镜像，不要绕过许可或激活；获得合法介质后须先做本地校验，再按 setup record 建立目标环境。

如需追踪会话中提到的 APT 代理错误，可标为“此前聊天/截图中的 setup 失败，仓库记录未确认”；它不是当前仓库日志中的问题，也不是已确认应用缺陷。不要在报告中填写密码、令牌或其他凭据。

## 2. 已确认的实现问题

### D1 — provider-aware 诊断错配

- **严重度**：高（诊断可信度/修复效率）。
- **影响**：用户选择 `leidian-adb` 时，采集设置可以选择雷电，但诊断窗口仍以 MuMu 为目标。结果可能是 MuMu SDK 自检、MuMu 实例清单和 MuMu 支持包数据，无法证明当前实际选中的雷电采集链路；故障定位会被导向错误 provider。
- **代码位置与符号**：
  - `internal/ui/capture_settings.go`：`captureProviderLabels`、`captureConfigForLeidianProbe`，证明 `leidian-adb` 是可选 provider。
  - `internal/ui/diagnostics.go`：`diagnosticsState.diagnosticProbe` 是 `*mumu.ProbeResult`；MuMu probe 按钮读取 `s.cfg.Capture.MuMu` 并调用 `support.ProbeSDK`。
  - 同文件 `exportSupportBundle`：无论当前 provider，都用 `cfg.Capture.MuMu.InstallDir` 调用 `mumu.DiscoverInventory`，并交给 `support.ExportBundle`。
  - `internal/capture/leidian/client_windows.go`：雷电正确 probe 入口是 `leidian.ProbeContext`，但当前诊断路径没有使用它。
- **复现步骤（代码级/目标机可操作）**：
  1. 在采集设置选择“雷电 ADB”，选择实际雷电安装目录和实例，点击“测试当前选择（不保存）”，记录该 provider probe 成功或失败。
  2. 打开“采集与延迟诊断”，点击“验证 MuMu SDK”；观察它仍读取 MuMu 配置而不是已选雷电配置。
  3. 导出支持诊断包，解压后检查 inventory/probe 是否仍是 MuMu 数据；同时保存配置中的 `capture_provider` 和实际选择目标，作为对照。
- **预期**：所选 provider → 对应 probe → 对应实例/路径/截图证据 → 脱敏且 provider 相关的支持包；选择雷电时应调用 `leidian.ProbeContext`，并记录雷电的 `list2`、`isrunning`、ADB state、截图尺寸和错误阶段。
- **当前实际**：诊断 UI 固定调用 MuMu `support.ProbeSDK`；支持包固定收集 MuMu inventory，未随所选 provider 切换为雷电。此结论来自代码，不是 Windows 运行结果。
- **候选修复方向（不在本次交付实现）**：引入 provider-neutral 的诊断结果接口/联合结果，按 `cfg.Capture.Provider`（或实际回退中的 provider）分发到 MuMu probe 或 `leidian.ProbeContext`；导出前只保留对应 provider 的脱敏字段。不要把雷电路径伪装成 MuMu `ProbeResult`。
- **验收证据**：分别用 MuMu 和雷电配置运行 probe，保存 provider、目标安装目录/实例（路径可脱敏）、阶段结果、截图宽高和支持包目录清单；雷电场景中不得出现仅有 MuMu inventory/probe 的“成功”报告。支持包默认不得含图像，除非明确勾选隐私选项。

### D2 — 雷电同步 capture 缺少调用方可取消的 context

- **严重度**：高（超时恢复、UI 响应和进程生命周期）。
- **影响**：雷电截图调用可能受单次命令超时约束，但调用方的采集 observation context 不能直接取消该同步调用。超时、切换 provider 或关闭应用时，无法按与 frame worker 相同的 caller-cancel 语义及时终止当前 ADB 操作并确认恢复；需要防止旧调用迟到后污染下一次采集。
- **代码位置与符号**：
  - `internal/capture/leidian/client_windows.go`：`Client.Capture()` 持锁、调用 `c.connect()` 和 `runCommand(c.ctx, c.opts.CommandTimeout, ...)`，截图命令为 `exec-out screencap -p`。
  - 同文件 `commandContext`/`runCommand` 使用 client 生命周期 context 加每命令 timeout；它不是 `Capture(ctx)` 的调用方 context。
  - `internal/frame/source.go`：`newSelectableSnapshotter` 在 client 实现 `capture.ContextClient` 时调用 `CaptureContext(ctx)`，否则退回同步 `client.Capture()`；注释明确 in-process client 会保持 busy 直到 native call 返回。
  - `internal/capture/client.go`：`ContextClient` 是“可被调用方 deadline 终止”的显式接口；雷电 `Client` 当前未实现它。
- **复现步骤（先假进程、后实机）**：
  1. 用 fake `adb.exe`/fake process 让 `connect` 或 `exec-out screencap -p` 长时间不退出，并确保测试能观察到进程树。
  2. 从 frame worker 发出一次短 `capture.timeoutMs` 请求，随后立即发出下一次请求或切换 provider；记录 UI 返回时间、`busy`/timeout 状态、旧 fake process 是否退出，以及新请求是否重叠执行。
  3. 在真实 Windows + 雷电上让一次 ADB 截图卡住或超时；重复上述步骤并保存 Task Manager/进程树、`capture.jsonl` 和恢复后的当前帧证据。
- **预期**：frame worker context → `CaptureContext(ctx)` → ADB exec 接受取消 → 子进程被 kill、wait/reap 完成 → 当前请求只返回 timeout/held 状态 → 下一次采集可恢复；不能让 timed-out 调用完成的迟到帧满足下一请求，也不能重叠 ADB capture。
- **当前实际**：frame worker 对非 `ContextClient` 直接调用同步 `Capture()`；雷电实现只使用 client context 和每命令 timeout，没有调用方取消入口。代码因此存在恢复语义缺口；尚未证明某个真实雷电版本一定会卡死。
- **候选修复方向（不在本次交付实现）**：增加 `CaptureContext(ctx)`，把 frame worker context 传到每条 ADB 命令；在 Windows 上确保取消会终止子进程并 wait/reap，锁定 single worker/one in-flight invariant；完成取消后才能允许下一次 capture。补充 fake-process 测试和真实 Windows stall/recovery 验收。
- **验收证据**：必须同时有 fake-process 的退出/reap 证据和真实 Windows 的 `capture.jsonl`。记录 `source_revision`、request/deadline/outcome/reap、origin/terminal 字段；允许的分类包括 `busy`、`timeout`、`helper_launch`、`helper_protocol`、`sdk_worker`、`success`。不得把 timed-out helper/进程后来完成的帧当作有效当前帧。

### D3 — 雷电 package 校验未实现，probe 结果会产生误导

- **严重度**：中高（配置正确性和诊断结论）。
- **影响**：`Options.Package` 会被保存并传入雷电 client，`ProbeResult.PackageFound` 也已声明，但 probe 没有执行 package 查询、也没有设置 `PackageFound`。当前成功只证明能连接 ADB 并取得整屏 PNG，不能证明 `com.tencent.KiHan` 已安装或正在运行。
- **代码位置与符号**：`internal/capture/leidian/client_windows.go` 的 `Options.Package`、`ProbeResult.PackageFound`、`ProbeContext`；其 probe 流程为 resolve → instances → connect → `client.Capture()` → `ImageAvailable`，没有 `pm path`/`pidof` 或等价检查。`docs/leidian-adapter.md` 也明确 ADB 是整屏截图，package 当前只是目标包元数据，并建议另行做 package 校验。
- **复现步骤**：
  1. 在雷电中启动 Android，但不安装目标包，或安装后停留在非目标应用。
  2. 配置相同 `Package`，点击“测试当前选择（不保存）”，保存返回的 `ProbeResult`。
  3. 对照 `ImageAvailable`、`PackageFound` 与 Windows 上 `adb -s <serial> shell pm path com.tencent.KiHan` 或 `shell pidof com.tencent.KiHan` 的现场输出。
- **预期**：要么 probe 真实验证目标 package 并明确区分 installed/running；要么把结果明确标为“display-only/整屏截图，未验证 package”，不输出看似已验证的 `PackageFound` 字段。`ImageAvailable=true` 不得等同于游戏可用。
- **当前实际**：`PackageFound` 保持默认 false 且没有对应失败步骤；非空截图即可使 probe 的截图阶段成功。此处“缺少 package 证明”是代码事实，不是声称某台机器上包缺失。
- **候选修复方向（不在本次交付实现）**：实现真实 package 检查并记录阶段/错误，或删除/改名为明确的 display-only 结果；同步更新 UI、支持包和 `docs/leidian-adapter.md` 的措辞。
- **验收证据**：至少准备“包已安装并运行”“包未安装”“包已安装但当前未运行”三种目标机状态，分别保留 probe JSON、ADB 命令输出和截图尺寸；验收报告必须明确截图证明范围。

### D4 — MuMu 多候选 DLL 只尝试第一个，不回退到后续候选

- **严重度**：中（多版本安装兼容性）。
- **影响**：候选顺序是确定的，但确定顺序不代表第一个 DLL 可加载、导出完整或适配当前 MuMu。当前候选一旦在加载 DLL、查找导出或连接阶段失败，不会尝试后续候选，可能把可用安装误报为 SDK 故障。
- **代码位置与符号**：
  - `internal/capture/mumu/diagnostics_windows.go` 的 `FindDLLCandidates` 按 `nx_main`、`shell`、排序后的 `nx_device` 路径生成稳定列表；`Probe` 将默认 `options.DLLPath` 设为 `candidates[0].Path`，随后只调用一次 `Open(options)`。
  - `internal/capture/mumu/mumu_windows.go` 的 `FindDLL` 返回 `candidates[0].Path`；`Open` 仅对这个 path 执行 `syscall.LoadDLL`、`FindProc` 和 connect。
  - 当前诊断虽会枚举/哈希候选，但 `SDKCandidates` 的记录不等于逐个加载尝试；支持包也不能据此证明后续候选可用。
- **复现步骤**：在测试副本中准备多个候选 DLL，使排序第一项在 load/export 阶段失败、后续项具备所需导出；运行 SDK probe，保存 candidates、selected SDK 和 failure stage。目标机不得为了复现替换用户安装文件；可使用隔离的测试安装目录或 fake DLL/process。
- **预期**：按稳定顺序逐个尝试；每个候选记录 load/export/connect 的失败类别和耗时；成功候选成为 selected SDK；全部失败时汇总各候选失败，不隐藏第一候选以外的信息。
- **当前实际**：默认路径只使用第一候选；load/export 失败直接返回。代码审阅没有证明任何特定 MuMu 版本已损坏，因此本项是已确认的 fallback 实现缺口，不是“某版本 MuMu 已坏”的结论。
- **候选修复方向（不在本次交付实现）**：把候选尝试封装为可记录的逐候选 probe/open 流程；成功即停止，全部失败才返回聚合结果；显式 `DLLPath` 仍应尊重用户指定且不应静默改用别的 DLL，除非产品策略明确允许。
- **验收证据**：提供至少两个候选的排序、每候选 load/export/connect 结果、最终选中项和支持包脱敏摘要；单候选安装仍需证明失败分类清晰。不要仅凭候选文件存在或 SHA-256 报告宣称兼容。

## 3. 兼容性风险与未验证行为（不是已确认 bug）

以下项目当前只能作为风险、观察点或验收缺口，不得改写成已确认缺陷：

1. `internal/capture/mumu/discovery_windows.go` 的进程名白名单和最多六层目录布局发现较窄；未验证不同 MuMu 版本、改名进程、权限或安装布局是否漏检。
2. Windows 原生 CGO/Fyne 构建、MuMu DLL 加载/导出/连接/截图，在目标机尚无证据；Linux 交叉编译不能替代它们。
3. 嵌套虚拟化、MuMu 在 4 GiB 内存下的性能、帧率和长时间稳定性未验证；宿主机有 VMX/KVM 记录不等于 guest 内运行 MuMu 成功。
4. 真实厂商 stall 的取消、kill/reap 和恢复行为未验证；D2 是接口/数据流缺口，不能声称已在真实 vendor stall 中复现。
5. replay/support ZIP 的真实 Windows 导出、打开文件夹、脱敏内容和图像隐私选项未验证。
6. HUD recognition、current-frame effects 和各场景覆盖未验证；应按 canonical acceptance doc 的 live-fight 步骤取证。
7. current-frame identity 与 bean reuse 在验证规则中是**禁止接受的证据**，不是已观察到的坏行为：不能用旧 identity/旧 bean state 通过验收，也不能把旧帧标注成当前帧。若现场发现问题，保留连续原始帧和同帧诊断后再单独立项。

## 4. 设置/环境阻塞与应用缺陷的分界

| 类别 | 当前结论 | 下一步 | 不应作出的结论 |
|---|---|---|---|
| Windows 10 安装介质 | `docs/linux-kvm-mumu-setup.md` 记录的两条候选 CDN URL 当时均 HTTP 404；无 ISO、VM 或 guest | 由用户提供合法、可校验介质；本地核验后再建 `qemu:///session` VM | 不应说应用或 MuMu 已失败 |
| Windows/MuMu 实机 | 尚未安装/启动；无 MuMu、游戏、APK 或实时截图证据 | 在真实 Windows 10 中安装并保留当前桌面、MuMu Android 主屏和目标实例证据 | 不应说 Windows/MuMu 通过 |
| D1–D4 | 由代码路径确认的实现问题/缺口 | 按优先级修复后在目标机验证 | 不应以环境未就绪为理由把它们标成运行时已复现 |
| APT 503（如需提及） | 仅此前聊天/截图线索，仓库记录未确认 | 若仍相关，单独收集不含凭据的当前安装环境日志 | 不应标为当前仓库 issue |

## 5. 优先修复顺序

1. **D1 provider-aware 诊断**：先让 probe、capture state、inventory 和支持包指向同一 provider，否则其他故障证据可能全部错配。
2. **D2 雷电 caller-cancellable capture**：补齐 context → ADB exec → kill/reap → recovery 链路，优先加入 fake-process 测试，再做真实 Windows stall 验收。
3. **D3 package 语义**：在“真实 package 验证”和“明确 display-only”之间做产品决策，避免 `ImageAvailable` 被误读为游戏证明。
4. **D4 MuMu 候选回退**：逐候选记录 load/export/connect 结果并回退；用隔离多候选目录验证，不把特定版本兼容性写成无证据结论。
5. D1–D4 后，再处理第 3 节的兼容性风险和 acceptance gaps；没有目标机证据时保持“未验证”标签。

## 6. 真实 Windows 10 + MuMu 取证和验收

### 6.1 目标机前置条件

严格采用 [`docs/windows-mumu-acceptance.md`](windows-mumu-acceptance.md)：Windows x64、Go 1.26.1、PowerShell、Fyne 所需 x64 C/C++ 工具链、`CGO_ENABLED=1`，以及运行中的 MuMu 和已安装 `com.tencent.KiHan`；SDK DLL 必须可从选定 MuMu 安装找到。需要回放 MP4 时再准备 GStreamer。不要用 `CGO_ENABLED=0` 代替。

### 6.2 构建和聚焦测试命令

以下命令原样取自 canonical acceptance doc；它们只证明目标机上的构建/聚焦测试，不单独证明 MuMu runtime：

```powershell
$env:TIMER_VERSION = 'acceptance-local'
cmd /c build.bat
if ($LASTEXITCODE -ne 0) { throw 'build.bat failed' }
Get-ChildItem bin\*.exe | Get-FileHash -Algorithm SHA256
```

确认 `bin\timer.exe`、`bin\timer-gui.exe`、`bin\timer-app.exe`、`bin\timer-app-debug.exe`、`bin\timer-lab.exe` 存在，并用目标机 PE 工具检查架构，例如 `dumpbin /headers bin\timer-app.exe`。

```powershell
go test ./internal/ninja -run 'TestReviewedTargetTitlesRecognizeWithoutGameplayInference|TestObitoCurrentTemplateRejectsPartialVersion|TestSasukeXiayinNameAndBoundedGeometryHint|TestItachiHyakusenMuMuVideoTitles'
go test ./internal/engine -run TestConfiguredLayoutMapsMuMuReferenceAcrossResolutions
```

若执行 Itachi 视频测试，仍按 canonical doc 使用：

```powershell
$env:NARUTO_VIDEO_FRAMES = 'C:\temp\naruto-video-frames'
# 创建与 MP4 basename 对应的两个子目录，并用本机 GStreamer 每秒抽取一张 PNG
go test ./internal/ninja -run TestItachiHyakusenMuMuVideoTitles -count=1
```

### 6.3 MuMu runtime 和 D1/D3/D4 证据

1. 启动 MuMu 和游戏；多实例时在应用设置中选择精确的安装/实例。
2. 使用 capture probe，记录 SDK connection、display selection 和**非空 current screenshot**。同时保存脱敏 probe JSON、当前 provider、实例标识和截图宽高。
3. 若配置的是雷电，使用雷电设置中的 probe，记录 `list2`、`isrunning`、`adb connect`、`get-state=device`、截图和 package 语义；不要把 MuMu probe 的成功当作雷电成功。
4. D1：分别导出 MuMu 与雷电支持包，比较 provider、probe 和 inventory 是否一致；默认支持包不应含游戏图像，只有明确选择隐私选项时才可包含。
5. D3：对“包已安装并运行/未安装/已安装但未运行”保存 probe 与 ADB 现场结果，明确 `ImageAvailable` 与 package proof 的差异。
6. D4：对多候选 MuMu 安装保存稳定候选顺序、每候选失败阶段、最终选中 DLL；没有 live proof 时，不写“某 MuMu 版本损坏”。

### 6.4 D2 timeout/reap 和恢复步骤

按 canonical acceptance doc 的 runtime 步骤执行：`capture.timeoutMs` 是同步 observation deadline；MuMu runtime helper 另有 15 秒硬 transaction ceiling。先确认正常但较慢的 helper 在 observation deadline 内完成并产生 current frame；然后故意让一次 SDK capture stall（例如采集中暂停模拟器），确认 UI 不因 15 秒 helper ceiling 被阻塞，返回 timeout，不重叠新的 helper，并在 reap/recovery 后恢复 current valid frame。关闭应用时另有 stall，确认 helper 在应用退出前终止，Task Manager 中无残留 helper。

雷电还必须执行 D2 的 fake-process 测试和真实 ADB stall 测试：确认 caller context 能取消、旧进程被 kill 并 reap、下一次 capture 不重叠且能恢复。保存 `capture.jsonl`；检查 `source_revision` 及非敏感的 request/deadline/outcome/reap、origin/terminal 字段。可接受分类包括 `busy`、`timeout`、`helper_launch`、`helper_protocol`、`sdk_worker`、`success`；不得保存路径、token 或图像数据，也不得接受超时调用迟到完成的帧。

### 6.5 分辨率、live fight、replay 和 support ZIP

严格执行 canonical doc 的以下步骤：

- 在 1280x720、1600x900、1920x1080 检查 HUD 不标记 `unsupported-resolution`，bean centers 跟随游戏内容区。
- live fights 检查：百战鼬右侧标题和四颗 energy-row beans；神驹佑祥斑/木叶创立柱间六颗 current beans 与颜色；九喇嘛连结水门标题和 energy-row geometry；十尾带土/侠隐佐助在可见 current-frame 移动后恢复标题，在真实遮挡时进入 unknown。
- 开启代表性战斗的可选三分钟 diagnostic replay 后停止；确认 `replay/manifest.json` schema 2、每个 `raw_path`/`annotated_path` 成对且完整，每条 annotation 描述自身 frame 而不是后来的 UI 状态。
- 导出小型 replay ZIP 并使用“Open folder”；确认 support ZIP 除非明确选择隐私选项，否则不含游戏图像。
- 出现 mismatch 时记录 SDK diagnostic bundle；不要用旧 identity 或旧 bean state 验收。十尾带土不稳定时保留原分辨率连续帧、title/avatar scores 和 purple-bean diagnostics，不要仅凭有损 replay JPEG 降低全局阈值。

## 7. 完成清单

### 修复者

- [ ] D1 的 probe、inventory、capture state 和 bundle 按实际 provider 分发，并完成脱敏输出。
- [ ] D2 增加 `CaptureContext(ctx)` 或等价 caller-cancellable 设计；ADB 子进程可取消、kill、wait/reap，且 single worker 不重叠。
- [ ] D3 明确实现 package 校验，或把 probe/UI/文档改成显式 display-only；更新 `PackageFound` 语义。
- [ ] D4 逐候选尝试 MuMu DLL，记录每候选失败并在允许的策略下回退。
- [ ] 为每项修复补 focused/fake-process 覆盖；不把 Linux 结果写成 Windows/MuMu 结果。

### Windows/MuMu 测试者

- [ ] 合法 Windows ISO、Windows guest、MuMu、游戏和目标包均有当前环境证据；没有凭据写入日志。
- [ ] 已按 canonical acceptance doc 使用 `build.bat`、指定 `go test` 和目标机 CGO/Fyne 工具链。
- [ ] D1–D4 各有配置、步骤、预期/实际、日志/JSON、屏幕或进程证据；未验证项仍明确标注。
- [ ] D2 有 fake-process 与真实 Windows stall/reap/recovery 两套证据。
- [ ] 完成三种分辨率、指定 live-fight、current-frame、replay manifest、replay ZIP 和 support ZIP 检查。
- [ ] 最终报告清楚区分 confirmed defect、compatibility risk、setup blocker、unverified acceptance gap；没有 Windows/MuMu runtime success 的无证据声明。

## 8. 本文验证记录

本次文档更新仅创建 `docs/windows-mumu-bug-handoff.md`。未运行项目构建或测试；按任务要求只需执行文档检查：

```text
git diff --check -- docs/windows-mumu-bug-handoff.md
```

并读取/检索本文件的标题和引用路径，确认四个 D 编号、风险/阻塞分节以及 canonical acceptance/setup 文档引用存在。若上述文档检查尚未在交接执行环境中运行，应在提交前补跑并把结果记录为“通过”或“阻塞”，不得用源码审阅冒充运行验收。
