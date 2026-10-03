// Owner: cpp-identity
// Port of: internal/identity/glyph_mask.go
// Contract: include/nt/identity.h — see android/ARCHITECTURE.md.
#include <algorithm>

#include "nt/identity.h"

namespace nt::identity {

// HUD name patches include animated scenery outside the white text's dark
// outline. Match the glyph and its nearby outline, not that old scenery. This
// is derived from every configured account template, never from a ninja name.
//
// 返回默认构造的 Gray（Nil()）等价于 Go 的 nil。
Gray accountGlyphMask(const Gray& templ) {
    const Rect b = templ.Bounds();
    Gray mask = Gray::New(b);
    int seeds = 0;
    const int radius = std::max(1, b.Dy() / 11);
    for (int y = b.Min.Y; y < b.Max.Y; ++y) {
        for (int x = b.Min.X; x < b.Max.X; ++x) {
            if (templ.GrayAt(x, y) < 180) {
                continue;
            }
            bool outlined = false;
            const Point ps[4] = {Pt(x - radius, y), Pt(x + radius, y), Pt(x, y - radius), Pt(x, y + radius)};
            for (const Point& p : ps) {
                if (p.In(b) && templ.GrayAt(p.X, p.Y) < 90) {
                    outlined = true;
                    break;
                }
            }
            if (!outlined) {
                continue;
            }
            ++seeds;
            for (int dy = -radius; dy <= radius; ++dy) {
                for (int dx = -radius; dx <= radius; ++dx) {
                    if (Pt(x + dx, y + dy).In(b)) {
                        mask.SetGray(x + dx, y + dy, 255);
                    }
                }
            }
        }
    }
    if (seeds < 12) {
        return Gray{};
    }
    return mask;
}

}  // namespace nt::identity
