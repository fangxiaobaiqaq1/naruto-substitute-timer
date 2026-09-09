# Third-party notices

The project's original source code uses the MIT License in LICENSE.

Fyne and the Go modules in go.mod retain their own licenses. Release archives include dependency license files under licenses/.

Recognition templates in assets/templates/ and internal/ninja/templates/ derive from game HUD captures. Catalogs in assets/game/ and internal/hudtext/data/ contain game-derived names and reference data. These materials and game trademarks remain with their respective owners; the project MIT License does not relicense them.

MuMu's capture SDK is loaded from the user's existing installation. This release does not redistribute MuMu binaries or a game APK. This is an independent community tool.

The embedded PP-OCRv4 recognition model comes from the [RapidOCR 1.4.4 wheel](https://pypi.org/project/rapidocr-onnxruntime/1.4.4/), based on [Baidu PaddleOCR](https://github.com/PaddlePaddle/PaddleOCR) (Apache-2.0). The embedded ONNX Runtime 1.27.0 CPU library comes from the [Microsoft ONNX Runtime wheel](https://pypi.org/project/onnxruntime/1.27.0/) (MIT, with its third-party notices). Both binary files are unmodified. Their exact archive URLs, archive member paths, sizes and SHA-256 hashes are recorded in [provenance.json](internal/ocr/neuraldata/provenance.json); license copies are under internal/ocr/neuraldata in source and licenses/local-ocr in release archives. Images are processed locally; these components do not upload screenshots.

The Go binding [github.com/yalue/onnxruntime_go v1.24.0](https://github.com/yalue/onnxruntime_go/tree/v1.24.0) uses MIT; release archives include its license with the other Go dependency licenses. Neither Python nor the RapidOCR Python package is required at runtime.

The Windows ONNX Runtime binary requires the Microsoft Visual C++ runtime, as specified in the [upstream installation requirements](https://onnxruntime.ai/docs/install/). This project does not redistribute the Visual C++ installer or relicense it under MIT; obtain the current x64 package from [Microsoft](https://learn.microsoft.com/en-us/cpp/windows/latest-supported-vc-redist).
