// Owner: cpp-match-detect
// Port of: internal/detect/beads.go
// Contract: include/nt/detect.h — see android/ARCHITECTURE.md.
//
// Package detect 负责把游戏逻辑坐标映射到窗口实际坐标，并定义豆子位置。
#include <cstdio>

#include "nt/detect.h"

namespace nt::detect {

namespace {
// Go fmt %q 的简化版（仅用于错误文本）：转义引号、反斜杠和控制字符，UTF-8 原样保留。
std::string quote(const std::string& s) {
    std::string out = "\"";
    for (unsigned char c : s) {
        switch (c) {
            case '"': out += "\\\""; break;
            case '\\': out += "\\\\"; break;
            case '\n': out += "\\n"; break;
            case '\t': out += "\\t"; break;
            case '\r': out += "\\r"; break;
            default:
                if (c < 0x20 || c == 0x7f) {
                    char buf[8];
                    std::snprintf(buf, sizeof buf, "\\x%02x", c);
                    out += buf;
                } else {
                    out += static_cast<char>(c);
                }
        }
    }
    out += '"';
    return out;
}
}  // namespace

// ParseMode 解析字符串为 ContentMode。
bool ParseMode(const std::string& s, ContentMode& out, std::string* err) {
    if (s.empty() || s == "auto") {
        out = ModeAuto;
        return true;
    }
    if (s == "stretch" || s == "拉伸") {
        out = ModeStretch;
        return true;
    }
    if (s == "letterbox" || s == "等比" || s == "黑边") {
        out = ModeLetterbox;
        return true;
    }
    out = ModeAuto;
    if (err != nullptr) {
        *err = "未知内容模式 " + quote(s) + "（可用: auto/stretch/letterbox）";
    }
    return false;
}

// ComputeContentArea 根据客户区尺寸与模式计算游戏画面实际区域。
ContentArea ComputeContentArea(int clientW, int clientH, ContentMode mode) {
    ContentArea ca{0, 0, clientW, clientH};
    if (clientW <= 0 || clientH <= 0) {
        return ca;
    }

    bool letterbox = false;
    switch (mode) {
        case ModeStretch:
            letterbox = false;
            break;
        case ModeLetterbox:
            letterbox = true;
            break;
        default: {  // ModeAuto
            // 客户区宽高比与 16:9 偏差小于 1.5% 视为铺满，否则视为有黑边。
            // Go 的 16.0/9.0*0.985 是无类型常量，按精确有理数求值后只舍入一次：
            // 16/9*0.985 = 394/225，16/9*1.015 = 406/225（整数相除只舍入一次，逐位一致）。
            const double ratio = static_cast<double>(clientW) / static_cast<double>(clientH);
            if (ratio < 394.0 / 225.0 || ratio > 406.0 / 225.0) {
                letterbox = true;
            }
        }
    }
    if (!letterbox) {
        return ca;
    }

    // 等比缩放：以宽或高为准取较小缩放比。
    const double scaleW = static_cast<double>(clientW) / LogicWidth;
    const double scaleH = static_cast<double>(clientH) / LogicHeight;
    double scale = scaleW;
    if (scaleH < scaleW) {
        scale = scaleH;
    }
    const int w = static_cast<int>(static_cast<double>(LogicWidth) * scale);
    const int h = static_cast<int>(static_cast<double>(LogicHeight) * scale);
    ca.W = w;
    ca.H = h;
    ca.X = (clientW - w) / 2;
    ca.Y = (clientH - h) / 2;
    return ca;
}

// Map 把 1920×1080 逻辑坐标映射到内容区内的实际坐标。
Point ContentArea::Map(double lx, double ly) const {
    const int x = X + static_cast<int>(static_cast<double>(W) * lx / LogicWidth);
    const int y = Y + static_cast<int>(static_cast<double>(H) * ly / LogicHeight);
    return Pt(x, y);
}

std::string BeadStateString(BeadState s) {
    switch (s) {
        case StateDark: return "暗(冷却中)";
        case StateLight: return "亮(可用)";
        case StateGone: return "消失";
        default: return "未知";
    }
}

namespace {
// defaultBeadLayout 是训练场四槽豆子的逻辑坐标（1920×1080 基准）。
//
// UI 层级（从上到下）：角色名字 → 血条 → 替身豆。
// 血条是连续的橙/红色横条（y≈88~94 客户区，勿与豆子混淆）；
// 豆子位于血条正下方一行（客户区 y≈100~118，内容区 y≈80~98）。
//
// 充能规则：按 1→n 递增，前面亮了后面才可能亮。
// 六槽必须通过显式配置标定，不把血条/木纹当作默认第五、第六槽。
// 右侧镜像：豆1 在最右，数组按右→左。间隔 30px。
//
// 标定：4 豆来自实机（188/218/248/278 @ y=155）。
const std::vector<Bead>& defaultBeadLayout() {
    static const std::vector<Bead> v = {
        {"left", 0, 188, 155},
        {"left", 1, 218, 155},
        {"left", 2, 248, 155},
        {"left", 3, 278, 155},

        {"right", 0, 1674, 155},
        {"right", 1, 1644, 155},
        {"right", 2, 1614, 155},
        {"right", 3, 1584, 155},
    };
    return v;
}

// duelBeadLayout 真决斗场 HUD 比训练营整排偏右约 21 逻辑像素。
// 985×594 实机：训练营左豆 x≈96/111/127/142，决斗场 x≈107/122/137/152。
const std::vector<Bead>& duelBeadLayout() {
    static const std::vector<Bead> v = {
        {"left", 0, 209, 174},
        {"left", 1, 239, 174},
        {"left", 2, 269, 174},
        {"left", 3, 299, 174},

        {"right", 0, 1688, 174},
        {"right", 1, 1658, 174},
        {"right", 2, 1628, 174},
        {"right", 3, 1604, 174},
    };
    return v;
}
}  // namespace

// DefaultBeads 返回训练营豆位（副本）。
std::vector<Bead> DefaultBeads() { return defaultBeadLayout(); }

// DuelBeads 返回真决斗场豆位（副本）。
std::vector<Bead> DuelBeads() { return duelBeadLayout(); }

// Layout 计算所有豆子在实际客户区内的屏幕坐标。
std::vector<BeadPosition> Layout(int clientW, int clientH, ContentMode mode, const std::vector<Bead>& beads) {
    const ContentArea ca = ComputeContentArea(clientW, clientH, mode);
    std::vector<BeadPosition> pos;
    pos.reserve(beads.size());
    for (const Bead& b : beads) {
        const Point p = ca.Map(b.LX, b.LY);
        pos.emplace_back(b, p.X, p.Y);
    }
    return pos;
}

// 颜色三态阈值，取自参考项目标定（1920×1080 基准下实测）。
// DarkRange 暗青 = 冷却中。描边/阴影会稍亮一点。
const ColorRange DarkRange = {{8, 3, 1}, {70, 90, 130}};
// LightBlueRange 亮蓝/暗青高光都算可用。决斗场菱形中间有暗带，整体比训练营暗。
const ColorRange LightBlueRange = {{0, 90, 115}, {255, 255, 255}};
// GoldRange 金豆（可用）。血条/爆炸是红橙，绿没这么高。
const ColorRange GoldRange = {{190, 150, 0}, {255, 255, 90}};

// Contains 判断 RGB 是否落在区间内（含容差）。
bool ColorRange::Contains(int r, int g, int b) const {
    return r >= Min[0] - Tolerance && r <= Max[0] + Tolerance && g >= Min[1] - Tolerance && g <= Max[1] + Tolerance &&
           b >= Min[2] - Tolerance && b <= Max[2] + Tolerance;
}

// Classify 把 RGB 归类为豆子状态。
// 金豆和亮蓝都是可用；纯白是空帧，不能当亮豆。
BeadState Classify(int r, int g, int b) {
    if (isNearWhite(r, g, b)) {
        return StateUnknown;
    }
    if (IsGold(r, g, b)) {
        return StateLight;
    }
    if (LightBlueRange.Contains(r, g, b)) {
        return StateLight;
    }
    if (DarkRange.Contains(r, g, b)) {
        return StateDark;
    }
    return StateUnknown;
}

// IsGold 判断是否金豆（可用），不是血条红橙。
bool IsGold(int r, int g, int b) {
    if (!GoldRange.Contains(r, g, b)) {
        return false;
    }
    // 金豆偏黄：G 接近 R。爆炸/血条偏红：R 远大于 G。
    return g - b >= 90 && r - g <= 70 && b <= 90;
}

bool isNearWhite(int r, int g, int b) { return r >= 245 && g >= 245 && b >= 245; }

// ApplyIncrementRule 按充能递增规则处理一侧豆子。
// 合法前缀原样返回；非法整侧标未知，绝不改写成“亮了 N 颗”。
std::vector<BeadState> ApplyIncrementRule(const std::vector<BeadState>& states) {
    std::vector<BeadState> out(states);
    if (IsLegalPrefix(out)) {
        return out;
    }
    for (auto& s : out) {
        s = StateUnknown;
    }
    return out;
}

// IsLegalPrefix 亮豆必须是从豆1 起的连续前缀。末尾空槽（4 豆角色的 5/6）忽略。
bool IsLegalPrefix(const std::vector<BeadState>& states) {
    size_t n = states.size();
    while (n > 0 && (states[n - 1] == StateUnknown || states[n - 1] == StateGone)) {
        n--;
    }
    if (n == 0) {
        return false;
    }
    bool seenDark = false;
    for (size_t i = 0; i < n; ++i) {
        const BeadState s = states[i];
        if (s == StateUnknown || s == StateGone) {
            return false;
        }
        if (s == StateLight) {
            if (seenDark) {
                return false;
            }
            continue;
        }
        seenDark = true;
    }
    return true;
}

// IsPossiblePrefix checks only contradictions in CURRENT known observations.
// An obscured slot cannot erase its readable neighbors or supply their state.
// This is not a complete count: callers still require every slot before voting
// a bean event. A known dark followed by a known light remains impossible.
bool IsPossiblePrefix(const std::vector<BeadState>& states) {
    bool seenDark = false;
    for (BeadState s : states) {
        switch (s) {
            case StateDark:
                seenDark = true;
                break;
            case StateLight:
                if (seenDark) {
                    return false;
                }
                break;
            default:
                break;
        }
    }
    return true;
}

// CountLight 统计一侧豆子中亮（可用）的数量。
int CountLight(const std::vector<BeadState>& states) {
    int n = 0;
    for (BeadState s : states) {
        if (s == StateLight) {
            n++;
        }
    }
    return n;
}

// 豆子原生形状（像素级实测，客户区 1092×654 决斗场画面）：
//   - 替身豆是竖向菱形：顶部尖点 → 中部最宽 → 底部尖点
//   - 亮蓝主体实测 11px 宽 × 16px 高；暗色描边外还有约 1px 发光
//   - 用户参考图（豆子特写）：菱形中部有一条横向暗青装饰带，勿当作两颗粒子
// BeadW/BeadH 与 InBeadDiamond（菱形方程 |dx|/(w/2) + |dy|/(h/2) <= 1，全整数运算）见 nt/detect.h。

}  // namespace nt::detect
