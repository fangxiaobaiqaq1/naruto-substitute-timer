// Owner: cpp-match-detect
// Port of: internal/detect/content.go
// Contract: include/nt/detect.h — see android/ARCHITECTURE.md.
#include <algorithm>
#include <cmath>

#include "nt/detect.h"

namespace nt::detect {

// ResolveContentArea maps calibrated coordinates into an actual screenshot.
// Auto accepts a different aspect ratio only when the predicted surrounding bars
// are visibly black. A native wider/taller HUD is a different layout, not proof
// of letterboxing. The boolean is false when its geometry cannot be established.
std::pair<ContentArea, bool> ResolveContentArea(const RGBA* img, ContentMode mode, int referenceWidth,
                                                int referenceHeight, double aspectTolerance) {
    if (img == nullptr || img->Bounds().Empty() || referenceWidth <= 0 || referenceHeight <= 0) {
        return {ContentArea{}, false};
    }
    const Rect b = img->Bounds();
    const ContentArea full{b.Min.X, b.Min.Y, b.Dx(), b.Dy()};
    if (mode == ModeStretch) {
        return {full, true};
    }
    const double target = static_cast<double>(referenceWidth) / static_cast<double>(referenceHeight);
    const double ratio = static_cast<double>(b.Dx()) / static_cast<double>(b.Dy());
    if (mode == ModeAuto && std::fabs(ratio / target - 1) <= aspectTolerance) {
        return {full, true};
    }
    const double scale = std::min(static_cast<double>(b.Dx()) / static_cast<double>(referenceWidth),
                                  static_cast<double>(b.Dy()) / static_cast<double>(referenceHeight));
    const int w = static_cast<int>(std::round(static_cast<double>(referenceWidth) * scale));
    const int h = static_cast<int>(std::round(static_cast<double>(referenceHeight) * scale));
    const Rect content = MakeRect(b.Min.X + (b.Dx() - w) / 2, b.Min.Y + (b.Dy() - h) / 2,
                                  b.Min.X + (b.Dx() - w) / 2 + w, b.Min.Y + (b.Dy() - h) / 2 + h);
    const ContentArea area{content.Min.X, content.Min.Y, content.Dx(), content.Dy()};
    if (mode == ModeLetterbox) {
        return {area, true};
    }
    if (mode != ModeAuto) {
        return {ContentArea{}, false};
    }
    // Examine every bar separately so a black toolbar on one edge cannot pass
    // as centered letterboxing. Skip the last two boundary pixels, where a
    // resize filter may blend content into a real bar.
    // 注意：Go 这里用复合字面量 image.Rectangle{Min, Max}，不做 min/max 规范化。
    const Rect bars[4] = {
        Rect(b.Min, Pt(b.Max.X, content.Min.Y - 2)),
        Rect(Pt(b.Min.X, content.Max.Y + 2), b.Max),
        Rect(Pt(b.Min.X, content.Min.Y), Pt(content.Min.X - 2, content.Max.Y)),
        Rect(Pt(content.Max.X + 2, content.Min.Y), Pt(b.Max.X, content.Max.Y)),
    };
    int checked = 0;
    for (const Rect& bar : bars) {
        if (bar.Empty()) {
            continue;
        }
        checked++;
        if (!blackBar(img, bar)) {
            return {ContentArea{}, false};
        }
    }
    if (checked < 2) {
        return {ContentArea{}, false};
    }
    return {area, true};
}

bool blackBar(const RGBA* img, const Rect& r) {
    // A bounded grid makes this cheap even for a 4K capture. A real bar should
    // be almost entirely neutral black, unlike a dark blue HUD or desktop frame.
    const int sx = std::max(1, r.Dx() / 64), sy = std::max(1, r.Dy() / 12);
    int total = 0, black = 0;
    for (int y = r.Min.Y; y < r.Max.Y; y += sy) {
        for (int x = r.Min.X; x < r.Max.X; x += sx) {
            const Color c = img->RGBAAt(x, y);
            total++;
            if (std::max({c.R, c.G, c.B}) <= 12) {
                black++;
            }
        }
    }
    return total > 0 && black * 100 >= total * 98;
}

}  // namespace nt::detect
