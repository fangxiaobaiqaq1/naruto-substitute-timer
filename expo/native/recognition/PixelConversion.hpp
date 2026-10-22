#pragma once
#import <CoreImage/CoreImage.h>
#import <CoreVideo/CoreVideo.h>
#include <cstdint>
#include <stdexcept>

namespace nt::ios {
// Core Image coordinates are bottom-left, but bitmap storage order must not be
// inferred from that fact. Probe the public CVPixelBuffer -> CI -> bitmap path
// once, using asymmetric, known TOP-DOWN BGRA rows. This avoids an untested
// unconditional flip and is also exercised by PixelOrientationTest on macOS.
inline bool BitmapNeedsFlip(CIContext *context, CGColorSpaceRef color) {
    uint8_t bgra[] = {0,0,255,255, 0,255,0,255, 255,0,0,255, 255,255,255,255};
    CVPixelBufferRef buffer = nullptr;
    if (CVPixelBufferCreateWithBytes(kCFAllocatorDefault, 2, 2, kCVPixelFormatType_32BGRA,
        bgra, 8, nullptr, nullptr, nullptr, &buffer) != kCVReturnSuccess)
        throw std::runtime_error("pixel orientation probe allocation failed");
    uint8_t out[16]{};
    CIImage *image = [CIImage imageWithCVPixelBuffer:buffer];
    [context render:image toBitmap:out rowBytes:8 bounds:CGRectMake(0, 0, 2, 2)
             format:kCIFormatRGBA8 colorSpace:color];
    CVPixelBufferRelease(buffer);
    if (out[0] > 240 && out[1] < 15 && out[2] < 15) return false; // top-left red
    if (out[0] < 15 && out[1] < 15 && out[2] > 240) return true;  // bottom-left blue
    throw std::runtime_error("unsupported Core Image bitmap row/channel order");
}
inline void RenderTopDown(CIContext *context, CIImage *image, uint8_t *pixels,
                          int width, int height, CGColorSpaceRef color) {
    static const bool flip = BitmapNeedsFlip(context, color);
    if (flip) image = [image imageByApplyingTransform:CGAffineTransformMake(1, 0, 0, -1, 0, height)];
    [context render:image toBitmap:pixels rowBytes:size_t(width) * 4
             bounds:CGRectMake(0, 0, width, height) format:kCIFormatRGBA8 colorSpace:color];
}
} // namespace nt::ios
