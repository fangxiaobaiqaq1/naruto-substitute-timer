// Owner: hudtext（安卓文字认边，见 nt/hudtext.h）
// Port of: internal/hudtext/images.go（nameRegions、lettering、buildSheet 的彩色行）。
#include <cmath>
#include <vector>

#include "nt/detect.h"
#include "nt/hash.h"
#include "nt/hudtext.h"
#include "nt/match.h"

namespace nt::hudtext {

NameLayout::NameLayout(const config::LayoutConfig& cfg) : cfg_(cfg) {
    ok_ = detect::ParseMode(cfg.ContentMode, mode_);
    if (ok_) layout_ = std::make_shared<engine::ConfiguredLayout>(mode_, cfg_);
}

// Go nameLayout.regions: the strip above each bead row, from the first bead's centre, at the
// fitted HUD scale (or the side's calibrated bead scale).
bool NameLayout::Regions(const RGBA* img, const std::string& profile, std::array<Rect, 2>& out) const {
    out = {};
    if (!ok_ || img == nullptr) return false;
    auto [ca, ok] = detect::ResolveContentArea(img, mode_, cfg_.ReferenceWidth, cfg_.ReferenceHeight, cfg_.AutoAspectTolerance);
    if (!ok) return false;
    const std::vector<detect::BeadPosition> positions = layout_->PositionsInImage(profile, img, ca);
    if (cfg_.BeadsPerSide < 1 || static_cast<int>(positions.size()) != 2 * cfg_.BeadsPerSide) return false;
    // OCR offsets must use the same fitted HUD scale and safe-area anchors.
    for (int i = 0; i < 2; ++i) {
        const detect::BeadPosition& first = positions[static_cast<size_t>(i * cfg_.BeadsPerSide)];
        const Point center(first.X, first.Y);
        double x0 = -4., x1 = 330.;
        if (i == 1) {
            x0 = -330.;
            x1 = 4.;
        }
        double scale = ca.HUDScale(960, 540);
        // Go: .020*540 and .073*540 are exact constants (10.8, 39.42).
        int y0 = ca.Y + static_cast<int>(std::round(10.8 * scale)), y1 = ca.Y + static_cast<int>(std::round(39.42 * scale));
        if (detect::CalBeads(first.Cal)) {
            // A calibrated row carries its own scale and vertical placement (.020/.073 × 900).
            scale = first.Cal->Scale * detect::HUDReferenceWidth / 960;
            y0 = first.Cal->Map(first.LX, 18.0).Y;
            y1 = first.Cal->Map(first.LX, 65.7).Y;
        }
        out[static_cast<size_t>(i)] =
            MakeRect(center.X + static_cast<int>(std::round(x0 * scale)), y0, center.X + static_cast<int>(std::round(x1 * scale)), y1)
                .Intersect(img->Bounds());
        if (out[static_cast<size_t>(i)].Dx() < 80 || out[static_cast<size_t>(i)].Dy() < 10) return false;
    }
    return true;
}

// Go lettering: conservative fallback before OCR supplies usable word coordinates. White,
// low-chroma pixels with a dark outline two pixels away.
uint64_t Lettering(const RGBA& img, Rect roi, int& count) {
    roi = roi.Intersect(img.Bounds());
    count = 0;
    std::vector<uint8_t> bits((static_cast<size_t>(roi.Dx()) * static_cast<size_t>(roi.Dy()) + 7) / 8);
    if (roi.Empty()) return HashBytes(0x6c657474u, bits.data(), bits.size());
    const Rect bounds = img.Bounds();
    auto dark = [&](int x, int y) {
        if (x < bounds.Min.X || y < bounds.Min.Y || x >= bounds.Max.X || y >= bounds.Max.Y) return false;
        const uint8_t* p = img.Pix + img.PixOffset(x, y);
        return std::max({p[0], p[1], p[2]}) < 90;
    };
    for (int y = roi.Min.Y; y < roi.Max.Y; ++y) {
        const uint8_t* row = img.RowPtr(y);
        for (int x = roi.Min.X; x < roi.Max.X; ++x) {
            const uint8_t* p = row + static_cast<ptrdiff_t>(x - bounds.Min.X) * 4;
            const uint8_t lo = std::min({p[0], p[1], p[2]}), hi = std::max({p[0], p[1], p[2]});
            if (lo < 180 || static_cast<int>(hi) - static_cast<int>(lo) > 65) continue;
            if (!dark(x - 2, y) && !dark(x + 2, y) && !dark(x, y - 2) && !dark(x, y + 2)) continue;
            const size_t i = static_cast<size_t>(y - roi.Min.Y) * static_cast<size_t>(roi.Dx()) + static_cast<size_t>(x - roi.Min.X);
            bits[i / 8] |= static_cast<uint8_t>(1u << (i % 8));
            count++;
        }
    }
    return HashBytes(0x6c657474u ^ (static_cast<uint64_t>(roi.Dx()) << 32) ^ static_cast<uint64_t>(roi.Dy()), bits.data(), bits.size());
}

// Go buildSheet, colour rows only (the local neural recognizer reads the colour sheet):
// original ×2, contrast ×2, original ×4 (≤ 2200 wide), nearest-neighbour scaled into rows
// stacked 12 px from the left with 32 px between them.
std::vector<Row> BuildRows(const std::array<RGBA, 2>& strips) {
    std::vector<Row> rows;
    int y = 0;
    for (int side = 0; side < 2; ++side) {
        const RGBA& s = strips[static_cast<size_t>(side)];
        if (s.Nil() || s.Bounds().Empty()) continue;
        const int dx = s.Bounds().Dx(), dy = s.Bounds().Dy();
        RGBA contrast;
        for (int v = 0; v < variants; ++v) {
            const int factor = v == 2 ? 4 : 2;
            const int w = std::min(2200, dx * factor);
            const int h = std::max(16, static_cast<int>(std::round(static_cast<double>(dy) * static_cast<double>(w) / static_cast<double>(dx))));
            Row row;
            row.side = side;
            row.variant = v;
            row.rect = MakeRect(12, y + 12, 12 + w, y + 12 + h);
            y += h + 32;
            const RGBA* source = &s;
            if (v == 1) {
                contrast = RGBA::NewUninit(s.Bounds());
                for (int py = s.Bounds().Min.Y; py < s.Bounds().Max.Y; ++py) {
                    const uint8_t* src = s.RowPtr(py);
                    uint8_t* dst = contrast.Pix + contrast.PixOffset(s.Bounds().Min.X, py);
                    for (int px = 0; px < dx; ++px) {
                        for (int c = 0; c < 3; ++c) {
                            const int value = (static_cast<int>(src[px * 4 + c]) - 128) * 115 / 100 + 128;
                            dst[px * 4 + c] = static_cast<uint8_t>(std::max(0, std::min(255, value)));
                        }
                        dst[px * 4 + 3] = 255;
                    }
                }
                source = &contrast;
            }
            row.image = RGBA::New(MakeRect(0, 0, w, h));
            ocr::ScaleNearest(row.image, row.image.Bounds(), *source);
            rows.push_back(std::move(row));
        }
    }
    return rows;
}

}  // namespace nt::hudtext
