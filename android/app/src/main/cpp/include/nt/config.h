// nt/config.h — internal/config 的默认值部分（architect 所有，实现在 src/config/config.cpp）。
// 只保留识别核心用得到的字段；桌面/采集/更新/调试相关配置已移除。
// Android 端没有 config.json：nt::config::Default() 就是唯一配置，
// 只有 UI.PlayerNames（nativeSetPlayerNames）与 Layout.Calibration（nativeSetCalibration）
// 由 Kotlin 设置页通过 JNI 覆盖。
#pragma once

#include <map>
#include <optional>
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

// RegionCalibration is drawn by the player on one captured frame (see
// docs/区域校准.md). Rects are fractions of the whole frame; each one is
// independent and nullopt (Go nil) means automatic for that region and side.
struct RegionCalibration {
    // Profile is the layout (camp/duel) of the frame the boxes were drawn on.
    std::string Profile;
    std::optional<NormalizedRect> LeftBeads, RightBeads, LeftAvatar, RightAvatar;
};

// Size limits of a calibration box (docs/区域校准.md 框的大小限制; port of
// internal/config/calibration_limits.go, same arithmetic as Go and Kotlin
// CalibrationLimits, so every layer accepts the same boxes). u, v = the box's
// width and height in automatic-box units at HUD scale 1 (bead box: 16w, 36h;
// portrait box: 960w/68.5, 540h/68.5), k = max(u, v) = how many times the
// automatic box it is, on any frame. Usable: 1/2 <= k <= 2 and
// min(u, v) >= k/4. Implemented in src/config/calibration_limits.cpp.
constexpr double CalibrationMaxScale = 2.0;
constexpr double CalibrationMinScale = 0.5;
constexpr double CalibrationMinShape = 0.25;

enum class CalibrationIssue { None, TooLarge, TooSmall, BadShape };

// CalibrationBoxUnits is the box's (u, v) (avatar: a portrait box, else a bead box).
void CalibrationBoxUnits(bool avatar, const NormalizedRect& r, double& u, double& v);
// CalibrationRectIssue is None for a usable box; r must already lie inside the frame.
CalibrationIssue CalibrationRectIssue(bool avatar, const NormalizedRect& r);

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
    // Calibration holds the player's own boxes; nullopt keeps the automatic layout.
    std::optional<RegionCalibration> Calibration;

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
