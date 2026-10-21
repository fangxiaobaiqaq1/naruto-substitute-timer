#pragma once
// Host stand-in for <android/hardware_buffer.h> (only what jni.cpp uses; NDK layout).
// tests/jni_host_test.cpp implements the functions over a fake buffer.
#include <stdint.h>
extern "C" {
typedef struct AHardwareBuffer AHardwareBuffer;
typedef struct ARect { int32_t left, top, right, bottom; } ARect;
typedef struct AHardwareBuffer_Desc {
    uint32_t width, height, layers, format;
    uint64_t usage;
    uint32_t stride, rfu0;
    uint64_t rfu1;
} AHardwareBuffer_Desc;
enum { AHARDWAREBUFFER_FORMAT_R8G8B8A8_UNORM = 1, AHARDWAREBUFFER_FORMAT_R8G8B8X8_UNORM = 2 };
enum { AHARDWAREBUFFER_USAGE_CPU_READ_OFTEN = 3UL };
void AHardwareBuffer_acquire(AHardwareBuffer* buffer);
void AHardwareBuffer_release(AHardwareBuffer* buffer);
void AHardwareBuffer_describe(const AHardwareBuffer* buffer, AHardwareBuffer_Desc* outDesc);
int AHardwareBuffer_lock(AHardwareBuffer* buffer, uint64_t usage, int32_t fence, const ARect* rect, void** outVirtualAddress);
int AHardwareBuffer_unlock(AHardwareBuffer* buffer, int32_t* fence);
}
