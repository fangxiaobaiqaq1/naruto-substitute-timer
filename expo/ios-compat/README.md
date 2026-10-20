# iOS 兼容性（高版本优先）

用户要求优先兼容高版本 iOS，而不是为了少量旧设备锁住老 SDK。本目录维护实际需要的生命周期适配和检查，不复制一套业务代码。`expo/ios/` 是 prebuild 生成物，长期原生修复应写入 `expo/plugins/`。

## 当前目标与工具链

- **最低系统**：iOS **16.4**（Expo SDK 58 的要求），arm64 iPhone。iOS 13/14 不再是构建或验收目标。
- **优先验收**：iOS 18、iOS 26 系列的自签安装、启动、布局和生命周期；不把“最低版本”误当“主要支持版本”。更新系统须真机回归，不承诺未测试版本。
- **稳定工具链**：Expo 58.0.16 / React Native 0.88.0 / React 19.3.0；GitHub `macos-26`、Xcode 26.6、iOS 26.5 SDK。未采用 Xcode 27 预览版，避免测试版工具链影响可复现性。
- **依赖匹配**：版本来自 Expo 的 `bundledNativeModules.json` 和官方 SDK 58 模板；锁文件随源码提交。使用新架构默认配置，不再维持旧版 React Native 兼容分支。
- **设备形态**：竖屏 iPhone，使用原生安全区适配刘海/灵动岛；iPad 暂走 iPhone 兼容模式，不宣称已完成横屏和平板布局。

## 已处理

- 倒计时使用绝对截止时间；后台和无有效倒计时时停止周期刷新，回到前台立即校正，不申请后台保活权限。
- 双边计时按空间换行、保留系统字体缩放，设置页处理键盘避让，主要操作区域至少 44 点。
- 新版安全区组件替代 React Native 已弃用的 SafeAreaView；主界面和独立 Modal 分别处理安全区。
- 启动页使用 `expo-splash-screen` config plugin，不依赖旧版 `splash` 配置。
- Release 包内置 JS/Hermes bundle，不依赖 Expo Go 或 Metro。未签名构建不包含开发者证书。
- ReplayKit 不可用时只显示前台演示，不假装等待真实广播；App Groups、游戏识别和跨 App 悬浮显示不在此次构建范围内。

## 检查

在 `expo/` 执行：

```bash
npm ci --include=dev
npm run typecheck
npm test
npx expo install --check
```

GitHub Actions 另验证 iPhoneOS 平台、arm64 主程序、内置 JS bundle、App/嵌入 framework 的 `MinimumOSVersion` 声明和 IPA ZIP 完整性。编译和这些检查通过**不等于真机兼容性已验收**。

## 必须补做的自签真机验收

1. 当前稳定高版本 iOS：爱思签名安装、冷启动、断网启动，不请求 Metro、不闪退；确认工具完整重签嵌入 frameworks。
2. 小屏、刘海/灵动岛，大号系统字体下的单/双边计时、迷你控制和设置操作。
3. 设置页键盘不遮挡操作；“完成”与系统下滑关闭后可再次打开。
4. 15 秒/10 秒计时中切后台或锁屏后返回，剩余时间按实际经过时间更新，已过期的不重新开始。
5. 设置保存、重启恢复、免费签名续签，以及至少一台 iOS 16.4–18 的回归设备。

旧的 SDK 51 / Xcode 16.2 构建实验见 Git 历史，不再为 iOS 14 安装额外旧模拟器组件或限制当前 SDK 升级。
