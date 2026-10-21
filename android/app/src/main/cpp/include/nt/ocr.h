// nt/ocr.h — 本机 PP-OCRv4 文字行识别（Android 端；对应 Go internal/ocr 的 local_rows.go）。
//
// Windows 版把 internal/ocr/neuraldata/recognizer.onnx（RapidOCR 1.4.4 的 ch_PP-OCRv4_rec_infer.onnx）
// 交给 ONNX Runtime 1.27.0 运行。安卓端用同一个模型文件（APK assets/ocr/recognizer.onnx，逐字节相同），
// 但不带 ONNX Runtime（onnxruntime-android 1.27.0 的 arm64 + x86_64 共 62 MB 未压缩 .so）：
// src/ocr/onnx.cpp 只解析 ONNX protobuf，src/ocr/net.cpp 按图里的节点逐个执行这个模型用到的
// 25 种算子（Conv/MatMul/Softmax/…，浮点 NCHW）。结果与 ONNX Runtime 只差浮点累加顺序
// （主机上对两段视频全部行逐字比较，见 android/tools/ocrhost）。
//
// 行预处理、CTC 解码、低置信替换符逐行对应 Go recognizeRow / decodeCharacters：
//   行 → x/image/draw BiLinear 缩放到高 48（宽 ceil(w·48/h)，夹在 16..1600）→ BGR、x/127.5−1 →
//   网络 → 每步取最大类（同分取前者），去空白与连续重复，概率 < 0.45 的字记作 U+FFFD。
//
// 线程：Recognizer 加载后只读；ReadRegions / Run 可在多个线程同时调用（每次调用自带缓冲）。
#pragma once

#include <atomic>
#include <cstddef>
#include <cstdint>
#include <memory>
#include <string>
#include <vector>

#include "nt/image.h"

namespace nt::ocr {

// Go ocr.Word / ocr.Line（坐标是传入整图的像素坐标，浮点）。
struct Word {
    std::string Text;
    double X = 0, Y = 0, Width = 0, Height = 0;
};
struct Line {
    std::string Text;
    std::vector<Word> Words;
};

class Net;

// Recognizer 持有解析好的网络与字典。
class Recognizer {
public:
    ~Recognizer();
    // Load 解析 ONNX 模型字节（调用方保留 data 直到返回；之后不再引用）。失败返回 nullptr。
    static std::unique_ptr<Recognizer> Load(const uint8_t* data, size_t size, std::string* err);

    // Go recognizeRequest 去掉 PNG 往返：regions 为空 = 整图；最多 8 行；行必须在图内。
    // 只收有字的行（与 Go 相同）。threads > 1 时各行分给最多 threads 个线程（结果与串行相同）。
    bool ReadRegions(const RGBA& img, const std::vector<Rect>& regions, std::vector<Line>& out, std::string* err,
                     int threads = 1) const;
    // Go recognizeRow：一行。
    bool ReadRow(const RGBA& img, const Rect& region, Line& out, std::string* err) const;

    // 行图分开给（hudtext 不拼整张图）：image 的整幅是这一行的像素，rect 是它在 Go 拼版图里的位置
    // （只决定字坐标，像素与 Go 拼版图 rect 内逐字节相同时结果与 ReadRegions 相同）。
    struct RowImage {
        const RGBA* image = nullptr;
        Rect rect;
    };
    // cancel 置位时尽快返回 false（err = "cancelled"）。rowMillis（可空）：每行的识别耗时。
    bool ReadRows(const std::vector<RowImage>& rows, std::vector<Line>& out, std::string* err, int threads = 1,
                  const std::atomic<bool>* cancel = nullptr, std::vector<double>* rowMillis = nullptr) const;

    // 网络本身：input = 1×3×48×width（NCHW，float），out = steps×classes 概率。供主机比对工具用。
    bool Run(const float* input, int width, std::vector<float>& out, int& steps, int& classes, std::string* err,
             const std::atomic<bool>* cancel = nullptr) const;
    // Go recognizeRow 的输入张量（BiLinear 缩放 + BGR 归一化）；width 为行宽。
    static std::vector<float> RowInput(const RGBA& img, const Rect& region, int& width);

    const std::vector<std::string>& Characters() const { return characters_; }
    size_t WeightBytes() const;

private:
    Recognizer() = default;
    std::unique_ptr<Net> net_;
    std::vector<std::string> characters_;  // "" (CTC blank) + 模型 metadata "character" 各行 + " "
};

// Go decodeCharacters（导出给主机测试）。
Line DecodeCharacters(const float* values, int steps, const std::vector<std::string>& characters, const Rect& region);

// x/image/draw BiLinear.Scale(dst, dst.Bounds(), src, sr, draw.Src, nil)，dst/src 为 *image.RGBA。
void ScaleBiLinear(RGBA& dst, const RGBA& src, const Rect& sr);
// x/image/draw NearestNeighbor.Scale(dst, dr, src, src.Bounds(), draw.Src, nil)。
void ScaleNearest(RGBA& dst, const Rect& dr, const RGBA& src);

}  // namespace nt::ocr
