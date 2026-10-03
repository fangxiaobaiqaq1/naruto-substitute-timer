// Owner: cpp-match-detect
// Port of: internal/detect/chrome.go
// Contract: include/nt/detect.h — see android/ARCHITECTURE.md.
#include "nt/detect.h"

namespace nt::detect {

// GuessTopChrome 估计模拟器标题栏/标签栏占用的顶部像素。
// 客户区常把 MuMu 顶栏算进去，游戏画面整体下移，模板和豆位都会偏。
int GuessTopChrome(const RGBA* img) {
    if (img == nullptr) {
        return 0;
    }
    const Rect b = img->Bounds();
    const int w = b.Dx(), h = b.Dy();
    if (w < 400 || h < 250) {
        return 0;
    }
    int limit = h / 5;
    if (limit > 72) {
        limit = 72;
    }
    int chrome = 0;
    for (int y = 0; y < limit; ++y) {
        int colorful = 0, n = 0;
        for (int x = 0; x < w; x += 4) {
            const Color c = img->RGBAAt(b.Min.X + x, b.Min.Y + y);
            n++;
            if (isColorful(c.R, c.G, c.B)) {
                colorful++;
            }
        }
        if (n == 0) {
            break;
        }
        if (static_cast<double>(colorful) / static_cast<double>(n) > 0.12) {
            break;
        }
        chrome = y + 1;
    }
    if (chrome < 18 || chrome > 64) {
        return 0;
    }
    return chrome;
}

// StripChrome 裁掉顶部模拟器栏，没有则原样返回。
RGBA StripChrome(const RGBA* img) {
    const int top = GuessTopChrome(img);
    if (top == 0 || img == nullptr) {
        return img != nullptr ? *img : RGBA{};
    }
    const Rect b = img->Bounds();
    RGBA out = RGBA::New(MakeRect(0, 0, b.Dx(), b.Dy() - top));
    for (int y = 0; y < out.Bounds().Dy(); ++y) {
        for (int x = 0; x < out.Bounds().Dx(); ++x) {
            out.SetRGBA(x, y, img->RGBAAt(b.Min.X + x, b.Min.Y + top + y));
        }
    }
    return out;
}

bool isColorful(uint8_t r, uint8_t g, uint8_t b) {
    uint8_t maxc = r, minc = r;
    if (g > maxc) maxc = g;
    if (b > maxc) maxc = b;
    if (g < minc) minc = g;
    if (b < minc) minc = b;
    return int(maxc) - int(minc) > 40 && maxc > 80;
}

}  // namespace nt::detect
