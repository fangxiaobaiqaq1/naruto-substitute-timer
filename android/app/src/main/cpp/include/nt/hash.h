// nt/hash.h — 进程内 64 位快速哈希（替代 Go hash/maphash），architect 所有。
//
// 只用于「同一进程内判断像素是否完全相同」（frame 指纹、Gated pixelHash），
// 从不持久化，所以不必与 Go 的 AES 哈希数值一致，只需等值语义一致。
// 实现：wyhash 风格的 64 位乘法混合，种子在进程启动时随机化。
#pragma once

#include <cstddef>
#include <cstdint>
#include <cstring>
#include <string>

namespace nt {

namespace hash_detail {
inline uint64_t mum(uint64_t a, uint64_t b) {
    __uint128_t r = static_cast<__uint128_t>(a) * b;
    return static_cast<uint64_t>(r) ^ static_cast<uint64_t>(r >> 64);
}
inline uint64_t r8(const uint8_t* p) { uint64_t v; std::memcpy(&v, p, 8); return v; }
inline uint64_t r4(const uint8_t* p) { uint32_t v; std::memcpy(&v, p, 4); return v; }
inline uint64_t r3(const uint8_t* p, size_t k) {
    return (static_cast<uint64_t>(p[0]) << 16) | (static_cast<uint64_t>(p[k >> 1]) << 8) | p[k - 1];
}
constexpr uint64_t s0 = 0xa0761d6478bd642full, s1 = 0xe7037ed1a0b428dbull, s2 = 0x8ebc6af09c88c6e3ull,
                   s3 = 0x589965cc75374cc3ull;
}  // namespace hash_detail

// 进程级随机种子（等价 maphash.MakeSeed()）。
uint64_t ProcessHashSeed();

// maphash.Bytes(seed, b) 的替身。
inline uint64_t HashBytes(uint64_t seed, const void* data, size_t len) {
    using namespace hash_detail;
    const uint8_t* p = static_cast<const uint8_t*>(data);
    seed ^= s0;
    uint64_t a, b;
    if (len <= 16) {
        if (len >= 4) {
            a = (r4(p) << 32) | r4(p + ((len >> 3) << 2));
            b = (r4(p + len - 4) << 32) | r4(p + len - 4 - ((len >> 3) << 2));
        } else if (len > 0) {
            a = r3(p, len);
            b = 0;
        } else {
            a = b = 0;
        }
    } else {
        size_t i = len;
        if (i > 48) {
            uint64_t see1 = seed, see2 = seed;
            do {
                seed = mum(r8(p) ^ s1, r8(p + 8) ^ seed);
                see1 = mum(r8(p + 16) ^ s2, r8(p + 24) ^ see1);
                see2 = mum(r8(p + 32) ^ s3, r8(p + 40) ^ see2);
                p += 48;
                i -= 48;
            } while (i > 48);
            seed ^= see1 ^ see2;
        }
        while (i > 16) {
            seed = mum(r8(p) ^ s1, r8(p + 8) ^ seed);
            i -= 16;
            p += 16;
        }
        a = r8(p + i - 16);
        b = r8(p + i - 8);
    }
    return mum(s1 ^ len, mum(a ^ s1, b ^ seed));
}

inline uint64_t HashString(uint64_t seed, const std::string& s) { return HashBytes(seed, s.data(), s.size()); }

// frame.mixHash：与 Go 完全相同的组合公式。
constexpr uint64_t MixHash(uint64_t acc, uint64_t value) {
    acc ^= value + 0x9e3779b97f4a7c15ull + (acc << 6) + (acc >> 2);
    return acc;
}

}  // namespace nt
