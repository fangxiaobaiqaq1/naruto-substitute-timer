// nt/result.h — engine.Result / engine.BeadInfo（architect 所有）。
// 字段与 Go internal/engine/engine.go 一一对应，名字保持 Go 拼写。
#pragma once

#include <string>
#include <vector>

namespace nt::engine {

// Side 表示左侧/右侧。
enum class Side { Left = 0, Right = 1 };

inline const char* SideString(Side s) { return s == Side::Right ? "right" : "left"; }

// BeadInfo 是一颗豆的判定结果（坐标已换算为截图像素，供 UI 标注）。
struct BeadInfo {
    int X = 0, Y = 0;      // 豆心截图像素坐标
    std::string Label;     // 如 "L1"、"R3"
    bool Lit = false;      // true = 亮蓝（可用）
    bool Gold = false;     // true = 金色可用豆；Lit 同时为 true
    bool Unknown = false;  // true = 这颗没看清，不能当暗豆
    double Conf = 0;       // 视觉证据得分 0~1，不是已校准的正确概率。
};

// Result 是一帧画面的完整分析结果。
struct Result {
    std::string TextStatus;  // OCR 已移除：恒为空，仅为保持结构一致。
    std::string TextError;
    bool Fighting = false;   // 明确在决斗场
    bool Uncertain = false;  // 当前证据不足，不能用缓存的豆数确认事件。
    std::vector<BeadInfo> Beads;  // 检测到的豆子（左 4 + 右 4），可空
    std::string Name;
    std::string LayoutProfile;  // Actual coordinate profile used by the inner engine.
    std::string Scene;          // 门闩给出的场景 id，可空
    double GateScore = 0;       // 门闩最高分，可空
    bool RoundOpening = false;  // Current frame carries the verified round-opening timer/HUD marker.
    std::string LeftNinja;
    std::string RightNinja;
    std::string LeftNinjaCandidate;  // Display-only table match, never special-rule evidence.（OCR 移除后恒空）
    std::string RightNinjaCandidate;
    int LeftSlots = 0;
    int RightSlots = 0;
    std::string PlayerSide;  // 从 VS 底栏 / 对局顶部账号认出的我方 left/right，无新证据时为空
    std::string PlayerName;  // 我方账号，来自 ui.playerNames
    std::string OppName;     // 对面账号，能对上图鉴才有
};

}  // namespace nt::engine
