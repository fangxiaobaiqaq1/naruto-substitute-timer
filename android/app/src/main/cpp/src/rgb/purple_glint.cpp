// Owner: cpp-rgb
// Port of: internal/engine/rgb/purple_glint.go
// Contract: include/nt/rgb_internal.h — see android/ARCHITECTURE.md.
#include <algorithm>

#include "nt/rgb_internal.h"

namespace nt::rgb {

// The purple idle sparkle can clip the whole upper core to white. Require a
// filled, saturated lower body on this frame, separated from BOTH neighboring
// gaps and from the pixels below it. An empty rim, white cover, or retained name
// alone cannot supply the missing votes.
bool purpleGlintBody(const RGBA* img, const BeadPosition& p, int w, int h, int gap) {
    // Go: chroma(x, y) (chroma, value int, purple bool)
    struct sample {
        int chroma = 0, value = 0;
        bool purple = false;
    };
    auto chroma = [&](int x, int y) -> sample {
        if (!Pt(x, y).In(img->Bounds())) {
            return sample{};
        }
        Color c = img->RGBAAt(x, y);
        int lo = std::min(int(c.R), int(c.B));
        return sample{lo - int(c.G), lo + int(c.G),
                      c.R >= 180 && c.B >= 210 && int(c.R) - int(c.G) >= 40 && int(c.B) - int(c.G) >= 55};
    };
    // Two core heights can still land on the sprite's lower sparkle. Check
    // outside that halo; the interior must independently pass both slot gaps.
    Point far = Pt(p.X, p.Y + 3 * h);
    if (!far.In(img->Bounds())) {
        return false;
    }
    Color c = img->RGBAAt(far.X, far.Y);
    if (chroma(far.X, far.Y).purple || (c.R >= 235 && c.G >= 210 && c.B >= 235)) {
        return false;
    }
    bodyContrast contrast = newBodyContrast(std::max(2, h / 3));
    const int side = std::max(1, w / 3);
    const int offsets[3] = {-side, 0, side};
    for (int dy = std::max(2, h / 3); dy <= h + std::max(1, h / 2); dy++) {
        int y = p.Y + dy;
        int filled = 0;
        for (int dx : offsets) {
            if (chroma(p.X + dx, y).purple) {
                filled++;
            }
        }
        if (!Pt(p.X - gap, y).In(img->Bounds()) || !Pt(p.X + gap, y).In(img->Bounds())) {
            return false;
        }
        sample core = chroma(p.X, y);
        sample left = chroma(p.X - gap, y);
        sample right = chroma(p.X + gap, y);
        // A moving flare may desaturate one gap and brighten the other. Both
        // gaps must remain separated, but each can use its surviving channel.
        // The diamond narrows towards its tip: require its center and at least
        // one interior shoulder, not three pixels across an already narrow row.
        if (filled >= 2 && core.purple) {
            if (contrast.add(std::max(core.chroma - left.chroma, core.value - left.value),
                             std::max(core.chroma - right.chroma, core.value - right.value), 32)) {
                return true;
            }
        } else {
            contrast.reset();
        }
    }
    return false;
}

}  // namespace nt::rgb
