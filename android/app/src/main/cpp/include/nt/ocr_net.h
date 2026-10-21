// nt/ocr_net.h — src/ocr 私有：ONNX 图解析与执行（实现 src/ocr/net.cpp、src/ocr/gemm.cpp）。
//
// 只支持 PP-OCRv4 识别模型（paddle2onnx 导出，opset 12）用到的东西：权重全在 Constant 节点里
// （也接受 initializer），张量只有 float 与整数（int32/int64 一律按 int64 存），batch 任意。
// 遇到不认识的算子 / 属性组合，Parse 或 Run 报错，绝不猜测。
#pragma once

#include <atomic>
#include <cstddef>
#include <cstdint>
#include <memory>
#include <string>
#include <vector>

namespace nt::ocr {

struct Tensor {
    std::vector<int64_t> shape;
    bool isFloat = true;
    std::vector<float> f;    // isFloat
    std::vector<int64_t> i;  // !isFloat
    int64_t Size() const {
        int64_t n = 1;
        for (int64_t d : shape) n *= d;
        return n;
    }
};
using TensorPtr = std::shared_ptr<Tensor>;

struct Attr {
    bool hasI = false, hasF = false, hasInts = false;
    int64_t i = 0;
    float f = 0;
    std::vector<int64_t> ints;
};

struct Node;

class Net {
public:
    // 解析 ModelProto。metadata 里的 "character" 放进 dictionary。
    bool Parse(const uint8_t* data, size_t size, std::string* err);
    // input: [1, 3, 48, width] float。out: 最后一个输出（[1, steps, classes]）的数据。
    bool Run(const float* input, int64_t width, std::vector<float>& out, std::vector<int64_t>& shape,
             std::string* err, const std::atomic<bool>* cancel = nullptr) const;

    std::string dictionary;
    size_t WeightBytes() const { return weightBytes_; }

    Net();
    ~Net();

private:
    std::vector<std::unique_ptr<Node>> nodes_;
    std::vector<TensorPtr> constants_;  // per value; null = computed
    std::vector<int> lastUse_;          // per value: last node index that reads it (-1 = none)
    std::vector<std::string> names_;
    int input_ = -1, output_ = -1;
    int opset_ = 0;
    size_t weightBytes_ = 0;
};

// C[M×N] = A[M×K]·B[K×N]（行主序，lda/ldb/ldc 为行步长，单位 float），再加 rowBias[m]（可空）。
// 每个输出元素按 k = 0..K-1 顺序在一个累加器里求和（与线程、分块无关，结果确定）。
void Gemm(int M, int N, int K, const float* A, int lda, const float* B, int ldb, float* C, int ldc,
          const float* rowBias);
// 逐通道卷积（group = C = M，权重 [C,1,kh,kw]）：每个输出 = Σ_(ky,kx) w·x（行主序）+ bias。
void DepthwiseConv(const float* x, const float* w, const float* bias, float* y, int64_t C, int64_t H, int64_t W,
                   int64_t kh, int64_t kw, int64_t sy, int64_t sx, int64_t pt, int64_t pl, int64_t OH, int64_t OW);
// 同上（Gemm），但总用 4 宽的可移植内核（测试：x86_64 的 SSE 与 AVX 路径必须逐位相同）。
void GemmPortable(int M, int N, int K, const float* A, int lda, const float* B, int ldb, float* C, int ldc,
                  const float* rowBias);

}  // namespace nt::ocr
