# 内置本地 OCR 资源

Windows x64/cgo 发布版将下列两份文件以及 `vcruntime/` 中的四个 VC 运行库 DLL 编译进 EXE，通过本机子进程完成 PP-OCRv4 文字行识别。姓名条位置由程序提供，不包含整图文本检测模型。运行时不需要 Python、RapidOCR Python 包或系统中文 OCR 语言包，也不联网下载模型或上传截图。

| 文件 | 来源 | 字节数 | SHA-256 |
| --- | --- | ---: | --- |
| `recognizer.onnx` | RapidOCR 1.4.4：`ch_PP-OCRv4_rec_infer.onnx` | 10,857,958 | `48fc40f24f6d2a207a2b1091d3437eb3cc3eb6b676dc3ef9c37384005483683b` |
| `onnxruntime.dll` | ONNX Runtime 1.27.0，Windows x64 CPU | 17,321,312 | `e6de9dcd61a85c4100edafe286d3fd2639513210b81b9dd50d200c4199a06c01` |

两份文件均直接取自官方 PyPI 分发包，未修改、未重新训练。归档 URL、归档 SHA-256 和成员路径见 [provenance.json](provenance.json)。记录中的归档哈希已与 PyPI 元数据核对，归档内成员也已与此目录文件逐字节核对。名字库另在 `assets/game/` 与 `internal/hudtext/data/`，不属于神经网络权重。

模型在内存中加载。原生运行库首次使用时释放至当前用户缓存目录 `naruto-timer/ocr/` 下的版本目录，同版本复用；加载前校验内容哈希。模型缓存不保存用户画面。

Windows 10/11 x64 发布版额外内置 Microsoft Visual C++ v14 x64 运行库 14.51.36247.0 的 `vcruntime140.dll`、`vcruntime140_1.dll`、`msvcp140.dll`、`msvcp140_1.dll`，无需用户安装 VC 安装程序。四个文件取自官方 Visual Studio Community 2026 稳定版的 VC 开发者组件，并与独立 Redistributable 安装包中的对应文件逐字节一致，保留原始字节及微软数字签名；完整下载 URL、归档位置、哈希与许可见 [vcruntime/provenance.json](vcruntime/provenance.json) 和 [vcruntime/README.md](vcruntime/README.md)。递归依赖检查的其余项为 Windows 10/11 自带的系统 DLL 与 UCRT。

许可文件：

- `OCR-MODEL-LICENSE.md`：RapidOCR 的 Apache-2.0 许可。模型基于 [PaddleOCR](https://github.com/PaddlePaddle/PaddleOCR)，保留上游许可与归属。
- `ONNX-RUNTIME-LICENSE.md`：Microsoft ONNX Runtime 的 MIT 许可。
- `ONNX-RUNTIME-NOTICES.md`：随相同 ONNX Runtime 包提供的第三方声明。
- Go 调用绑定 [onnxruntime_go v1.24.0](https://github.com/yalue/onnxruntime_go/tree/v1.24.0) 使用 MIT；许可随发布包的 Go 依赖许可一起分发。
- `vcruntime/VS-COMMUNITY-LICENSE.md`、`VS-COMMUNITY-REDIST.md`：完整的 Community 开发者许可及官方再分发名单；`VC-RUNTIME-LICENSE.md` 是单独的 Runtime 最终用户许可。四个微软 DLL 仍受这些微软条款约束，不采用项目 MIT 许可。

本目录的上游材料保留其原许可；项目 MIT 许可不会改写第三方许可。更新文件时应同步版本、哈希和许可声明，并重新验证实际发布 EXE 的子进程识别及超时回收。
