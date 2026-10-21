// Owner: cpp-rgb
// Port of: internal/engine/rgb/body_contrast.go
// Contract: include/nt/rgb_internal.h — see android/ARCHITECTURE.md.
//
// bodyContrast / newBodyContrast 已内联在 nt/rgb_internal.h（逐行对应 Go）：
// bodyContrast pools a few adjacent, already color-verified interior rows.
// One anti-aliased edge pixel must not toggle a body proof on/off. BOTH sides
// still need independent contrast in this same spatial window, never time.
#include <algorithm>

#include "nt/rgb_internal.h"

namespace nt::rgb {

// Inter-slot probes come from calibrated spacing, not a rounded sample-core
// width. At 1600px, half of a 25px pitch is 13px, while 2*roundedCore is 12px.
int beadHalfPitch(const std::vector<BeadPosition>& positions, const BeadPosition& p, int fallback) {
    int pitch = 0;
    for (const BeadPosition& other : positions) {
        if (other.Side != p.Side || other.Idx == p.Idx) {
            continue;
        }
        int dx = other.X - p.X;
        if (dx < 0) {
            dx = -dx;
        }
        if (dx > 0 && (pitch == 0 || dx < pitch)) {
            pitch = dx;
        }
    }
    if (pitch == 0) {
        return fallback;
    }
    return std::max(2, (pitch + 1) / 2);
}

}  // namespace nt::rgb
