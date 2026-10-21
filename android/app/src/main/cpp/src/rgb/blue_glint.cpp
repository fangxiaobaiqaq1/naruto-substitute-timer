// Owner: cpp-rgb
// Port of: internal/engine/rgb/blue_glint.go
// Contract: include/nt/rgb_internal.h — see android/ARCHITECTURE.md.
#include <algorithm>

#include "nt/rgb_internal.h"

namespace nt::rgb {

// A glowing portrait can add warm light to a blue bean, removing the core's
// B-R chroma for seconds. Its lower CYAN interior remains visible. Require
// filled current pixels on adjacent rows and contrast against BOTH slot gaps;
// a pale core alone, an empty rim or a flat overlay cannot provide this proof.
bool blueGlintBody(const RGBA* img, const BeadPosition& p, int w, int h, int gap) {
    auto cyanAt = [&](int x, int y) -> bool {
        if (!Pt(x, y).In(img->Bounds())) {
            return false;
        }
        Color c = img->RGBAAt(x, y);
        return c.G >= 150 && c.B >= 150 && int(c.B) - int(c.R) >= 20;
    };
    bodyContrast contrast = newBodyContrast(std::max(2, h / 3));
    const int side = std::max(1, w / 3);
    const int offsets[3] = {-side, 0, side};
    for (int dy = std::max(1, h / 3); dy <= h; dy++) {
        int y = p.Y + dy;
        if (!Pt(p.X - gap, y).In(img->Bounds()) || !Pt(p.X + gap, y).In(img->Bounds())) {
            return false;
        }
        int filled = 0;
        for (int dx : offsets) {
            if (cyanAt(p.X + dx, y)) {
                filled++;
            }
        }
        if (filled < 2 || !cyanAt(p.X, y)) {
            contrast.reset();
            continue;
        }
        Color core = img->RGBAAt(p.X, y), left = img->RGBAAt(p.X - gap, y), right = img->RGBAAt(p.X + gap, y);
        int v = int(std::min(core.G, core.B));
        if (contrast.add(v - int(std::min(left.G, left.B)), v - int(std::min(right.G, right.B)), 32)) {
            return true;
        }
    }
    return false;
}

}  // namespace nt::rgb
