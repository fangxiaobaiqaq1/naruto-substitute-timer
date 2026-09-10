# 安装包构建

`timer.iss` 生成当前用户安装版；不请求管理员权限。默认目录为 LocalAppData/Programs/NarutoTimer，个人配置位于 AppData/NarutoTimer。

使用 Inno Setup 6（本次验证编译器6.2.2）。可设置环境变量 INNO_ISCC，运行 build.ps1。编译器不进入源码仓库或用户发布包。

ChineseSimplified.isl 来自 Inno Setup 官方源码仓库的 Files/Languages/ChineseSimplified.isl；保留文件内译者信息和上游注释。项目其余原创安装脚本采用 MIT。语言文件中的新增消息在旧编译器中可能被忽略，不影响已支持的中文安装步骤。

ReleaseDirectory 必须包含 timer-app.exe 及完整 licenses/。发布前验证全新用户目录、覆盖升级、快捷方式、卸载，以及卸载后个人数据仍在。
