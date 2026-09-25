# v0.2.6 · 自动更新 Gitee/GitHub 适配

## 更新内容

- 自动更新现支持在 **Gitee** 与 **GitHub** 间切换；新配置及旧配置迁移后的默认更新源均为 Gitee。
- Gitee 更新使用 Release 官方附件接口，仅接受 `timer-app.exe` 与配套 `SHA256SUMS.txt`，下载前读取校验文件，下载后再次核对 EXE 的大小和 SHA-256；两个更新源分别使用独立缓存。
- GitHub 仍可在设置中选择；项目内与更新无关的 GitHub 仓库/发布链接保持不变。

## 发布附件

本次只发布在线更新所需的两个附件：

- `timer-app.exe`
- `SHA256SUMS.txt`

不提供源码压缩包、安装包或大型归档附件。

## 已知限制

本版本在 Linux 上完成了交叉编译与自动化测试；Windows x64 的 Fyne/CGO 运行、MuMu 连接及实际更新流程仍须由用户按 `docs/windows-mumu-acceptance.md` 手动验收，尚未在本次发布中确认。
