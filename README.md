# 火影忍者手游替身计时器 | Naruto Mobile Substitution Timer

**免费开源的火影忍者手游替身 CD 计时工具**：通过本地图像识别检测替身豆变化，在悬浮窗显示对方替身冷却倒计时，支持决斗场和训练场。提供可直接使用的 **Windows EXE / Android APK**，以及可供二次开发的完整源码，不只是算法演示。

**Windows 10/11 x64（MuMu / 雷电 LDPlayer）· Android 12+（手机 / 模拟器）· Go/Fyne + Kotlin/C++ · 原创代码 MIT**

An open-source substitution cooldown timer and tracker for **Tencent Naruto Mobile**. Detects substitution charges from screen images and displays a countdown overlay on Windows (MuMu / LDPlayer) and Android 12+. Includes local OCR for player-side detection, ready-to-run EXE/APK downloads, and Go/Fyne + Kotlin/C++ source code. Original code is MIT-licensed; bundled third-party assets have separate terms.

[下载最新版](https://github.com/fangxiaobaiqaq1/naruto-substitute-timer/releases/latest) · [官网与教程](https://hy.312166.xyz) · [Gitee 镜像](https://gitee.com/xiaobaiqaq/naruto-substitute-timer) · [使用说明](docs/使用说明.md) · **[详细项目索引](docs/INDEX.md)** · [复用与二次开发](#复用与二次开发)

**索引直达：** [搜索词与功能](docs/INDEX.md#按搜索词找功能) · [Windows 源码](docs/INDEX.md#windows-源码索引) · [Android 源码](docs/INDEX.md#android-源码索引) · [开发测试与发布](docs/INDEX.md#开发测试与发布) · [平台与复用边界](docs/INDEX.md#平台支持与复用边界)

## 已实现的功能

- **替身识别与自动计时**：识别左右替身豆变化，显示对方或两边替身 CD；画面遮挡、证据不足时显示未知，不凭旧画面猜测替身。
- **OCR 自动认边**：识别游戏账号名判断我方左右位置，也支持手动换边；模型、忍者资料、场景模板和 OCR 运行依赖均已内置。
- **悬浮窗与分辨率适配**：完整 / 迷你计时浮窗，可调整位置、大小和透明度；支持豆与头像区域校准，校准方案可在电脑和手机之间导入导出。
- **本地屏幕识别**：不上传截图，不读取游戏内存，不注入或修改游戏；Android 版无需 Root。

**BUG 反馈 QQ 群：1109044204**。遇到 MuMu SDK、截图或多开实例问题时，请在「诊断」中导出“支持诊断包（ZIP）”后反馈。

## 下载与使用

到 [最新发布页](https://github.com/fangxiaobaiqaq1/naruto-substitute-timer/releases/latest) 选择：

- **naruto-timer-universal.apk**：Android 12 及以上手机版。
- **timer-app.exe**：便携版，放在可写目录直接运行。
- **完整 ZIP**：附带诊断工具和第三方许可。

**Android：** 安装 APK 后，按 App 指引开启无障碍服务 → 启动悬浮计时 → 授权录屏识别（推荐）；详见 [手机版使用说明](docs/使用说明.md#手机版android)。

**Windows：** 启动 MuMu 或雷电与火影忍者，然后打开计时器：

1. 在「设置 → 常规」选择我方在左或右；也可填写账号名使用自动认边。若我方名字被特效遮住，另一边已精确确认的非我方账号会作为对面证据，从而推断我方在另一侧；证据不足时不猜边。
2. 默认自动寻找运行游戏的模拟器实例。MuMu 继续使用原生 SDK；雷电使用安装目录中的 `ldconsole.exe` + `adb.exe`，实例身份是“安装目录 + 多开编号”，ADB serial 默认是 `127.0.0.1:5555 + 实例编号`。多开或需要指定时，在「设置 → 模拟器」选择对应 provider 和实例后保存并应用。
3. 进入对局后自动识别豆数并计时。训练场也可使用；看不清时显示未知，不凭旧画面猜测替身。

Windows 版需要已安装且正在运行的受支持 MuMu 或雷电；雷电无需额外 SDK，只需使用本机安装目录中的 `ldconsole.exe` 和 `adb.exe`。不需要 Go、Python 或额外安装 OCR/VC 运行库。软件未签名，Windows 仍可能显示下载信誉提示。

## 更新与版本

悬浮窗标题与「关于与更新」显示当前版本。正式版默认在启动约 10 秒后检查 GitHub Releases，之后最多每 6 小时检查一次；发现新版时浮窗出现入口。自动检查只提醒，**绝不自动下载、安装或重启**，可在「设置 → 常规 → 外观与更新」关闭。点击「检查更新」可立即刷新并查看发布日期和更新日志。

在线更新流程：下载 EXE → 验证 GitHub 提供的 SHA256、文件大小和版本 → 点击「重启并更新」。断网、下载中断或校验失败不会覆盖当前程序。更新保留配置，并保存一个旧 EXE 备份。v0.1.x / v0.2.0 用户先手动检查或下载一次 v0.2.1；之后新版可自动提醒。

## 设置、数据与诊断

- 安装版配置和诊断录制：`%APPDATA%\NarutoTimer`；便携版：程序目录中的 `config.json` 和 `debug`。
- OCR 与更新缓存：`%LOCALAPPDATA%\naruto-timer`。卸载程序保留个人设置和日志。
- 「设置 → 常规 → 外观与更新」可调整**计时浮窗**不透明度（40%–100%）和字号（80%–160%）；拖动立即预览，松开后保存。设置、更新和错误窗口始终不透明，避免影响操作。
- 迷你模式使用独立的无标题栏悬浮屏，窗口就是计时底板本身，和安卓迷你卡片一样显示「我方·…」和倒计时；拖动底板移动，拖右边缘调长度、下边缘调高度（数字随高度缩放），大小和位置都会记住。点击计时区域展开设置、换边、两边计时和「退出迷你」（回到完整浮窗），主设置仍从「设置」窗口进入。可用 `Ctrl+M` 切换。
- 「诊断」可查看采集来源、OCR 状态、延迟报告和原帧录制；也可独立验证 MuMu SDK，导出支持诊断 ZIP。ZIP 默认不含原帧 PNG，包含实际配置、MuMu 安装目录、实例号、PID、SDK DLL、连接阶段和日志。
- 采集函数耗时不等于端到端延迟；没有有效帧或真实绘制回调时不报告完整端到端数据。

详见 [使用说明](docs/使用说明.md)、[诊断说明](docs/诊断说明.md) 和 [Android 原生开发](docs/安卓原生开发.md)。

## 复用与二次开发

如果你要实现「火影替身计时器」「替身 CD 识别」「模拟器截图识别 + 悬浮窗」或「Android 录屏识别计时」，本仓库已有从画面采集、识别到计时显示的完整实现，可先从以下入口评估复用。更详细的模块职责、命令行工具、测试入口和专题文档见 **[项目索引](docs/INDEX.md)**。

| 要解决的问题 | 已有实现 / 文档 |
| --- | --- |
| Windows 计时器与悬浮窗（Go / Fyne） | [`cmd/timer-app`](cmd/timer-app)、[`internal/ui`](internal/ui) |
| RGB 像素识别 / NCC 模板匹配 / 场景识别 | [`internal/engine/rgb`](internal/engine/rgb)、[`internal/match`](internal/match)、[`internal/scene`](internal/scene) |
| 替身事件与冷却计时状态 | [`internal/app/timer.go`](internal/app/timer.go)、[`internal/ui/run.go`](internal/ui/run.go) |
| 本地 OCR 账号识别与自动认边 | [`internal/ocr`](internal/ocr)、[`internal/hudtext`](internal/hudtext) |
| MuMu / 雷电截图采集与多开实例接入 | [`internal/capture`](internal/capture)、[模拟器使用说明](docs/使用说明.md#模拟器与多开)、[雷电适配](docs/leidian-adapter.md) |
| Android 录屏采集、C++ 识别与原生悬浮窗 | [`android`](android)、[Android 原生开发](docs/安卓原生开发.md) |
| 不同分辨率下的豆 / 头像区域校准 | [区域校准](docs/区域校准.md) |

这是完整应用，**不是可直接 `go get` 引入的公共 SDK**；Go 核心实现位于 `internal/`，移植时需按目标场景验证采集接口、布局、模板和识别阈值。原创代码可按 MIT 许可复用，游戏素材、OCR 模型等须分别遵守 [第三方声明](THIRD_PARTY_NOTICES.md)，不应将整包资源视为 MIT 授权。

## 开发

Windows 原生构建的验收路径需要 Go 1.26.10（`go.mod` 的 `toolchain`；旧的 Go 1.26.x 会自动下载它）、PowerShell 和 Fyne 所需的 Windows x64 C/C++ 工具链（cgo）。在 Developer PowerShell 中：

```powershell
$env:TIMER_VERSION = "v0.3.1"
.\build.bat
```

脚本先执行 `scripts\fyne-diagnostics\prepare.ps1`，再生成 `bin\timer-app.exe`（以及其余四个 EXE）；不要用 `CGO_ENABLED=0` 替代该构建。Linux 可用已安装的 MinGW-w64/llvm-mingw 交叉编译，具体的可复现命令、工具链版本和边界见 [开发与发布](docs/开发与发布.md)。交叉编译产物不等同于 Windows 运行验收。部分历史图像回归需要本地 `inbox/` 证据，不随仓库分发；便携资源、更新协议、配置和合成界面测试可独立运行。

代码入口：`cmd/timer-app`、`internal/ui`、`internal/frame`、`internal/engine`、`internal/updates`。

## 许可

原创代码为 [MIT](LICENSE)。游戏素材、OCR 模型和微软运行库保留各自权利，见 [第三方声明](THIRD_PARTY_NOTICES.md)。程序仅处理游戏画面，不读取游戏内存，不注入或修改游戏。
