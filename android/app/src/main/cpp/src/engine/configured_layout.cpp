// Owner: cpp-gated-frame
// Port of: internal/engine/configured_layout.go
// Contract: include/nt/engine.h — see android/ARCHITECTURE.md.
#include "nt/engine.h"

#include <cmath>

namespace nt::engine {

namespace {

// cloneProfile：Go 里复制切片以免与配置共享底层数组；C++ 按值复制本来就是深拷贝。
config::LayoutProfile cloneProfile(const config::LayoutProfile& p) { return p; }

}  // namespace

// ConfiguredLayout is immutable after construction and shared by both engines.
ConfiguredLayout::ConfiguredLayout(detect::ContentMode mode, const config::LayoutConfig& cfg)
    : mode_(mode),
      preferred_(cfg.PreferredProfile),
      profiles_{{"camp", cloneProfile(cfg.Profile("camp"))}, {"duel", cloneProfile(cfg.Profile("duel"))}},
      count_(cfg.BeadsPerSide),
      reference_(Pt(cfg.ReferenceWidth, cfg.ReferenceHeight)),
      tolerance_(cfg.AutoAspectTolerance) {}

std::vector<detect::BeadPosition> ConfiguredLayout::Positions(int w, int h) const {
    std::string name = preferred_;
    if (name != "duel") {
        name = "camp";
    }
    return PositionsFor(name, w, h);
}

std::vector<detect::BeadPosition> ConfiguredLayout::PositionsFor(const std::string& name, int w, int h) const {
    return PositionsIn(name, detect::ComputeContentArea(w, h, mode_));
}

// ContentArea is shared with scene matching. A size alone cannot prove bars.
std::pair<detect::ContentArea, bool> ConfiguredLayout::ContentArea(const RGBA* img) const {
    return detect::ResolveContentArea(img, mode_, reference_.X, reference_.Y, tolerance_);
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
            int x = ca.X + static_cast<int>(std::round(static_cast<double>(ca.W) * point.X));
            int y = ca.Y + static_cast<int>(std::round(static_cast<double>(ca.H) * point.Y));
            detect::Bead b;
            b.Side = side.name;
            b.Idx = static_cast<int>(i);
            b.LX = point.X * detect::LogicWidth;
            b.LY = point.Y * detect::LogicHeight;
            out.emplace_back(b, x, y);
        }
    }
    return out;
}

}  // namespace nt::engine
