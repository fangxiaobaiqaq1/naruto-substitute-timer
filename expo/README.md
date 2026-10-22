# iOS 原生版 · v0.3.0

基于正式版 `v0.3.0`（`325eee5`），开发分支 **`ios/v0.3.0-native`**。不是 Expo Go 演示或网页套壳：主 App 使用 Expo / React Native，游戏采集由 **ReplayKit Broadcast Upload Extension** 完成，共享最新版 Android 的 C++ 视觉核心，跨应用显示使用原生 **AVSampleBufferDisplayLayer 画中画**。

## IPA：GitHub 云编译，用户自签

工作流：[`../.github/workflows/ios-unsigned.yml`](../.github/workflows/ios-unsigned.yml)。

1. 推送本分支；`ios/**` 分支中的 iOS / 识别核心修改会自动构建，也可在 Actions → **iOS unsigned IPA** → **Run workflow** 选择分支。非默认分支也可推送专用 `ios-unsigned-*` 标签触发。
2. 打开对应运行的 **Artifacts**，下载 `naruto-timer-ios-unsigned-<编号>`。
3. 解压下载的 ZIP，得到 **`naruto-timer-unsigned.ipa`** 及 **SHA256**。不要把外层 artifact ZIP 当作 IPA。
4. 用自己的签名工具重签主 App、`PlugIns/NarutoBroadcast.appex` 以及嵌入 frameworks，再安装。**不得删除扩展、只签主 App，或把扩展包标识改成与主 App 无关的标识。**

构建不需要 Apple 证书、Developer Team、Expo 账号或仓库签名 Secrets；未签名 IPA 本身不能直接安装。Personal Team 的期限/应用数量由 Apple 决定；签名过期需重新签名。已签名实际安装是否支持特定签名工具，仍须真机验证。

### 构建做了什么

- `macos-26` runner，自动选择**已安装的最新正式 Xcode**（至少 26.6），不选 Beta/RC、不假设某个尚未安装的 Xcode 路径。日志记录确切 Xcode / SDK；runner 没有新 SDK 时，不把旧 SDK 伪称为新 SDK。
- Node 22 + 锁定的 Expo 58 / React Native 0.88，Release arm64 真机包，包含离线 JS/Hermes bundle。
- TypeScript、生命周期、桥接、校准跨端规则与 C++ 计时回归。
- prebuild 生成完整广播 target、编译依赖与 PlugIns 嵌入关系；检查扩展声明、原生源文件和资源。
- 验证实际 `.app` / `.appex` 的 iPhoneOS、arm64、版本、最低系统声明，逐个核对识别 PNG 与原文件字节一致，最后打包并验证 IPA。
- Artifact 保留 14 天。编译失败另保存 Xcode 日志和工程诊断，**失败不发布“可用”空壳 IPA**。

## 使用

1. 在首页手动选 **「我方左边 / 我方右边」**。可记住选择、一键换边，或显示两边；iOS 按用户要求不做自动认边、不读取游戏账号，不运行账号 OCR。
2. 点「准备采集」，再点「开启画中画」，保持原生预览在屏幕内，等系统真正开启 PiP。
3. 点击原生系统录屏按钮，选择「替身计时器」，确认「开始直播」。麦克风不采集；录屏权限不能绕过。
4. 切到游戏，进入决斗/训练场。扩展从实际游戏画面分析，主 App 的原生画中画显示结果；不依赖后台 JavaScript 定时器。
5. 用系统控制中心停止录屏，或返回 App 点「停止采集与画中画」。关闭 PiP / 系统媒体中断时停止采集，避免留着无显示的后台工作。

如果系统尚未允许 PiP，界面显示等待/失败原因，不把调用按钮成功当作画中画已经运行。录屏中断、画面遮挡、连接超时只撤销视觉证据，**已经确认的倒计时继续按真实经过时间走**。

## 功能与 iOS 对应方式

