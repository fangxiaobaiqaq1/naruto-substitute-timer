// internal/config 默认值（architect 所有）。数值与 Go config.Default() 逐个对应。
#include "nt/config.h"

namespace nt::config {

LayoutProfile LayoutConfig::Profile(const std::string& name) const {
    auto it = Profiles.find(name);
    if (it != Profiles.end()) return it->second;
    if (name == "duel") return DefaultDuelProfile();
    return LayoutProfile{Left, Right};
}

LayoutProfile DefaultDuelProfile() {
    auto points = [](std::initializer_list<double> xs) {
        std::vector<NormalizedPoint> out;
        out.reserve(xs.size());
        for (double x : xs) out.push_back(NormalizedPoint{x / 1600, 103.0 / 900});
        return out;
    };
    LayoutProfile p;
    p.Left = SideLayout{NormalizedRect{0.04, 0.07, 0.18, 0.09}, points({172, 198, 223, 249})};
    p.Right = SideLayout{NormalizedRect{0.78, 0.07, 0.18, 0.09}, points({1423, 1398, 1373, 1348})};
    return p;
}

Config Default() {
    Config c;
    c.SchemaVersion = SchemaVersion;

    LayoutConfig& l = c.Layout;
    l.PreferredProfile = "auto";
    l.ReferenceWidth = 1920;
    l.ReferenceHeight = 1080;
    l.ContentMode = "auto";
    l.AutoAspectTolerance = 0.015;
    l.BeadsPerSide = 4;
    l.Left = SideLayout{NormalizedRect{0.04, 0.05, 0.18, 0.12},
                        {{155.0 / 1600, 101.0 / 900}, {180.0 / 1600, 101.0 / 900}, {205.0 / 1600, 101.0 / 900},
                         {230.0 / 1600, 101.0 / 900}}};
    l.Right = SideLayout{NormalizedRect{0.78, 0.05, 0.18, 0.12},
                         {{1391.0 / 1600, 101.0 / 900}, {1366.0 / 1600, 101.0 / 900}, {1340.0 / 1600, 101.0 / 900},
                          {1315.0 / 1600, 101.0 / 900}}};
    l.SearchRadiusX = 0.012;
    l.SearchRadiusY = 0.018;
    l.SpacingTolerance = 0.22;
    l.MirrorTolerance = 0.025;
    l.RelocalizeAfterLowConfidenceFrames = 3;

    VisionConfig& v = c.Vision;
    // 空豆暗青实测 V≈0.35~0.40；亮豆 V≈0.85+。亮蓝下限必须高于空豆，否则第 4 颗会被算亮。
    v.Dark = {HSVRange{0.45, 0.80, 0.20, 1, 0.08, 0.52}};
    v.Light = {HSVRange{0.42, 0.68, 0.18, 1, 0.58, 1}};
    v.Gold = {HSVRange{0.00, 0.20, 0.25, 1, 0.45, 1}, HSVRange{0.90, 1.00, 0.25, 1, 0.45, 1}};
    v.SampleWidthReferencePX = 13;
    v.SampleHeightReferencePX = 16;
    v.CoreScale = 0.55;
    v.ColorWeight = 0.55;
    v.ShapeWeight = 0.20;
    v.PositionWeight = 0.15;
    v.SymmetryWeight = 0.10;
    v.UnknownBelow = 0.35;
    v.MinimumMargin = 0.08;
    v.ScreenFightThreshold = 0.75;

    TrackingConfig& t = c.Tracking;
    t.PollIntervalMS = 16;
    t.WindowFrames = 7;
    t.MinimumConfirmFrames = 2;
    t.EnterFightFrames = 1;
    t.LeaveFightFrames = 10;
    t.StateChangeMargin = 0.16;
    t.UnknownHoldMS = 1200;
    t.StaleAfterMS = 3000;
    t.ResetAfterMS = 8000;

    SceneConfig& s = c.Scene;
    s.Enabled = true;
    s.Manifest = "assets/templates/manifest.json";
    s.FightScenes = {"fight"};
    s.EndScenes = {"lobby", "result"};
    s.HoldScenes = {"vs", "queue", "pick", "ban"};
    s.MinFightScore = 0.62;
    s.MinOtherScore = 0.80;
    s.Margin = 0.05;
    s.UncertainBelow = 0.55;

    UIConfig& u = c.UI;
    u.PollIntervalMS = 16;
    u.IdlePollIntervalMS = 400;
    u.PlayerSide = "auto";
    u.PlayerNames = {};
    return c;
}

}  // namespace nt::config
