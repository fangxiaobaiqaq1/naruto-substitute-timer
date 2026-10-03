// Owner: cpp-ninja
// Port of: internal/ninja/rules.go
// Contract: include/nt/ninja.h — see android/ARCHITECTURE.md.
//
// Package ninja describes visual HUD variants and the user-confirmed cooldown
// policy. Archived skill tables are not authoritative for live timer rules.
#include <string>
#include <vector>

#include "nt/ninja.h"
#include "nt/utf8.h"

namespace nt::ninja {

namespace {

// strings.ContainsRune("[]【】()（）·・", r)
bool ignoredRune(char32_t r) {
    switch (r) {
        case U'[': case U']': case U'【': case U'】': case U'(': case U')':
        case U'（': case U'）': case U'·': case U'・':
            return true;
    }
    return false;
}

}  // namespace

// DualCooldown matches the full version, never a bare name or an ambiguous
// numeric ID from the old extracted table. Bracket typography is insignificant.
bool DualCooldown(const std::string& name) { return normalize(name) == normalize(FifthMizukage); }

// Go: strings.Map —— 空白与括号/间隔号被丢弃；非法 UTF-8 字节按 Go 写成 U+FFFD。
std::string normalize(const std::string& s) {
    std::string out;
    out.reserve(s.size());
    for (size_t i = 0, w = 0; i < s.size(); i += w) {
        char32_t r = utf8::DecodeRune(s, i, w);
        if (utf8::IsSpace(r) || ignoredRune(r)) continue;
        utf8::AppendRune(out, r);
    }
    return out;
}

std::string ShortLabel(const std::string& name) {
    // Go switch：按 case 顺序比较 normalize 后的名字。
    static const std::vector<std::pair<std::string, std::string>> labels = {
        {normalize(FifthMizukage), "五代目水影"},
        {normalize(Hashirama), "柱间·木叶创立"},
        {normalize(Madara), "斑·神驹佑将"},
        {normalize(MadaraLegacyAlias), "斑·神驹佑将"},
        {normalize(Obito), "带土·十尾"},
        {normalize(SasukeXiayin), "佐助·侠隐江湖"},
        {normalize(NarutoStudent), "鸣人·忍者学员"},
        {normalize(Naruto), "鸣人·第六尾"},
        {normalize(ItachiHyakusen), "鼬·百战"},
        {normalize(MinatoKyubi), "水门·九喇嘛连结"},
        {normalize(HashiramaEdo), "柱间·秽土转生"},
    };
    const std::string key = normalize(name);
    for (const auto& [norm, label] : labels) {
        if (key == norm) return label;
    }
    std::vector<char32_t> text = utf8::Runes(name);
    if (text.size() > 10) return utf8::FromRunes(text, 0, 9) + "…";
    return name;
}

const char* PaletteString(Palette p) {
    switch (p) {
        case Palette::Warm: return "warm";
        case Palette::Purple: return "purple";
        case Palette::Red: return "red";
        case Palette::Xiayin: return "xiayin";
        case Palette::None: break;
    }
    return "";
}

}  // namespace nt::ninja
