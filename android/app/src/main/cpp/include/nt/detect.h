// nt/detect.h — internal/detect（owner: cpp-match-detect；实现 src/detect/{beads,chrome,content,screen}.cpp）
//
// Package detect 负责把游戏逻辑坐标映射到窗口实际坐标，并定义豆子位置。
#pragma once

#include <array>
#include <string>
#include <utility>
#include <vector>

#include "nt/image.h"

namespace nt::detect {

// 游戏画面逻辑分辨率（火影忍者手游决斗场 UI 以 1920×1080 为基准布局）。
constexpr int LogicWidth = 1920;
constexpr int LogicHeight = 1080;

// ContentMode 描述游戏画面在窗口客户区内的呈现方式。
enum class ContentMode {
    ModeAuto = 0,   // 自动判断：客户区宽高比接近 16:9 视为铺满（拉伸），否则按等比黑边。
    ModeStretch,    // 强制铺满客户区（无黑边，画面拉伸变形）。
    ModeLetterbox,  // 强制等比缩放（上下或左右黑边）。
};
constexpr ContentMode ModeAuto = ContentMode::ModeAuto;
constexpr ContentMode ModeStretch = ContentMode::ModeStretch;
constexpr ContentMode ModeLetterbox = ContentMode::ModeLetterbox;

// ParseMode 解析字符串为 ContentMode。返回 false 等价于 Go 的 err != nil（out 置 ModeAuto）。
bool ParseMode(const std::string& s, ContentMode& out, std::string* err = nullptr);

// ContentArea 是游戏画面在客户区内的实际区域（客户区坐标）。
struct ContentArea {
    int X = 0, Y = 0, W = 0, H = 0;

    // Map 把 1920×1080 逻辑坐标映射到内容区内的实际坐标（截断取整，与 Go int() 一致）。
    Point Map(double lx, double ly) const;
    bool operator==(const ContentArea& o) const { return X == o.X && Y == o.Y && W == o.W && H == o.H; }
    bool operator!=(const ContentArea& o) const { return !(*this == o); }
};

// ComputeContentArea 根据客户区尺寸与模式计算游戏画面实际区域。
ContentArea ComputeContentArea(int clientW, int clientH, ContentMode mode);

// ResolveContentArea maps calibrated coordinates into an actual screenshot.
// second == false when its geometry cannot be established.
std::pair<ContentArea, bool> ResolveContentArea(const RGBA* img, ContentMode mode, int referenceWidth,
                                                int referenceHeight, double aspectTolerance);
bool blackBar(const RGBA* img, const Rect& r);

// Bead 是一个替身豆。
struct Bead {
    std::string Side;  // "left" / "right"
    int Idx = 0;       // 0..n-1，创立柱间等最多 6
    double LX = 0;     // 逻辑 X（0~1920）
    double LY = 0;     // 逻辑 Y（0~1080）
};

// BeadState 是替身豆的视觉状态。
enum class BeadState {
    StateUnknown = 0,  // 未知/未检测。
    StateDark,         // 暗色 = 替身已释放，冷却中（倒计时起点）。
    StateLight,        // 亮色 = 替身可用。
    StateGone,         // 消失 = 无豆（未入场/已阵亡）。
};
constexpr BeadState StateUnknown = BeadState::StateUnknown;
constexpr BeadState StateDark = BeadState::StateDark;
constexpr BeadState StateLight = BeadState::StateLight;
constexpr BeadState StateGone = BeadState::StateGone;
std::string BeadStateString(BeadState s);

// DefaultBeads 返回训练营豆位（副本）。DuelBeads 返回真决斗场豆位（副本）。
std::vector<Bead> DefaultBeads();
std::vector<Bead> DuelBeads();

// BeadPosition 是豆子在屏幕上的实际位置（Go 内嵌 Bead → C++ 继承，字段访问同名）。
struct BeadPosition : Bead {
    int X = 0, Y = 0;
    BeadPosition() = default;
    BeadPosition(const Bead& b, int x, int y) : Bead(b), X(x), Y(y) {}
};

// Layout 计算所有豆子在实际客户区内的屏幕坐标。
std::vector<BeadPosition> Layout(int clientW, int clientH, ContentMode mode, const std::vector<Bead>& beads);

// ColorRange 是一个 RGB 区间。
struct ColorRange {
    std::array<int, 3> Min{};
    std::array<int, 3> Max{};
    // Contains 判断 RGB 是否落在区间内（含容差）。
    bool Contains(int r, int g, int b) const;
};

// 颜色三态阈值，取自参考项目标定（1920×1080 基准下实测）。
extern const ColorRange DarkRange;       // 暗青 = 冷却中
extern const ColorRange LightBlueRange;  // 亮蓝/暗青高光都算可用
extern const ColorRange GoldRange;       // 金豆（可用）
constexpr int Tolerance = 4;             // 每通道允许的额外容差

// Classify 把 RGB 归类为豆子状态。金豆和亮蓝都是可用；纯白是空帧，不能当亮豆。
BeadState Classify(int r, int g, int b);
// IsGold 判断是否金豆（可用），不是血条红橙。
bool IsGold(int r, int g, int b);
bool isNearWhite(int r, int g, int b);

// ApplyIncrementRule 按充能递增规则处理一侧豆子。合法前缀原样返回；非法整侧标未知。
std::vector<BeadState> ApplyIncrementRule(const std::vector<BeadState>& states);
// IsLegalPrefix 亮豆必须是从豆1 起的连续前缀。末尾空槽（4 豆角色的 5/6）忽略。
bool IsLegalPrefix(const std::vector<BeadState>& states);
// IsPossiblePrefix checks only contradictions in CURRENT known observations.
bool IsPossiblePrefix(const std::vector<BeadState>& states);
// CountLight 统计一侧豆子中亮（可用）的数量。
int CountLight(const std::vector<BeadState>& states);

// 检测区域取豆心 7×10 菱形（小于主体 11×16），只判状态，不包裹整颗豆。
constexpr int BeadW = 7;   // 检测菱形宽（客户区像素，1092 基准）
constexpr int BeadH = 10;  // 检测菱形高

// InBeadDiamond 判断点 (x,y) 是否落在以 (cx,cy) 为中心、宽 w 高 h 的菱形内。
inline bool InBeadDiamond(int cx, int cy, int w, int h, int x, int y) {
    int a = w / 2, b = h / 2;
    int dx = x - cx, dy = y - cy;
    if (dx < 0) dx = -dx;
    if (dy < 0) dy = -dy;
    return dx * b + dy * a <= a * b;
}

// ScreenState 是游戏窗口当前画面状态。
enum class ScreenState {
    ScreenUnknown = 0,  // 无法判断。
    ScreenFighting,     // 决斗场对局中（豆子行存在豆子特征）。
    ScreenBlank,        // 白屏/黑屏（加载、切后台、弹窗全屏遮罩）。
    ScreenOther,        // 其他界面（主菜单、结算、选人等）。
};
std::string ScreenStateString(ScreenState s);

// ClassifyScreen 判断截图属于哪种画面状态。
ScreenState ClassifyScreen(const RGBA* img, ContentMode mode);

// GuessTopChrome 估计模拟器标题栏/标签栏占用的顶部像素。
int GuessTopChrome(const RGBA* img);
// StripChrome 裁掉顶部模拟器栏，没有则原样返回（返回视图；裁剪时为新图）。
RGBA StripChrome(const RGBA* img);
bool isColorful(uint8_t r, uint8_t g, uint8_t b);

}  // namespace nt::detect
