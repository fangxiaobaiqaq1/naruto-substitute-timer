// Owner: ocr（安卓本机文字识别，见 nt/ocr.h）
// 单精度矩阵乘与逐通道卷积：网络的全部乘加都在这里（只有本文件允许 arm64 乘加融合）。
//
// 打包 A（MR 行一组）与 B（NR 列一组），用 GCC/Clang 向量扩展写的 MR×NR 寄存器块累加：
// arm64 8×12（32 个 NEON 寄存器）；x86_64 有 AVX 时 6×16（8 宽 ymm），否则 4×8（SSE，ABI 只保证
// SSE4.2）。每个输出元素都只在一个累加器里按 k 从 0 到 K−1 依次相加，与分块、线程、向量宽度
// 无关：x86_64 的 SSE 与 AVX 路径逐位相同（都不融合乘加）。arm64 允许乘加融合（FMLA，与
// ONNX Runtime 的 NEON 内核一样），所以 arm64 与 x86_64 只在末位舍入上可能不同。
#include <algorithm>
#include <cstring>
#include <vector>

#include "nt/ocr_net.h"

#if defined(__clang__) && defined(__aarch64__)
#pragma clang fp contract(fast)
#endif

#if defined(__x86_64__)
#define NT_GEMM_AVX 1
#define NT_TARGET_AVX __attribute__((target("avx")))
#endif

