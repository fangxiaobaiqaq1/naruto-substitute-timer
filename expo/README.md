# Expo iOS 前台版

优先兼容当前高版本 iOS，使用稳定版 **Expo SDK 58 / React Native 0.88 / React 19.3**，不再为了 iOS 13/14 固定老 SDK。当前最低系统为 **iOS 16.4**；重点验收 iOS 18、iOS 26 系列，具体系统是否可用仍以自签真机测试为准。

## GitHub Actions 未签名 IPA（用户自行签名）

`.github/workflows/ios-unsigned.yml` 使用标准 `macos-26` runner、Xcode 26.6 和 Node 22 编译 Release 真机包，再打包成 `Payload/*.app` 格式的未签名 IPA。不需要 Apple 开发者会员、证书、Expo 账号或仓库 Secrets；安装时由用户用爱思助手等工具重新签名。

普通提交和现有 `v*` 发布标签不会触发此工作流，不影响 Android / Windows 发布。有两个入口：

- **构建当前分支**：先提交 `expo/` 源码、锁文件和工作流，然后在该提交上推送一个未使用的专用标签：

  ```bash
  git push origin v0.23beta
  tag="ios-unsigned-$(date +%Y%m%d-%H%M%S)"
  git tag "$tag"
  git push origin "$tag"
  ```

  标签指向哪个提交，就构建哪个提交，无需先合并默认分支。
- **网页手动运行**：工作流进入仓库默认分支后，在 Actions → **iOS unsigned IPA** → **Run workflow** 选择包含该工作流和 `expo/` 的分支。

成功后，在该次运行的 **Artifacts** 下载 `naruto-timer-ios-unsigned-<运行编号>`，解压外层 ZIP，得到 `naruto-timer-unsigned.ipa`。Artifact 保留 7 天，任务最多运行 60 分钟。

CI 执行 TypeScript 和生命周期检查，并验证真机平台、arm64 主程序、内置 JS bundle、App/framework 的最低系统版本声明以及 ZIP 完整性。不会把模拟器 `.app` 或需要 Metro 的开发包当作 IPA。

## 兼容性与边界

- 专门适配放在 [ios-compat/](ios-compat/README.md)：前后台切换计时、小屏布局、键盘和安全区，以及真机验收清单。
- 使用新 SDK 的原生启动页插件与 `react-native-safe-area-context`，不再维护旧 iOS 14 / Inferno 运行目标。
- 当前仅有前台手动计时、迷你 pill、左右/双边计时、照美冥 15/10 秒倒计时和设置持久化。
- ReplayKit / 游戏画面识别仍未接入；`plugins/withReplayKitPlaceholder` 仅写入占位状态，不会创建 Broadcast Upload Extension。界面不会把占位状态误报成采集成功。
- iOS 不允许普通 App 像 Android 系统悬浮窗一样覆盖第三方游戏。真实跨 App 识别需另行设计 ReplayKit 原生扩展和结果展示。
- 免费 Apple 账号签名通常每 7 天需要续签，不支持规划中的 App Groups；编译成功也不代表任意签名工具、证书和系统组合均已验证。

## 本地开发与检查

使用 Node 22.13+（22.x）或 24.3+（24.x）：

```bash
npm ci --include=dev
npm run typecheck
npm test
npm run start
```

macOS / Xcode 下可运行 `npm run ios` 验证前台界面。iOS 原生目录由 prebuild 生成，长期原生修改应写成 config plugin，不要只修改被忽略的 `ios/` 目录。Linux 可做 TypeScript、JS 打包及 prebuild 检查，但不能执行 Xcode 真机编译。

如选择 EAS：

```bash
npx eas login
npm run build:ios:simulator
# 或：需要 Apple Developer 签名凭据的真机包，与未签名 Actions 路线不同。
npm run build:ios:device
```

模拟器产物是 `.app` 压缩包，不能装到 iPhone。EAS 模拟器构建不需要苹果签名，真机 `preview` 构建需要匹配的签名凭据。
