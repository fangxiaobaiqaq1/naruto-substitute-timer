# v0.23beta · 特殊忍者适配与前端重做（测试版）

> 测试分支，尚未定正式版本号。本文同时作为交接记录：新对话从这里了解当前状态、产物、验证范围和待办。

## 分支与提交

- 分支：`v0.23beta`（GitHub `origin`），基于本地 `v0.22`（`f9c993c`）。本地 `v0.22` 比远端 `v0.22`（`142ac14`）多 10 个提交，这些提交只随 `v0.23beta` 推送，`v0.22` 本身未改动、未推送。
- `cfd8659` 特殊忍者 HUD、跨分辨率与计时链路修复。
- `bc2e1f4` 安卓与 Windows 前端重做及 bug 修复。

## 一、识别与计时（`cfd8659`）

- HUD 统一按 `min(W/refW, H/refH)` 缩放：平板按宽、宽屏按高，贴顶不裁边框。宽屏按当前画面生命条外端斜边分别校正左右偏移，不参考豆子亮度；用户手动标定优先。
- 特殊忍者：侠隐佐助紫/红、带土紫、鸣人红、柱间六槽、斑暖色、水门能量条行偏移。六槽按前四槽平均间距延伸；侠隐红豆必须是与两侧间隙分离的实体；未确认身份不能把整排移到下方找亮点。
- 场景门禁：首次进入对局需两个位置独立的战斗控件命中 + 两侧完整合法的豆排；其它页面或竞争布局一票否决。新增训练场公共控件模板「复位」「退出」；控件按整屏区域搜索、模板等比缩放，并支持小/中/标准三档按键尺寸及 ±2 像素细化（同一控件的变体不重复计票）。
- 连续性：新采集到的重复画面只维持已有观测的有效期，不增加确认票；没有新采集的 heartbeat 不能跨越采集中断。
- 验证：Go 合成连续帧矩阵 336/336（12 种尺寸 × camp/duel × 左右 × 7 种外观/拓扑），原 12 张截图静态回归 48 例；安卓生产 JNI 链路用真实侠隐截图得到「第 1 次」「14.9」。详见 [豆变化链路修复验证](豆变化链路修复验证.md)、[分辨率适配验证](分辨率适配验证.md)。

## 二、前端（`bc2e1f4`）

### 安卓

- **游戏内计时卡片**：旧实现在画完一行小字后提前返回，计时卡片从未绘制，「完整/迷你」两种模式看起来一样，倒计时颜色也不显示。现为：
  - 完整卡片：对面忍者 + 次数 / 大号倒计时 / 我方模式与场景、豆数；
  - 迷你卡片：我方模式 + 大号倒计时。
  - 左侧状态条与数字颜色：青 = 对面替身冷却中，琥珀 = 最后 3 秒，灰 = 无计时；等待状态（待替身、待识别…）显示为小号文字。
- **操作**：点卡片展开 设置/换边/两边/迷你(完整)/退出；只有展开后或「调整位置」模式下才能拖动，对局中误滑不会移动卡片；点游戏其它地方自动收起；旋转屏幕后回到保存的位置。默认大小改为 100%（旧默认 75% 是为细条设计的）。
- **游戏内设置面板**：深色主题控件（原来在服务上下文里是浅色主题，复选框和滑条几乎看不见），尺寸封顶并居中，左侧标签可滚动，状态每秒刷新；「授权录屏」先关闭面板，系统授权框不再被挡住，也不会把主界面带到前台。
- **主界面**：「开始使用」三步（无障碍 → 悬浮计时 → 录屏识别），每步有状态点和一个按钮；实时状态卡；对局设置；高级设置折叠。应用改用固定深色主题。
- **修复**：更新对话框在旋转/退出后弹出导致崩溃；同一进程重复自动检查更新；重复点下载会排队多次；允许安装未知来源后需要重新下载；回到应用时正在输入的名字被清空；系统「停止共享」后录屏通知不消失；安卓 13+ 未申请通知权限；悬浮窗设置与主界面的名字拆分规则不一致；不记住认边时重启仍显示「我方·左」；导入图片时页面重建丢失账号名；状态行出现「待识别 · 待识别」。删除了从未创建的拖动把手代码。

### Windows

- **完整浮窗**：左侧状态条（同安卓配色）、「第 N 次」标签、实体按钮；最后 3 秒改为琥珀色。
- **仅计时器（迷你）**：圆角底板 + 大号数字；展开的 设置/换边/两边计时/退出 不再挤在一起重叠；控件不再使用透明键色（原来「退出」文字被抠空、按钮间隙点击穿透到游戏）。
- **修复**：首次切到迷你时没有透明/置顶样式（窗口尚未创建句柄就去设置）；置顶改为用窗口句柄设置（原来按标题查找，迷你启动后完整窗口不置顶）；字号预览没有作用到两边计时和迷你窗口；迷你在手动缩放模式展开时数字变小、收起后窗口不缩回；窗口标题为默认值。
- **新增**：记住完整浮窗和迷你窗口的位置，恢复时限制在可见显示器内（配置 `ui.windowPosition` / `ui.miniPosition`）。
- **设置窗口**：常规页按卡片分组；重复打开设置不再重建窗口（原来会取消正在进行的更新下载和模拟器扫描）；窗口置顶即时生效；Ctrl+M 在设置窗口也可用；更新开关和更新源移到「关于与更新」；切换更新源后隐藏旧源的「发现新版本」；模拟器页重新扫描时保留尚未保存的选择；扫描线程不再读取界面输入框；禁用按钮有明显区分。

