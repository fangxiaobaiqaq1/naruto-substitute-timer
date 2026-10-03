// Owner: cpp-match-detect
// Port of: internal/layout/transform.go
// Contract: include/nt/layout.h — see android/ARCHITECTURE.md.
#include <algorithm>
#include <cmath>

#include "nt/layout.h"

namespace nt::layout {

bool New(const Rect& client, const Rect& capture, Point reference, const Mode& mode, double aspectTolerance,
         Transform& out, std::string* err) {
    out = Transform{};
    auto fail = [&](std::string msg) {
        if (err != nullptr) *err = std::move(msg);
        return false;
    };
    if (client.Empty() || capture.Empty()) {
        return fail("client and capture rectangles must not be empty");
    }
    if (reference.X <= 0 || reference.Y <= 0) {
        return fail("reference dimensions must be positive");
    }
    if (!capture.In(client)) {
        return fail("capture rectangle must be inside client rectangle");
    }
    if (mode != Auto && mode != Stretch && mode != Letterbox) {
        return fail("unsupported mode \"" + mode + "\"");
    }
    Rect content = client;
    bool letterbox = mode == Letterbox;
    if (mode == Auto) {
        const double target = static_cast<double>(reference.X) / static_cast<double>(reference.Y);
        const double ratio = static_cast<double>(client.Dx()) / static_cast<double>(client.Dy());
        letterbox = std::fabs(ratio / target - 1) > aspectTolerance;
    }
    if (letterbox) {
        const double sx = static_cast<double>(client.Dx()) / static_cast<double>(reference.X);
        const double sy = static_cast<double>(client.Dy()) / static_cast<double>(reference.Y);
        const double scale = std::min(sx, sy);
        const int w = static_cast<int>(std::floor(static_cast<double>(reference.X) * scale));
        const int h = static_cast<int>(std::floor(static_cast<double>(reference.Y) * scale));
        const int x = client.Min.X + (client.Dx() - w) / 2;
        const int y = client.Min.Y + (client.Dy() - h) / 2;
        content = MakeRect(x, y, x + w, y + h);
    }
    out = Transform{client, content, capture, reference};
    return true;
}

Point Transform::ReferenceToClient(Point p) const {
    return Pt(Content.Min.X + static_cast<int>(std::round(static_cast<double>(p.X) * static_cast<double>(Content.Dx()) /
                                                          static_cast<double>(Reference.X))),
              Content.Min.Y + static_cast<int>(std::round(static_cast<double>(p.Y) * static_cast<double>(Content.Dy()) /
                                                          static_cast<double>(Reference.Y))));
}

Point Transform::ClientToReference(Point p) const {
    return Pt(static_cast<int>(std::round(static_cast<double>(p.X - Content.Min.X) * static_cast<double>(Reference.X) /
                                          static_cast<double>(Content.Dx()))),
              static_cast<int>(std::round(static_cast<double>(p.Y - Content.Min.Y) * static_cast<double>(Reference.Y) /
                                          static_cast<double>(Content.Dy()))));
}

Point Transform::ClientToCapture(Point p) const { return p.Sub(Capture.Min); }

Point Transform::CaptureToClient(Point p) const { return p.Add(Capture.Min); }

Point Transform::ReferenceToCapture(Point p) const { return ClientToCapture(ReferenceToClient(p)); }

Point Transform::NormalizedContentToCapture(double x, double y) const {
    const Point client(Content.Min.X + static_cast<int>(std::round(x * static_cast<double>(Content.Dx()))),
                       Content.Min.Y + static_cast<int>(std::round(y * static_cast<double>(Content.Dy()))));
    return ClientToCapture(client);
}

}  // namespace nt::layout
