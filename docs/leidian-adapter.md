# 雷电模拟器（LDPlayer）适配与云端 Agent 接入方案

## 结论

本项目采用 **一个统一采集 SDK 边界 + 两个厂商 provider**：

- `mumu-sdk`：继续使用 MuMu 安装目录中的 `external_renderer_ipc.dll`。
- `leidian-adb`：使用雷电安装目录中的 `ldconsole.exe` 管理实例，用同目录 `adb.exe` 通过 ADB `exec-out screencap -p` 取图。
- 上层识别、计时、诊断、超时、迟到帧和重试策略不区分厂商。

雷电没有被假设成 MuMu 的 DLL ABI。这样不需要分发雷电二进制，也不需要云端 agent 注入模拟器；云端只需要把任务参数传到同一个 provider SDK。

## 已验证的本机雷电 14 实例

当前机器检测到：

- 安装目录：`E:\leidian\LDPlayer14`
- 控制器：`E:\leidian\LDPlayer14\ldconsole.exe`
- ADB：`E:\leidian\LDPlayer14\adb.exe`
- `ldconsole list2`：实例 `0`，标题为“雷电模拟器”，分辨率 `1920x1080`，DPI `280`
- 启动命令：`ldconsole launch --index 0`
- 运行状态：`ldconsole isrunning --index 0` 返回 `running`
- ADB 端口：默认实例 `0` 对应 `127.0.0.1:5555`
- 连接命令：`adb connect 127.0.0.1:5555`
- 设备状态：`adb -s 127.0.0.1:5555 get-state` 返回 `device`
- 属性命令：`adb -s 127.0.0.1:5555 shell getprop ro.product.model`
- 截图命令：`adb -s 127.0.0.1:5555 exec-out screencap -p`

`ldconsole` 的实际帮助还提供 `launch`、`quit`、`reboot`、`list2`、`runninglist`、`isrunning`、`adb`、`getprop`、`runapp`、`killapp`、`installapp`、`pull`、`push` 等命令。实例的稳定身份使用 **安装目录 + index**；PID 只做证据展示，不能持久化为目标。

## 配置

旧 MuMu 配置仍然有效。新增 `capture.provider` 和 `capture.leidian`：

```json
{
  "capture": {
    "provider": "leidian-adb",
    "leidian": {
      "selection": "manual",
      "installDir": "E:\\leidian\\LDPlayer14",
      "consolePath": "",
      "adbPath": "",
      "index": 0,
      "serial": "127.0.0.1:5555",
      "package": "com.tencent.KiHan",
      "connectOnStart": true
    },
    "preferredMethods": ["leidian-adb"],
    "timeoutMs": 2500
  }
}
```

字段含义：

- `provider`: `mumu-sdk`、`leidian-adb`、`auto` 或 `printwindow-fullcontent`。
- `installDir`: 雷电安装根目录。SDK 不随程序分发。
- `consolePath` / `adbPath`: 可选；为空时从 `installDir` 查找 `ldconsole.exe`、`dnconsole.exe` 和 `adb.exe`。
- `index`: 雷电多开器实例编号，`0/1/2...`。
- `serial`: 可选 ADB serial；为空按 `127.0.0.1:(5555+index)` 推导。若管理员改过端口，必须显式填写。
- `connectOnStart`: 启动采集时先执行 `adb connect` 并验证 `get-state=device`。
- `package`: 当前保留为目标包元数据；ADB 截图是整台 Android 显示层，不依赖 MuMu 的 display API。

## SDK 调用方式

Go 代码入口：

```go
import (
    "narutotimer/internal/capture/leidian"
)

client, err := leidian.Open(leidian.Options{
    InstallDir: "E:\\leidian\\LDPlayer14",
    Index:       0,
    Serial:      "127.0.0.1:5555",
    Package:     "com.tencent.KiHan",
    Connect:     true,
})
if err != nil { /* 记录诊断阶段 */ }
defer client.Close()
img, err := client.Capture() // owned top-down RGBA
_ = img
```

程序使用的实际命令序列是：

```text
ldconsole.exe list2
ldconsole.exe isrunning --index 0
adb.exe connect 127.0.0.1:5555
adb.exe -s 127.0.0.1:5555 get-state
adb.exe -s 127.0.0.1:5555 exec-out screencap -p
```

每帧启动一个 adb 子进程，第一版优先稳定和隔离；它不承诺 MuMu 原生 SDK 的 16ms/60fps。云端 agent 做视觉任务时推荐 2–15 fps，或后续把 ADB 连接升级为持久 shell/截图守护进程。采集调用仍在已有单 worker、有界超时调度器内，避免卡死时无限堆积。

## 实例管理

### 本地桌面

