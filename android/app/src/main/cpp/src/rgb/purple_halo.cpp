// Owner: cpp-rgb
// Port of: internal/engine/rgb/purple_halo.go
// Contract: include/nt/rgb_internal.h — see android/ARCHITECTURE.md.
#include <algorithm>
#include <array>

#include "nt/rgb_internal.h"

namespace nt::rgb {

// Xiayin Sasuke's energy bar above and idle glow below can trip both wash
// guards. A confirmed version may keep its saturated bean vote only if the
// current body is filled, distinct from BOTH inter-slot gaps on consecutive
// scanlines, and the purple glow fades further below. This does not count white cores
// or search for a brighter position. Flat bands and hollow rims fail.
bool isolatedPurpleHalo(const RGBA* img, const BeadPosition& p, int w, int h, int guard, int gap) {
    Point far = Pt(p.X, p.Y + 2 * guard);
    if (!far.In(img->Bounds())) {
        return false;
    }
    Color c = img->RGBAAt(far.X, far.Y);
    // White damage numbers can pass BELOW an intact saturated bean. They do
    // not invalidate its current body; white cores still receive no votes here.
    if (specialPixel(ninja::Purple, int(c.R), int(c.G), int(c.B)) == detect::StateLight) {
        return false;
    }
    auto signal = [&](int x, int y, std::array<int, 3>& out) -> bool {
        if (!Pt(x, y).In(img->Bounds())) {
            out = {};
            return false;
        }
        Color c = img->RGBAAt(x, y);
        // Bright scenery can exceed the purple body's luminance, while an
        // energy-bar halo saturates R. Keep independent value/chroma channels.
        int lo = std::min(int(c.R), int(c.B));
        out = {lo + int(c.G), lo - int(c.G), int(c.B) - int(c.G)};
        return true;
    };
    bodyContrast contrast = newBodyContrast(std::max(2, h / 3));
    const int side = std::max(1, w / 3);
    const int offsets[3] = {-side, 0, side};
    for (int dy = -h; dy <= h; dy++) {
        int y = p.Y + dy;
        int filled = 0;
        for (int dx : offsets) {
            Point point = Pt(p.X + dx, y);
            if (!point.In(img->Bounds())) {
                return false;
            }
            Color c = img->RGBAAt(point.X, point.Y);
            if (specialPixel(ninja::Purple, int(c.R), int(c.G), int(c.B)) == detect::StateLight) {
                filled++;
            }
        }
        std::array<int, 3> core{}, left{}, right{};
        bool ok = signal(p.X, y, core);
        bool leftOK = signal(p.X - gap, y, left);
        bool rightOK = signal(p.X + gap, y, right);
        Color cc = img->RGBAAt(p.X, y);
        if (filled >= 2 && specialPixel(ninja::Purple, int(cc.R), int(cc.G), int(cc.B)) == detect::StateLight && ok &&
            leftOK && rightOK) {
            int lc = core[0] - left[0], rc = core[0] - right[0];
            for (size_t channel = 1; channel < core.size(); channel++) {
                lc = std::max(lc, core[channel] - left[channel]);
                rc = std::max(rc, core[channel] - right[channel]);
            }
            if (contrast.add(lc, rc, 32)) {
                return true;
            }
        } else {
            contrast.reset();
        }
    }
    return false;
}

}  // namespace nt::rgb
