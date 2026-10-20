// src/match/ncc_dot.h — PreparedNCC::at 的 u8·u8 点积内核（match 模块私有）。
//
// 每个内核返回 sum(a[i]*b[i])（i < n）的 uint32 结果。PrepareNCC 把每段 run 限制在
// maxDotRun 以内（255*255*maxDotRun < 2^32），所以精确和不会溢出 uint32；各内核只是用
// 不同的整数加法顺序（全部按 2^32 取模，结合律/交换律成立），结果与 Scalar 逐位相同。
// 由 tests/perf_kernels_test.cpp 做随机等价测试（含 NEON 的 x86 仿真）。
//
// 尾部：n >= 16 时最后一块从 n-16 起重叠读取，用 kTailMask 把已经算过的前几个模板字节清零
// （只读 [0, n) 内的字节，绝不越界）；8 <= n < 16 同理用 8 字节块；n < 8 走标量。
#pragma once

#include <cstdint>

#if defined(__x86_64__) || defined(__i386__)
#include <immintrin.h>
#define NT_DOT_X86 1
#define NT_TARGET_AVX2 __attribute__((target("avx2")))
#endif

#if defined(__aarch64__) || defined(NT_DOT_NEON_EMULATED)
#if defined(__aarch64__)
#include <arm_neon.h>
#define NT_TARGET_DOTPROD __attribute__((target("dotprod")))
#else
#define NT_TARGET_DOTPROD
#endif
#define NT_DOT_NEON 1
#endif