1. 扫描 `installDir` 下 `ldconsole.exe` 和 `adb.exe`。
2. 执行 `list2` 获取实例名称、运行标记、PID、分辨率。
3. 对每个实例执行 `isrunning --index N`，修正运行状态。
4. UI 保存 `installDir + index + serial`，不保存 PID/HWND。
5. 手动选择后创建 provider client，测试一帧截图。

### 云端 agent

云端 agent 不应直接猜端口，也不应把 Windows PID 传到云端。建议统一任务协议：

```json
{
  "emulator": {
    "provider": "leidian",
    "installDir": "E:\\leidian\\LDPlayer14",
    "index": 0,
    "serial": "127.0.0.1:5555",
    "package": "com.tencent.KiHan",
    "autoStart": false
  },
  "job": {
    "action": "capture",
    "fps": 5,
    "durationSeconds": 10
  }
}
```

agent worker 在 Windows 主机上执行：

1. 根据 `provider` 选择 `leidian-adb`。
2. 如果 `autoStart=true`，执行 `ldconsole launch --index N`，轮询 `isrunning`。
3. `adb connect serial`，等待 `get-state=device`。
4. 可选执行 `shell pm path <package>` 或 `shell pidof <package>` 验证应用。
5. 周期执行 `exec-out screencap -p`，返回 PNG/RGBA、尺寸、采集完成时间、serial、index 和 source。
6. 任务结束后默认不关闭实例；只有显式 `autoStop=true` 才执行 `ldconsole quit --index N`。

云端服务只负责调度/鉴权/任务状态，真正的 ADB 和 `ldconsole` 必须在能够访问雷电安装目录的 Windows worker 上运行。Linux 云端容器不能直接调用宿主机的 `E:\leidian`，应通过 worker RPC、队列或反向连接提交任务。

## 推荐的云端 RPC 形状

```text
CreateCaptureJob(provider, installDir, index, serial, package, fps, duration)
  -> jobId
WorkerProbe(jobId)
  -> resolved paths, list2 row, adb state, package state, screen size
StreamFrame(jobId)
  -> {seq, capturedAt, width, height, rgba/png, source, duplicate}
StopCaptureJob(jobId)
  -> final metrics and errors
```

最小 agent 实现可以不做完整视频流：先让 worker 每次返回一张 PNG 和结构化 probe JSON。以后只替换 transport，不改变 `leidian.Client`、`capture.Client` 和 `frame` 分析层。

## 兼容性与限制

| 项目 | MuMu | 雷电 |
|---|---|---|
| 实例身份 | 安装目录 + MuMu index | 安装目录 + LD index |
| 像素来源 | 私有 `external_renderer_ipc.dll` | ADB `screencap -p` |
| 多开枚举 | `MuMuManager info -v all` | `ldconsole list2` + `isrunning` |
| 默认连接 | vendor connect | `adb connect host:port` |
| 包/显示层 | 可用 MuMu display API | 整屏截图，包仅用于校验 |
| 运行性能 | 原生 SDK，低开销 | 每帧 adb 进程，延迟更高 |
| 需要分发模拟器文件 | 否 | 否 |
| 需要 Windows worker | 是 | 是 |

风险：雷电不同大版本可能改变 `list2` 字段或 ADB 端口。实现对 `list2` 使用容错字段解析；生产部署应在 worker 启动时记录 `ldconsole` 版本、`adb version`、`list2` 原文和端口探测结果。

## 目录与测试

新增代码：

- `internal/capture/client.go`：统一截图 client 边界。
- `internal/capture/provider.go`：provider registry 和方法常量。
- `internal/capture/registry_windows.go`：MuMu/雷电 provider 分发。
- `internal/capture/leidian/client_windows.go`：雷电实例、ADB、截图、probe。
- `internal/capture/leidian/client_test.go`：`list2`、工具路径和实例 serial 测试。

本机检查：

```powershell
go test ./internal/config ./internal/capture/leidian ./internal/frame ./internal/support
& E:\leidian\LDPlayer14\ldconsole.exe list2
& E:\leidian\LDPlayer14\ldconsole.exe isrunning --index 0
& E:\leidian\LDPlayer14\adb.exe connect 127.0.0.1:5555
& E:\leidian\LDPlayer14\adb.exe -s 127.0.0.1:5555 get-state
& E:\leidian\LDPlayer14\adb.exe -s 127.0.0.1:5555 shell wm size
```

UI 当前仍是 MuMu 专用的实例选择页；下一步应把扫描/测试控件提取为通用 provider UI，再加入“MuMu / 雷电”单选项。核心 SDK、配置和 frame dispatch 已经完成，不需要复制一套新的 agent 逻辑。
