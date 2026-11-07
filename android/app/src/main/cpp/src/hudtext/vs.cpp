// Port of internal/hudtext/vs.go. Both opening and death swap use scene=vs and
// the same bottom account line; neither is battle/round-opening evidence.
#include <algorithm>
#include <cmath>
#include <map>

#include "nt/hudtext.h"

namespace nt::hudtext {

bool VSNameRegions(const detect::ContentArea& area, const Rect& bounds, std::array<Rect, 2>& out) {
    out = {};
    const double scale = area.HUDScale(960, 540);
    const double boxes[2][4] = {{.11, .905, .08, .05}, {.828, .905, .085, .05}};
    for (size_t i = 0; i < 2; ++i) {
        const auto& r = boxes[i];
        const int x0 = area.MapHUDNormalized(r[0], 0, i == 1, 960, 540).X;
        const int x1 = area.MapHUDNormalized(r[0] + r[2], 0, i == 1, 960, 540).X;
        const int bottom = area.Y + area.H;
        const int y0 = bottom - static_cast<int>(std::round((1 - r[1]) * 540 * scale));
        const int y1 = bottom - static_cast<int>(std::round((1 - r[1] - r[3]) * 540 * scale));
        out[i] = MakeRect(x0, y0, x1, y1).Intersect(bounds);
        if (out[i].Dx() < 16 || out[i].Dy() < 8) return false;
    }
    return true;
}

// A footer has no account parentheses. Only complete configured names count:
// never a prefix, fuzzy decoration, rank, percentage or an unknown opponent.
std::string FooterAccount(const std::array<std::string, variants>& raw, const std::vector<std::string>& names) {
    std::map<std::string, int> votes;
    for (const auto& text : raw) {
        const std::string value = Compact(text, false); // remove OCR word spacing ONLY
        if (!value.empty() && std::find(names.begin(), names.end(), value) != names.end() && ++votes[value] >= 2)
            return value;
    }
    return "";
}

SideDecision DecideFooterSide(const std::array<std::string, 2>& accounts, const std::vector<std::string>& names) {
    bool mine[2]{};
    for (size_t i = 0; i < 2; ++i)
        mine[i] = !accounts[i].empty() && std::find(names.begin(), names.end(), accounts[i]) != names.end();
    SideDecision d;
    if (mine[0] != mine[1]) {
        d.side = mine[0] ? 0 : 1;
        d.player = accounts[static_cast<size_t>(d.side)];
        d.how = "VS full account";
    }
    return d;
}

bool ReadFooterAccounts(const ocr::Recognizer& recognizer, const std::array<RGBA, 2>& strips,
                        const std::array<Rect, 2>& regions, const std::vector<std::string>& names,
                        ReadResult& out, const std::atomic<bool>* cancel) {
    (void)regions; // job retains them for whole-ROI signature validation on delivery
    out = ReadResult{};
    const auto rows = BuildRows(strips);
    std::vector<ocr::Recognizer::RowImage> images;
    for (const auto& row : rows) images.push_back({&row.image, row.rect});
    std::vector<ocr::Line> lines;
    std::vector<double> millis;
    if (!recognizer.ReadRows(images, lines, &out.err, 2, cancel, &millis)) return false;
    out.rows = static_cast<int>(rows.size());
    for (double m : millis) out.rowMillis += m;
    out.texts = Texts(rows, lines);
    for (size_t i = 0; i < 2; ++i) {
        out.values[i] = FooterAccount(out.texts[i], names);
        // Bind the ENTIRE clean footer ROI via its lettering signature, not a
        // local glyph proof: an appended suffix must revoke the old shorter
        // account, both while pending and after acceptance. No dynamic loading
        // percentage lies in this ROI. Battle glyph evidence stays unchanged.
        out.proofs[i] = nullptr;
    }
    return true;
}

}  // namespace nt::hudtext
