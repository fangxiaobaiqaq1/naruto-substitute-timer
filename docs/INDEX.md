# 项目索引：火影忍者手游替身计时器

`naruto-substitute-timer` 是面向腾讯《火影忍者》手游的免费开源**替身计时器**，也可按「火影替身计时器」「火影手游替身 CD」「替身冷却倒计时」「安卓替身计时器」查找。项目通过屏幕图像识别替身豆变化，在悬浮窗显示冷却时间，提供 Windows EXE、Android APK 和源码。

Repository index for an open-source **Naruto Mobile substitution cooldown timer** — also described as a substitute timer or cooldown tracker. Includes Windows / Android apps, screen capture, local OCR, RGB / NCC image recognition, and countdown overlay implementations.

[返回 README](../README.md) · [下载最新版](https://github.com/fangxiaobaiqaq1/naruto-substitute-timer/releases/latest) · [官网与教程](https://hy.312166.xyz) · [GitHub 仓库](https://github.com/fangxiaobaiqaq1/naruto-substitute-timer) · [Gitee 镜像](https://gitee.com/xiaobaiqaq/naruto-substitute-timer)

## 索引目录

- [按搜索词找功能](#按搜索词找功能)：常见叫法、需求和已有实现。
- [下载与使用导航](#下载与使用导航)：电脑、安卓手机、认边、悬浮窗、校准和故障反馈。
- [Windows 源码索引](#windows-源码索引)：Go / Fyne、采集、识别、计时、更新与诊断。
- [Android 源码索引](#android-源码索引)：Kotlin、C++ / JNI、MediaProjection 与原生悬浮窗。
- [命令行与诊断工具](#命令行与诊断工具)：区分正式入口、调试工具和旧流程探针。
- [开发测试与发布](#开发测试与发布)：构建、回归检查、运行验收和发布脚本。
- [专题资料与历史说明](#专题资料与历史说明)：性能、视觉数据、验证记录和设计文档。
- [平台支持与复用边界](#平台支持与复用边界)：哪些能直接用，哪些不能当作已实现。

## 按搜索词找功能

这里将同一功能的常见中文叫法和英文术语对应到实际入口，不表示支持其他火影游戏或任意技能计时。

| 搜索词 / 需求 | 本仓库对应内容 | 从这里开始 |
| --- | --- | --- |
| 替身计时器、火影忍者替身计时器、火影忍者手游替身计时器、火影手游计时器 | 可直接使用的替身冷却计时应用，不只是算法示例 | [下载与使用](../README.md#下载与使用) |
| 替身 CD、替身冷却倒计时、替身豆识别、自动替身计时 | 豆数变化观测、事件确认和冷却状态机 | [计时状态机](../internal/app/timer.go)、[对局处理](../internal/ui/run.go) |
| 双边计时、对方替身计时、训练场计时 | 左右侧独立跟踪；可显示对方或两边计时，训练场可手动认边 | [常规设置](使用说明.md#常规设置) |
| 安卓替身计时器、手机替身计时器、Android cooldown overlay | Android 12+ 原生应用，录屏 / 无障碍截图与悬浮卡片 | [手机版使用说明](使用说明.md#手机版android)、[Android 源码](#android-源码索引) |
| MuMu 替身计时器、雷电替身计时器、LDPlayer 截图、多开实例 | Windows 端模拟器采集适配和实例选择 | [模拟器与多开](使用说明.md#模拟器与多开)、[采集实现](../internal/capture) |
| OCR 自动认边、账号名识别、我方左右识别 | 本机文字识别与账号证据判定，也可手动指定左右 | [HUD 文字处理](../internal/hudtext)、[账号图片匹配](../internal/identity) |
| 游戏图像识别、RGB 像素识别、NCC 模板匹配、computer vision | 已有像素采样、模板匹配、场景和忍者视觉识别实现 | [Windows 源码](#windows-源码索引)、[Android C++ 核心](../android/app/src/main/cpp) |
| 悬浮窗倒计时、迷你计时器、桌面 overlay | Windows Fyne 浮窗、Android 原生浮窗，支持位置与外观设置 | [迷你悬浮窗](使用说明.md#仅计时器迷你悬浮窗)、[Android 悬浮窗](../android/app/src/main/java/com/narutotimer/overlay) |
| 平板适配、带鱼屏、特殊分辨率、豆位置不准 | 豆 / 头像区域校准，以及电脑和手机之间的方案导入导出 | [区域校准](区域校准.md) |
| Naruto Mobile substitution timer、substitute timer、substitution cooldown tracker | 本项目的英文功能描述；目标是腾讯火影忍者手游的替身冷却 | [项目介绍](../README.md)、[平台支持](#平台支持与复用边界) |
| 开源替身计时器、火影计时器源码、Go / Kotlin / C++ 计时器 | 完整应用源码与相关测试，可据许可评估移植 | [源码索引](#windows-源码索引)、[复用边界](#平台支持与复用边界) |

## 下载与使用导航

| 想完成的事情 | 文档入口 | 关键说明 |
| --- | --- | --- |
| 下载 Windows EXE / 完整 ZIP / Android APK | [最新发布页](https://github.com/fangxiaobaiqaq1/naruto-substitute-timer/releases/latest)、[下载说明](../README.md#下载与使用) | 以发布页实际附件和说明为准；使用成品不需要安装 Go 或 Python |
| 在电脑模拟器上运行 | [首次使用](使用说明.md#首次使用)、[模拟器与多开](使用说明.md#模拟器与多开) | Windows 端支持 MuMu 和雷电；多开时选择正确实例 |
| 在 Android 手机上运行 | [手机版](使用说明.md#手机版android) | Android 12+；按指引开启无障碍服务、启动浮窗并授权录屏识别，无需 Root |
| 配置我方左右、自动认边或两边计时 | [常规设置](使用说明.md#常规设置) | 填写游戏账号名，不是忍者名；证据不足时不猜边 |
| 使用迷你浮窗、调整位置 / 大小 / 透明度 | [迷你悬浮窗](使用说明.md#仅计时器迷你悬浮窗)、[外观设置](使用说明.md#外观与自动更新) | Windows 与 Android 有各自的原生显示实现 |
| 修正平板、带鱼屏或特殊 HUD 的识别区域 | [使用入口](使用说明.md#区域校准特殊分辨率)、[校准方案说明](区域校准.md) | 自动结果正确时无需手动覆盖；方案可导入导出 |
| 排查 SDK、截图、OCR 或计时问题 | [诊断说明](诊断说明.md)、[MuMu 自检与反馈](使用说明.md#mumu-多开sdk-自检与反馈) | 优先提供版本、实例信息和支持诊断包；检查包内个人信息后再公开分享 |
| 检查更新、下载并保留原配置 | [关于与更新](使用说明.md#关于与更新)、[更新说明](../README.md#更新与版本) | 自动检查只提醒，不自动安装或重启；下载后需校验并由用户确认 |

## Windows 源码索引

主要链路：**模拟器采集 → 帧与布局处理 → 场景 / 豆 / 忍者识别 → 观测与计时状态 → Fyne 悬浮窗**。账号 OCR 为左右身份判断提供证据，不直接代替替身事件确认。

| 模块 | 源码入口 | 职责 |
| --- | --- | --- |
| 正式桌面应用 | [`cmd/timer-app`](../cmd/timer-app) | 加载配置、装配采集与识别引擎、启动正式 UI；包含版本查询、自检与更新辅助模式 |
| 引擎装配 | [`internal/engine`](../internal/engine) | 识别引擎与运行时组装入口；阅读正式应用实际使用的路径，勿将所有可选分支视为默认启用 |
| MuMu 采集 | [`internal/capture/mumu`](../internal/capture/mumu) | MuMu 原生截图 SDK、安装与实例接入 |
| 雷电采集 | [`internal/capture/leidian`](../internal/capture/leidian) | 雷电实例、ADB / 窗口截图适配；[适配说明](leidian-adapter.md) |
| 帧处理与坐标转换 | [`internal/frame`](../internal/frame)、[`internal/layout/transform.go`](../internal/layout/transform.go) | 帧处理，以及参考分辨率、客户区、内容区和采集区的坐标转换 |
| 替身豆像素识别 | [`internal/engine/rgb`](../internal/engine/rgb) | RGB 豆位采样与判定，依赖实际 HUD 布局和识别阈值 |
| 模板匹配 | [`internal/match/match.go`](../internal/match/match.go) | NCC（归一化互相关）图像模板匹配 |
| 场景识别 | [`internal/scene`](../internal/scene) | 对局、训练等场景证据与识别门闩，避免任意画面直接触发计时 |
| 替身状态机与观测链路 | [`internal/app/timer.go`](../internal/app/timer.go)、[`internal/ui/run.go`](../internal/ui/run.go) | 左右侧冷却、基线建立、变化确认和重复帧处理 |
| 忍者识别与特殊冷却规则 | [`internal/ninja`](../internal/ninja)、[`rules.go`](../internal/ninja/rules.go) | 忍者名条 / 头像证据、视觉变体和特殊冷却显示规则 |
| 本地 OCR 与 HUD 账号文字 | [`internal/ocr`](../internal/ocr)、[`internal/hudtext`](../internal/hudtext) | OCR 后端与 HUD 文字解析，为自动认边提供账号证据 |
| 账号名图片匹配 | [`internal/identity/identity.go`](../internal/identity/identity.go) | 账号名图鉴与 VS / 换人 / 对局 HUD 身份判断；不是忍者角色分类器 |
| 配置与区域校准 | [`internal/config`](../internal/config)、[区域校准文档](区域校准.md) | 配置默认值、校验、原子保存和校准方案 |
| 桌面悬浮窗与设置 | [`internal/ui`](../internal/ui) | Fyne 正式界面、完整 / 迷你浮窗、设置与更新交互 |
| 时序诊断与回放 | [`internal/diagnostics`](../internal/diagnostics) | 帧与事件记录、时序关联和限额诊断回放 |
| SDK 自检与支持包 | [`internal/support`](../internal/support) | 隔离进程中的 MuMu SDK 检查、诊断 ZIP 导出和隐私处理 |
| 桌面在线更新 | [`internal/updates`](../internal/updates) | GitHub / Gitee 更新检查、下载校验与 Windows 程序替换 |
| 内置视觉资源 | [`assets`](../assets) | 模板、忍者 / 游戏资料和头像资源；权利范围见[第三方声明](../THIRD_PARTY_NOTICES.md) |

## Android 源码索引

主要链路：**MediaProjection / 无障碍截图 → 最新帧分析调度 → JNI / C++ 识别 → Kotlin 会话与计时 → 原生 Canvas 悬浮窗**。接口、资源映射和线程约定见 [Android 架构契约](../android/ARCHITECTURE.md)。

| 模块 | 源码入口 | 职责 |
| --- | --- | --- |
| 画面采集与分析调度 | [`capture`](../android/app/src/main/java/com/narutotimer/capture) | `ProjectionCaptureService`、`ScreenshotCapture`、`FrameLoop`：录屏采集、无障碍截图回退和最新帧分析 |
| Kotlin / JNI 边界 | [`NativeCore.kt`](../android/app/src/main/java/com/narutotimer/NativeCore.kt)、[`jni.cpp`](../android/app/src/main/cpp/jni.cpp) | Kotlin 调用原生识别核心的接口和图像缓冲区边界 |
| 原生识别核心 | [`cpp/src`](../android/app/src/main/cpp/src)、[`cpp/include/nt`](../android/app/src/main/cpp/include/nt) | C++ 图像匹配、场景、豆、忍者、身份和 OCR / HUD 文字处理 |
| 计时与会话跟踪 | [`tracker`](../android/app/src/main/java/com/narutotimer/tracker) | `SideClock`、`Session`、忍者规则和观测节奏；与桌面计时语义对应 |
| 悬浮窗 | [`overlay`](../android/app/src/main/java/com/narutotimer/overlay) | 原生计时卡片、迷你视图和浮窗内设置 |
| 无障碍服务生命周期 | [`service/TimerAccessibilityService.kt`](../android/app/src/main/java/com/narutotimer/service/TimerAccessibilityService.kt) | 管理明确的启停意图；服务连接本身不代表自动开启浮窗或识别 |
| 主界面、权限与校准 | [`ui`](../android/app/src/main/java/com/narutotimer/ui) | `MainActivity`、`ProjectionPermissionActivity`、`CalibrationActivity` |
| APK 在线更新 | [`update`](../android/app/src/main/java/com/narutotimer/update) | 更新检查、APK 校验、文件提供与安装交互 |
| 工程与构建 | [`android`](../android)、[原生开发文档](安卓原生开发.md) | Kotlin / C++ 工程、工具链、权限、构建与发布说明 |

## 命令行与诊断工具

这些入口用途不同。寻找可以直接使用的计时器时，优先选择 `timer-app.exe` 或 Android APK，而不是调试工具。

| 入口 | 用途 | 边界 |
| --- | --- | --- |
| [`cmd/timer-app`](../cmd/timer-app) | 正式 Windows 计时前端 | 普通用户的桌面入口 |
| [`cmd/timer-lab`](../cmd/timer-lab) | `capture`、`replay`、`observe`、`bench`、`ocr`：采集、回放、观察、测量和 OCR 检查 | 诊断 CLI，不是用户前端；`observe` 不等于事件确认到 UI 绘制的端到端测量 |
| [`cmd/timer`](../cmd/timer) | Windows 调试 CLI：窗口、豆位、截图、校准框、场景检查与 ROI 裁图 | 保留的调试入口，不是正式计时 UI |
| [`cmd/timer-gui`](../cmd/timer-gui) | Windows 可视化截图 / 豆位校准 | 独立校准工具，不是 `timer-app` 的设置窗口 |
| [`cmd/scanbeads`](../cmd/scanbeads) | 单张 PNG 的默认豆位 RGB / HSV 采样探针 | 不加载用户配置，不经过正式识别和计时链路 |
| [`cmd/verifypng`](../cmd/verifypng) | 单图场景与豆位验证 | 使用旧 `detect` 流程，不能当作当前生产链路或连续计时验收 |

## 开发测试与发布

| 任务 | 入口 | 验证范围 |
| --- | --- | --- |
| Windows 构建、Linux 交叉编译、Go 定向检查 | [开发与发布](开发与发布.md)、[`build.bat`](../build.bat) | Fyne 需要 cgo；Linux 生成 PE 不代表在 Windows 已运行通过 |
| Windows / MuMu 运行验收 | [验收清单](windows-mumu-acceptance.md) | 真实截图、识别与浮窗运行；不能只凭编译通过下结论 |
| 替身状态机回归 | [`internal/app`](../internal/app)、[`timer_test.go`](../internal/app/timer_test.go)、[`duplicate_test.go`](../internal/app/duplicate_test.go) | 计时规则与重复观测等定向行为，不代替整机实战验证 |
| Android 构建与检查 | [原生开发](安卓原生开发.md)、[架构契约](../android/ARCHITECTURE.md) | Kotlin / C++ 工具链、模块接口和线程约定 |
| Android C++ 主机检查 | [`android/tools/hostcheck.sh`](../android/tools/hostcheck.sh) | 资源加载、编译链接和可运行性，不等于真实设备验收 |
| 豆变化到起钟链路回归 | [链路验证记录](豆变化链路修复验证.md)、[`android/tools/chaincheck/run.sh`](../android/tools/chaincheck/run.sh) | 连续观测和事件确认；部分用例依赖未分发的本地截图 |
| Android / Go OCR 对照 | [`android/tools/ocrhost/run.sh`](../android/tools/ocrhost/run.sh)、[`android/tools/ocrcheck`](../android/tools/ocrcheck) | 本机 OCR 路径与输出对照；环境要求见原生开发文档 |
| 校准方案检查 | [`android/tools/schemecheck/run.sh`](../android/tools/schemecheck/run.sh)、[架构契约](../android/ARCHITECTURE.md) | 方案文件编解码；相关运行环境要求以脚本及文档为准 |
| Fyne 绘制诊断与安装包 | [绘制回调补丁说明](../scripts/fyne-diagnostics/README.md)、[安装包脚本](../scripts/installer/README.md) | 诊断构建和当前用户安装流程 |
| GitHub 发布与 Gitee 镜像 | [发布约定](开发与发布.md#github-发布约定)、[Release workflow](../.github/workflows/release.yml)、[镜像 workflow](../.github/workflows/mirror-gitee.yml) | 版本标签、构建、校验和附件发布；文档更新不需要发布软件新版本 |

**测试数据边界：** 部分历史图像测试依赖本地 `inbox/`、个人配置或专用性能语料，不随仓库分发。不要把「缺少本地样本」误判成公开仓库已有完整可运行语料，也不要为了跑通测试上传用户原图。静态单图正确、主机检查通过、交叉编译成功，都不等于真实设备端到端验收。

## 专题资料与历史说明

| 资料 | 内容与阅读方式 |
| --- | --- |
| [区域校准](区域校准.md) | 校准字段、坐标含义、框大小限制、特殊忍者换算与方案导入导出 |
| [雷电适配](leidian-adapter.md) | 已有本机接入和多开配置；其中云端 RPC 部分属于接入方案，不等于已经部署了云服务 |
| [诊断说明](诊断说明.md) | 采集、延迟、原帧录制、回放、OCR 与更新问题排查 |
| [Windows 性能记录](性能优化-windows.md) | 对应版本、语料和机器上的优化与测量；不要外推为任意硬件性能保证 |
| [Android App 性能记录](性能优化-android-app.md)、[C++ 核心性能记录](性能优化-android-native.md) | 分别覆盖采集 / 分析 / 显示链路和原生识别热点 |
| [分辨率静态验证](分辨率适配验证.md)、[豆变化链路验证](豆变化链路修复验证.md) | 区分单图布局 / 豆数检查与连续事件 / 起钟验证 |
| [视觉数据说明](解包与视觉数据.md) | 对应游戏版本的数据来源、已整理资料及视觉资源限制，不是所有游戏版本都适用的保证 |
| [后端审计记录](审计-后端.md) | 历史版本的发现与当时验证状态，不能作为当前版本已解决所有问题的证明 |
| [更新签名设计](更新签名设计.md) | 标记为未实现的离线签名清单设计，不等于当前已经具备该签名验证能力 |
| [`expo/README.md`](../expo/README.md)、[iOS 兼容说明](../expo/ios-compat/README.md) | Expo / iOS 实验路径及兼容性范围，与 Android 原生正式实现分开阅读 |
| [官网源码](../site)、[官网配置](../site/.vitepress/config.mts) | VitePress 下载 / 教程站点；不是计时识别核心 |
| [发布页](https://github.com/fangxiaobaiqaq1/naruto-substitute-timer/releases)、[本目录](.)中的 `release-*.md` | 版本说明与历史变更；实际是否发布、有哪些附件以发布页为准 |

## 平台支持与复用边界

| 平台 / 形态 | 当前定位 |
| --- | --- |
| Windows 10/11 x64 + MuMu / 雷电 | 正式桌面应用，提供 EXE；通过受支持的模拟器采集游戏画面 |
| Android 12+ 手机 / 模拟器 | 原生 Kotlin + C++ 应用，支持 `arm64-v8a` / `x86_64`；需要用户授予相应截图 / 录屏权限，无需 Root |
| iOS / iPadOS | `expo/` 中的前台手动计时 / 兼容性实验；不等于已实现游戏自动识别或跨应用悬浮计时 |
| Linux / macOS | 不应从开发工具或交叉编译支持推断已有与 Windows / Android 等价的正式客户端 |

- **功能范围是替身计时。** 不应将本项目介绍为已实现通灵、秘卷或完整奥义冷却识别，也不承诺全忍者、全分辨率或零误识别。
- **输入是屏幕图像。** 不读取游戏内存，不注入或修改游戏；本地识别不向服务端上传截图。这些技术边界不代表获得游戏官方认可，也不是风险保证。
- **不是公共 SDK。** Go 代码大量位于 `internal/`，不能当作可直接 `go get` 引入的公共模块；复用时需要整理依赖，并重新验证采集接口、布局、模板、阈值和计时语义。
- **许可分开看。** 原创代码采用 [MIT](../LICENSE)；游戏素材、OCR 模型及运行依赖遵守各自条款，见 [第三方声明](../THIRD_PARTY_NOTICES.md)。不能把整包资源统一当成 MIT 授权。
- **以证据和当前实现为准。** 本索引用于定位已有功能与源码，不承诺其他游戏适配；历史记录、设计方案、实验入口和正式实现已分别标注。
