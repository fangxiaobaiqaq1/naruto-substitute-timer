// Owner: cpp-rgb
// Port of: internal/engine/rgb/gold_glint.go
// Contract: include/nt/rgb_internal.h — see android/ARCHITECTURE.md.
#include <algorithm>

#include "nt/rgb_internal.h"

namespace nt::rgb {

// goldGlintBody examines fixed upper/lower interior lobes of THIS calibrated
// slot, not a search for the brightest neighbor. The narrow white center can
// then vote without lowering the normal core confidence threshold. No history
// or other side/slot supplies evidence. All distances scale with the core.
bool goldGlintBody(const RGBA* img, const BeadPosition& p, int w, int h) {
    auto goldAt = [&](int x, int y) -> bool {
        if (!Pt(x, y).In(img->Bounds())) {
            return false;
        }
        Color c = img->RGBAAt(x, y);
        int r = c.R, g = c.G, b = c.B;
        return r >= 190 && g >= 110 && r - b >= 80 && g - b >= 70;
    };
    // A full-height gold/white wash has no separated bottom. The health bar
    // above must not provide the second lobe for an otherwise hidden bead.
    if (!Pt(p.X, p.Y + 2 * h).In(img->Bounds()) || goldAt(p.X, p.Y + 2 * h)) {
        return false;
    }
    // A white glint can brighten a gap's G as much as the filled gold
    // body. Yellow chroma still separates that body from the pale flare.
    auto yellow = [](Color c) -> int { return std::min(int(c.R), int(c.G)) - int(c.B); };
    const int side = std::max(1, w / 3);
    const int offsets[3] = {-side, 0, side};
    bool separated = false;
    for (int sign : {-1, 1}) {
        int consecutive = 0, longest = 0, distinct = 0;
        // Integer resizing can put the outermost row on the dim rim and the
        // next inner row on the real gold body. Require contiguous body rows
        // within each fixed lobe, rather than a majority of a rim-heavy box.
        for (int dy = std::max(1, h / 3); dy <= h; dy++) {
            int y = p.Y + sign * dy;
            int gold = 0;
            for (int dx : offsets) {
                if (goldAt(p.X + dx, y)) {
                    gold++;
                }
            }
            if (gold >= 2) {
                consecutive++;
                longest = std::max(longest, consecutive);
            } else {
                consecutive = 0;
            }
            // Compare the body to BOTH gaps on the same row. A moving sparkle
            // may cover the gaps in one lobe, but not erase all shape evidence.
            if (!Pt(p.X - 2 * w, y).In(img->Bounds()) || !Pt(p.X + 2 * w, y).In(img->Bounds())) {
                return false;
            }
            Color core = img->RGBAAt(p.X, y);
            Color left = img->RGBAAt(p.X - 2 * w, y), right = img->RGBAAt(p.X + 2 * w, y);
            bool distinctColor = yellow(core) - std::max(yellow(left), yellow(right)) >= 32;
            if (gold >= 2 && (int(core.G) - std::max(int(left.G), int(right.G)) >= 32 || distinctColor)) {
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
