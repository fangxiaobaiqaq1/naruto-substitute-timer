// Owner: hudtext（安卓文字认边，见 nt/hudtext.h）
// Port of: internal/hudtext/text_words.go（wordsForRow）、images.go（sheet.texts）、parse.go（compact、
// validAccount、consensus 的账号部分）；AccountSegment / MatchName 是安卓的账号读法（见 nt/hudtext.h）。
#include <algorithm>
#include <cmath>
#include <map>

#include "nt/hudtext.h"
#include "nt/utf8.h"

namespace nt::hudtext {

namespace {

bool validWordBox(const ocr::Word& w, const Rect& row) {
    for (double v : {w.X, w.Y, w.Width, w.Height})
        if (std::isnan(v) || std::isinf(v)) return false;
    return w.Width > 0 && w.Height > 0 && w.X >= static_cast<double>(row.Min.X) && w.Y >= static_cast<double>(row.Min.Y) &&
           w.X + w.Width <= static_cast<double>(row.Max.X) && w.Y + w.Height <= static_cast<double>(row.Max.Y);
}

constexpr char32_t kOpen = U'(', kClose = U')';

bool bracket(char32_t r) { return r == U'(' || r == U')' || r == U'[' || r == U']'; }

}  // namespace

// OCR may attach a remote scenery mark to a title on the same horizontal line. Remove only
// small, detached groups (<1/3 of the main text width) across a gap larger than two glyph heights.
std::vector<ocr::Word> WordsForRow(const std::vector<ocr::Line>& lines, const Rect& rect) {
    std::vector<ocr::Word> words;
    const Rect box = rect.Inset(-3);
    for (const ocr::Line& line : lines) {
        if (line.Words.empty()) continue;
        bool valid = true;
        double top = line.Words[0].Y, bottom = line.Words[0].Y + line.Words[0].Height;
        for (const ocr::Word& w : line.Words) {
            if (!validWordBox(w, box)) {
                valid = false;
                break;
            }
            top = std::min(top, w.Y);
            bottom = std::max(bottom, w.Y + w.Height);
        }
        if (!valid) continue;
        size_t from = 0;
        if (line.Words.size() > 1 && line.Words[0].Height < (bottom - top) * .25) from = 1;
        words.insert(words.end(), line.Words.begin() + static_cast<ptrdiff_t>(from), line.Words.end());
    }
    std::stable_sort(words.begin(), words.end(), [](const ocr::Word& a, const ocr::Word& b) { return a.X < b.X; });
    if (words.size() < 2) return words;
    struct Group {
        size_t first = 0, last = 0;
        double width = 0;
    };
    std::vector<Group> groups(1);
    for (size_t i = 0; i < words.size(); ++i) {
        const ocr::Word& w = words[i];
        if (i > 0) {
            const ocr::Word& prev = words[i - 1];
            if (w.X - (prev.X + prev.Width) > std::max(12.0, 2 * std::max(w.Height, prev.Height))) {
                groups.back().last = i;
                Group g;
                g.first = i;
                groups.push_back(g);
            }
        }
        groups.back().width += w.Width;
    }
    groups.back().last = words.size();
    size_t main = 0;
    for (size_t i = 0; i < groups.size(); ++i)
        if (groups[i].width > groups[main].width) main = i;
    size_t first = 0, last = groups.size() - 1;
    while (first < main && groups[first].width * 3 < groups[main].width) first++;
    while (last > main && groups[last].width * 3 < groups[main].width) last--;
    return std::vector<ocr::Word>(words.begin() + static_cast<ptrdiff_t>(groups[first].first),
                                  words.begin() + static_cast<ptrdiff_t>(groups[last].last));
}

std::array<std::array<std::string, variants>, 2> Texts(const std::vector<Row>& rows, const std::vector<ocr::Line>& lines) {
    std::array<std::array<std::string, variants>, 2> out;
    for (const Row& row : rows) {
        std::string& s = out[static_cast<size_t>(row.side)][static_cast<size_t>(row.variant)];
        for (const ocr::Word& w : WordsForRow(lines, row.rect)) {
            if (!s.empty()) s += " ";
            s += w.Text;
        }
    }
    return out;
}

std::string Compact(const std::string& s) {
    std::string out;
    out.reserve(s.size());
    for (size_t i = 0, w = 0; i < s.size(); i += w) {
        char32_t r = utf8::DecodeRune(s, i, w);
        if (utf8::IsSpace(r)) continue;
        switch (r) {
            case U'［': case U'【': case U'〔': r = U'['; break;
            case U'］': case U'】': case U'〕': r = U']'; break;
            case U'（': case U'{': r = U'('; break;
            case U'）': case U'}': r = U')'; break;
            default: break;
        }
        utf8::AppendRune(out, r);
    }
    return out;
}

// The account is the last bracketed part of 忍者名[版本](账号). The phone's own overlay card can
// follow it on the same line, and a support portrait can cover its end: the last '(' with the
// first ')' after it, else (nothing closes it) the visible rest of the line.
std::string AccountSegment(const std::string& raw) {
    const std::vector<char32_t> text = utf8::Runes(Compact(raw));
    size_t open = text.size();
    for (size_t i = text.size(); i-- > 0;) {
        if (text[i] == kOpen) {
            open = i;
            break;
        }
    }
    if (open == text.size()) return "";
    size_t close = open + 1;
    while (close < text.size() && text[close] != kClose) close++;
    const bool full = close < text.size();
    size_t end = full ? close : text.size();
    if (!full)
        while (end > open + 1 && text[end - 1] == utf8::RuneError) end--;  // the glyph at the occlusion edge
    const size_t n = end - (open + 1);
    if (n < 2 || n > 24) return "";
    for (size_t i = open + 1; i < end; ++i)
        if (bracket(text[i])) return "";
    std::string out = "(" + utf8::FromRunes(text, open + 1, end);
    if (full) out += ")";
    return out;
}

// Two of the three treatments must agree exactly (Go consensus for accounts).
std::string Consensus(const std::array<std::string, variants>& raw) {
    std::map<std::string, int> counts;
    for (const std::string& text : raw) {
        const std::string seg = AccountSegment(text);
        if (!seg.empty()) counts[seg]++;
    }
    for (const auto& [seg, n] : counts)
        if (n >= 2) return seg;
    return "";
}

namespace {

// name runes [i..) against OCR runes [j..): characters the recognizer knows must be equal; one it
// cannot output (not in its dictionary) matches nothing or any one character.
bool matchFrom(const std::vector<char32_t>& name, size_t i, const std::vector<char32_t>& seen, size_t j, bool prefix,
               const Dictionary& known, int depth = 0) {
    if (depth > 64) return false;
    if (prefix && j == seen.size()) return true;
    if (i == name.size()) return j == seen.size();
    if (known(name[i])) return j < seen.size() && seen[j] == name[i] && matchFrom(name, i + 1, seen, j + 1, prefix, known, depth + 1);
    if (matchFrom(name, i + 1, seen, j, prefix, known, depth + 1)) return true;
    return j < seen.size() && matchFrom(name, i + 1, seen, j + 1, prefix, known, depth + 1);
}

}  // namespace

int MatchName(const std::string& segment, const std::string& name, const Dictionary& known) {
    if (segment.size() < 3 || segment[0] != '(' || name.empty()) return 0;
    const bool full = segment.back() == ')';
    const std::vector<char32_t> seen = utf8::Runes(segment.substr(1, segment.size() - (full ? 2 : 1)));
    const std::vector<char32_t> want = utf8::Runes(Compact(name));
    if (seen.empty() || want.empty()) return 0;
    if (full) return matchFrom(want, 0, seen, 0, false, known) ? 2 : 0;
    // A covered end: at least two visible characters the recognizer knows, and at least half of
    // the name's known characters, must be the start of the name.
    size_t knownWant = 0, knownSeen = 0;
    for (char32_t r : want)
        if (known(r)) knownWant++;
    for (char32_t r : seen)
        if (known(r)) knownSeen++;
    if (knownSeen < 2 || 2 * knownSeen < knownWant) return 0;
    return matchFrom(want, 0, seen, 0, true, known) ? 1 : 0;
}

// Go validAccount: letters, digits and _·.- only, 2 to 24 of them. Han and other scripts count
// as letters; symbols, punctuation and U+FFFD do not.
bool ValidAccount(const std::string& s) {
    size_t n = 0;
    for (char32_t r : utf8::Runes(s)) {
        n++;
        if ((r >= U'0' && r <= U'9') || (r >= U'a' && r <= U'z') || (r >= U'A' && r <= U'Z') || r == U'_' || r == U'·' ||
            r == U'.' || r == U'-')
            continue;
        if (r < 0x80 || r == utf8::RuneError) return false;
        // Common punctuation / symbol blocks.
        if ((r >= 0x2000 && r <= 0x2BFF) || (r >= 0x3000 && r <= 0x303F) || (r >= 0xFF00 && r <= 0xFF0F) ||
            (r >= 0xFF1A && r <= 0xFF20) || (r >= 0xFF3B && r <= 0xFF40) || (r >= 0xFF5B && r <= 0xFF65) || (r >= 0xA0 && r <= 0xBF) ||
            r == 0xD7 || r == 0xF7)
            return false;
    }
    return n >= 2 && n <= 24;
}

}  // namespace nt::hudtext
