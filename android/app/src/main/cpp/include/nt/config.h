// nt/config.h — internal/config 的默认值部分（architect 所有，实现在 src/config/config.cpp）。
// 只保留识别核心用得到的字段；桌面/采集/更新/调试相关配置已移除。
// Android 端没有 config.json：nt::config::Default() 就是唯一配置，
// 只有 UI.PlayerNames 由 Kotlin 设置页通过 JNI（nativeSetPlayerNames）覆盖。
#pragma once

#include <map>
#include <string>
#include <vector>

namespace nt::config {

constexpr int SchemaVersion = 1;

struct NormalizedRect {
    double X = 0, Y = 0, Width = 0, Height = 0;
};

struct NormalizedPoint {
    double X = 0, Y = 0;
};

struct SideLayout {
    NormalizedRect Search;
    std::vector<NormalizedPoint> NominalCenters;
};

// LayoutProfile keeps camp and duel calibration independent. Coordinates are
// normalized to the game content area, before window scaling or letterboxing.
struct LayoutProfile {
    SideLayout Left;
    SideLayout Right;
};

struct LayoutConfig {
    std::string PreferredProfile;
    std::map<std::string, LayoutProfile> Profiles;
    int ReferenceWidth = 0;
    int ReferenceHeight = 0;
    std::string ContentMode;
    double AutoAspectTolerance = 0;
    int BeadsPerSide = 0;
    SideLayout Left;
    SideLayout Right;
    double SearchRadiusX = 0;
    double SearchRadiusY = 0;
    double SpacingTolerance = 0;
    double MirrorTolerance = 0;
    int RelocalizeAfterLowConfidenceFrames = 0;

    // Profile resolves legacy left/right as camp when an explicit camp is absent.
    // The built-in duel profile has four verified slots; six require explicit data.
    LayoutProfile Profile(const std::string& name) const;
};

LayoutProfile DefaultDuelProfile();

struct HSVRange {
    double HMin = 0, HMax = 0, SMin = 0, SMax = 0, VMin = 0, VMax = 0;
};

struct VisionConfig {
    std::vector<HSVRange> Dark;
    std::vector<HSVRange> Light;
    std::vector<HSVRange> Gold;
    double SampleWidthReferencePX = 0;
    double SampleHeightReferencePX = 0;
    double CoreScale = 0;
    double ColorWeight = 0;
    double ShapeWeight = 0;
    double PositionWeight = 0;
    double SymmetryWeight = 0;
    double UnknownBelow = 0;
    double MinimumMargin = 0;
    double ScreenFightThreshold = 0;
};

// SceneConfig 是场景模板门闩的策略。模板清单在 manifest，这里只写表决规则。
struct SceneConfig {
    bool Enabled = false;
    std::string Manifest;  // Android 上恒为内置 "assets/templates/manifest.json"
    std::vector<std::string> FightScenes;
    std::vector<std::string> EndScenes;
    std::vector<std::string> HoldScenes;
    double MinFightScore = 0;
    double MinOtherScore = 0;
    double Margin = 0;
    double UncertainBelow = 0;
};

struct TrackingConfig {
    int PollIntervalMS = 0;
    int WindowFrames = 0;
    int MinimumConfirmFrames = 0;
    int EnterFightFrames = 0;
    int LeaveFightFrames = 0;
    double StateChangeMargin = 0;
    int UnknownHoldMS = 0;
    int StaleAfterMS = 0;
    int ResetAfterMS = 0;
};

// 只保留 native 需要的 UI 字段。其余（计时显示、悬浮窗）在 Kotlin Settings.kt。
struct UIConfig {
    int PollIntervalMS = 0;
    int IdlePollIntervalMS = 0;
    std::string PlayerSide;
    std::vector<std::string> PlayerNames;
};

struct Config {
    int SchemaVersion = 0;
    LayoutConfig Layout;
    VisionConfig Vision;
    SceneConfig Scene;
    TrackingConfig Tracking;
    UIConfig UI;
};

// 与 Go config.Default() 逐值一致（只含上面保留的字段）。
Config Default();

}  // namespace nt::config
