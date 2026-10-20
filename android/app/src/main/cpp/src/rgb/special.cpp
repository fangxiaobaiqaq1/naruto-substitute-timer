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
                                                                std::array<ninja::Readout, 2>& identified,
                                                                std::array<engine::SideGeometry, 2>* geometry) {
    int profileIndex = 0;
    if (profile == "duel") {
        profileIndex = 1;
    }
    identified = {};
    std::array<std::vector<BeadPosition>, 2> rows;
    std::array<engine::SideGeometry, 2> regions{};
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
            // Use the same fitted HUD scale as the centers and sampling cores.
            double scale = detect::RowHUDScale(row, area, 960, 540);
            Point first = Pt(row[0].X, row[0].Y);
            const detect::SideCalibrationPtr cal = row[0].Cal;
            const Rect nameROI = ninja::NameRegion(first, scale, index == 0);
            Rect avatarROI = ninja::AvatarRegion(first, scale, index == 0);
            double avatarScale = scale;
            ninja::Tracker& tracker = nameTracker_[profileIndex][index];
            ninja::AvatarTracker* avatar = &avatarTracker_[profileIndex][index];
            if (cal != nullptr) {
                // A drawn portrait box wins over the bead-derived window.
                if (cal->Portrait()) {
                    avatarROI = ninja::AvatarRegionFromBox(cal->Avatar.X, cal->Avatar.Y, cal->Avatar.W, cal->Avatar.H,
                                                           index == 0, avatarScale);
                }
                identified[index] = tracker.ReadCalibrated(names_.get(), avatar, img, nameROI, avatarROI, scale,
                                                           avatarScale, index == 1, at);
            } else {
                identified[index] = tracker.ReadWithAvatar(names_.get(), avatar, img, nameROI, avatarROI, scale, at);
            }
            regions[index] = regionGeometry(img->Bounds(), nameROI, avatarROI, avatarScale, index == 0);
            // Only a recognized six-slot NAME permits usable slots five and six.
            // A brief label gap retains six UNKNOWN placeholders, never extra votes.
            if (row.size() == 4 && identified[index].Slots == 6) {
                // Extend the calibrated pitch in its own 1600x900 units, not
                // the old 1920-wide 30px constant (which skips slots 5/6).
                const BeadPosition& last = row.back();
                const double dx = (last.LX - row[0].LX) / double(row.size() - 1);
                const double dy = (last.LY - row[0].LY) / double(row.size() - 1);
                int xOffset = 0;
                if (!detect::CalBeads(cal)) {
                    xOffset = row[0].X - area.MapHUDPixel(row[0].LX, row[0].LY, name == "right",
                                                         detect::HUDReferenceWidth, detect::HUDReferenceHeight).X;
                }
                for (int i = 4; i < 6; i++) {
                    double lx = row[0].LX + double(i) * dx, ly = row[0].LY + double(i) * dy;
                    detect::Bead bead;
                    bead.Side = name;
                    bead.Idx = i;
                    bead.LX = lx;
                    bead.LY = ly;
                    Point mapped;
                    if (detect::CalBeads(cal)) {
                        // Calibrated rows extrapolate their own pixel pitch.
                        mapped = cal->Map(lx, ly);
                    } else {
                        mapped = area.MapHUDPixel(lx, ly, name == "right", detect::HUDReferenceWidth,
                                                  detect::HUDReferenceHeight);
                        mapped.X += xOffset;
                    }
                    BeadPosition extra(bead, mapped.X, mapped.Y);
                    extra.Cal = cal;
                    row.push_back(std::move(extra));
                }
            }
            // Some exact variants have an extra energy bar above their ordinary
            // colored beans. Shift only a name-verified row (or its bounded
            // geometry hint), after name search so its ROI cannot drift on
            // subsequent frames. This applies to Sasuke Xiayin and Minato Kyubi.
            if (double offset = identified[index].RowOffsetY; offset != 0) {
                for (BeadPosition& p : row) {
                    p.LY += offset * detect::HUDReferenceHeight / 540;
                    if (detect::CalBeads(cal)) {
                        p.Y = cal->Map(p.LX, p.LY).Y;
                    } else {
                        p.Y = area.MapHUDPixel(p.LX, p.LY, name == "right", detect::HUDReferenceWidth,
                                               detect::HUDReferenceHeight).Y;
                    }
                }
            }
        }
        rows[index] = std::move(row);
    };
    // Go: wg.Go(func() { side(1, "right") }); side(0, "left"); wg.Wait()
    ParallelInvoke([&] { side(0, "left"); }, [&] { side(1, "right"); });
    std::vector<BeadPosition> out = std::move(rows[0]);
    out.insert(out.end(), rows[1].begin(), rows[1].end());
    if (geometry != nullptr) *geometry = std::move(regions);
    return out;
}

