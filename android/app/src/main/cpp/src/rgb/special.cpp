// Owner: cpp-rgb
// Port of: internal/engine/rgb/special.go
// Contract: include/nt/rgb_internal.h — see android/ARCHITECTURE.md.
//
// Go 的 parallelSides 包级开关（测试用来对比并行/串行）由 nt::SetParallelEnabled 统一控制；
// nt::ParallelInvoke 在关闭时按 a、b 顺序串行执行，与 Go 的 else 分支一致。
// specialPositions / specialPositionsFor 只是 time.Now() 的便捷包装（仅测试使用），未移植。
#include <algorithm>
#include <cmath>

#include "nt/parallel.h"
#include "nt/rgb.h"
#include "nt/rgb_internal.h"

namespace nt::rgb {

std::vector<detect::BeadPosition> Engine::specialPositionsForAt(const std::string& profile, const RGBA* img,
                                                                const std::vector<detect::BeadPosition>& positions,
                                                                const detect::ContentArea& area, TimeNs at,
                                                                std::array<ninja::Readout, 2>& identified) {
    int profileIndex = 0;
    if (profile == "duel") {
        profileIndex = 1;
    }
    identified = {};
    std::array<std::vector<BeadPosition>, 2> rows;
    // Each side touches only its own trackers and its own result slots; the
    // shared Reader and catalog are safe for concurrent tracker calls.
    auto side = [&](int index, const std::string& name) {
        std::vector<BeadPosition> row;
        for (const BeadPosition& p : positions) {
            if (p.Side == name) {
                row.push_back(p);
            }
        }
        if (row.size() >= 2) {
            // Stored name strips use the 960-wide HUD reference. Individual
            // integer-rounded bean centers may alternate 15/16px; their first
            // interval must not accidentally stretch all of the name lettering.
            double scale = double(area.W) / 960;
            Point first = Pt(row[0].X, row[0].Y);
            identified[index] = nameTracker_[profileIndex][index].ReadWithAvatar(
                names_.get(), &avatarTracker_[profileIndex][index], img, ninja::NameRegion(first, scale, index == 0),
                ninja::AvatarRegion(first, scale, index == 0), scale, at);
            // Only a recognized six-slot NAME permits usable slots five and six.
            // A brief label gap retains six UNKNOWN placeholders, never extra votes.
            if (row.size() == 4 && identified[index].Slots == 6) {
                double dx = 30.0, dy = 0.0;
                if (row[1].LX < row[0].LX) {
                    dx = -dx;
                }
                for (int i = 4; i < 6; i++) {
                    double lx = row[0].LX + double(i) * dx, ly = row[0].LY + double(i) * dy;
                    detect::Bead bead;
                    bead.Side = name;
                    bead.Idx = i;
                    bead.LX = lx;
                    bead.LY = ly;
                    row.push_back(BeadPosition(
                        bead, area.X + static_cast<int>(std::round(lx * double(area.W) / detect::LogicWidth)),
                        area.Y + static_cast<int>(std::round(ly * double(area.H) / detect::LogicHeight))));
                }
            }
            // Some exact variants have an extra energy bar above their ordinary
            // colored beans. Shift only a name-verified row (or its bounded
            // geometry hint), after name search so its ROI cannot drift on
            // subsequent frames. This applies to Sasuke Xiayin and Minato Kyubi.
            if (double offset = identified[index].RowOffsetY; offset != 0) {
                for (BeadPosition& p : row) {
                    p.LY += offset * detect::LogicHeight / 540;
                    p.Y = area.Y + static_cast<int>(std::round(p.LY * double(area.H) / detect::LogicHeight));
                }
            }
        }
        rows[index] = std::move(row);
    };
    // Go: wg.Go(func() { side(1, "right") }); side(0, "left"); wg.Wait()
    ParallelInvoke([&] { side(0, "left"); }, [&] { side(1, "right"); });
    std::vector<BeadPosition> out = std::move(rows[0]);
    out.insert(out.end(), rows[1].begin(), rows[1].end());
    return out;
}

void applyNames(engine::Result& res, const std::array<ninja::Readout, 2>& readouts) {
    res.LeftNinja = readouts[0].Name;
    res.RightNinja = readouts[1].Name;
    for (const BeadInfo& b : res.Beads) {
        if (!b.Label.empty() && b.Label[0] == 'L') {
            res.LeftSlots++;
        } else if (!b.Label.empty() && b.Label[0] == 'R') {
            res.RightSlots++;
        }
    }
}

BeadState specialPixel(ninja::Palette palette, int r, int g, int b) {
    switch (palette) {
        case ninja::Palette::Warm:
            // Six-slot end flares become pale yellow when scaled, while retaining
            // clear yellow chroma. Do not require a saturated-red center in that slot.
            if (r >= 225 && g >= 175 && r - b >= 55 && g - b >= 45) {
                return detect::StateLight;
            }
            if (r >= 145 && r - g >= 50 && r - b >= 45) {
                return detect::StateLight;
            }
            // Yellow/gold and blue continue through the ordinary calibrated path.
            break;
        case ninja::Palette::Purple:
            if (r >= 100 && b >= 165 && r - g >= 40 && b - g >= 55) {
                return detect::StateLight;
            }
            if (r >= 200 && b >= 225 && b - g >= 25 && r - g >= 15) {
                return detect::StateLight;
            }
            if (r <= 85 && g <= 55 && b <= 125 && b - r >= 10 && b - g >= 15) {
                return detect::StateDark;
            }
            break;
        case ninja::Palette::Red:
            if (r >= 130 && r - g >= 65 && r - b >= 55) {
                return detect::StateLight;
            }
            if (r <= 85 && g <= 50 && b <= 55 && r - g >= 12 && r - b >= 12) {
                return detect::StateDark;
            }
            break;
        default:
            break;
    }
    return detect::StateUnknown;
}

// A broad colored wash is not a row of beans. Strong special-color pixels
// above AND below the body must be treated as obscured rather than six votes.
bool specialWash(const RGBA* img, const BeadPosition& p, ninja::Palette palette, int guard) {
    if (palette == ninja::Palette::None) {
        return false;
    }
    for (int dy : {-guard, guard}) {
        Point point = Pt(p.X, p.Y + dy);
        if (!point.In(img->Bounds())) {
            return false;
        }
        Color c = img->RGBAAt(point.X, point.Y);
        if (specialPixel(palette, int(c.R), int(c.G), int(c.B)) != detect::StateLight) {
            return false;
        }
    }
    if (palette == ninja::Red && isolatedRedHalo(img, p, guard)) {
        return false;
    }
    return true;
}

// Read only the lower portion of this calibrated body, without moving the core
// to a brighter pixel. Empty red beans have dim shoulders even during rim glow.
bool redLowerBody(const RGBA* img, const BeadPosition& p, int w, int h) {
    int red = 0, total = 0;
    const int side = std::max(1, w / 3);
    const int offsets[3] = {-side, 0, side};
    for (int dy = h / 2 + 1; dy <= h; dy++) {
        for (int dx : offsets) {
            Point point = Pt(p.X + dx, p.Y + dy);
            if (!point.In(img->Bounds())) {
                return false;
            }
            Color c = img->RGBAAt(point.X, point.Y);
            total++;
            if (specialPixel(ninja::Red, int(c.R), int(c.G), int(c.B)) == detect::StateLight) {
                red++;
            }
        }
    }
    return total > 0 && red * 3 >= total * 2;
}

// A red health bar above and this skin's short halo below may trip the normal
// wash guard. Accept that case only with a distinct core against BOTH slot gaps
// and a halo that has already faded further below. A flat red/white band still
// fails; this never enlarges the sample core or relaxes ordinary/purple skins.
bool isolatedRedHalo(const RGBA* img, const BeadPosition& p, int guard) {
    Point far = Pt(p.X, p.Y + 2 * guard);
    if (!far.In(img->Bounds())) {
        return false;
    }
    Color c = img->RGBAAt(far.X, far.Y);
    if (specialPixel(ninja::Red, int(c.R), int(c.G), int(c.B)) == detect::StateLight) {
        return false;
    }
    const int radius = std::max(1, guard / 8);
    auto chromaLight = [&](int x, int y, double& out) -> bool {
        int sum = 0, n = 0;
        for (int dx = -radius; dx <= radius; dx++) {
            Point point = Pt(x + dx, y);
            if (!point.In(img->Bounds())) {
                out = 0;
                return false;
            }
            Color c = img->RGBAAt(point.X, point.Y);
            // R is saturated across the halo; G+B distinguishes the hot body.
            sum += int(c.G) + int(c.B);
            n++;
        }
        out = double(sum) / double(n);
        return true;
    };
    const int gap = std::max(2, static_cast<int>(std::round(double(guard) * .75)));
    const int halfBody = std::max(2, guard * 3 / 8);
    int consecutive = 0;
    // The glint travels horizontally across the midpoint. Verify the bounded
    // body's shape on two adjacent scanlines instead of treating one bright gap
    // at the midpoint as proof that the entire body is an obscuring wash.
    for (int y = p.Y - halfBody; y <= p.Y + halfBody; y++) {
        double core = 0, left = 0, right = 0;
        bool ok = chromaLight(p.X, y, core);
        bool leftOK = chromaLight(p.X - gap, y, left);
        bool rightOK = chromaLight(p.X + gap, y, right);
        if (ok && leftOK && rightOK && core - std::max(left, right) >= 32) {
            consecutive++;
            if (consecutive >= 2) {
                return true;
            }
        } else {
            consecutive = 0;
        }
    }
    return false;
}

}  // namespace nt::rgb
