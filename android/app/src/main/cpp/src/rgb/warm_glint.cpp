// Owner: cpp-rgb
// Port of: internal/engine/rgb/warm_glint.go
// Contract: include/nt/rgb_internal.h — see android/ARCHITECTURE.md.
#include <algorithm>

#include "nt/rgb_internal.h"

namespace nt::rgb {

// A six-slot orange/red bean retains colored body lobes when its narrow idle
// sweep clips the center to white. Gold-only lobe checks cannot see that body.
// Require current color above AND below this fixed core, plus separation from
// both neighboring gaps on at least one lobe. No past count or adjacent bean
// supplies a vote; a white cover, hollow rim, or flat warm wash fails.
bool warmGlintBody(const RGBA* img, const BeadPosition& p, int w, int h) {
    struct sample {
        bool warm = false;
        int chroma = 0, value = 0;
    };
    auto at = [&](int x, int y, sample& out) -> bool {
        if (!Pt(x, y).In(img->Bounds())) {
            out = sample{};
            return false;
        }
        Color c = img->RGBAAt(x, y);
        int r = c.R, g = c.G, b = c.B;
        out = sample{specialPixel(ninja::Warm, r, g, b) == detect::StateLight, r - b, r + g - b};
        return true;
    };
    // The moving lower sparkle can reach two core heights. A farther probe,
    // together with BOTH filled lobes and slot-gap contrast, bounds the body.
    Point far = Pt(p.X, p.Y + 3 * h);
    sample body;
    bool ok = at(far.X, far.Y, body);
    if (!ok || body.warm) {
        return false;
    }
    Color c = img->RGBAAt(far.X, far.Y);
    if (c.R >= 235 && c.G >= 210 && c.B >= 210) {
        return false;
    }
    const int side = std::max(1, w / 3);
    const int offsets[3] = {-side, 0, side};
    bool separated = false;
    for (int sign : {-1, 1}) {
        int consecutive = 0, longest = 0, distinct = 0;
        for (int dy = std::max(2, h / 3); dy <= h; dy++) {
            int y = p.Y + sign * dy;
            int filled = 0;
            for (int dx : offsets) {
                sample s;
                if (at(p.X + dx, y, s) && s.warm) {
                    filled++;
                }
            }
            sample core, left, right;
            bool cok = at(p.X, y, core);
            bool leftOK = at(p.X - 2 * w, y, left);
            bool rightOK = at(p.X + 2 * w, y, right);
            if (!cok || !leftOK || !rightOK) {
                return false;
            }
            if (filled >= 2 && core.warm) {
                consecutive++;
                longest = std::max(longest, consecutive);
            } else {
                consecutive = 0;
            }
            if (filled >= 2 && core.warm &&
                (core.chroma - std::max(left.chroma, right.chroma) >= 32 ||
                 core.value - std::max(left.value, right.value) >= 32)) {
                distinct++;
            }
        }
        if (longest < std::max(2, h / 3)) {
            return false;
        }
        separated = separated || distinct >= 2;
    }
    return separated;
}

}  // namespace nt::rgb
