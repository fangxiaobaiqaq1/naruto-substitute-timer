// Owner: cpp-gated-frame
// Port of: internal/engine/configured_layout.go
// Contract: include/nt/engine.h — see android/ARCHITECTURE.md.
#include "nt/engine.h"

#include <algorithm>
#include <cmath>
#include <limits>

namespace nt::engine {

namespace {

// cloneProfile：Go 里复制切片以免与配置共享底层数组；C++ 按值复制本来就是深拷贝。
config::LayoutProfile cloneProfile(const config::LayoutProfile& p) { return p; }

// Same bounds as config validation (inside the frame, size limits of
// config::CalibrationRectIssue, as in Go and Kotlin); anything else stays automatic.
std::optional<config::NormalizedRect> validCalibrationRect(const std::optional<config::NormalizedRect>& r,
                                                           bool avatar) {
    if (!r || !(r->X >= 0 && r->Y >= 0 && r->Width > 0 && r->Height > 0 && r->X + r->Width <= 1 &&
                r->Y + r->Height <= 1)) {
        return std::nullopt;
    }
    if (config::CalibrationRectIssue(avatar, *r) != config::CalibrationIssue::None) return std::nullopt;
    return r;
}

int sideIndex(const std::string& side) { return side == "right" ? 1 : 0; }

// calibrate moves calibrated rows onto their transform and tags every
// position of a calibrated side with its geometry.
void calibrate(std::vector<detect::BeadPosition>& positions, const std::array<detect::SideCalibrationPtr, 2>& cals) {
    for (auto& p : positions) {
        const detect::SideCalibrationPtr& c = cals[sideIndex(p.Side)];
        if (c == nullptr) continue;
        p.Cal = c;
        if (c->Beads()) {
            const Point m = c->Map(p.LX, p.LY);
            p.X = m.X;
            p.Y = m.Y;
        }
    }
}

// settleMargin (px) keeps a default bead box clear of rounding boundaries,
// above the float32 error of Android's stored calibration.
constexpr double settleMargin = 1e-3;

// settle keeps v settleMargin inside [lo, hi), the range rounding every
// centre back. If the automatic centres round half-pixel ties inconsistently
// there is none: then bead 0 (n, the anchor of the name and portrait
// windows) stays exact and another centre may differ by 1px.
double settle(double v, double lo, double hi, double n) {
    if (hi - lo <= 2 * settleMargin) {
        lo = n - .5;
        hi = n + .5;
    }
    return std::min(std::max(v, lo + settleMargin), hi - settleMargin);
}

}  // namespace

// ConfiguredLayout is immutable after construction and shared by both engines.
ConfiguredLayout::ConfiguredLayout(detect::ContentMode mode, const config::LayoutConfig& cfg)
    : mode_(mode),
      preferred_(cfg.PreferredProfile),
      profiles_{{"camp", cloneProfile(cfg.Profile("camp"))}, {"duel", cloneProfile(cfg.Profile("duel"))}},
      count_(cfg.BeadsPerSide),
      reference_(Pt(cfg.ReferenceWidth, cfg.ReferenceHeight)),
      tolerance_(cfg.AutoAspectTolerance) {
    const config::LayoutConfig defaults = config::Default().Layout;
    auto sameCenters = [](const std::vector<config::NormalizedPoint>& a,
                          const std::vector<config::NormalizedPoint>& b) {
        if (a.size() != b.size()) return false;
        for (size_t i = 0; i < a.size(); ++i) {
            if (a[i].X != b[i].X || a[i].Y != b[i].Y) return false;
        }
        return true;
    };
    autoAnchor_ = cfg.Profiles.empty() && cfg.BeadsPerSide == defaults.BeadsPerSide &&
                  sameCenters(cfg.Left.NominalCenters, defaults.Left.NominalCenters) &&
                  sameCenters(cfg.Right.NominalCenters, defaults.Right.NominalCenters);
    if (const auto& c = cfg.Calibration; c && (c->Profile == "camp" || c->Profile == "duel")) {
        calProfile_ = c->Profile;
        calBeads_ = {validCalibrationRect(c->LeftBeads, false), validCalibrationRect(c->RightBeads, false)};
        calAvatars_ = {validCalibrationRect(c->LeftAvatar, true), validCalibrationRect(c->RightAvatar, true)};
    }
}

std::vector<detect::BeadPosition> ConfiguredLayout::Positions(int w, int h) const {
    std::string name = preferred_;
    if (name != "duel") {
        name = "camp";
    }
    return PositionsFor(name, w, h);
}

std::vector<detect::BeadPosition> ConfiguredLayout::PositionsFor(const std::string& name, int w, int h) const {
    const detect::ContentArea ca = detect::ComputeContentArea(w, h, mode_);
    auto positions = PositionsIn(name, ca);
    calibrate(positions, sideCalibrations(name, MakeRect(0, 0, w, h), ca));
    return positions;
}

// ContentArea is shared with scene matching. A size alone cannot prove bars.
std::pair<detect::ContentArea, bool> ConfiguredLayout::ContentArea(const RGBA* img) const {
    return detect::ResolveContentArea(img, mode_, reference_.X, reference_.Y, tolerance_);
}

// Explicit user calibrations remain authoritative. Size-only callers never
// invent safe-area margins from a resolution or from old frames. Sides with
// a player-drawn bead box use that box instead (docs/区域校准.md).
std::vector<detect::BeadPosition> ConfiguredLayout::PositionsInImage(const std::string& name, const RGBA* img,
                                                                  const detect::ContentArea& ca) const {
    auto positions = PositionsIn(name, ca);
    std::array<detect::SideCalibrationPtr, 2> cals;
    if (img != nullptr) cals = sideCalibrations(name, img->Bounds(), ca);
    if (autoAnchor_) {
        for (const auto& first : positions) {
            if (first.Idx != 0 || detect::CalBeads(cals[sideIndex(first.Side)])) continue;
            const int dx = detect::HUDRowOffset(img, ca, Pt(first.X, first.Y), first.Side == "right", name);
            for (auto& p : positions) {
                if (p.Side == first.Side) p.X += dx;
            }
        }
    }
    calibrate(positions, cals);
    return positions;
}

// sideCalibrations is each side's calibrated geometry for one frame and
// profile (null = automatic). Boxes are fractions of the whole frame. A bead
// box defines a similarity transform from the calibration profile's nominal
// centres; the same transform maps the other profile. A portrait box drawn on
// the other profile moves with that side's bead 0.
std::array<detect::SideCalibrationPtr, 2> ConfiguredLayout::sideCalibrations(const std::string& name, const Rect& frame,
                                                                            const detect::ContentArea& ca) const {
    std::array<detect::SideCalibrationPtr, 2> out;
    auto drawnIt = profiles_.find(calProfile_);
    auto currentIt = profiles_.find(name);
    if (calProfile_.empty() || drawnIt == profiles_.end() || currentIt == profiles_.end() || frame.Empty()) {
        return out;
    }
    const double w = frame.Dx(), h = frame.Dy();
    const config::SideLayout* sides[2][2] = {{&drawnIt->second.Left, &currentIt->second.Left},
                                             {&drawnIt->second.Right, &currentIt->second.Right}};
    for (int i = 0; i < 2; ++i) {
        const auto& ref = sides[i][0]->NominalCenters;
        const auto& now = sides[i][1]->NominalCenters;
        if (ref.size() < 4 || now.empty()) continue;
        auto c = std::make_shared<detect::SideCalibration>();
        c->RefX = ref[0].X * detect::HUDReferenceWidth;
        c->RefY = ref[0].Y * detect::HUDReferenceHeight;
        if (const auto& r = calBeads_[i]) {
            // Bead 0 is the outermost of four evenly spaced centres in the box.
            if (const double pitch = std::fabs(ref[3].X - ref[0].X) * detect::HUDReferenceWidth / 3; pitch > 0) {
                double x = r->X + .5 * r->Width / 4;
                if (i == 1) x = r->X + r->Width - .5 * r->Width / 4;
                c->X0 = static_cast<double>(frame.Min.X) + x * w;
                c->Y0 = static_cast<double>(frame.Min.Y) + (r->Y + r->Height / 2) * h;
                c->Scale = r->Width * w / 4 / pitch;
            }
        }
        if (const auto& r = calAvatars_[i]) {
            c->Avatar = detect::PixelBox{static_cast<double>(frame.Min.X) + r->X * w,
                                         static_cast<double>(frame.Min.Y) + r->Y * h, r->Width * w, r->Height * h};
            if (name != calProfile_) {
                double scale = c->Scale;
                if (scale == 0) scale = ca.HUDScale(detect::HUDReferenceWidth, detect::HUDReferenceHeight);
                c->Avatar.X += scale * (now[0].X * detect::HUDReferenceWidth - c->RefX);
                c->Avatar.Y += scale * (now[0].Y * detect::HUDReferenceHeight - c->RefY);
            }
        }
        if (c->Beads() || c->Portrait()) out[i] = std::move(c);
    }
    return out;
}

bool ConfiguredLayout::DefaultBeadBox(const std::string& name, const std::vector<detect::BeadPosition>& row,
                                      const Rect& frame, const detect::ContentArea& ca, config::NormalizedRect& out,
                                      const std::vector<detect::BeadPosition>& derived) const {
    out = config::NormalizedRect{};
    const auto it = profiles_.find(name);
    if (it == profiles_.end() || row.size() < 4 || frame.Empty()) return false;
    const detect::BeadPosition& first = row[0];
    const bool right = first.Side == "right";
    const auto& ref = right ? it->second.Right.NominalCenters : it->second.Left.NominalCenters;
    // Unrounded bead 0 by the row's own mapping.
    double s = 0, x = 0, y = 0;
    if (detect::CalBeads(first.Cal)) {
        const detect::SideCalibration& c = *first.Cal;
        s = c.Scale;
        x = c.X0 + c.Scale * (first.LX - c.RefX);
        y = c.Y0 + c.Scale * (first.LY - c.RefY);
    } else {
        s = ca.HUDScale(detect::HUDReferenceWidth, detect::HUDReferenceHeight);
        x = static_cast<double>(ca.X) + first.LX * s;
        if (right) x = static_cast<double>(ca.X + ca.W) - (detect::HUDReferenceWidth - first.LX) * s;
        y = static_cast<double>(ca.Y) + first.LY * s;
        // Plus the whole-pixel HP-bar correction.
        x += static_cast<double>(
            first.X -
            ca.MapHUDPixel(first.LX, first.LY, right, detect::HUDReferenceWidth, detect::HUDReferenceHeight).X);
    }
    if (ref.size() < 4 || !(s > 0) || ref[3].X == ref[0].X) return false;
    // Every centre must round back, also at half-pixel ties.
    constexpr double inf = std::numeric_limits<double>::infinity();
    auto span = [&](const std::vector<detect::BeadPosition>& centres, double& xlo, double& xhi, double& ylo,
                    double& yhi) {
        xlo = -inf, xhi = inf, ylo = -inf, yhi = inf;
        for (const detect::BeadPosition& b : centres) {
            const double dx = s * (b.LX - first.LX), dy = s * (b.LY - first.LY);
            xlo = std::max(xlo, static_cast<double>(b.X) - .5 - dx);
            xhi = std::min(xhi, static_cast<double>(b.X) + .5 - dx);
            ylo = std::max(ylo, static_cast<double>(b.Y) - .5 - dy);
            yhi = std::min(yhi, static_cast<double>(b.Y) + .5 - dy);
        }
    };
    double xlo = 0, xhi = 0, ylo = 0, yhi = 0;
    span(row, xlo, xhi, ylo, yhi);
    // A derived centre may sit on a half-pixel tie of its own (a 9@540 drop
    // of exactly 25.5px at scale 1.7): keep it clear as well, unless that
    // leaves no room, so the row itself is never less exact.
    if (!derived.empty()) {
        double dxlo = 0, dxhi = 0, dylo = 0, dyhi = 0;
        span(derived, dxlo, dxhi, dylo, dyhi);
        if (const double lo = std::max(xlo, dxlo), hi = std::min(xhi, dxhi); hi - lo > 2 * settleMargin) {
            xlo = lo;
            xhi = hi;
        }
        if (const double lo = std::max(ylo, dylo), hi = std::min(yhi, dyhi); hi - lo > 2 * settleMargin) {
            ylo = lo;
            yhi = hi;
        }
    }
    x = settle(x, xlo, xhi, static_cast<double>(first.X));
    y = settle(y, ylo, yhi, static_cast<double>(first.Y));
    // Same pitch_P as sideCalibrations.
    const double pitch = s * (std::fabs(ref[3].X - ref[0].X) * detect::HUDReferenceWidth / 3);
    double left = x - pitch / 2;
    if (right) left = x + pitch / 2 - 4 * pitch;
    const double w = frame.Dx(), h = frame.Dy();
    out = config::NormalizedRect{(left - static_cast<double>(frame.Min.X)) / w,
                                 (y - pitch / 2 - static_cast<double>(frame.Min.Y)) / h, 4 * pitch / w, pitch / h};
    return true;
}

std::vector<detect::BeadPosition> ConfiguredLayout::PositionsIn(const std::string& name,
                                                                const detect::ContentArea& ca) const {
    auto it = profiles_.find(name);
    if (it == profiles_.end() || ca.W <= 0 || ca.H <= 0) {
        return {};
    }
    const config::LayoutProfile& p = it->second;
    std::vector<detect::BeadPosition> out;
    out.reserve(static_cast<size_t>(count_ > 0 ? count_ * 2 : 0));
    struct namedSide {
        const char* name;
        const config::SideLayout* data;
    };
    const namedSide sides[] = {{"left", &p.Left}, {"right", &p.Right}};
    for (const namedSide& side : sides) {
        // Invalid/missing calibration must not synthesize unverified slots.
        if (static_cast<int>(side.data->NominalCenters.size()) != count_) {
            return {};
        }
        for (size_t i = 0; i < side.data->NominalCenters.size(); ++i) {
            const config::NormalizedPoint& point = side.data->NominalCenters[i];
            const bool right = std::string(side.name) == "right";
            const Point mapped = ca.MapHUDNormalized(point.X, point.Y, right, detect::HUDReferenceWidth, detect::HUDReferenceHeight);
            detect::Bead b;
            b.Side = side.name;
            b.Idx = static_cast<int>(i);
            b.LX = point.X * detect::HUDReferenceWidth;
            b.LY = point.Y * detect::HUDReferenceHeight;
            out.emplace_back(b, mapped.X, mapped.Y);
        }
    }
    return out;
}

}  // namespace nt::engine
