#pragma once
// Host-only platform shells. HardwareBuffer must never be exercised by this test.
#include <cstdint>
#include <cstdlib>

struct AHardwareBuffer;
struct ARect;
struct AHardwareBuffer_Desc {
    uint32_t width, height, layers, format;
    uint64_t usage;
    uint32_t stride, rfu0;
    uint64_t rfu1;
};
constexpr uint32_t AHARDWAREBUFFER_FORMAT_R8G8B8A8_UNORM = 1;
constexpr uint32_t AHARDWAREBUFFER_FORMAT_R8G8B8X8_UNORM = 2;
constexpr uint64_t AHARDWAREBUFFER_USAGE_CPU_READ_OFTEN = 3;
inline void AHardwareBuffer_acquire(AHardwareBuffer*) { std::abort(); }
inline void AHardwareBuffer_release(AHardwareBuffer*) { std::abort(); }
inline void AHardwareBuffer_describe(const AHardwareBuffer*, AHardwareBuffer_Desc*) { std::abort(); }
inline int AHardwareBuffer_lock(AHardwareBuffer*, uint64_t, int32_t, const ARect*, void**) { std::abort(); }
inline int AHardwareBuffer_unlock(AHardwareBuffer*, int32_t*) { std::abort(); }
