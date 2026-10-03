// Owner: cpp-rgb
// Port of: internal/engine/rgb/gold_body.go
// Contract: include/nt/rgb_internal.h — see android/ARCHITECTURE.md.
#include <algorithm>

#include "nt/rgb_internal.h"

namespace nt::rgb {

// The ordinary saturated-gold branch also needs a spatially distinct body.
// Otherwise a flat yellow skill overlay can turn an empty blue slot into a
// gold recovery. Sample fixed core/gap rows only, never follow a bright rim.
// White glints use the independently checked upper/lower gold lobes instead.
bool goldBodyVisible(const RGBA* img, const BeadPosition& p, int w, int h) {
    const int radius = std::max(1, w / 4), gap = std::max(3, 2 * w);
    auto value = [&](int x, int y, int& out) -> bool {
        int sum = 0;
        for (int dx = -radius; dx <= radius; dx++) {
            Point q = Pt(x + dx, y);
            if (!q.In(img->Bounds())) {
                out = 0;
                return false;
            }
            Color c = img->RGBAAt(q.X, q.Y);
            sum += int(c.R) + int(c.G) - int(c.B);
        }
        out = sum / (2 * radius + 1);
        return true;
    };
    int hits = 0;
    // A neighboring sparkle can cover the core's horizontal mid-band. The
    // upper/lower body remains inside this fixed slot; it is not a relocation.
    for (int y = p.Y - h; y <= p.Y + h; y++) {
        int core = 0, left = 0, right = 0;
        bool ok = value(p.X, y, core);
        bool lok = value(p.X - gap, y, left);
        bool rok = value(p.X + gap, y, right);
        if (!ok || !lok || !rok) {
            return false;
        }
        if (core - std::min(left, right) >= 8) {
            hits++;
            if (hits >= 2) {
                return true;
            }
        } else {
            hits = 0;
        }
    }
    return false;
}

}  // namespace nt::rgb
