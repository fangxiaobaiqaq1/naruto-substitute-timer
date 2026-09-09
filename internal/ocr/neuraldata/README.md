# 内置本地 OCR 资源

Windows x64/cgo 发布版将下列两份文件编译进 EXE，通过本机子进程完成 PP-OCRv4 文字行识别。姓名条位置由程序提供，不包含整图文本检测模型。运行时不需要 Python、RapidOCR Python 包或系统中文 OCR 语言包，也不联网下载模型或上传截图。

| 文件 | 来源 | 字节数 | SHA-256 |
| --- | --- | ---: | --- |
| `recognizer.onnx` | RapidOCR 1.4.4：`ch_PP-OCRv4_rec_infer.onnx` | 10,857,958 | `48fc40f24f6d2a207a2b1091d3437eb3cc3eb6b676dc3ef9c37384005483683b` |
| `onnxruntime.dll` | ONNX Runtime 1.27.0，Windows x64 CPU | 17,321,312 | `e6de9dcd61a85c4100edafe286d3fd2639513210b81b9dd50d200c4199a06c01` |

两份文件均直接取自官方 PyPI 分发包，未修改、未重新训练。归档 URL、归档 SHA-256 和成员路径见 [provenance.json](provenance.json)。记录中的归档哈希已与 PyPI 元数据核对，归档内成员也已与此目录文件逐字节核对。名字库另在 `assets/game/` 与 `internal/hudtext/data/`，不属于神经网络权重。

模型在内存中加载。运行库首次使用时释放至当前用户缓存目录 `naruto-timer/ocr/<SHA256>/onnxruntime.dll`，同版本复用；加载前校验内容哈希，不从工作目录或 PATH 搜索 DLL。模型缓存不保存用户画面。

Windows 10/11 x64 还需 Microsoft Visual C++ v14 x64 运行库；内置 ONNX DLL 不包含这项系统依赖。[ONNX Runtime 官方要求](https://onnxruntime.ai/docs/install/) 指定 Visual C++ 2019 运行库并建议最新版本；[Microsoft 官方下载页](https://learn.microsoft.com/en-us/cpp/windows/latest-supported-vc-redist) 提供兼容的最新版 x64 运行库。本项目不随包分发该安装程序。

许可文件：

- `OCR-MODEL-LICENSE.md`：RapidOCR 的 Apache-2.0 许可。模型基于 [PaddleOCR](https://github.com/PaddlePaddle/PaddleOCR)，保留上游许可与归属。
- `ONNX-RUNTIME-LICENSE.md`：Microsoft ONNX Runtime 的 MIT 许可。
- `ONNX-RUNTIME-NOTICES.md`：随相同 ONNX Runtime 包提供的第三方声明。
- Go 调用绑定 [onnxruntime_go v1.24.0](https://github.com/yalue/onnxruntime_go/tree/v1.24.0) 使用 MIT；许可随发布包的 Go 依赖许可一起分发。

本目录的上游材料保留其原许可；项目 MIT 许可不会改写第三方许可。更新文件时应同步版本、哈希和许可声明，并重新验证实际发布 EXE 的子进程识别及超时回收。