## 测试包

本地 `dist/ui-refresh/`（git 忽略，不提交）：

| 文件 | 说明 | SHA-256 |
|---|---|---|
| `timer-app.exe` | Windows x64，版本 `development-ui-refresh` | `8c34b00229d8f91ea41df4d13ec70a494b6312abefbbab63bddf0ca606fc8256` |
| `naruto-timer-universal-debug.apk` | 0.2.10（versionCode 30），arm64-v8a/x86_64，调试签名与之前测试包相同，可覆盖安装 | `4e99fedc78c74b40d790a94cdf94e0239c03cff3b642737d8409f41a0ee5d092` |

本机临时用 Tailscale 内网提供下载：`dist/ui-refresh` 在 8777 端口（`python3 -m http.server`），停止：`pkill -f "http.server 8777"`。

## 验证范围

- 安卓：在 Android 14 模拟器上实际运行，游戏截图作背景检查卡片各状态、迷你、操作行、位置调整、设置面板各页、录屏授权和主界面。倒计时各状态截图使用临时演示数据，演示代码已删除、未提交。
- Windows：只做了交叉编译和软件渲染截图，**原生窗口行为（透明键、点击穿透、置顶、位置记忆、迷你样式）未在真实 Windows 上运行**。
- Go：`./internal/ui ./internal/config ./internal/app ./internal/win32` 通过；失败项均为缺少私有 `inbox/` 截图的旧用例，以及 `cmd/timer-app` 的 `TestInstalledAppUsesUserDataAndPortableKeepsOwnConfig`（改动前在 Linux 上同样失败）。
- 安卓生产 JNI 链路（`android/tools/chaincheck/run.sh`）：8 组普通场景和真实侠隐截图通过；后续合成特殊忍者部分在 `decoded profile: actual=duel expected=camp` 处中断，改动前已存在，与本次前端无关。

## 待用户实机确认

1. Windows 仅计时器：底板外透明并可点击穿透到游戏，底板和按钮可点击/拖动；第一次进入迷你就有透明和置顶。
2. Windows：退出迷你后完整浮窗保持置顶；重启后两个窗口回到上次位置。
3. 安卓：卡片大小是否合适（默认 100%）；先点开再拖动的操作是否顺手；游戏内授权录屏流程；通知栏「停止识别」按钮。

## 待办

- 修复 chaincheck 合成特殊忍者用例的 camp/duel 判定（合成背景与控件证据）。
- 退出悬浮窗后录屏 VirtualDisplay 仍在运行，可暂停以省电（需保持单次授权令牌）。
- 雷电安装目录探测仍在首次打开设置时于界面线程执行（逐个检查盘符）。
- 主界面的 ADB 命令会覆盖已开启的其它无障碍服务（界面已注明）。
- 正式发布前：定版本号和 versionCode、写发布说明、使用正式签名构建 APK。

## 开发备忘

```bash
# Go 测试（跳过耗时的合成矩阵）
go test ./internal/ui ./internal/config ./internal/app -count=1 -skip TestSpecialHUDResolutionMatrix

# Windows 窗口预览（软件渲染，不开原生窗口）
TIMER_UI_PREVIEW_DIR=/tmp/ui go test ./internal/ui -run TestWindowPreviews -count=1

# Windows 交叉构建（见 开发与发布.md；需 cgo + Fyne diagnostics overlay）
scripts/fyne-diagnostics/prepare-linux.sh bin/fyne-diagnostics-linux
GOOS=windows GOARCH=amd64 CGO_ENABLED=1 CC=<llvm-mingw x86_64-w64-mingw32-gcc> go build \
  -modfile bin/fyne-diagnostics-linux/diagnostics.mod -overlay bin/fyne-diagnostics-linux/overlay.json \
  -ldflags '-X narutotimer/internal/buildinfo.Version=<版本> -H windowsgui' -o dist/timer-app.exe ./cmd/timer-app

# APK（JDK 17、Android SDK 35）
gradle -p android :app:assembleDebug --offline --console=plain

# 安卓生产 JNI 链路检查；可选第三个参数为 Go 导出的合成矩阵目录
bash android/tools/chaincheck/run.sh
```

- 本机模拟器：AVD `ui34`（Android 14 x86_64，系统镜像手动解压到 SDK 的 `system-images/android-34/default/x86_64`）。无窗口启动：`emulator -avd ui34 -no-window -no-audio -gpu swiftshader_indirect -no-snapshot`。注意 `uiautomator dump` 会重新绑定无障碍服务并关掉悬浮窗；模拟器里当前装的是改动前的旧 APK（用于对比截图），继续使用前先装新包。
- 推送 GitHub 时 13302 出口出现 TLS 握手失败，改用 AGENTS.md 中的 2260 下载代理成功（凭据见 AGENTS.md，不写入仓库）。
