// nt/utf8.h — Go 字符串按 rune 处理的最小复刻（architect 所有，header-only）。
//
// Go 的 string 是字节串：len(s) / s[i] 按字节；`for _, r := range s` 按 UTF-8 解码 rune，
// 非法字节解成 U+FFFD（宽度 1）。ninja.normalize / ShortLabel、identity.NormalizeName、
// strings.TrimSpace 等都依赖 rune 语义，统一用这里的函数。
#pragma once

#include <cstddef>
#include <cstdint>
#include <string>
#include <vector>

namespace nt::utf8 {

constexpr char32_t RuneError = 0xFFFD;

// utf8.DecodeRuneInString：返回 rune，并把字节宽度写入 size（空串 size=0）。
inline char32_t DecodeRune(const std::string& s, size_t i, size_t& size) {
    size_t n = s.size();
    if (i >= n) { size = 0; return RuneError; }
    auto b = [&](size_t k) { return static_cast<uint8_t>(s[k]); };
    uint8_t c0 = b(i);
    if (c0 < 0x80) { size = 1; return c0; }
    auto cont = [&](size_t k) { return k < n && (b(k) & 0xC0) == 0x80; };
    if (c0 >= 0xC2 && c0 <= 0xDF) {
        if (cont(i + 1)) { size = 2; return (char32_t(c0 & 0x1F) << 6) | (b(i + 1) & 0x3F); }
    } else if (c0 >= 0xE0 && c0 <= 0xEF) {
        if (cont(i + 1) && cont(i + 2)) {
            uint8_t c1 = b(i + 1);
            bool ok = !(c0 == 0xE0 && c1 < 0xA0) && !(c0 == 0xED && c1 > 0x9F);  // 过长编码 / 代理区
            if (ok) {
                size = 3;
                return (char32_t(c0 & 0x0F) << 12) | (char32_t(c1 & 0x3F) << 6) | (b(i + 2) & 0x3F);
            }
        }
    } else if (c0 >= 0xF0 && c0 <= 0xF4) {
        if (cont(i + 1) && cont(i + 2) && cont(i + 3)) {
            uint8_t c1 = b(i + 1);
            bool ok = !(c0 == 0xF0 && c1 < 0x90) && !(c0 == 0xF4 && c1 > 0x8F);
            if (ok) {
                size = 4;
                return (char32_t(c0 & 0x07) << 18) | (char32_t(c1 & 0x3F) << 12) | (char32_t(b(i + 2) & 0x3F) << 6) |
                       (b(i + 3) & 0x3F);
            }
        }
    }
    size = 1;
    return RuneError;
}

// utf8.EncodeRune / strings.Builder.WriteRune（非法 rune 写 U+FFFD）。
inline void AppendRune(std::string& out, char32_t r) {
    if (r > 0x10FFFF || (r >= 0xD800 && r <= 0xDFFF)) r = RuneError;
    if (r < 0x80) {
        out.push_back(static_cast<char>(r));
    } else if (r < 0x800) {
        out.push_back(static_cast<char>(0xC0 | (r >> 6)));
        out.push_back(static_cast<char>(0x80 | (r & 0x3F)));
    } else if (r < 0x10000) {
        out.push_back(static_cast<char>(0xE0 | (r >> 12)));
        out.push_back(static_cast<char>(0x80 | ((r >> 6) & 0x3F)));
        out.push_back(static_cast<char>(0x80 | (r & 0x3F)));
    } else {
        out.push_back(static_cast<char>(0xF0 | (r >> 18)));
        out.push_back(static_cast<char>(0x80 | ((r >> 12) & 0x3F)));
        out.push_back(static_cast<char>(0x80 | ((r >> 6) & 0x3F)));
        out.push_back(static_cast<char>(0x80 | (r & 0x3F)));
    }
}

// []rune(s)
inline std::vector<char32_t> Runes(const std::string& s) {
    std::vector<char32_t> out;
    for (size_t i = 0, w = 0; i < s.size(); i += w) out.push_back(DecodeRune(s, i, w));
    return out;
}

// string(runes)
inline std::string FromRunes(const std::vector<char32_t>& rs, size_t begin = 0, size_t end = SIZE_MAX) {
    std::string out;
    if (end > rs.size()) end = rs.size();
    for (size_t i = begin; i < end; ++i) AppendRune(out, rs[i]);
    return out;
}

// utf8.RuneCountInString
inline size_t RuneCount(const std::string& s) {
    size_t n = 0;
    for (size_t i = 0, w = 0; i < s.size(); i += w, ++n) DecodeRune(s, i, w);
    return n;
}

// unicode.IsSpace（Latin-1 快速表 + White_Space 属性）。
inline bool IsSpace(char32_t r) {
    if (r <= 0xFF) {
        switch (r) {
            case '\t': case '\n': case '\v': case '\f': case '\r': case ' ': case 0x85: case 0xA0:
                return true;
        }
        return false;
    }
    return r == 0x1680 || (r >= 0x2000 && r <= 0x200A) || r == 0x2028 || r == 0x2029 || r == 0x202F ||
           r == 0x205F || r == 0x3000;
}

// strings.TrimSpace
inline std::string TrimSpace(const std::string& s) {
    std::vector<char32_t> rs;
    std::vector<size_t> offs;
    for (size_t i = 0, w = 0; i < s.size(); i += w) {
        offs.push_back(i);
        rs.push_back(DecodeRune(s, i, w));
    }
    offs.push_back(s.size());
    size_t a = 0, b = rs.size();
    while (a < b && IsSpace(rs[a])) ++a;
    while (b > a && IsSpace(rs[b - 1])) --b;
    return s.substr(offs[a], offs[b] - offs[a]);
}

}  // namespace nt::utf8
