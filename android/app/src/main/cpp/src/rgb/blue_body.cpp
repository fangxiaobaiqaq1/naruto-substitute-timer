// Owner: cpp-rgb
// Port of: internal/engine/rgb/blue_body.go
// Contract: include/nt/rgb_internal.h — see android/ARCHITECTURE.md.
#include <algorithm>

#include "nt/rgb_internal.h"

namespace nt::rgb {

// A translucent start/skill effect can move an EMPTY dark core into the RGB
// range of a blue available bean. The core is still a dim hollow relative to
// its surroundings. Verify local blue/white body prominence, not just color.
// Fixed offsets only: never search for a brighter neighbor or reuse old pixels.
bool blueBodyVisible(const RGBA* img, const BeadPosition& p, int w, int h) {
    const int radius = std::max(1, w / 4);
    const int gap = std::max(3, 2 * w);
    auto value = [&](int x, int y, int& out) -> bool {
        int sum = 0;
        for (int dx = -radius; dx <= radius; dx++) {
            Point q = Pt(x + dx, y);
            if (!q.In(img->Bounds())) {
                out = 0;
                return false;
            }
            Color c = img->RGBAAt(q.X, q.Y);
            // Retain white-core brightness while distinguishing blue from a
            // white background. Plain G+B alone can favor the bright scenery.
            sum += int(c.G) + int(c.B) - int(c.R);
        }
        out = sum / (2 * radius + 1);
        return true;
    };
    int hits = 0;
    for (int y = p.Y - h / 2; y <= p.Y + h / 2; y++) {
        int core = 0, left = 0, right = 0;
        bool ok = value(p.X, y, core);
        bool lok = value(p.X - gap, y, left);
        bool rok = value(p.X + gap, y, right);
        if (!ok || !lok || !rok) {
            return false;
        }
        // One side may contain a neighboring bead's halo or a moving sparkle.
        // A filled core must still stand out from at least one gap on two
        // adjacent scanlines. A hollow/flat wash stands out from neither.
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
