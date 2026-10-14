// Owner: cpp-match-detect
// Port of: internal/detect/hud_anchor.go
// Contract: include/nt/detect.h — see android/ARCHITECTURE.md.
#include <algorithm>
#include <cmath>

#include "nt/detect.h"

namespace nt::detect {

// HUDRowOffset measures a wide-screen HUD's horizontal safe-area adjustment
// from the OUTER sloping edge of its health bar, never from bean brightness.
// Three adjacent scanlines must agree on a long bar and its diagonal end.
// A purple idle flare may tint the red HP bar; blue-dominant effects are not
// an anchor. This changes geometry only, never a bean's palette or state.
// Missing/covered bars leave the original calibration alone. The two sides are
// independent: training controls and device safe areas need not be symmetric.
int HUDRowOffset(const RGBA* img, const ContentArea& ca, Point first, bool right, const std::string& profile) {
    if (img == nullptr || ca.H <= 0 || static_cast<double>(ca.W) / ca.H < 1.9) {
        return 0;
    }
    const double scale = ca.HUDScale(HUDReferenceWidth, HUDReferenceHeight);
    const double y = profile == "camp" ? 70.0 : 74.0;
    const int cy = ca.Y + static_cast<int>(std::round(y * scale));
    const int step = std::max(1, static_cast<int>(std::round(scale)));
    const int limit = static_cast<int>(std::round(160 * scale));
    int x0 = ca.X, x1 = ca.X + ca.W / 3;
    if (right) {
        x0 = ca.X + ca.W * 2 / 3;
        x1 = ca.X + ca.W;
    }
    auto red = [&](int x, int y) {
        if (!Pt(x, y).In(img->Bounds())) return false;
        const Color c = img->RGBAAt(x, y);
        return c.R >= 150 && int(c.R) - int(c.G) >= 65 && c.G >= 30 && int(c.B) - int(c.R) <= 20;
    };
    std::array<int, 3> edges{};
    for (int row = 0; row < 3; ++row) {
        const int y = cy + (row - 1) * step;
        int bestLength = 0, edge = -1;
        for (int x = x0; x < x1;) {
            if (!red(x, y)) {
                ++x;
                continue;
            }
            const int start = x;
            while (x < x1 && red(x, y)) ++x;
            const int length = x - start;
            const int candidate = right ? x - 1 : start;
            if (length < 60 * scale || length > 330 * scale || std::abs(candidate - first.X) > limit) {
                continue;
            }
            if (length > bestLength) {
                bestLength = length;
                edge = candidate;
            }
        }
        if (edge < 0) return 0;
        edges[row] = edge;
    }
    const int direction = right ? 1 : -1;
    for (size_t i = 1; i < edges.size(); ++i) {
        const int slope = direction * (edges[i] - edges[i - 1]);
        if (slope < 0 || slope > 3 * step) return 0;
    }
    if (direction * (edges[2] - edges[0]) < step) {
        return 0;  // A flat red band/effect is not a HUD end cap.
    }
    const int delta = edges[1] - first.X;
    if (std::abs(delta) <= 8 * scale) {
        return 0;  // Preserve an already aligned, hand-calibrated core.
    }
    return delta;
}

}  // namespace nt::detect
