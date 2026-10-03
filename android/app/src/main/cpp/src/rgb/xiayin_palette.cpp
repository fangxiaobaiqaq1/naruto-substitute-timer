// Owner: cpp-rgb
// Port of: internal/engine/rgb/xiayin_palette.go
// Contract: include/nt/rgb_internal.h — see android/ARCHITECTURE.md.
#include <algorithm>

#include "nt/rgb_internal.h"

namespace nt::rgb {

// The exact Xiayin variant can render purple OR red, including a purple/red
// mirror match. Do not infer skin from screen side, player identity or the other
// row. Resolve only this frame's calibrated cores; retain no color/count history.
// Red needs two independently readable cores and no readable purple core, so a
// single red sparkle on an otherwise purple row cannot change its classifier.
ninja::Palette xiayinPalette(const RGBA* img, const std::vector<BeadPosition>& positions, const std::string& side,
                             int w, int h) {
    int redSlots = 0, purpleSlots = 0;
    for (const BeadPosition& p : positions) {
        if (p.Side != side) {
            continue;
        }
        int red = 0, purple = 0, total = 0;
        for (int dy = -h / 2; dy <= h / 2; dy++) {
            for (int dx = -w / 2; dx <= w / 2; dx++) {
                if (!detect::InBeadDiamond(0, 0, w, h, dx, dy)) {
                    continue;
                }
                total++;
                Point q = Pt(p.X + dx, p.Y + dy);
                if (!q.In(img->Bounds())) {
                    continue;
                }
                Color c = img->RGBAAt(q.X, q.Y);
                if (specialPixel(ninja::Red, int(c.R), int(c.G), int(c.B)) != detect::StateUnknown) {
                    red++;
                }
                if (specialPixel(ninja::Purple, int(c.R), int(c.G), int(c.B)) != detect::StateUnknown) {
                    purple++;
                }
            }
        }
        if (total > 0 && red * 5 >= total * 3) {
            redSlots++;
        }
        if (total > 0 && purple * 5 >= total * 3) {
            purpleSlots++;
        }
    }
    if (redSlots >= 2 && purpleSlots == 0) {
        return ninja::Red;
    }
    return ninja::Purple;
}

// Only the bounded hint of this dual-skin variant permits current RED votes
// during a name gap. Unlike a verified identity it cannot turn white highlights
// into red beans. A lit core also needs a bounded body against both slot gaps.
BeadState sampleUnverifiedXiayinRed(const RGBA* img, const BeadPosition& p, int w, int h, int guard, double& conf) {
    int light = 0, dark = 0, total = 0;
    for (int dy = -h / 2; dy <= h / 2; dy++) {
        for (int dx = -w / 2; dx <= w / 2; dx++) {
            if (!detect::InBeadDiamond(0, 0, w, h, dx, dy)) {
                continue;
            }
            total++;
            Point q = Pt(p.X + dx, p.Y + dy);
            if (!q.In(img->Bounds())) {
                continue;
            }
            Color c = img->RGBAAt(q.X, q.Y);
            switch (specialPixel(ninja::Red, int(c.R), int(c.G), int(c.B))) {
                case detect::StateLight:
                    light++;
                    break;
                case detect::StateDark:
                    dark++;
                    break;
                default:
                    break;
            }
        }
    }
    if (total == 0) {
        conf = 0;
        return detect::StateUnknown;
    }
    if (dark * 5 >= total * 3) {
        conf = double(dark) / double(total);
        return detect::StateDark;
    }
    if (light * 5 >= total * 3 && isolatedRedHalo(img, p, guard)) {
        conf = double(light) / double(total);
        return detect::StateLight;
    }
    conf = double(std::max(light, dark)) / double(total);
    return detect::StateUnknown;
}

}  // namespace nt::rgb