namespace nt::match::dot {

// kTailMask + r（r = 1..15）的 16 字节：前 16-r 个为 0，其余 0xFF；
// kTailMask + 8 + r（r = 1..7）的 8 字节：前 8-r 个为 0，其余 0xFF。
alignas(16) inline constexpr uint8_t kTailMask[32] = {
    0,    0,    0,    0,    0,    0,    0,    0,    0,    0,    0,    0,    0,    0,    0,    0,
    0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF};

// Scalar is the reference: one uint32 accumulator, ascending order.
inline uint32_t Scalar(const uint8_t* a, const uint8_t* b, int n) {
    uint32_t s = 0;
    for (int i = 0; i < n; ++i) s += uint32_t(a[i]) * uint32_t(b[i]);
    return s;
}

#if defined(NT_DOT_X86)

// SSE2 (x86-64 baseline): zero-extend to 16 bits, pmaddwd pairs into 32-bit lanes.
// Products are <= 65025 and pair sums <= 130050, so the signed 16x16 multiply-add is exact.
inline __m128i maddU8(__m128i x, __m128i y, __m128i acc) {
    const __m128i z = _mm_setzero_si128();
    acc = _mm_add_epi32(acc, _mm_madd_epi16(_mm_unpacklo_epi8(x, z), _mm_unpacklo_epi8(y, z)));
    return _mm_add_epi32(acc, _mm_madd_epi16(_mm_unpackhi_epi8(x, z), _mm_unpackhi_epi8(y, z)));
}

inline uint32_t hsum128(__m128i v) {
    v = _mm_add_epi32(v, _mm_shuffle_epi32(v, 0x4e));
    v = _mm_add_epi32(v, _mm_shuffle_epi32(v, 0xb1));
    return static_cast<uint32_t>(_mm_cvtsi128_si32(v));
}

inline uint32_t SSE2(const uint8_t* a, const uint8_t* b, int n) {
    if (n < 8) return Scalar(a, b, n);
    __m128i acc0 = _mm_setzero_si128(), acc1 = _mm_setzero_si128();
    if (n < 16) {
        const __m128i z = _mm_setzero_si128();
        const __m128i x = _mm_loadl_epi64(reinterpret_cast<const __m128i*>(a));
        const __m128i y = _mm_loadl_epi64(reinterpret_cast<const __m128i*>(b));
        acc0 = _mm_madd_epi16(_mm_unpacklo_epi8(x, z), _mm_unpacklo_epi8(y, z));
        if (n > 8) {
            const __m128i x2 = _mm_loadl_epi64(reinterpret_cast<const __m128i*>(a + n - 8));
            const __m128i y2 = _mm_and_si128(_mm_loadl_epi64(reinterpret_cast<const __m128i*>(b + n - 8)),
                                             _mm_loadl_epi64(reinterpret_cast<const __m128i*>(kTailMask + n)));
            acc1 = _mm_madd_epi16(_mm_unpacklo_epi8(x2, z), _mm_unpacklo_epi8(y2, z));
        }
        return hsum128(_mm_add_epi32(acc0, acc1));
    }
    int i = 0;
    for (; i + 32 <= n; i += 32) {
        acc0 = maddU8(_mm_loadu_si128(reinterpret_cast<const __m128i*>(a + i)),
                      _mm_loadu_si128(reinterpret_cast<const __m128i*>(b + i)), acc0);
        acc1 = maddU8(_mm_loadu_si128(reinterpret_cast<const __m128i*>(a + i + 16)),
                      _mm_loadu_si128(reinterpret_cast<const __m128i*>(b + i + 16)), acc1);
    }
    if (i + 16 <= n) {
        acc0 = maddU8(_mm_loadu_si128(reinterpret_cast<const __m128i*>(a + i)),
                      _mm_loadu_si128(reinterpret_cast<const __m128i*>(b + i)), acc0);
        i += 16;
    }
    if (i < n) {
        const int r = n - i;  // 1..15: the last block starts at n-16 and skips 16-r counted lanes.
        const __m128i y = _mm_and_si128(_mm_loadu_si128(reinterpret_cast<const __m128i*>(b + n - 16)),
                                        _mm_loadu_si128(reinterpret_cast<const __m128i*>(kTailMask + r)));
        acc1 = maddU8(_mm_loadu_si128(reinterpret_cast<const __m128i*>(a + n - 16)), y, acc1);
    }
    return hsum128(_mm_add_epi32(acc0, acc1));
}

// AVX2: 16 bytes -> 16 x u16 (vpmovzxbw) -> vpmaddwd. Same exactness argument as SSE2.
NT_TARGET_AVX2 inline __m256i maddU8x16(const uint8_t* a, const uint8_t* b, __m256i acc) {
    const __m256i x = _mm256_cvtepu8_epi16(_mm_loadu_si128(reinterpret_cast<const __m128i*>(a)));
    const __m256i y = _mm256_cvtepu8_epi16(_mm_loadu_si128(reinterpret_cast<const __m128i*>(b)));
    return _mm256_add_epi32(acc, _mm256_madd_epi16(x, y));
}

NT_TARGET_AVX2 inline uint32_t AVX2(const uint8_t* a, const uint8_t* b, int n) {
    if (n < 16) return SSE2(a, b, n);
    __m256i acc0 = _mm256_setzero_si256(), acc1 = _mm256_setzero_si256();
    int i = 0;
    for (; i + 32 <= n; i += 32) {
        acc0 = maddU8x16(a + i, b + i, acc0);
        acc1 = maddU8x16(a + i + 16, b + i + 16, acc1);
    }
    if (i + 16 <= n) {
        acc0 = maddU8x16(a + i, b + i, acc0);
        i += 16;
    }
    if (i < n) {
        const int r = n - i;
        const __m128i yb = _mm_and_si128(_mm_loadu_si128(reinterpret_cast<const __m128i*>(b + n - 16)),
                                         _mm_loadu_si128(reinterpret_cast<const __m128i*>(kTailMask + r)));
        const __m256i x = _mm256_cvtepu8_epi16(_mm_loadu_si128(reinterpret_cast<const __m128i*>(a + n - 16)));
        acc1 = _mm256_add_epi32(acc1, _mm256_madd_epi16(x, _mm256_cvtepu8_epi16(yb)));
    }
    const __m256i acc = _mm256_add_epi32(acc0, acc1);
    return hsum128(_mm_add_epi32(_mm256_castsi256_si128(acc), _mm256_extracti128_si256(acc, 1)));
}

#endif  // NT_DOT_X86

#if defined(NT_DOT_NEON)

// NEON (AArch64 baseline): umull u8x8 -> u16x8 (<= 65025), uadalp pairs into u32x4.
inline uint32x4_t mlalU8(uint32x4_t acc, uint8x16_t x, uint8x16_t y) {
    acc = vpadalq_u16(acc, vmull_u8(vget_low_u8(x), vget_low_u8(y)));
    return vpadalq_u16(acc, vmull_high_u8(x, y));
}

inline uint32_t NEON(const uint8_t* a, const uint8_t* b, int n) {
    if (n < 8) return Scalar(a, b, n);
    uint32x4_t acc0 = vdupq_n_u32(0), acc1 = vdupq_n_u32(0);
    if (n < 16) {
        acc0 = vpadalq_u16(acc0, vmull_u8(vld1_u8(a), vld1_u8(b)));
        if (n > 8) {
            const uint8x8_t y = vand_u8(vld1_u8(b + n - 8), vld1_u8(kTailMask + n));
            acc1 = vpadalq_u16(acc1, vmull_u8(vld1_u8(a + n - 8), y));
        }
        return vaddvq_u32(vaddq_u32(acc0, acc1));
    }
    int i = 0;
    for (; i + 32 <= n; i += 32) {
        acc0 = mlalU8(acc0, vld1q_u8(a + i), vld1q_u8(b + i));
        acc1 = mlalU8(acc1, vld1q_u8(a + i + 16), vld1q_u8(b + i + 16));
    }
    if (i + 16 <= n) {
        acc0 = mlalU8(acc0, vld1q_u8(a + i), vld1q_u8(b + i));
        i += 16;
    }
    if (i < n) {
        const int r = n - i;
        const uint8x16_t y = vandq_u8(vld1q_u8(b + n - 16), vld1q_u8(kTailMask + r));
        acc1 = mlalU8(acc1, vld1q_u8(a + n - 16), y);
    }
    return vaddvq_u32(vaddq_u32(acc0, acc1));
}

// Armv8.2 DotProd (udot): 16 products per instruction, 4 per u32 lane. Runtime-gated.
NT_TARGET_DOTPROD inline uint32_t DotProd(const uint8_t* a, const uint8_t* b, int n) {
    if (n < 8) return Scalar(a, b, n);
    if (n < 16) {
        uint32x2_t d = vdot_u32(vdup_n_u32(0), vld1_u8(a), vld1_u8(b));
        if (n > 8) {
            const uint8x8_t y = vand_u8(vld1_u8(b + n - 8), vld1_u8(kTailMask + n));
            d = vdot_u32(d, vld1_u8(a + n - 8), y);
        }
        return vaddv_u32(d);
    }
    uint32x4_t acc0 = vdupq_n_u32(0), acc1 = vdupq_n_u32(0);
    int i = 0;
    for (; i + 32 <= n; i += 32) {
        acc0 = vdotq_u32(acc0, vld1q_u8(a + i), vld1q_u8(b + i));
        acc1 = vdotq_u32(acc1, vld1q_u8(a + i + 16), vld1q_u8(b + i + 16));
    }
    if (i + 16 <= n) {
        acc0 = vdotq_u32(acc0, vld1q_u8(a + i), vld1q_u8(b + i));
        i += 16;
    }
    if (i < n) {
        const int r = n - i;
        const uint8x16_t y = vandq_u8(vld1q_u8(b + n - 16), vld1q_u8(kTailMask + r));
        acc1 = vdotq_u32(acc1, vld1q_u8(a + n - 16), y);
    }
    return vaddvq_u32(vaddq_u32(acc0, acc1));
}

#endif  // NT_DOT_NEON

}  // namespace nt::match::dot