namespace nt::ocr {

namespace {

#define NT_INLINE inline __attribute__((always_inline))

typedef float v4 __attribute__((vector_size(16)));
typedef float v4u __attribute__((vector_size(16), aligned(4)));

// Vectors pass by reference only: 32-byte vectors by value outside a target("avx") function
// would change the calling convention.
struct Vec4 {
    using V = v4;
    static constexpr int W = 4;
    static NT_INLINE void load(V& v, const float* p) { v = *reinterpret_cast<const v4u*>(p); }
    static NT_INLINE void store(float* p, const V& v) { *reinterpret_cast<v4u*>(p) = v; }
    static NT_INLINE void splat(V& v, float x) { v = V{x, x, x, x}; }
};

#if defined(NT_GEMM_AVX)
typedef float v8 __attribute__((vector_size(32)));
typedef float v8u __attribute__((vector_size(32), aligned(4)));
struct Vec8 {
    using V = v8;
    static constexpr int W = 8;
    static NT_INLINE void load(V& v, const float* p) { v = *reinterpret_cast<const v8u*>(p); }
    static NT_INLINE void store(float* p, const V& v) { *reinterpret_cast<v8u*>(p) = v; }
    static NT_INLINE void splat(V& v, float x) { v = V{x, x, x, x, x, x, x, x}; }
};
#endif

// acc[MR][NR] = Σ_k a[k][r]·b[k][c]; a is K×MR, b is K×NR (packed).
template <class S, int MR, int NV>
NT_INLINE void micro(int K, const float* a, const float* b, float* out) {
    using V = typename S::V;
    constexpr int NR = NV * S::W;
    V acc[MR][NV] = {};
    for (int k = 0; k < K; ++k) {
        V bv[NV];
        for (int j = 0; j < NV; ++j) S::load(bv[j], b + k * NR + S::W * j);
        const float* ak = a + k * MR;
        for (int r = 0; r < MR; ++r) {
            V av;
            S::splat(av, ak[r]);
            for (int j = 0; j < NV; ++j) acc[r][j] += av * bv[j];
        }
    }
    for (int r = 0; r < MR; ++r)
        for (int j = 0; j < NV; ++j) S::store(out + r * NR + S::W * j, acc[r][j]);
}

template <class S, int MR, int NV>
NT_INLINE void gemm(int M, int N, int K, const float* A, int lda, const float* B, int ldb, float* C, int ldc,
                    const float* rowBias) {
    constexpr int NR = NV * S::W;
    constexpr int NB = NR * 16;  // columns of B packed at a time
    const int mPanels = (M + MR - 1) / MR;
    std::vector<float> apack(static_cast<size_t>(mPanels) * MR * K);
    for (int p = 0; p < mPanels; ++p) {
        float* dst = apack.data() + static_cast<size_t>(p) * K * MR;
        for (int r = 0; r < MR; ++r) {
            const int row = p * MR + r;
            if (row < M) {
                const float* src = A + static_cast<size_t>(row) * lda;
                for (int k = 0; k < K; ++k) dst[k * MR + r] = src[k];
            } else {
                for (int k = 0; k < K; ++k) dst[k * MR + r] = 0.0f;
            }
        }
    }
    std::vector<float> bpack(static_cast<size_t>(K) * NB);
    alignas(32) float acc[MR * NR];
    for (int j0 = 0; j0 < N; j0 += NB) {
        const int nb = std::min(NB, N - j0);
        const int nPanels = (nb + NR - 1) / NR;
        for (int q = 0; q < nPanels; ++q) {
            float* dst = bpack.data() + static_cast<size_t>(q) * K * NR;
            const int c0 = j0 + q * NR;
            const int cols = std::min(NR, N - c0);
            for (int k = 0; k < K; ++k) {
                const float* src = B + static_cast<size_t>(k) * ldb + c0;
                float* d = dst + k * NR;
                if (cols == NR) {
                    std::memcpy(d, src, sizeof(float) * NR);
                } else {
                    for (int c = 0; c < NR; ++c) d[c] = c < cols ? src[c] : 0.0f;
                }
            }
        }
        for (int p = 0; p < mPanels; ++p) {
            const float* ap = apack.data() + static_cast<size_t>(p) * K * MR;
            const int rows = std::min(MR, M - p * MR);
            for (int q = 0; q < nPanels; ++q) {
                micro<S, MR, NV>(K, ap, bpack.data() + static_cast<size_t>(q) * K * NR, acc);
                const int c0 = j0 + q * NR;
                const int cols = std::min(NR, N - c0);
                for (int r = 0; r < rows; ++r) {
                    const int row = p * MR + r;
                    float* c = C + static_cast<size_t>(row) * ldc + c0;
                    const float* s = acc + r * NR;
                    if (rowBias != nullptr) {
                        const float bias = rowBias[row];
                        for (int x = 0; x < cols; ++x) c[x] = s[x] + bias;
                    } else {
                        for (int x = 0; x < cols; ++x) c[x] = s[x];
                    }
                }
            }
        }
    }
}

#if defined(NT_GEMM_AVX)
NT_TARGET_AVX void gemmAVX(int M, int N, int K, const float* A, int lda, const float* B, int ldb, float* C, int ldc,
                           const float* rowBias) {
    gemm<Vec8, 6, 2>(M, N, K, A, lda, B, ldb, C, ldc, rowBias);
}

bool haveAVX() {
    static const bool avx = __builtin_cpu_supports("avx");
    return avx;
}
#endif

}  // namespace

void Gemm(int M, int N, int K, const float* A, int lda, const float* B, int ldb, float* C, int ldc,
          const float* rowBias) {
    if (M <= 0 || N <= 0) return;
    if (K <= 0) {
        for (int m = 0; m < M; ++m)
            for (int n = 0; n < N; ++n) C[static_cast<size_t>(m) * ldc + n] = rowBias != nullptr ? rowBias[m] : 0.0f;
        return;
    }
#if defined(__aarch64__)
    gemm<Vec4, 8, 3>(M, N, K, A, lda, B, ldb, C, ldc, rowBias);
#else
#if defined(NT_GEMM_AVX)
    if (haveAVX()) {
        gemmAVX(M, N, K, A, lda, B, ldb, C, ldc, rowBias);
        return;
    }
#endif
    gemm<Vec4, 4, 2>(M, N, K, A, lda, B, ldb, C, ldc, rowBias);
#endif
}

// One output row: acc[ox] = Σ_(ky,kx) w·x (taps in row-major order), then + bias.
namespace {

NT_INLINE void depthwise(const float* x, const float* w, const float* bias, float* y, int64_t C, int64_t H, int64_t W,
                          int64_t kh, int64_t kw, int64_t sy, int64_t sx, int64_t pt, int64_t pl, int64_t OH, int64_t OW) {
    // Valid output columns of each kernel column: ix = ox·sx − pl + kx in [0, W).
    std::vector<int64_t> lo(static_cast<size_t>(kw)), hi(static_cast<size_t>(kw));
    for (int64_t kx = 0; kx < kw; ++kx) {
        const int64_t off = kx - pl;
        int64_t a = 0, b = OW;
        if (off < 0) a = (-off + sx - 1) / sx;
        if ((OW - 1) * sx + off >= W) b = (W - 1 - off) / sx + 1;
        lo[kx] = std::min(a, OW);
        hi[kx] = std::max(lo[kx], std::min(b, OW));
    }
    std::vector<float> rowBuf(static_cast<size_t>(OW));
    float* __restrict acc = rowBuf.data();
    for (int64_t c = 0; c < C; ++c) {
        const float* xc = x + c * H * W;
        const float* wc = w + c * kh * kw;
        for (int64_t oy = 0; oy < OH; ++oy) {
            for (int64_t ox = 0; ox < OW; ++ox) acc[ox] = 0.0f;
            for (int64_t ky = 0; ky < kh; ++ky) {
                const int64_t iy = oy * sy - pt + ky;
                if (iy < 0 || iy >= H) continue;
                const float* __restrict xr = xc + iy * W;
                for (int64_t kx = 0; kx < kw; ++kx) {
                    const float wv = wc[ky * kw + kx];
                    const int64_t off = kx - pl;
                    if (sx == 1) {
                        const float* __restrict xs = xr + off;
                        for (int64_t ox = lo[kx]; ox < hi[kx]; ++ox) acc[ox] += wv * xs[ox];
                    } else {
                        for (int64_t ox = lo[kx]; ox < hi[kx]; ++ox) acc[ox] += wv * xr[ox * sx + off];
                    }
                }
            }
            float* __restrict yr = y + (c * OH + oy) * OW;
            if (bias != nullptr) {
                const float bv = bias[c];
                for (int64_t ox = 0; ox < OW; ++ox) yr[ox] = acc[ox] + bv;
            } else {
                for (int64_t ox = 0; ox < OW; ++ox) yr[ox] = acc[ox];
            }
        }
    }
}

#if defined(NT_GEMM_AVX)
// The same loops compiled for 8-wide AVX: every output element is its own sum in the same
// order, so the result is identical to the SSE build.
NT_TARGET_AVX void depthwiseAVX(const float* x, const float* w, const float* bias, float* y, int64_t C, int64_t H, int64_t W,
                                int64_t kh, int64_t kw, int64_t sy, int64_t sx, int64_t pt, int64_t pl, int64_t OH, int64_t OW) {
    depthwise(x, w, bias, y, C, H, W, kh, kw, sy, sx, pt, pl, OH, OW);
}
#endif

}  // namespace

void DepthwiseConv(const float* x, const float* w, const float* bias, float* y, int64_t C, int64_t H, int64_t W,
                   int64_t kh, int64_t kw, int64_t sy, int64_t sx, int64_t pt, int64_t pl, int64_t OH, int64_t OW) {
#if defined(NT_GEMM_AVX)
    if (haveAVX()) {
        depthwiseAVX(x, w, bias, y, C, H, W, kh, kw, sy, sx, pt, pl, OH, OW);
        return;
    }
#endif
    depthwise(x, w, bias, y, C, H, W, kh, kw, sy, sx, pt, pl, OH, OW);
}

// Test hook (nt/ocr_net.h): the portable 4-wide kernel, whatever the CPU offers.
void GemmPortable(int M, int N, int K, const float* A, int lda, const float* B, int ldb, float* C, int ldc,
                  const float* rowBias) {
    if (M <= 0 || N <= 0 || K <= 0) {
        Gemm(M, N, K, A, lda, B, ldb, C, ldc, rowBias);
        return;
    }
    gemm<Vec4, 4, 2>(M, N, K, A, lda, B, ldb, C, ldc, rowBias);
}

}  // namespace nt::ocr
