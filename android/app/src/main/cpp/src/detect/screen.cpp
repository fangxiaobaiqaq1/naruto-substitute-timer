// Owner: cpp-match-detect
// Port of: internal/detect/screen.go
// Contract: include/nt/detect.h — see android/ARCHITECTURE.md.
#include <algorithm>

#include "nt/detect.h"

namespace nt::detect {

// String 返回状态的中文名。
std::string ScreenStateString(ScreenState s) {
    switch (s) {
        case ScreenState::ScreenFighting: return "决斗场对局中";
        case ScreenState::ScreenBlank: return "白屏/黑屏（非对局）";
        case ScreenState::ScreenOther: return "其他界面（非对局）";
        default: return "未知";
    }
}

namespace {
// 亮度阈值。
constexpr double blankDarkTh = 25.0;   // 平均亮度低于此视为黑屏
constexpr double blankBrightT = 235.0; // 平均亮度高于此视为白屏
}  // namespace

// ClassifyScreen 判断截图属于哪种画面状态。
// 判据：
//  1. 整体平均亮度过暗/过亮 → 白屏黑屏；
//  2. 豆子行区域（内容区 y≈80~98）内三态颜色（暗青/亮蓝/赤金）像素占比
//     达到豆子特征密度 → 决斗场对局；
//  3. 其余 → 其他界面。
//
// img 为窗口客户区截图（与 ComputeContentArea 同一坐标系）。
// （Go 对 nil 指针会 panic；这里 nullptr 视为无法判断。）
ScreenState ClassifyScreen(const RGBA* img, ContentMode mode) {
    if (img == nullptr) {
        return ScreenState::ScreenUnknown;
    }
    const Rect bounds = img->Bounds();
    if (bounds.Dx() <= 0 || bounds.Dy() <= 0) {
        return ScreenState::ScreenUnknown;
    }

    // 整体亮度采样（隔行隔列，快）
    double sum = 0, n = 0;
    for (int y = bounds.Min.Y; y < bounds.Max.Y; y += 8) {
        for (int x = bounds.Min.X; x < bounds.Max.X; x += 8) {
            const Color c = img->RGBAAt(x, y);
            sum += 0.299 * static_cast<double>(c.R) + 0.587 * static_cast<double>(c.G) + 0.114 * static_cast<double>(c.B);
            n++;
        }
    }
    const double avg = sum / n;
    if (avg < blankDarkTh || avg > blankBrightT) {
        return ScreenState::ScreenBlank;
    }

    // Use the same pixel-backed content transform as the bead/name engines.
    // ComputeContentArea assumes the whole capture is game content and is
    // therefore wrong for black bars, window scaling and non-zero origins.
    const auto [ca, supported] = ResolveContentArea(img, mode, LogicWidth, LogicHeight, 0.015);
    if (!supported) {
        return ScreenState::ScreenUnknown;
    }
    // 豆子行：略放宽，使用与 HUD 豆位相同的 fixed-height + edge-anchor
    // 映射，超宽屏不会把扫描带横向拉伸到中央场景。
    const Point left0 = ca.MapHUDPixel(80, 70, false, LogicWidth, LogicHeight);
    const Point left1 = ca.MapHUDPixel(340, 130, false, LogicWidth, LogicHeight);
    const Point right0 = ca.MapHUDPixel(1560, 70, true, LogicWidth, LogicHeight);
    const Point right1 = ca.MapHUDPixel(1800, 130, true, LogicWidth, LogicHeight);
    const int y0 = std::max(std::min(left0.Y, left1.Y), bounds.Min.Y);
    const int y1 = std::min(std::max(left0.Y, left1.Y), bounds.Max.Y);
    const int xL0 = std::max(std::min(left0.X, left1.X), bounds.Min.X);
    const int xL1 = std::min(std::max(left0.X, left1.X), bounds.Max.X);
    const int xR0 = std::max(std::min(right0.X, right1.X), bounds.Min.X);
    const int xR1 = std::min(std::max(right0.X, right1.X), bounds.Max.X);

    const int regs[2][2] = {{xL0, xL1}, {xR0, xR1}};
    int beadPix = 0, total = 0;
    for (int y = y0; y < y1; ++y) {
        for (const auto& reg : regs) {
            for (int x = reg[0]; x < reg[1]; ++x) {
                const Color c = img->RGBAAt(x, y);
                if (Classify(int(c.R), int(c.G), int(c.B)) != StateUnknown) {
                    beadPix++;
                }
                total++;
            }
        }
    }
    if (total > 0 && static_cast<double>(beadPix) / static_cast<double>(total) > 0.08) {
        return ScreenState::ScreenFighting;
    }
    return ScreenState::ScreenOther;
}

}  // namespace nt::detect
