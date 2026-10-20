# Expo 移动端

这是替身计时器的 Expo iOS 前台版。SDK 固定在 51，iOS deployment target 为 13.4，以覆盖 Inferno 当前可启动的 iOS 14 环境。

## GitHub Actions 未签名 IPA（用户自行签名）

`.github/workflows/ios-unsigned.yml` 在标准 `macos-15` runner 上使用 Xcode 16.2 / Node 22 编译 Release 真机包，再打包成 `Payload/*.app` 格式的未签名 IPA。不需要 Apple 开发者会员、证书、Expo 账号或仓库 Secrets；安装时由用户用爱思助手等工具重新签名。

工作流不会在普通提交或 `v*` 发布标签上自动运行，也不影响现有 Android / Windows 发布。有两个入口：

- **当前分支首次构建**：先将 `expo/` 源码、`package-lock.json` 和工作流一起提交（不要只上传工作流），然后在该提交上创建并推送专用标签：

  ```bash
  # 在已经提交上述文件的 v0.23beta 分支运行；后续构建换一个新标签。
  git push origin v0.23beta
  git tag ios-unsigned-v0.23beta-1
  git push origin ios-unsigned-v0.23beta-1
  ```

  标签指向哪个提交，就构建哪个提交，不需要先合并默认分支。
- **网页手动运行**：工作流进入仓库默认分支后，在 Actions → **iOS unsigned IPA** → **Run workflow** 选择 `v0.23beta` 或其他包含 `expo/` 源码和该工作流的分支。

成功后，在该次运行的 **Artifacts** 下载 `naruto-timer-ios-unsigned-<运行编号>`，解压外层 ZIP，得到 `naruto-timer-unsigned.ipa`，交给用户自签。Artifact 保留 7 天，单次任务最多运行 60 分钟。工作流会检查真机平台、arm64 架构、内置 JS bundle 和 IPA 压缩包完整性，避免把模拟器包或依赖 Metro 的开发包当成 IPA。

这只打包当前前台计时版，不会自动补齐 ReplayKit、屏幕识别或跨应用悬浮窗。免费 Apple 账号自签通常需要每 7 天续签，也不支持后续方案中的 App Groups；实际安装和运行仍需用户设备验收。

iOS 13.4+ / arm64 兼容目标、后台恢复计时、小屏/键盘适配及真机检查清单见 [ios-compat/README.md](ios-compat/README.md)。CI 会运行 `npm test` 并检查产物声明的最低系统版本，不能以云端构建成功代替真机验收。

## 本地开发与 EAS 签名构建

```bash
npm install
npm run start
```

在 macOS + Xcode 的 iOS Simulator 中验收前台界面：

```bash
npm install --include=dev
npx expo start --ios
```

这条路径可以验证 pill、操作栏、设置持久化和前台倒计时；ReplayKit 占位状态会按预期显示。当前 Linux 工作区没有 `xcrun` 或 Xcode，无法在这里启动 Apple Simulator。

日常开发优先使用真实手机或 Android；Inferno 用于最后的 iOS 14 IPA 验收。当前 Expo Go 不适合直接作为 Inferno 客户端，后续需要用 EAS 或 macOS 构建自定义 development client / device IPA。

构建给 Inferno 的包使用 device profile，而不是 iOS Simulator `.app`：

```bash
npx eas build --platform ios --profile preview
```

如果目标是 iOS Simulator，使用专门的 simulator profile；EAS 会返回包含完整 `.app` 的压缩包，模拟器安装的不是设备 IPA：

```bash
npm run build:ios:simulator
```

需要 EAS 签名设备包时使用 `npm run build:ios:device`，它与上面的未签名 GitHub Actions 路线不同。两种 EAS 构建都需要先执行 `eas login`；设备包安装到兼容设备，模拟器包安装到 macOS/Xcode 的 iOS Simulator。

EAS 真机 `preview` 构建需要配置 Apple Developer 签名凭据；`simulator` 构建不需要苹果签名。Inferno 侧的 IPA 安装和系统恢复仍需单独验证。

当前前台版已经对齐 Android 的计时语义：迷你 pill、点击操作栏、自动/左右认边、双边计时、照美冥 15/10 秒双倒计时、设置持久化和透明度/字号参数。左侧掉落和右侧掉落按钮用于在没有识别输入时验收计时状态。

截图识别和 ReplayKit Broadcast Upload Extension 尚未接入。它们需要 Expo config plugin、App Group 和 Xcode 原生 target，不能依赖 Expo Go 的纯 JS 运行时。iOS 也不允许普通 App 像 Android `TYPE_ACCESSIBILITY_OVERLAY` 一样覆盖第三方游戏；跨 App 识别必须由用户在系统录屏控件中显式启动广播，计时结果再通过 App Group 回传。当前 UI 会明确显示这一状态，不会把前台演示误报成已连接识别。

工程中的 `plugins/withReplayKitPlaceholder` 只会在生成的 iOS `Info.plist` 中写入占位状态，不会自动创建 ReplayKit Broadcast Upload Extension，也不会提供实际屏幕采集原生模块。`replayKit.ts` 提供兼容 API；没有原生桥接时返回 `status: "placeholder"`，因此 Expo Go 和 Android 开发仍可运行前台演示计时。

要接入真实画面采集，需要在 macOS/Xcode 中手动添加并签名 Broadcast Upload Extension，实现 `NarutoReplayKit` 原生模块，再通过 development client 或 EAS device 构建验证。普通 Expo Go 运行时不会加载该扩展。
