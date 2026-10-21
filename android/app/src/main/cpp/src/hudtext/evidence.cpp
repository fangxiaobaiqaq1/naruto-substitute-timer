// Owner: hudtext（安卓文字认边，见 nt/hudtext.h）
// Port of: internal/hudtext/glyph_evidence.go、bound_value.go、engine.go（valueState）。
#include <cmath>
#include <vector>

#include "nt/hudtext.h"
#include "nt/utf8.h"

namespace nt::hudtext {

namespace {

double luma(const RGBA& img, Point p) {
    const Color c = img.RGBAAt(p.X, p.Y);
    return static_cast<double>((299u * c.R + 587u * c.G + 114u * c.B + 500u) / 1000u);
}

}  // namespace

// Ephemeral visual evidence from OCR word coordinates. Each small glyph cell must match
// CURRENT pixels; a long account cannot hide a changed character in a whole-line average.
std::shared_ptr<GlyphEvidence> NewGlyphEvidence(const RGBA& img, Rect bounds) {
    bounds = bounds.Intersect(img.Bounds());
    if (bounds.Dx() < 8 || bounds.Dy() < 8 || bounds.Dx() > 1000 || bounds.Dy() > 120) return nullptr;
    const int bw = bounds.Dx(), bh = bounds.Dy();
    std::vector<uint8_t> mask(static_cast<size_t>(bw) * static_cast<size_t>(bh), 0);
    int ink = 0;
    for (int y = bounds.Min.Y; y < bounds.Max.Y; ++y) {
        for (int x = bounds.Min.X; x < bounds.Max.X; ++x) {
            const Color c = img.RGBAAt(x, y);
            const uint8_t lo = std::min({c.R, c.G, c.B}), hi = std::max({c.R, c.G, c.B});
            if (lo < 170 || static_cast<int>(hi) - static_cast<int>(lo) > 65) continue;
            bool dark = false;
            for (const Point p : {Pt(x - 2, y), Pt(x + 2, y), Pt(x, y - 2), Pt(x, y + 2)}) {
                if (p.In(bounds) && luma(img, p) < 100) {
                    dark = true;
                    break;
                }
            }
            if (!dark) continue;
            ink++;
            for (int dy = -1; dy <= 1; ++dy)
                for (int dx = -1; dx <= 1; ++dx) {
                    const Point p = Pt(x + dx, y + dy);
                    if (p.In(bounds)) mask[static_cast<size_t>(p.Y - bounds.Min.Y) * bw + static_cast<size_t>(p.X - bounds.Min.X)] = 1;
                }
        }
    }
    if (ink < 12) return nullptr;
    auto e = std::make_shared<GlyphEvidence>();
    e->bounds = bounds;
    const int step = std::max(4, bounds.Dy() / 2);
    for (int x = bounds.Min.X; x < bounds.Max.X; x += step) {
        GlyphCell cell;
        double squares = 0;
        for (int y = bounds.Min.Y; y < bounds.Max.Y; ++y) {
            for (int px = x; px < std::min(x + step, bounds.Max.X); ++px) {
                if (!mask[static_cast<size_t>(y - bounds.Min.Y) * bw + static_cast<size_t>(px - bounds.Min.X)]) continue;
                const double v = luma(img, Pt(px, y));
                cell.samples.push_back(GlyphSample{Pt(px, y), v});
                cell.sum += v;
                squares += v * v;
            }
        }
        const double n = static_cast<double>(cell.samples.size());
        if (n < 12) continue;
        cell.variance = squares - cell.sum * cell.sum / n;
        if (cell.variance / n < 400) continue;
        e->cells.push_back(std::move(cell));
    }
    if (e->cells.size() < 2) return nullptr;
    return e;
}

bool GlyphEvidence::Matches(const RGBA* img) const {
    if (img == nullptr) return false;
    // One global alignment, never independently move each glyph to fit another.
    for (int dy : {0, -1, 1}) {
        for (int dx : {0, -1, 1}) {
            const Point delta(dx, dy);
            if (!bounds.Add(delta).In(img->Bounds())) continue;
            bool all = true;
            for (const GlyphCell& cell : cells) {
                double sum = 0, squares = 0, dot = 0;
                for (const GlyphSample& sample : cell.samples) {
                    const double v = luma(*img, sample.p.Add(delta));
                    sum += v;
                    squares += v * v;
                    dot += v * sample.value;
                }
                const double n = static_cast<double>(cell.samples.size());
                const double variance = squares - sum * sum / n;
                if (variance <= 0 || (dot - cell.sum * sum / n) / std::sqrt(variance * cell.variance) < .92) {
                    all = false;
                    break;
                }
            }
            if (all) return true;
        }
    }
    return false;
}

// Recover only a field that the OCR parser actually read, using the word boxes in an agreeing
// treatment. Account evidence includes the parentheses so a longer account cannot validate a
// shorter one. Coordinates map back to native pixels, including the strip's origin.
std::shared_ptr<GlyphEvidence> Evidence(const std::vector<Row>& rows, const std::vector<ocr::Line>& lines, int side,
                                        const std::string& expected, const RGBA& strip, const Rect& roi) {
    if (expected.empty() || strip.Nil()) return nullptr;
    const std::vector<char32_t> want = utf8::Runes(expected);
    for (const Row& row : rows) {
        if (row.side != side) continue;
        std::vector<char32_t> chars;
        std::vector<Rect> boxes;
        for (const ocr::Word& w : WordsForRow(lines, row.rect)) {
            const std::vector<char32_t> text = utf8::Runes(Compact(w.Text));
            if (text.empty()) continue;
            for (size_t i = 0; i < text.size(); ++i) {
                const double x0 = w.X + static_cast<double>(i) * w.Width / static_cast<double>(text.size());
                const double x1 = w.X + static_cast<double>(i + 1) * w.Width / static_cast<double>(text.size());
                chars.push_back(text[i]);
                boxes.push_back(MakeRect(static_cast<int>(std::floor(x0)), static_cast<int>(std::floor(w.Y)),
                                         static_cast<int>(std::ceil(x1)), static_cast<int>(std::ceil(w.Y + w.Height))));
            }
        }
        // strings.Index / strings.Count (non-overlapping) on runes: expected must occur exactly once.
        size_t first = chars.size(), count = 0;
        for (size_t i = 0; i + want.size() <= chars.size();) {
            if (std::equal(want.begin(), want.end(), chars.begin() + static_cast<ptrdiff_t>(i))) {
                if (count == 0) first = i;
                count++;
                i += want.size();
            } else {
                i++;
            }
        }
        if (count != 1) continue;
        Rect box;
        for (size_t i = first; i < first + want.size(); ++i) box = box.Union(boxes[i]);
        const double sx = static_cast<double>(roi.Dx()) / static_cast<double>(row.rect.Dx());
        const double sy = static_cast<double>(roi.Dy()) / static_cast<double>(row.rect.Dy());
        const Rect local = MakeRect(static_cast<int>(std::floor(static_cast<double>(box.Min.X - row.rect.Min.X) * sx)),
                                    static_cast<int>(std::floor(static_cast<double>(box.Min.Y - row.rect.Min.Y) * sy)),
                                    static_cast<int>(std::ceil(static_cast<double>(box.Max.X - row.rect.Min.X) * sx)),
                                    static_cast<int>(std::ceil(static_cast<double>(box.Max.Y - row.rect.Min.Y) * sy)))
                               .Inset(-1);
        std::shared_ptr<GlyphEvidence> proof = NewGlyphEvidence(strip, local);
        if (proof == nullptr) continue;
        // The strip owns zero-origin pixels; samples are stored in frame coordinates.
        proof->bounds = proof->bounds.Add(roi.Min);
        for (GlyphCell& cell : proof->cells)
            for (GlyphSample& s : cell.samples) s.p = s.p.Add(roi.Min);
        return proof;
    }
    return nullptr;
}

void ValueState::observe(const std::string& value, TimeNs t) {
    if (value.empty()) {
        *this = ValueState{};
        return;
    }
    if (value != candidate) {
        *this = ValueState{};
        candidate = value;
        hits = 1;
        at = t;
        return;
    }
    if (t > at) {
        hits++;
        at = t;
    }
    if (hits >= 2) accepted = value;
}

// A blank/noisy OCR result from retrying the other side is not evidence that these CURRENT,
// still-matching glyphs changed. A real glyph/scene/geometry change clears them before this
// is called. Initial acquisition still needs two OCR results.
void ObserveBound(ValueState& state, std::shared_ptr<GlyphEvidence>& proof, const std::string& value,
                  const std::shared_ptr<GlyphEvidence>& incoming, const RGBA* img, TimeNs at, bool unchanged) {
    const bool validProof = incoming != nullptr && incoming->Matches(img);
    if (proof != nullptr && proof->Matches(img)) {
        if (!state.accepted.empty()) {
            // Go upgrades a stable base name to a full version; accounts never contain '['.
            if (value.rfind(state.accepted + "[", 0) == 0 && validProof) {
                const std::string accepted = state.accepted;
                state.observe(value, at);
                if (state.accepted.empty()) {
                    state.accepted = accepted;
                } else if (state.accepted == value) {
                    proof = incoming;
                }
            }
            return;
        }
        if (value.empty()) return;
    }
    if ((incoming != nullptr && !validProof) || (incoming == nullptr && !unchanged)) return;
    const bool keep = state.candidate == value && proof != nullptr;
    state.observe(value, at);
    if (value.empty()) {
        proof = nullptr;
    } else if (!keep) {
        proof = validProof ? incoming : nullptr;
    }
}

}  // namespace nt::hudtext
