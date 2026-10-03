package com.narutotimer.tracker

// Owner: kt-tracker
// Port of: internal/ninja/rules.go（UI/状态机需要的部分：冷却策略与短名）
//
// Package ninja describes visual HUD variants and the user-confirmed cooldown
// policy. Archived skill tables are not authoritative for live timer rules.

/**
 * The current policy is user-confirmed: all primary clocks are 15 seconds.
 * Only 照美冥[五代目水影] additionally displays a simultaneous 10-second estimate.
 */
object NinjaRules {
    const val NARUTO_STUDENT = "漩涡鸣人[忍者学员]"
    const val FIFTH_MIZUKAGE = "照美冥[五代目水影]"
    const val HASHIRAMA = "千手柱间[木叶创立]"
    const val MADARA = "宇智波斑[神驹佑将]"

    /**
     * MadaraLegacyAlias is retained for old local fixtures only. It is an exact
     * spelling alias, not a relaxed match for other Madara variants.
     */
    const val MADARA_LEGACY_ALIAS = "宇智波斑[神驹佑祥]"
    const val OBITO = "宇智波带土[十尾人柱力]"
    const val SASUKE_XIAYIN = "宇智波佐助[侠隐江湖]"
    const val NARUTO = "漩涡鸣人[暴怒·第六尾]"
    const val ITACHI_HYAKUSEN = "宇智波鼬[百战]"
    const val MINATO_KYUBI = "波风水门[九喇嘛连结]"
    const val HASHIRAMA_EDO = "千手柱间[秽土转生]"

    /** Go ninja.DefaultCooldown = 15 * time.Second。 */
    const val DEFAULT_COOLDOWN_NANOS = 15_000_000_000L

    /** Go ninja.AlternateCooldown = 10 * time.Second。 */
    const val ALTERNATE_COOLDOWN_NANOS = 10_000_000_000L

    /** strings.ContainsRune("[]【】()（）·・", r) 的字符集（全部在 BMP 内）。 */
    private const val IGNORED_RUNES = "[]【】()（）·・"

    private val normalizedFifthMizukage = normalize(FIFTH_MIZUKAGE)

    /** normalize(名字常量) → 短名；顺序与 Go switch 相同（键互不相同，顺序不影响结果）。 */
    private val shortLabels: Map<String, String> = linkedMapOf(
        normalize(FIFTH_MIZUKAGE) to "五代目水影",
        normalize(HASHIRAMA) to "柱间·木叶创立",
        normalize(MADARA) to "斑·神驹佑将",
        normalize(MADARA_LEGACY_ALIAS) to "斑·神驹佑将",
        normalize(OBITO) to "带土·十尾",
        normalize(SASUKE_XIAYIN) to "佐助·侠隐江湖",
        normalize(NARUTO_STUDENT) to "鸣人·忍者学员",
        normalize(NARUTO) to "鸣人·第六尾",
        normalize(ITACHI_HYAKUSEN) to "鼬·百战",
        normalize(MINATO_KYUBI) to "水门·九喇嘛连结",
        normalize(HASHIRAMA_EDO) to "柱间·秽土转生",
    )

    /**
     * DualCooldown matches the full version, never a bare name or an ambiguous
     * numeric ID from the old extracted table. Bracket typography is insignificant.
     */
    fun dualCooldown(name: String): Boolean = normalize(name) == normalizedFifthMizukage

    /** 去掉空白（unicode.IsSpace）与 "[]【】()（）·・"（按 code point，Go strings.Map 返回 -1 即删除）。 */
    fun normalize(s: String): String {
        val b = StringBuilder(s.length)
        var i = 0
        while (i < s.length) {
            val cp = s.codePointAt(i)
            i += Character.charCount(cp)
            if (GoText.isSpace(cp) || (cp <= 0xFFFF && IGNORED_RUNES.indexOf(cp.toChar()) >= 0)) continue
            b.appendCodePoint(cp)
        }
        return b.toString()
    }

    /** 特殊变体短名；其它超过 10 个字符截成 9 个 + "…"（按 code point）。 */
    fun shortLabel(name: String): String {
        shortLabels[normalize(name)]?.let { return it }
        val runes = name.codePointCount(0, name.length)
        if (runes > 10) {
            return name.substring(0, name.offsetByCodePoints(0, 9)) + "…"
        }
        return name
    }
}

/** Go 字符串处理里与 Unicode 相关的小工具（unicode.IsSpace / strings.TrimSpace 语义）。 */
internal object GoText {
    /**
     * Go unicode.IsSpace：Latin-1 内为 '\t' '\n' '\v' '\f' '\r' ' ' U+0085 U+00A0；
     * 其余为 White_Space 属性（U+1680、U+2000–U+200A、U+2028、U+2029、U+202F、U+205F、U+3000）。
     * 注意它与 Java Character.isWhitespace 不同（后者不含 NBSP、含 U+001C–U+001F）。
     */
    fun isSpace(cp: Int): Boolean {
        if (cp <= 0xFF) {
            return when (cp) {
                '\t'.code, '\n'.code, 0x0B, 0x0C, '\r'.code, ' '.code, 0x85, 0xA0 -> true
                else -> false
            }
        }
        return when (cp) {
            0x1680, 0x2028, 0x2029, 0x202F, 0x205F, 0x3000 -> true
            in 0x2000..0x200A -> true
            else -> false
        }
    }

    /** Go strings.TrimSpace。 */
    fun trimSpace(s: String): String {
        var start = 0
        var end = s.length
        while (start < end) {
            val cp = s.codePointAt(start)
            if (!isSpace(cp)) break
            start += Character.charCount(cp)
        }
        while (end > start) {
            val cp = s.codePointBefore(end)
            if (!isSpace(cp)) break
            end -= Character.charCount(cp)
        }
        return s.substring(start, end)
    }

    /**
     * Go identity.NormalizeName：TrimSpace 后去掉所有空白以及 '/' '\\' ':'。
     * （识别核心里同名函数用于比对账号名；状态机用它判断 PlayerName 是否是"我方"。）
     */
    fun normalizeName(s: String): String {
        val t = trimSpace(s)
        val b = StringBuilder(t.length)
        var i = 0
        while (i < t.length) {
            val cp = t.codePointAt(i)
            i += Character.charCount(cp)
            if (isSpace(cp) || cp == '/'.code || cp == '\\'.code || cp == ':'.code) continue
            b.appendCodePoint(cp)
        }
        return b.toString()
    }
}
