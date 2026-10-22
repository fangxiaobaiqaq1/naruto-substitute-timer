#import <Foundation/Foundation.h>
#include "../PixelConversion.hpp"
#include <cassert>
#include <cstdio>
#include <cstdlib>
#include <cstring>
int main() {
    @autoreleasepool {
        CIContext *context = [CIContext contextWithOptions:@{kCIContextCacheIntermediates:@NO,
            kCIContextWorkingColorSpace:NSNull.null, kCIContextOutputColorSpace:NSNull.null}];
        CGColorSpaceRef color = CGColorSpaceCreateDeviceRGB();
        // Top row red/green, bottom row blue/white. Non-palindromic under EVERY EXIF transform.
        uint8_t bgra[] = {0,0,255,255, 0,255,0,255, 255,0,0,255, 255,255,255,255};
        const uint8_t rgba[][4] = {{255,0,0,255},{0,255,0,255},{0,0,255,255},{255,255,255,255}};
        const int expected[8][4] = {{0,1,2,3},{1,0,3,2},{3,2,1,0},{2,3,0,1},
                                    {0,2,1,3},{2,0,3,1},{3,1,2,0},{1,3,0,2}};
        CVPixelBufferRef buffer = nullptr;
        assert(CVPixelBufferCreateWithBytes(kCFAllocatorDefault, 2, 2, kCVPixelFormatType_32BGRA,
            bgra, 8, nullptr, nullptr, nullptr, &buffer) == kCVReturnSuccess);
        for (int orientation = 1; orientation <= 8; ++orientation) {
            CIImage *image = [[CIImage imageWithCVPixelBuffer:buffer] imageByApplyingOrientation:orientation];
            image = [image imageByApplyingTransform:CGAffineTransformMakeTranslation(-image.extent.origin.x, -image.extent.origin.y)];
            uint8_t output[16]{};
            nt::ios::RenderTopDown(context, image, output, 2, 2, color);
            for (int p = 0; p < 4; ++p) for (int c = 0; c < 4; ++c) {
                if (std::abs(int(output[p*4+c]) - int(rgba[expected[orientation-1][p]][c])) > 2) {
                    std::fprintf(stderr, "EXIF %d pixel %d channel %d: got %d expected %d\n",
                        orientation, p, c, output[p*4+c], rgba[expected[orientation-1][p]][c]);
                    return 1;
                }
            }
        }
        std::printf("CoreImage top-down RGBA: all 8 EXIF orientations PASS (bitmapNeedsFlip=%d)\n",
                    nt::ios::BitmapNeedsFlip(context, color));
        CVPixelBufferRelease(buffer); CGColorSpaceRelease(color);
    }
}
