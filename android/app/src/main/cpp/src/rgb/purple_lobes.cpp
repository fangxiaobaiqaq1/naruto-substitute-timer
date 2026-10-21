// Owner: cpp-rgb
// Port of: internal/engine/rgb/purple_lobes.go
// Contract: include/nt/rgb_internal.h — see android/ARCHITECTURE.md.
#include <algorithm>
#include <array>

#include "nt/rgb_internal.h"

namespace nt::rgb {

// A white idle sweep crosses the middle of the diamond. A damage number may
// simultaneously obscure the distant background probe used by purpleGlintBody.
// In that case require BOTH saturated upper and lower interiors, each separated
// from BOTH slot gaps on adjacent scanlines. The extra independent upper proof
// permits weaker (20/255) local contrast, not a lone lower patch under a cover.
bool purplePairedBody(const RGBA* img, const BeadPosition& p, int w, int h, int gap) {
    Point far = Pt(p.X, p.Y + 4 * h);
    if (!far.In(img->Bounds())) {
        return false;
    }
    Color c = img->RGBAAt(far.X, far.Y);
    if (specialPixel(ninja::Purple, int(c.R), int(c.G), int(c.B)) == detect::StateLight) {
        return false;
    }
    // Go 的 signal 不做边界检查：越界时 RGBAAt 返回全 0（与 nt::RGBA::RGBAAt 相同）。
    auto signal = [&](int x, int y, std::array<int, 3>& out) -> bool {
        Color c = img->RGBAAt(x, y);
        int lo = std::min(int(c.R), int(c.B));
        out = {lo - int(c.G), lo + int(c.G), int(c.B) - int(c.G)};
        return c.R >= 180 && c.B >= 210 && int(c.R) - int(c.G) >= 40 && int(c.B) - int(c.G) >= 55;
    };
    const int side = std::max(1, w / 3);
    const int offsets[3] = {-side, 0, side};
    auto lobe = [&](int lo, int hi) -> bool {
        bodyContrast contrast = newBodyContrast(std::max(2, h / 3));
        for (int dy = lo; dy <= hi; dy++) {
            int y = p.Y + dy;
            if (!Pt(p.X - gap, y).In(img->Bounds()) || !Pt(p.X + gap, y).In(img->Bounds())) {
                return false;
            }
            int filled = 0;
            std::array<int, 3> tmp{};
            for (int dx : offsets) {
                if (signal(p.X + dx, y, tmp)) {
                    filled++;
                }
            }
            std::array<int, 3> core{};
            bool ok = signal(p.X, y, core);
            if (!ok || filled < 2) {
                contrast.reset();
                continue;
            }
            std::array<int, 3> left{}, right{};
            signal(p.X - gap, y, left);
            signal(p.X + gap, y, right);
            int lc = core[0] - left[0], rc = core[0] - right[0];
            for (size_t k = 1; k < core.size(); k++) {
                lc = std::max(lc, core[k] - left[k]);
                rc = std::max(rc, core[k] - right[k]);
            }
            if (contrast.add(lc, rc, 20)) {
                return true;
            }
        }
        return false;
    };
    const int extent = h + std::max(1, h / 2);
    return lobe(-extent, -std::max(2, h / 2)) && lobe(std::max(2, h / 2 + 1), extent);
}

}  // namespace nt::rgb
