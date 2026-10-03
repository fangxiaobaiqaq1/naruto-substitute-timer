// Owner: cpp-rgb
// Port of: internal/engine/rgb/dark_glint.go
// Contract: include/nt/rgb_internal.h — see android/ARCHITECTURE.md.
#include <algorithm>

#include "nt/rgb_internal.h"

namespace nt::rgb {

// A horizontal idle sweep can turn an empty blue/purple/red core pale without
// filling the slot. Require dark interior on BOTH sides of that stripe in the
// current frame, bounded by a brighter rim on both sides. Never reuse a count
// or take a neighbor's rim as the missing core. Whole covers and flat bands fail.
bool darkGlintBody(const RGBA* img, const BeadPosition& p, int w, int h, ninja::Palette palette) {
    if (img == nullptr || w < 2 || h < 3) {
        return false;
    }
    auto darkAt = [&](int x, int y) -> bool {
        if (!Pt(x, y).In(img->Bounds())) {
            return false;
        }
        Color c = img->RGBAAt(x, y);
        int r = c.R, g = c.G, b = c.B;
        return (detect::DarkRange.Contains(r, g, b) && b - r >= 15 && b - g >= 8) ||
               specialPixel(palette, r, g, b) == detect::StateDark;
    };
    auto max3 = [](Color c) -> int { return int(std::max({c.R, c.G, c.B})); };
    const int rim = w + std::max(1, w / 4);
    const int side = std::max(1, w / 3);
    const int offsets[3] = {-side, 0, side};
    for (int sign : {-1, 1}) {
        bodyContrast contrast = newBodyContrast(std::max(2, h / 3));
        bool proved = false;
        for (int dy = std::max(2, h / 3); dy <= h; dy++) {
            int y = p.Y + sign * dy;
            if (!Pt(p.X - rim, y).In(img->Bounds()) || !Pt(p.X + rim, y).In(img->Bounds())) {
                return false;
            }
            int filled = 0;
            for (int dx : offsets) {
                if (darkAt(p.X + dx, y)) {
                    filled++;
                }
            }
            Color core = img->RGBAAt(p.X, y), left = img->RGBAAt(p.X - rim, y), right = img->RGBAAt(p.X + rim, y);
            int value = max3(core);
            if (filled >= 2 && darkAt(p.X, y)) {
                if (contrast.add(max3(left) - value, max3(right) - value, 24)) {
                    proved = true;
                    break;
                }
            } else {
                contrast.reset();
            }
        }
        if (!proved) {
            return false;
        }
    }
    return true;
}

}  // namespace nt::rgb