| 最新版功能 | iOS 实现 / 边界 |
|---|---|
| 自动识别豆、忍者、场景 | 直接编译共享 C++ 场景/头像/标题/特殊豆算法；支持决斗和训练、4/6 颗豆、特殊配色/下移布局 |
| 替身计时 | 原生状态机；新鲜帧确认、去重、掉帧重新同步、恢复即再次替身、换轮/整局计数 |
| 特殊忍者 | 照美冥[五代目水影]、半藏[秽土转生]同时 15/10 秒；自动识别与完整变体名覆盖，按每侧独立处理 |
| 我方位置 | 仅手动左/右，记住位置、一键换边；不做账号文字识别或自动认边 |
| 双边/对面、迷你、字号、透明度 | 主界面支持；PiP 支持单/双边和字号，位置/缩放/贴边由系统手势管理。iOS 不开放任意透明、任意长宽的 Android 悬浮窗 |
| 区域校准 | 从「文件」选择完整游戏截图，画/拖/缩放四个区域、像素微调、恢复自动；使用和电脑/安卓相同的方案 JSON，可导入/导出。文件截图没有该帧自动识别几何，不伪造自动框；特殊下移忍者请选普通忍者截图校准 |
| 设置 | 自动持久化，输入范围校验、错误提示；主 App 将有效设置传给扩展 |
| 诊断 | 实时连接/录屏/PiP/豆数/忍者/计数/尺寸/识别耗时，导出脱敏 JSON。不包含账号、截图、原始录屏 |
| 更新 | Gitee / GitHub 版本检查、发布说明、自动检查开关、IPA 云构建入口；iOS 更新必须自行重签，不下载 Android APK / Windows EXE |
| 手动备用 | 前台手动计时；明确隔离自动识别状态，不冒充后台自动功能 |

### 不能直接照搬的系统行为

iOS 普通应用没有 Android `SYSTEM_ALERT_WINDOW` / 无障碍截屏权限，也不能像 Windows 读取模拟器窗口；替代方式是用户授权 ReplayKit + PiP，而不是私有 API / 越狱 / 静音音频保活。PiP 不能承载可点的 React Native 设置面板，修改设置要返回主 App。

本版不声称包含 Windows 的原帧/滚动录像录制器、模拟器诊断、桌面快捷键；诊断文件也不冒充包含这些数据。截图校准使用用户选择的游戏截图，不伪装成自动抓取当前游戏帧。自动认边、账号 OCR 与账号图片模板按用户最新要求不纳入 iOS 版。

## 系统、隐私与资源

- 最低 **iOS / iPadOS 16.4**，iPhone / iPad，横竖屏、安全区、独立设置 sheet、键盘避让和动态字体。重点验收最新 **iOS 27 系列**，保留 26 / 18 回归；适配源码/编译通过不等于已在这些设备实测。
- 2026-10-22 核对 [Apple 安全更新](https://support.apple.com/en-us/100100) 列出的最新系统为 27.0.1；[Xcode 支持表](https://developer.apple.com/support/xcode/) 与 [runner 镜像清单](https://github.com/actions/runner-images/blob/main/images/macos/macos-26-Readme.md) 可能更新不同步，以构建日志为准。
- 无 App Groups / iCloud / Push 依赖。扩展只通过 **127.0.0.1** 传输有界识别结果/控制消息；屏幕原帧不出扩展、不上传到服务器。更新检查只访问发布 API。
- 免费自签也要包含扩展。扩展会受到系统内存限制；按 1280 宽缩小帧、限制缓存、控制识别频率。内存不足或识别失败应明确显示，不用错误状态猜结果。

## 开发与验证

```bash
cd expo
npm ci --include=dev
npm run typecheck
npm test
bash native/recognition/tests/run.sh
npx --no-install expo prebuild --platform ios --no-install
node ios-compat/validate-project.cjs
```

macOS 再执行 `pod install --project-directory=ios` / `npm run ios`。`expo/ios/` 是生成物，持久修改在 `plugins/`、`native/`、`modules/naruto-capture/ios/`。Linux 可以验证 JS、C++ 和工程生成，不能替代 Xcode arm64 编译，更不能替代游戏真机运行。

发布前按 [真机验收清单](ios-compat/README.md) 检查后台 PiP、采集方向、画面识别、扩展内存和自签安装。**未做的验收必须保留“待验证”标记。**
