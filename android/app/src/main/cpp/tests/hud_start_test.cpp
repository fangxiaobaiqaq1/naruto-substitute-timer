// Host-only regressions for internal/engine/gated.go + internal/scene/scene.go.
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
} // namespace

int main() {
    testCompleteHUDAndGated();
    testStartFight();
    testSupportHints();
    std::cout << "HUD start/continuation regressions: " << checks << " checks passed\n";
}
