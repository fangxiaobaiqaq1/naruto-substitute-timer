// tests/neon_emu.h — 标量仿真 ncc_dot.h 用到的少数 AArch64 NEON 内建函数，让 NEON / DotProd
// 内核的分块、尾部掩码与累加逻辑能在 x86 主机上做随机等价测试（语义按 Arm ACLE 文档逐 lane 实现）。
// 只用于 tests/；真实 arm64 构建用 <arm_neon.h>。
#pragma once

#include <cstdint>
#include <cstring>

struct uint8x8_t { uint8_t v[8]; };
struct uint8x16_t { uint8_t v[16]; };
struct uint16x8_t { uint16_t v[8]; };
struct uint32x2_t { uint32_t v[2]; };
struct uint32x4_t { uint32_t v[4]; };

inline uint8x16_t vld1q_u8(const uint8_t* p) { uint8x16_t r; std::memcpy(r.v, p, 16); return r; }
inline uint8x8_t vld1_u8(const uint8_t* p) { uint8x8_t r; std::memcpy(r.v, p, 8); return r; }
inline uint8x8_t vget_low_u8(uint8x16_t a) { uint8x8_t r; std::memcpy(r.v, a.v, 8); return r; }
inline uint8x8_t vget_high_u8(uint8x16_t a) { uint8x8_t r; std::memcpy(r.v, a.v + 8, 8); return r; }
inline uint32x4_t vdupq_n_u32(uint32_t x) { return uint32x4_t{{x, x, x, x}}; }
inline uint32x2_t vdup_n_u32(uint32_t x) { return uint32x2_t{{x, x}}; }
// UMULL: widening multiply, lane i = a[i] * b[i] (fits u16).
inline uint16x8_t vmull_u8(uint8x8_t a, uint8x8_t b) {
    uint16x8_t r;
    for (int i = 0; i < 8; ++i) r.v[i] = static_cast<uint16_t>(a.v[i] * b.v[i]);
    return r;
}
inline uint16x8_t vmull_high_u8(uint8x16_t a, uint8x16_t b) { return vmull_u8(vget_high_u8(a), vget_high_u8(b)); }
// UADALP: acc[i] += a[2i] + a[2i+1] (mod 2^32).
inline uint32x4_t vpadalq_u16(uint32x4_t acc, uint16x8_t a) {
    for (int i = 0; i < 4; ++i) acc.v[i] += static_cast<uint32_t>(a.v[2 * i]) + a.v[2 * i + 1];
    return acc;
}
inline uint32x4_t vaddq_u32(uint32x4_t a, uint32x4_t b) {
    for (int i = 0; i < 4; ++i) a.v[i] += b.v[i];
    return a;
}
inline uint32_t vaddvq_u32(uint32x4_t a) { return a.v[0] + a.v[1] + a.v[2] + a.v[3]; }
inline uint32_t vaddv_u32(uint32x2_t a) { return a.v[0] + a.v[1]; }
inline uint8x16_t vandq_u8(uint8x16_t a, uint8x16_t b) {
    for (int i = 0; i < 16; ++i) a.v[i] &= b.v[i];
    return a;
}
inline uint8x8_t vand_u8(uint8x8_t a, uint8x8_t b) {
    for (int i = 0; i < 8; ++i) a.v[i] &= b.v[i];
    return a;
}
// UDOT (by element group): acc[i] += sum_{k<4} a[4i+k] * b[4i+k] (mod 2^32).
inline uint32x4_t vdotq_u32(uint32x4_t acc, uint8x16_t a, uint8x16_t b) {
    for (int i = 0; i < 4; ++i)
        for (int k = 0; k < 4; ++k) acc.v[i] += static_cast<uint32_t>(a.v[4 * i + k]) * b.v[4 * i + k];
    return acc;
}
inline uint32x2_t vdot_u32(uint32x2_t acc, uint8x8_t a, uint8x8_t b) {
    for (int i = 0; i < 2; ++i)
        for (int k = 0; k < 4; ++k) acc.v[i] += static_cast<uint32_t>(a.v[4 * i + k]) * b.v[4 * i + k];
    return acc;
}