// regionGeometry is a side's search regions and the portrait box that
// reproduces its window.
engine::SideGeometry regionGeometry(const Rect& frame, const Rect& nameROI, const Rect& avatarROI, double avatarScale,
                                    bool left) {
    engine::SideGeometry g;
    g.NameROI = nameROI;
    g.AvatarROI = avatarROI;
    if (!avatarROI.Empty() && avatarScale > 0 && !frame.Empty()) {
        double x = 0, y = 0, w = 0, h = 0;
        ninja::AvatarBoxFromRegion(avatarROI, avatarScale, left, x, y, w, h);
        g.AvatarBox = fraction(x, y, w, h, frame);
    }
    return g;
}

// fraction is a pixel box as fractions of frame.
config::NormalizedRect fraction(double x, double y, double w, double h, const Rect& frame) {
    const double fw = frame.Dx(), fh = frame.Dy();
    return config::NormalizedRect{(x - static_cast<double>(frame.Min.X)) / fw,
                                  (y - static_cast<double>(frame.Min.Y)) / fh, w / fw, h / fh};
}

// completeGeometry adds the sampled centres and, on a configured layout, the
// default bead boxes of the layout rows (before identity offsets).
std::array<engine::SideGeometry, 2> completeGeometry(std::array<engine::SideGeometry, 2> sides,
                                                     const engine::ConfiguredLayout* layout, const std::string& profile,
                                                     const RGBA* img, const detect::ContentArea& area,
                                                     const std::vector<BeadPosition>& positions,
                                                     const std::vector<BeadInfo>& beads) {
    static const char* const names[2] = {"left", "right"};
    for (int i = 0; i < 2; ++i) {
        if (layout != nullptr) {
            std::vector<BeadPosition> row;
            for (const BeadPosition& p : positions) {
                if (p.Side == names[i]) row.push_back(p);
            }
            layout->DefaultBeadBox(profile, row, img->Bounds(), area, sides[i].BeadBox);
        }
        for (const BeadInfo& b : beads) {
            if (!b.Label.empty() && b.Label[0] == "LR"[i]) sides[i].Beads.push_back(Pt(b.X, b.Y));
        }
    }
    return sides;
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

// energyGaugeBand is the unidentified energy-gauge signature on the default
// row: a THIN gold band through every slot center (not gold 4px@540 above and
// below, unlike real gold diamonds or a broad wash) that blanks all cores or
// also fills every slot midpoint. It only permits a re-read; never a vote.
bool energyGaugeBand(const RGBA* img, const std::vector<BeadPosition>& row, const detect::ContentArea& area,
                     const std::vector<BeadState>& states) {
    auto gold = [&](int x, int y) {
        if (!Pt(x, y).In(img->Bounds())) {
            return false;
        }
        Color c = img->RGBAAt(x, y);
        int r = c.R, g = c.G, b = c.B;
        return r >= 190 && g >= 110 && r - b >= 80 && g - b >= 70;
    };
    const int d = std::max(2, static_cast<int>(std::round(4 * detect::RowHUDScale(row, area, 960, 540))));
    bool continuous = true;
    for (size_t i = 0; i < row.size(); i++) {
        const BeadPosition& p = row[i];
        if (!gold(p.X, p.Y) || gold(p.X, p.Y - d) || gold(p.X, p.Y + d)) {
            return false;
        }
        if (i > 0 && !gold((row[i - 1].X + p.X) / 2, (row[i - 1].Y + p.Y) / 2)) {
            continuous = false;
        }
    }
    return !row.empty() && (continuous || allUnknownStates(states));
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
