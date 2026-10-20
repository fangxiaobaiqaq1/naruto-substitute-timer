// Host-only regressions for internal/engine/gated.go + internal/scene/scene.go
// (+ the rgb energy-gauge row re-read).
// Run tests/hud_start_test.sh; this directory is outside the APK's src/ glob.
#include <algorithm>
#include <cmath>
#include <cstdlib>
#include <functional>
#include <iostream>
#include <memory>
#include <string>
#include <vector>

#include "nt/engine.h"
#include "nt/frame.h"
#include "nt/ninja.h"
#include "nt/rgb.h"
#include "nt/scene.h"

namespace {
using namespace nt;
using namespace nt::engine;

int checks = 0;
void check(bool ok, const std::string& message) {
    ++checks;
    if (!ok) {
        std::cerr << "FAIL: " << message << '\n';
        std::exit(1);
    }
}

Result hud(int left = 4, int right = 4) {
    Result r;
    r.Name = "rgb-duel";
    r.LayoutProfile = "duel";
    r.LeftSlots = left;
    r.RightSlots = right;
    for (char side : {'L', 'R'}) {
        for (int i = 0; i < (side == 'L' ? left : right); ++i) {
            BeadInfo b;
            b.Label = std::string(1, side) + std::to_string(i + 1);
            b.Lit = side == 'L' && i == 0;
            b.Gold = side == 'R' && i < 4; // Gold is available even if Lit is false.
            b.Conf = 1;
            r.Beads.push_back(b);
        }
    }
    return r;
}

struct Inner : Engine, LayoutPreferrer {
    Result result = hud();
    int calls = 0;
    std::string preferred;
    Result AnalyzeAt(const RGBA* img, TimeNs) override {
        if (img) ++calls;
        return img ? result : Result{};
    }
    void Prefer(const std::string& profile) override { preferred = profile; }
};

struct SupportOnly : Gate, FightSupportGate {
    GateDecision decision{GateUncertain, "", .31, "", false};
    bool support = true;
    int supportCalls = 0;
    GateDecision DecideAt(const RGBA*, TimeNs) override { return decision; }
    bool SupportsFight(const RGBA*, const std::string&) override {
        ++supportCalls;
        return support;
    }
};

struct StartGate : SupportOnly, HUDStartGate {
    GateDecision candidate{GateFight, "fight", .91, "duel", false};
    int starts = 0;
    std::string sampledProfile;
    GateDecision StartFight(const RGBA*, const std::string& profile) override {
        ++starts;
        sampledProfile = profile;
        return candidate;
    }
};

void testCompleteHUDAndGated() {
    RGBA img = RGBA::New(MakeRect(0, 0, 128, 128));
    struct Case {
        const char* name;
        std::function<void(Result&)> change;
        bool valid = false;
    };
    const std::vector<Case> cases = {
        {"complete four-slot rows", [](Result&) {}, true},
        {"complete six-slot rows", [](Result& r) { r = hud(6, 6); }, true},
        {"mixed four/six-slot rows", [](Result& r) { r = hud(4, 6); }, true},
        {"unknown expected slots", [](Result& r) { r.LeftSlots = r.RightSlots = 0; }, true},
        {"confidence boundary", [](Result& r) { r.Beads[0].Conf = .6; }, true},
        {"unknown profile", [](Result& r) { r.LayoutProfile = "custom"; }},
        {"left expected six, sampled four", [](Result& r) { r.LeftSlots = 6; }},
        {"right expected six, sampled four", [](Result& r) { r.RightSlots = 6; }},
        {"sampled six, expected four", [](Result& r) { r = hud(6, 6); r.LeftSlots = 4; }},
        {"missing right slot", [](Result& r) { r.Beads.pop_back(); }},
        {"missing left row", [](Result& r) { r.Beads.erase(r.Beads.begin(), r.Beads.begin() + 4); }},
        {"unknown slot", [](Result& r) { r.Beads[0].Unknown = true; }},
        {"low confidence", [](Result& r) { r.Beads[0].Conf = std::nextafter(.6, 0.); }},
        {"duplicate label", [](Result& r) { r.Beads[1].Label = "L1"; }},
        {"skipped label", [](Result& r) { r.Beads[1].Label = "L3"; }},
        {"long label", [](Result& r) { r.Beads[1].Label = "L02"; }},
        {"reordered labels", [](Result& r) { std::swap(r.Beads[0], r.Beads[1]); }},
        {"illegal blue prefix", [](Result& r) { r.Beads[2].Lit = true; }},
        {"illegal gold prefix", [](Result& r) { r.Beads[2].Gold = true; }},
    };
    for (const auto& tc : cases) {
        auto inner = std::make_shared<Inner>();
        tc.change(inner->result);
        check(hasCompleteHUD(inner->result) == tc.valid, tc.name);
        auto gate = std::make_shared<StartGate>();
        auto g = NewGated(gate, inner);
        Result r = g->AnalyzeAt(&img, Second);
        check(r.Fighting == tc.valid && r.Uncertain == !tc.valid, std::string(tc.name) + ": gated");
        check(gate->starts == (tc.valid ? 1 : 0) && inner->calls == 1,
              std::string(tc.name) + ": fresh sample precedes start");
        check(inner->preferred == (tc.valid ? "duel" : ""), std::string(tc.name) + ": preference");
        if (tc.valid) {
            check(r.Scene == "fight" && r.GateScore == .91 && !r.RoundOpening &&
                  gate->sampledProfile == "duel", "start preserves decision metadata");
        }
    }
    for (int slots : {4, 6}) {
        for (int mask = 0; mask < (1 << slots); ++mask) {
            for (int side = 0; side < 2; ++side) {
                Result r = hud(slots, slots);
                for (int i = 0; i < slots; ++i) {
                    auto& b = r.Beads[side * slots + i];
                    b.Lit = (mask & (1 << i)) != 0;
                    b.Gold = false;
                }
                check(hasCompleteHUD(r) == ((mask & (mask + 1)) == 0), "all four/six-slot prefix patterns");
            }
        }
    }

    for (const GateDecision& d : {
            GateDecision{GateNotFight, "result", .99, "", false},
            GateDecision{GateNotFight, "", .99, "", false},
            GateDecision{GateUncertain, "vs", .99, "", false},
            GateDecision{GateUncertain, "unsupported-resolution", 0, "", false},
            GateDecision{GateBlank, "blank", 0, "", false}}) {
        auto gate = std::make_shared<StartGate>();
        gate->decision = d;
        auto inner = std::make_shared<Inner>();
        auto g = NewGated(gate, inner);
        Result r = g->AnalyzeAt(&img, Second);
        check(!r.Fighting && r.Scene == d.SceneID && gate->starts == 0, "explicit decisions cannot be bypassed");
        check(inner->calls == (d.Kind == GateNotFight ? 0 : 1), "ordinary page sampling policy preserved");
    }
    {
        auto gate = std::make_shared<StartGate>();
        auto inner = std::make_shared<Inner>();
        inner->result.Uncertain = true;
        auto g = NewGated(gate, inner);
        check(!g->AnalyzeAt(&img, Second).Fighting && gate->starts == 0, "uncertain inner result cannot start");
        check(!g->AnalyzeAt(nullptr, 2 * Second).Fighting && gate->starts == 0, "nil frame cannot start");
    }
    for (GateDecision candidate : {GateDecision{GateFight, "fight", .99, "camp", false},
                                  GateDecision{GateUncertain, "", 0, "", false}}) {
        auto gate = std::make_shared<StartGate>();
        gate->candidate = candidate;
        auto inner = std::make_shared<Inner>();
        auto g = NewGated(gate, inner);
        check(!g->AnalyzeAt(&img, Second).Fighting && gate->starts == 1 && inner->preferred.empty(),
              "only a fight candidate in the sampled profile can start");
    }
    {
        auto gate = std::make_shared<SupportOnly>();
        auto g = NewGated(gate, std::make_shared<Inner>());
        check(!g->AnalyzeAt(&img, Second).Fighting && gate->supportCalls == 0,
              "support-only gate cannot cold start");
    }
    {
        auto gate = std::make_shared<StartGate>();
        auto inner = std::make_shared<Inner>();
        auto g = NewGated(gate, inner);
        check(g->AnalyzeAt(&img, Second).Fighting, "cold start establishes context");
        inner->result = hud(3, 3);
        check(g->AnalyzeAt(&img, 2 * Second).Fighting && gate->starts == 1 && gate->supportCalls == 1,
              "established continuation keeps bilateral three-slot rule");
        gate->support = false;
        Result held = g->AnalyzeAt(&img, 3 * Second);
        check(!held.Fighting && held.Uncertain && held.Scene == "fight", "lost control holds, not counts");
        gate->decision = GateDecision{GateUncertain, "vs", .99, "", false};
        check(!g->AnalyzeAt(&img, 4 * Second).Fighting, "explicit page clears cold-start context");
        gate->decision = GateDecision{GateUncertain, "", 0, "", false};
        gate->support = true;
        check(!g->AnalyzeAt(&img, 5 * Second).Fighting && gate->starts == 1,
              "old context cannot resume after explicit page");
    }
}

// Real NCC, not injected scores: every marker is an 8x8 patch of a deterministic
// random frame. ROI geometry is exactly representable at this reference size.
struct SceneFixture {
    RGBA img = RGBA::New(MakeRect(0, 0, 128, 128));
    std::shared_ptr<scene::Catalog> c = std::make_shared<scene::Catalog>();
    SceneFixture() {
        uint32_t seed = 17;
        for (int y = 0; y < 128; ++y) {
            for (int x = 0; x < 128; ++x) {
                seed = seed * 1664525u + 1013904223u;
                uint8_t v = static_cast<uint8_t>(seed >> 24);
                img.SetRGBA(x, y, Color{v, v, v, 255});
            }
        }
        c->mode = detect::ModeStretch;
        c->layoutCfg.ReferenceWidth = c->layoutCfg.ReferenceHeight = 128;
        c->cfg.MinFightScore = .82;
        c->fightSet.insert("fight");
    }
    void add(Rect roi, Point peak, bool continuation = true, const std::string& profile = "duel",
             const std::string& sceneID = "fight") {
        scene::template_ t;
        t.spec.ID = "control-" + std::to_string(c->templates.size());
        t.spec.Scene = sceneID;
        t.spec.LayoutProfile = profile;
        t.spec.ContinuationOnly = continuation;
        t.spec.Anchor = "left";
        t.spec.Threshold = .9;
        t.spec.ROI = {roi.Min.X / 128., roi.Min.Y / 128., roi.Dx() / 128., roi.Dy() / 128.};
        t.refW = t.refH = 128;
        RGBA patch = img.SubImage(Rect(peak, peak.Add(Pt(8, 8))));
        t.gray = match::ToGray(&patch);
        c->templates.push_back(t);
        c->preparedValid = false;
    }
    void pair(const std::string& profile = "duel") {
        add(MakeRect(0, 0, 32, 32), Pt(8, 8), true, profile);
        add(MakeRect(64, 0, 96, 32), Pt(72, 8), true, profile);
    }
    void erase(Rect r) {
        for (int y = r.Min.Y; y < r.Max.Y; ++y)
            for (int x = r.Min.X; x < r.Max.X; ++x) img.SetRGBA(x, y, Color{64, 64, 64, 255});
    }
};

void testStartFight() {
    for (const std::string profile : {"duel", "camp"}) {
        SceneFixture f;
        f.pair(profile);
        auto d = f.c->StartFight(&f.img, profile);
        check(d.Kind == GateFight && d.SceneID == "fight" && d.LayoutProfile == profile &&
              d.Confidence >= .999 && !d.RoundOpening, "independent accepted control pair starts");
        auto inner = std::make_shared<Inner>();
        inner->result.LayoutProfile = profile;
        auto g = NewGated(f.c, inner);
        check(g->AnalyzeAt(&f.img, Second).Fighting && inner->preferred == profile,
              "actual Catalog integrates with HUDStartGate");
        check(f.c->StartFight(&f.img, "custom").Kind == GateUncertain &&
              f.c->StartFight(nullptr, profile).Kind == GateUncertain, "unsupported input rejected");
        RGBA empty;
        check(f.c->StartFight(&empty, profile).Kind == GateUncertain, "empty geometry rejected");
    }
    {
        SceneFixture f;
        f.add(MakeRect(0, 0, 32, 32), Pt(8, 8));
        auto g = NewGated(f.c, std::make_shared<Inner>());
        check(f.c->SupportsFight(&f.img, "duel") && !g->AnalyzeAt(&f.img, Second).Fighting,
              "one current control can continue but cannot cold start");
        f.add(MakeRect(0, 0, 32, 32), Pt(16, 16));
        check(f.c->StartFight(&f.img, "duel").Kind == GateUncertain, "duplicate ROI is not independent");
    }
    for (int x : {16, 24}) {
        SceneFixture f;
        f.add(MakeRect(0, 0, 32, 32), Pt(8, 8));
        f.add(MakeRect(x, 0, x + 32, 32), Pt(x + 8, 8));
        check((f.c->StartFight(&f.img, "duel").Kind == GateFight) == (x == 24),
              "overlap independence uses the original strict quarter-area limit");
    }
    for (const std::string sceneID : {"fight", "result", "vs"}) {
        for (bool continuation : {false, true}) {
            SceneFixture f;
            f.pair();
            f.add(MakeRect(0, 64, 32, 96), Pt(8, 72), continuation,
                  sceneID == "fight" ? "camp" : "", sceneID);
            f.c->minimumRegions[sceneID] = 3;
            f.c->cfg.MinOtherScore = 1.01;
            auto d = f.c->StartFight(&f.img, "duel");
            check(d.Kind == GateUncertain, "any accepted page/conflicting profile vetoes even after two controls");
        }
    }
    {
        SceneFixture f;
        f.add(MakeRect(0, 0, 32, 32), Pt(8, 8));
        f.add(MakeRect(64, 0, 96, 32), Pt(72, 8), false);
        check(f.c->StartFight(&f.img, "duel").Kind == GateUncertain, "primary marker is not a second continuation");
    }
    {
        SceneFixture f;
        f.pair();
        f.c->templates[1].spec.LayoutProfile.clear();
        check(f.c->StartFight(&f.img, "duel").Kind == GateFight, "unbound control remains compatible");
        f.erase(MakeRect(64, 0, 96, 32));
        check(f.c->StartFight(&f.img, "duel").Kind == GateUncertain, "both controls must be present now");
    }
    {
        SceneFixture f;
        f.pair();
        Color px = f.img.RGBAAt(72, 8);
        uint8_t v = static_cast<uint8_t>(255 - px.R);
        f.img.SetRGBA(72, 8, Color{v, v, v, 255});
        auto hits = f.c->ScoreAll(&f.img);
        double confidence = std::min(hits[0].Value, hits[1].Value);
        check(confidence >= .9 && confidence < 1, "threshold boundary fixture");
        f.c->cfg.MinFightScore = confidence;
        auto d = f.c->StartFight(&f.img, "duel");
        check(d.Kind == GateFight && d.Confidence == confidence, "minimum accepted score, inclusive config threshold");
        f.c->cfg.MinFightScore = std::nextafter(confidence, 1.);
        check(f.c->StartFight(&f.img, "duel").Kind == GateUncertain, "MinFightScore cannot be bypassed");
        f.c->cfg.MinFightScore = .82;
        f.c->templates[1].spec.Threshold = std::nextafter(confidence, 1.);
        f.c->preparedValid = false;
        check(f.c->StartFight(&f.img, "duel").Kind == GateUncertain, "original per-template thresholds are not relaxed");
    }
}

void testSupportHints() {
    SceneFixture f;
    f.add(MakeRect(0, 0, 64, 32), Pt(8, 8));
    for (int y = 0; y < 8; ++y)
        for (int x = 0; x < 8; ++x) f.img.SetRGBA(48 + x, 8 + y, f.img.RGBAAt(8 + x, 8 + y));
    f.img.SetRGBA(48, 8, Color{127, 127, 127, 255});
    auto [ca, ok] = f.c->contentArea(&f.img);
    check(ok, "hint fixture geometry");
    f.c->prepare(ca);
    f.c->setHint(0, Pt(48, 8), true);
    scene::templateHint hint;
    check(f.c->SupportsFight(&f.img, "duel") && f.c->hint(0, hint) && hint.peak == Pt(48, 8),
          "accepted remembered peak wins before higher full-ROI peak");
    f.erase(MakeRect(46, 6, 58, 18));
    check(f.c->SupportsFight(&f.img, "duel") && f.c->hint(0, hint) && hint.peak == Pt(8, 8),
          "hint miss falls back to current full ROI and refreshes peak");
    check(!f.c->SupportsFight(&f.img, "camp"), "continuation hint cannot cross profiles");
    f.erase(MakeRect(0, 0, 64, 32));
    check(!f.c->SupportsFight(&f.img, "duel") && !f.c->hint(0, hint), "full miss clears stale hint");
    f.c->setHint(0, Pt(8, 8), true);
    ca.W *= 2;
    f.c->prepare(ca);
    check(!f.c->hint(0, hint), "geometry change invalidates hints");
}

// defaultBoxes saves every default box of r unchanged (docs/区域校准.md 每帧几何).
config::RegionCalibration defaultBoxes(const Result& r) {
    config::RegionCalibration c;
    c.Profile = r.LayoutProfile;
    c.LeftBeads = r.Geometry[0].BeadBox;
    c.RightBeads = r.Geometry[1].BeadBox;
    c.LeftAvatar = r.Geometry[0].AvatarBox;
    c.RightAvatar = r.Geometry[1].AvatarBox;
    return c;
}

bool sameBeads(const Result& a, const Result& b) {
    if (a.Beads.size() != b.Beads.size() || a.LayoutProfile != b.LayoutProfile) return false;
    for (size_t i = 0; i < a.Beads.size(); ++i) {
        const BeadInfo &x = a.Beads[i], &y = b.Beads[i];
        if (x.X != y.X || x.Y != y.Y || x.Label != y.Label || x.Lit != y.Lit || x.Gold != y.Gold ||
            x.Unknown != y.Unknown || x.Conf != y.Conf)
            return false;
    }
    return true;
}

bool sameRegions(const Result& a, const Result& b) {
    for (int i = 0; i < 2; ++i) {
        const SideGeometry &x = a.Geometry[i], &y = b.Geometry[i];
        if (x.Beads != y.Beads || x.NameROI != y.NameROI || x.AvatarROI != y.AvatarROI) return false;
    }
    return true;
}

// Mirrors internal/engine/rgb/energy_gauge_test.go without assets (no title or
// portrait): a thin orange gauge on the default row and beans 13px@540 lower
// read via the guarded re-read; a white flash or broad gold wash over an
// ordinary row stays unknown although the scenery below reads as four darks.
void testEnergyGaugeRow() {
    const config::Config cfg = config::Default();
    const Color gold{255, 200, 0, 255}, blue{40, 200, 255, 255}, dark{28, 54, 98, 255};
    for (Point size : {Pt(1280, 720), Pt(1920, 1080), Pt(2400, 1080)}) {
        for (int cover = 0; cover < 4; ++cover) {  // 0/1: gauge left/right, 2: white flash, 3: gold wash
            auto layout = std::make_shared<ConfiguredLayout>(detect::ModeAuto, cfg.Layout);
            RGBA img = RGBA::New(MakeRect(0, 0, size.X, size.Y));
            auto fill = [&](Rect r, Color c) {
                for (int y = r.Min.Y; y < r.Max.Y; ++y)
                    for (int x = r.Min.X; x < r.Max.X; ++x) img.SetRGBA(x, y, c);
            };
            fill(img.Bounds(), Color{32, 32, 32, 255});
            const detect::ContentArea area = layout->ContentArea(&img).first;
            const double s = area.HUDScale(960, 540);
            auto px = [&](double v) { return static_cast<int>(std::round(v * s)); };
            std::vector<detect::BeadPosition> rows[2];
            for (const auto& p : layout->PositionsIn("duel", area)) rows[p.Side == "right"].push_back(p);
            const int special = cover == 1 ? 1 : 0;
            int want[2][4];
            for (int side = 0; side < 2; ++side) {
                const auto& row = rows[side];
                const int x0 = std::min(row[0].X, row[3].X), x1 = std::max(row[0].X, row[3].X);
                fill(MakeRect(x0 - px(10), row[0].Y + px(8), x1 + px(10) + 1, row[0].Y + px(22) + 1),
                     Color{21, 40, 57, 255});
                const bool gauge = cover < 2 && side == special;
                if (gauge) fill(MakeRect(x0 - px(10), row[0].Y - px(2), x1 + px(10) + 1, row[0].Y + px(2) + 1),
                                Color{245, 150, 40, 255});
                const Color beans[4] = {gauge ? gold : blue, blue, dark, dark};
                for (int i = 0; i < 4; ++i) {
                    const auto& p = row[i];
                    const double ly = p.LY + (gauge ? ninja::EnergyGaugeRowOffset * 900 / 540 : 0);
                    const int y = area.MapHUDPixel(p.LX, ly, side == 1, 1600, 900).Y;
                    const int rx = px(4.8), ry = px(6.5);
                    for (int dy = -ry; dy <= ry; ++dy)
                        for (int dx = -rx; dx <= rx; ++dx)
                            if (detect::InBeadDiamond(0, 0, 2 * rx, 2 * ry, dx, dy))
                                img.SetRGBA(p.X + dx, y + dy, beans[i]);
                    want[side][i] = y;
                }
            }
            if (cover >= 2) {
                const int half = px(cover == 2 ? 3 : 9);
                const auto& row = rows[0];
                fill(MakeRect(row[0].X - px(10), row[0].Y - half, row[3].X + px(10) + 1, row[0].Y + half + 1),
                     cover == 2 ? Color{255, 255, 255, 255} : gold);
            }
            rgb::Engine e(layout, cfg.Vision);
            e.Prefer("duel");
            const Result r = e.AnalyzeAt(&img, 1700000000LL * Second);
            check(r.Beads.size() == 8, "energy gauge: eight slots");
            for (const BeadInfo& b : r.Beads) {
                const int side = b.Label[0] == 'R', i = b.Label[1] - '1';
                const bool covered = cover >= 2 && side == 0;
                const bool lit = i < 2, isGold = cover < 2 && side == special && i == 0;
                check(covered ? b.Unknown && !b.Lit
                              : !b.Unknown && b.Lit == lit && b.Gold == isGold && std::abs(b.Y - want[side][i]) <= 1,
                      "energy gauge row " + std::to_string(size.X) + " case " + std::to_string(cover) + " " + b.Label);
            }
            // The geometry reports the re-read row; its default boxes read it again.
            config::LayoutConfig saved = cfg.Layout;
            saved.Calibration = defaultBoxes(r);
            rgb::Engine again(std::make_shared<ConfiguredLayout>(detect::ModeAuto, saved), cfg.Vision);
            again.Prefer("duel");
            const Result r2 = again.AnalyzeAt(&img, 1700000000LL * Second);
            check(sameBeads(r2, r) && sameRegions(r2, r), "energy gauge: default boxes reproduce the row");
            for (int side = 0; side < 2; ++side) {
                std::vector<Point> centres;
                for (const BeadInfo& b : r.Beads)
                    if (b.Label[0] == "LR"[side]) centres.push_back(Pt(b.X, b.Y));
                check(r.Geometry[side].Beads == centres && r.Geometry[side].BeadBox.Width > 0 &&
                          r.Geometry[side].AvatarBox.Width > 0,
                      "energy gauge: geometry beads are the sampled centres");
            }
        }
    }
}
// Region calibration (docs/区域校准.md), mirrors internal/engine and
// internal/engine/rgb calibration tests: box transform for both profiles,
// unset = automatic, beads read on a HUD the automatic layout misses.
void testRegionCalibration() {
    config::Config cfg = config::Default();
    const int w = 2388, h = 1668;
    RGBA img = RGBA::New(MakeRect(0, 0, w, h));
    for (int y = 0; y < h; ++y)
        for (int x = 0; x < w; ++x) img.SetRGBA(x, y, Color{32, 32, 32, 255});
    ConfiguredLayout automatic(detect::ModeAuto, cfg.Layout);
    const detect::ContentArea area = automatic.ContentArea(&img).first;
    config::RegionCalibration lobby;
    lobby.Profile = "lobby";
    lobby.LeftBeads = config::NormalizedRect{.1, .1, .1, .1};
    cfg.Layout.Calibration = lobby;
    ConfiguredLayout ignored(detect::ModeAuto, cfg.Layout);
    for (const char* profile : {"camp", "duel"}) {
        const auto a = automatic.PositionsInImage(profile, &img, area), b = ignored.PositionsInImage(profile, &img, area);
        check(a.size() == b.size(), "unset calibration size");
        for (size_t i = 0; i < a.size(); ++i)
            check(a[i].X == b[i].X && a[i].Y == b[i].Y && a[i].Cal == nullptr && b[i].Cal == nullptr,
                  "unset calibration keeps automatic positions");
    }
    // Boxes around a device HUD drawn at 1.7 px per reference unit.
    config::RegionCalibration c;
    c.Profile = "duel";
    const double s = 1.7, pl = 77.0 / 3 * s, pr = 25 * s;
    c.LeftBeads = config::NormalizedRect{(330 - pl / 2) / w, (250 - 11.0) / h, 4 * pl / w, 22.0 / h};
    c.RightBeads = config::NormalizedRect{(2041 + pr / 2 - 4 * pr) / w, (250 - 11.0) / h, 4 * pr / w, 22.0 / h};
    cfg.Layout.Calibration = c;
    auto layout = std::make_shared<ConfiguredLayout>(detect::ModeAuto, cfg.Layout);
    const Color blue{40, 200, 255, 255}, dark{28, 54, 98, 255};
    const int rx = static_cast<int>(std::round(4.8 * s * 1600 / 960)), ry = static_cast<int>(std::round(6.5 * s * 1600 / 960));
    for (const char* profile : {"duel", "camp"}) {
        const config::LayoutProfile ref = cfg.Layout.Profile(profile);
        const auto positions = layout->PositionsInImage(profile, &img, area);
        check(positions.size() == 8, "calibrated slot count");
        for (const auto& p : positions) {
            const bool right = p.Side == "right";
            const config::NormalizedPoint n = (right ? ref.Right : ref.Left).NominalCenters[p.Idx];
            const double x = (right ? 2041.0 : 330.0) + s * (n.X * 1600 - (right ? 1423 : 172));
            const double y = 250.0 + s * (n.Y * 900 - 103);
            check(p.Cal != nullptr && std::abs(p.Cal->Scale - s) < 1e-9 &&
                      p.X == static_cast<int>(std::round(x)) && p.Y == static_cast<int>(std::round(y)),
                  std::string("calibrated transform ") + profile + " " + p.Side + std::to_string(p.Idx));
        }
    }
    // Draw duel beans (L: 2 lit, R: 3 lit) where the calibration says.
    int lit = 0;
    for (const auto& p : layout->PositionsInImage("duel", &img, area)) {
        const bool on = p.Idx < (p.Side == "right" ? 3 : 2);
        lit += on;
        for (int dy = -ry; dy <= ry; ++dy)
            for (int dx = -rx; dx <= rx; ++dx)
                if (detect::InBeadDiamond(0, 0, 2 * rx, 2 * ry, dx, dy)) img.SetRGBA(p.X + dx, p.Y + dy, on ? blue : dark);
    }
    rgb::Engine calibrated(layout, cfg.Vision);
    calibrated.Prefer("duel");
    const Result res = calibrated.AnalyzeAt(&img, 1700000000LL * Second);
    int read = 0, known = 0;
    for (const auto& b : res.Beads) {
        read += b.Lit && !b.Unknown;
        known += !b.Unknown;
    }
    check(res.Beads.size() == 8 && known == 8 && read == lit, "calibrated beans read");
    rgb::Engine plain(std::make_shared<ConfiguredLayout>(detect::ModeAuto, config::Default().Layout), cfg.Vision);
    plain.Prefer("duel");
    known = 0;
    for (const auto& b : plain.AnalyzeAt(&img, 1700000000LL * Second).Beads) known += !b.Unknown;
    check(known == 0, "automatic layout misses the device HUD");
    // A box drawn exactly around the implied portrait inverts AvatarRegion.
    for (double scale : {1280.0 / 960, 1.5, 2.0, 3.0}) {
        for (bool left : {true, false}) {
            const Rect want = ninja::AvatarRegion(Pt(int(400 * scale), int(62 * scale)), scale, left);
            const double side = 68.5 * scale;
            double got = 0;
            const Rect r = ninja::AvatarRegionFromBox(want.Min.X + (left ? 42 : 36) * scale - side / 2,
                                                      want.Min.Y + 49.5 * scale - side / 2, side, side, left, got);
            check(std::abs(got - scale) < 1e-9 && std::abs(r.Min.X - want.Min.X) <= 1 && std::abs(r.Min.Y - want.Min.Y) <= 1,
                  "avatar box inverts AvatarRegion");
        }
    }
}

// Per-frame geometry (docs/区域校准.md 每帧几何), mirrors
// internal/engine/configured_layout_geometry_test.go, internal/ninja/avatar_box_test.go
// and internal/frame/source_test.go.
void testFrameGeometry() {
    const config::Config cfg = config::Default();
    const auto f32 = [](config::NormalizedRect r) {
        return config::NormalizedRect{double(float(r.X)), double(float(r.Y)), double(float(r.Width)), double(float(r.Height))};
    };
    // Default bead boxes saved unchanged (and as float32) reproduce every row.
    config::RegionCalibration existing;
    existing.Profile = "duel";
    existing.LeftBeads = config::NormalizedRect{.127, .143, .081, .019};
    existing.RightBeads = config::NormalizedRect{.79, .151, .07, .02};
    int corrected = 0;
    std::vector<std::string> inconsistent;
    const Rect frames[] = {MakeRect(0, 0, 1280, 720),  MakeRect(0, 0, 1440, 900),  MakeRect(0, 0, 1600, 900),
                           MakeRect(0, 0, 1680, 1050), MakeRect(0, 0, 1920, 1080), MakeRect(0, 0, 2160, 1080),
                           MakeRect(0, 0, 2400, 1080), MakeRect(0, 0, 2400, 1350), MakeRect(0, 0, 2388, 1668),
                           MakeRect(0, 0, 2880, 1800), MakeRect(0, 0, 3440, 1440), MakeRect(17, 23, 17 + 2340, 23 + 1080)};
    for (const Rect& bounds : frames) {
        for (const std::string profile : {"camp", "duel"}) {
            for (bool calibrated : {false, true}) {
                const std::string name = std::to_string(bounds.Dx()) + "x" + std::to_string(bounds.Dy()) + "/" + profile;
                config::LayoutConfig layoutCfg = cfg.Layout;
                if (calibrated) layoutCfg.Calibration = existing;
                ConfiguredLayout l(detect::ModeAuto, layoutCfg);
                RGBA img = RGBA::New(bounds);
                if (bounds.Min == Point{})
                    for (int y = bounds.Min.Y; y < bounds.Max.Y; ++y)
                        for (int x = bounds.Min.X; x < bounds.Max.X; ++x) img.SetRGBA(x, y, Color{32, 32, 32, 255});
                const detect::ContentArea area = l.ContentArea(&img).first;
                check((bounds.Min != Point{}) != (area.X == bounds.Min.X), "geometry: letterboxed offset frame");
                // Health bars move wide rows inward (HP-bar x-correction).
                const double s = area.HUDScale(1600, 900);
                const int shift = static_cast<int>(std::round(14 * s)), step = std::max(1, static_cast<int>(std::round(s)));
                const int cy = area.Y + static_cast<int>(std::round((profile == "camp" ? 70 : 74) * s));
                const auto plain = l.PositionsIn(profile, area);
                for (const auto& first : plain) {
                    if (first.Idx != 0) continue;
                    const int direction = first.Side == "right" ? 1 : -1;
                    for (int row = -1; row <= 1; ++row) {
                        const int x = first.X - direction * shift + direction * row * step, length = static_cast<int>(200 * s);
                        const int x0 = direction == 1 ? x - length + 1 : x, x1 = direction == 1 ? x + 1 : x + length;
                        for (int px = x0; px < x1; ++px) img.SetRGBA(px, cy + row * step, Color{240, 70, 10, 255});
                    }
                }
                const auto want = l.PositionsInImage(profile, &img, area);
                corrected += !calibrated && want[0].X != plain[0].X;
                std::vector<detect::BeadPosition> rows[2];
                for (const auto& p : want) rows[p.Side == "right"].push_back(p);
                config::NormalizedRect boxes[2];
                bool exact[2];
                for (int side = 0; side < 2; ++side) {
                    const auto& row = rows[side];
                    config::NormalizedRect& box = boxes[side];
                    const bool ok = l.DefaultBeadBox(profile, row, img.Bounds(), area, box);
                    const double w = bounds.Dx(), h = bounds.Dy(), pitch = box.Width * w / 4;
                    const double x0 = bounds.Min.X + box.X * w, x1 = bounds.Min.X + (box.X + box.Width) * w;
                    check(ok && box.X >= 0 && box.Y >= 0 && box.X + box.Width <= 1 && box.Y + box.Height <= 1 &&
                              std::abs(box.Height * h - pitch) <= 1e-9 &&
                              std::abs(bounds.Min.Y + (box.Y + box.Height / 2) * h - row[0].Y) <= .5 &&
                              std::min(row[0].X, row[3].X) >= x0 + pitch / 2 - .5 &&
                              std::max(row[0].X, row[3].X) <= x1 - pitch / 2 + .5,
                          "geometry: bead box encloses " + name);
                    // One bead-0 point rounding every centre back (with the margin)?
                    const double rs = detect::RowHUDScale(row, area, 1600, 900);
                    double xlo = -1e300, xhi = 1e300, ylo = -1e300, yhi = 1e300;
                    for (const auto& b : row) {
                        const double dx = rs * (b.LX - row[0].LX), dy = rs * (b.LY - row[0].LY);
                        xlo = std::max(xlo, b.X - .5 - dx), xhi = std::min(xhi, b.X + .5 - dx);
                        ylo = std::max(ylo, b.Y - .5 - dy), yhi = std::min(yhi, b.Y + .5 - dy);
                    }
                    exact[side] = xhi - xlo > 2e-3 && yhi - ylo > 2e-3;
                    if (!exact[side]) inconsistent.push_back(name + "/" + row[0].Side + "/" + std::to_string(calibrated));
                }
                for (bool single : {false, true}) {
                    config::LayoutConfig saved = cfg.Layout;
                    config::RegionCalibration c;
                    c.Profile = profile;
                    c.LeftBeads = single ? f32(boxes[0]) : boxes[0];
                    c.RightBeads = single ? f32(boxes[1]) : boxes[1];
                    saved.Calibration = c;
                    const auto got = ConfiguredLayout(detect::ModeAuto, saved).PositionsInImage(profile, &img, area);
                    check(got.size() == want.size(), "geometry: saved box slot count");
                    std::vector<detect::BeadPosition> gotRows[2];
                    for (size_t i = 0; i < got.size(); ++i) {
                        const int dx = got[i].X - want[i].X, dy = got[i].Y - want[i].Y;
                        // No exact box: bead 0 (the name/portrait anchor) stays exact.
                        const bool loose = !exact[got[i].Side == "right"] && got[i].Idx > 0 && std::abs(dx) <= 1 && std::abs(dy) <= 1;
                        check(((dx == 0 && dy == 0) || loose) && detect::CalBeads(got[i].Cal),
                              "geometry: saved box reproduces " + name + " " + got[i].Side + std::to_string(got[i].Idx));
                        gotRows[got[i].Side == "right"].push_back(got[i]);
                    }
                    for (int side = 0; side < 2; ++side) {
                        for (const auto& ref : {Pt(1600, 900), Pt(1920, 1080), Pt(960, 540)}) {
                            const double a = detect::RowHUDScale(rows[side], area, ref.X, ref.Y);
                            const double b = detect::RowHUDScale(gotRows[side], area, ref.X, ref.Y);
                            check(std::abs(a - b) <= (single ? 1e-6 : 1e-12) * a, "geometry: saved box scale " + name);
                        }
                    }
                }
            }
        }
    }
    check(corrected == 6, "geometry: HP-bar x-correction on every wide frame");
    check(inconsistent == std::vector<std::string>{"1440x900/camp/left/0", "2400x1350/camp/left/0"},
          "geometry: rows without an exact box");
    // Default avatar boxes convert back to their window and scale; only the
    // 38s/62s double-tie scales (1.25, 1.75) give a window 1px shorter.
    std::vector<double> scales = {1280.0 / 960, 1.25, 1.5, 1.75, 2, 2.4875, 3};
    for (double s = .5; s < 4; s += .0137) scales.push_back(s);
    int tall = 0;
    for (double scale : scales) {
        for (Point first : {Pt(155, 61), Pt(402, 125), Pt(2041, 250), Pt(3001, 20)}) {
            for (bool left : {true, false}) {
                const Rect window = ninja::AvatarRegion(first, scale, left);
                double x = 0, y = 0, w = 0, h = 0;
                ninja::AvatarBoxFromRegion(window, scale, left, x, y, w, h);
                check(w == h && std::abs(w - 68.5 * scale) <= 1e-9, "geometry: square avatar box");
                for (bool single : {false, true}) {
                    const auto g = [&](double v) { return single ? double(float(v)) : v; };
                    double gotScale = 0;
                    const Rect got = ninja::AvatarRegionFromBox(g(x), g(y), g(w), g(h), left, gotScale);
                    check(std::abs(gotScale - scale) <= 1e-6 * scale, "geometry: avatar box scale");
                    if (got == window) continue;
                    check(std::fmod(scale * 4, 2) == 1 && got.Min == window.Min && got.Max.X == window.Max.X &&
                              got.Max.Y == window.Max.Y - 1,
                          "geometry: avatar box window");
                    ++tall;
                }
            }
        }
    }
    check(tall == 2 * 2 * 4 * 2, "geometry: tie windows only at scales 1.25 and 1.75");
    // A frame reused from the duplicate cache carries the geometry it reuses.
    struct GeometryEngine : Engine {
        int calls = 0;
        Result AnalyzeAt(const RGBA*, TimeNs) override {
            ++calls;
            Result r;
            r.Name = "geometry";
            r.Geometry[1].Beads = {Pt(300, 40), Pt(280, 40)};
            r.Geometry[1].NameROI = MakeRect(1, 2, 3, 4);
            r.Geometry[1].BeadBox = config::NormalizedRect{.7, .1, .2, .05};
            return r;
        }
    } eng;
    RGBA still = RGBA::New(MakeRect(0, 0, 400, 250));
    frame::DedupedAnalysis d;
    const auto first = d.analyze(&still, &eng, detect::ModeStretch, Second, Second, "sdk");
    const auto second = d.analyze(&still, &eng, detect::ModeStretch, 2 * Second, 2 * Second, "sdk");
    check(eng.calls == 1 && second.Reused && first.Res.Geometry[1].Beads == second.Res.Geometry[1].Beads &&
              second.Res.Geometry[1].NameROI == MakeRect(1, 2, 3, 4) && second.Res.Geometry[1].BeadBox.X == .7 &&
              second.Res.Geometry[0].Beads.empty(),
          "geometry: reused frame carries the geometry");
}
} // namespace

int main() {
    testEnergyGaugeRow();  // first: independent of the scene/gate checks below
    testRegionCalibration();
    testFrameGeometry();
    testCompleteHUDAndGated();
    testStartFight();
    testSupportHints();
    std::cout << "HUD start/continuation regressions: " << checks << " checks passed\n";
}
