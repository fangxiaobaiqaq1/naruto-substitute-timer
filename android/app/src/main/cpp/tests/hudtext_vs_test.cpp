// Real unchanged user PNGs + real local OCR through Pipeline. No forged OCR or
// scene results in staticPipeline. Small explicit fakes below test scheduler/gate contracts.
#define main unused_hostcheck_main
#include "../../../../../tools/hostcheck.cpp"
#undef main

#include <atomic>
#include <chrono>
#include <thread>
#include "nt/hudtext.h"

namespace {
using namespace nt;
int checks = 0;
void check(bool ok, const std::string& message) {
    ++checks;
    if (!ok) { std::fprintf(stderr, "FAIL: %s\n", message.c_str()); std::exit(1); }
}
void idle() {
    for (int i = 0; i < 2000 && !hudtext::Worker::Instance().Idle(); ++i)
        std::this_thread::sleep_for(std::chrono::milliseconds(5));
    check(hudtext::Worker::Instance().Idle(), "OCR worker did not finish");
}
void signalled(const std::atomic<bool>& signal) {
    for (int i = 0; i < 2000 && !signal.load(); ++i) std::this_thread::sleep_for(std::chrono::milliseconds(5));
    check(signal.load(), "OCR callback not reached");
}
constexpr TimeNs base = 1000 * Second;

void rules() {
    using namespace hudtext;
    check(!config::Default().UI.PreMatchSideRecognition, "default is opt-in");
    const std::vector<std::string> names{"白方小"};
    check(FooterAccount({"白 方 小", "白\t方小", "自方小"}, names) == "白方小", "2/3 exact names, OCR spaces only");
    for (const auto& raw : std::vector<std::array<std::string, 3>>{
            {"白方小", "自方小", "白方"}, {"白方", "白方", "白方"}, {"(白方小)", "(白方小)", ""},
            {"白方小100%", "白方小100%", "白方小"}, {"陌生账号", "陌生账号", "陌生账号"},
            {"白方小ღ", "白方小ღ", "白方小"}, {"白方小号", "白方小号", "白方小号"}})
        check(FooterAccount(raw, names).empty(), "no prefix/fuzzy/unknown/footer parentheses");
    check(FooterAccount({"白方小", "白方小", "白方小"}, {}).empty(), "names required");
    check(FooterAccount({"白(方)", "白(方)", ""}, {"白（方）"}).empty(), "punctuation exact");
    check(FooterAccount({"白/方", "白/方", ""}, {"白方"}).empty(), "non-whitespace exact");
    check(DecideFooterSide({"白方小", ""}, names).side == 0, "left mine");
    check(DecideFooterSide({"", "白方小"}, names).side == 1, "right mine");
    check(DecideFooterSide({"白方小", "白方小"}, names).side == -1, "two mine sides ambiguous");
    check(DecideFooterSide({"", "陌生账号"}, names).side == -1, "never infer side from non-mine");
    ValueState value;
    value.observe("白方小", base);
    value.observe("白方小", base);
    check(value.accepted.empty(), "same acquisition time is not second proof");
    value.observe("白方小", base + 1);
    check(value.accepted == "白方小", "different acquisition time confirms");
    std::array<Rect, 2> boxes;
    check(VSNameRegions({0, 0, 960, 540}, MakeRect(0, 0, 960, 540), boxes), "reference footer");
    check(boxes[0] == MakeRect(106, 489, 182, 516) && boxes[1] == MakeRect(795, 489, 876, 516), "independent reference ROI");
    check(VSNameRegions({20, 30, 960, 600}, MakeRect(0, 0, 1100, 700), boxes), "16:10 inset footer");
    check(boxes[0] == MakeRect(126, 579, 202, 606) && boxes[1] == MakeRect(815, 579, 896, 606), "bottom not top anchoring");
    check(VSNameRegions({20, 30, 1200, 540}, MakeRect(0, 0, 1300, 700), boxes), "wide footer");
    check(boxes[0] == MakeRect(126, 519, 202, 546) && boxes[1] == MakeRect(1055, 519, 1136, 546), "independent left/right anchors");
}

void staticPipeline(const RGBA& img, const std::string& label) {
    config::Config cfg = config::Default();
    cfg.UI.PlayerNames = {"白方小"};
    cfg.UI.PreMatchSideRecognition = true;
    frame::Pipeline p(cfg, true);
    check(p.Ok(), "pipeline: " + p.Error());
    p.SetTextEnabled(false); // footer MUST remain enabled
    uint64_t sequence = 0;
    auto feed = [&](int ms, bool reused, const std::string& side = "") {
        const TimeNs at = base + ms * Millisecond;
        auto f = p.Analyze(&img, at, at, frame::MethodBuffer);
        check(!f.HasErr && f.Res.Scene == "vs" && f.Hold && !f.Res.Fighting && !f.Res.RoundOpening, label + " VS hold, not opening");
        check(f.Sequence == ++sequence, "monotonic sequence");
        check(f.Duplicate == (sequence > 1), "unchanged PNG must stay Duplicate after the first capture");
        check(f.Reused == reused, label + " expected Reused=" + std::to_string(reused) + " at " + std::to_string(ms));
        check(f.Res.PlayerSide == side && f.Res.OppName.empty(), label + " expected side=" + side + " actual=" + f.Res.PlayerSide);
        if (!side.empty()) check(f.Res.PlayerName == "白方小", "full configured account, no parentheses");
        return f;
    };
    feed(0, false);
    idle();
    check(p.Text()->NeedsAnalysis(base + 16 * Millisecond) && p.Text()->NeedsAnalysis(base + 16 * Millisecond), "mailbox hint only peeks");
    feed(16, false); // one OCR result cannot decide
    feed(32, true);
    feed(399, true);
    feed(400, false); // deadline submits second job on IDENTICAL pixels
    idle();
    feed(416, false, "left");
    feed(432, true, "left");
    p.SetTextEnabled(true);
    feed(448, false, "left");
    p.SetTextEnabled(false);
    feed(464, false, "left"); // battle toggle never discards VS evidence
    p.SetPreMatchSideRecognition(false);
    feed(480, false);
    feed(496, true);
    // Disable/enable without an intervening capture cannot resurrect accepted proof.
    p.SetPreMatchSideRecognition(true);
    feed(512, false);
    idle();
    feed(528, false);
    identity::SetMineNames({"张三"});
    feed(544, false);
    idle();
    feed(560, false); // previous name cannot become a non-mine inference
    feed(576, true);
    p.SetPreMatchSideRecognition(false);
    feed(592, false);
    // A seek must invalidate cached text even when no pixels/settings changed.
    feed(100, false);
    feed(116, true);
    idle();
    std::printf("PASS static %s: 2 acquisitions + whole-footer signature; Duplicate unchanged; toggle/rename/seek\n", label.c_str());
}

class FixedEngine : public engine::Engine {
public:
    engine::Result result;
    engine::Result AnalyzeAt(const RGBA*, TimeNs) override { return result; }
};
class VSGate : public engine::Gate {
public:
    engine::GateDecision DecideAt(const RGBA*, TimeNs) override {
        engine::GateDecision d; d.Kind = engine::GateUncertain; d.SceneID = "vs"; return d;
    }
};
void gates(const RGBA& img) {
    std::string err;
    auto eng = factory::New(factory::DefaultConfig(), &err);
    auto* g = dynamic_cast<engine::Gated*>(eng.get());
    check(g != nullptr, "factory gated");
    int calls = 0;
    g->Guess = [&](const RGBA*, const std::string&) { ++calls; return engine::Identity{"left", "白方小", ""}; };
    frame::DedupedAnalysis cache;
    auto feed = [&](TimeNs at) { return cache.analyze(&img, g, detect::ModeAuto, at, at, frame::MethodBuffer); };
    check(feed(base).Res.PlayerSide.empty() && calls == 0, "factory default disables legacy VS image guesser");
    check(feed(base + 1).Reused, "plain gated cache sleeps");
    g->SetPreMatchSideRecognition(true);
    check(feed(base + 2).Res.PlayerSide == "left" && calls == 1, "image option enabled on identical pixels");
    g->SetPreMatchSideRecognition(false);
    auto off = feed(base + 3);
    check(!off.Reused && off.Res.PlayerSide.empty() && calls == 1, "image option off invalidates static cache");
    auto legacy = engine::NewGatedWithGuess(std::make_shared<VSGate>(), std::make_shared<FixedEngine>(), g->Guess);
    check(legacy->AnalyzeAt(&img, base).PlayerSide == "left", "low-level compatibility defaults true");
}

void timeout(const RGBA& img) {
    auto& worker = hudtext::Worker::Instance();
    config::Config cfg = config::Default(); cfg.UI.PlayerNames = {"白方小"}; cfg.UI.PreMatchSideRecognition = true;
    frame::Pipeline p(cfg, true);
    p.SetTextEnabled(false);
    std::atomic<bool> entered{false}, release{false};
    p.Text()->OnRead = [&](TimeNs, const auto&, double) {
        entered = true;
        while (!release.load()) std::this_thread::sleep_for(std::chrono::milliseconds(2));
    };
    auto feed = [&](TimeNs at) { return p.Analyze(&img, at, at, frame::MethodBuffer); };
    feed(base);
    signalled(entered);
    check(!p.Text()->NeedsAnalysis(base + 100 * Second), "busy alone/acquisition clock jump cannot request analysis");
    std::this_thread::sleep_for(std::chrono::nanoseconds(hudtext::jobTimeout + 10 * Millisecond));
    check(p.Text()->NeedsAnalysis(base + 16 * Millisecond), "wall-clock timeout wakes static cache");
    auto failed = feed(base + 16 * Millisecond);
    check(failed.Duplicate && !failed.Reused && failed.Res.TextStatus == "unavailable", "timeout cancels, does not change fingerprint");
    release = true;
    idle();
    p.Text()->OnRead = nullptr;
    check(!p.Text()->NeedsAnalysis(base + 32 * Millisecond), "cancelled completion discarded, not every-frame busy polling");
    check(!p.Text()->NeedsAnalysis(base + 5015 * Millisecond), "backoff still sleeping");
    check(p.Text()->NeedsAnalysis(base + 5016 * Millisecond), "retry deadline wakes");
    feed(base + 5016 * Millisecond);
    idle();
    feed(base + 5032 * Millisecond);
    p.SetPreMatchSideRecognition(false);
    idle();
    (void)worker;
}

void cancellation(const RGBA& img, bool rename) {
    config::Config cfg = config::Default(); cfg.UI.PlayerNames = {"白方小"}; cfg.UI.PreMatchSideRecognition = true;
    frame::Pipeline p(cfg, true);
    p.SetTextEnabled(false);
    std::atomic<bool> entered{false}, release{false};
    p.Text()->OnRead = [&](TimeNs, const auto&, double) {
        entered = true;
        while (!release.load()) std::this_thread::sleep_for(std::chrono::milliseconds(2));
    };
    uint64_t sequence = 0;
    auto feed = [&](int ms) {
        auto f = p.Analyze(&img, base + ms * Millisecond, base + ms * Millisecond, frame::MethodBuffer);
        check(f.Sequence == ++sequence && f.Duplicate == (sequence > 1), "cancel never resets delivery");
        return f;
    };
    feed(0);
    signalled(entered); // actual OCR done, still in flight before mailbox delivery
    if (rename) {
        identity::SetMineNames({"张三"});
        check(feed(8).Res.PlayerSide.empty(), "rename invalidates in-flight job");
        identity::SetMineNames({"白方小"});
    } else {
        p.SetPreMatchSideRecognition(false);
        p.SetPreMatchSideRecognition(true); // no intervening capture must still discard old work
    }
    check(feed(16).Res.PlayerSide.empty(), "cancelled work cannot supply evidence");
    release = true;
    idle();
    p.Text()->OnRead = nullptr;
    check(feed(32).Res.PlayerSide.empty(), "late completion discarded before restarting");
    idle();
    check(feed(48).Res.PlayerSide.empty(), "new generation still needs two new captures, not old+new");
    feed(432);
    idle();
    check(feed(448).Res.PlayerSide == "left", "two new captures accepted after cancellation");
    // Current glyphs, not just a completed OCR string, remain mandatory.
    RGBA erased = img.Clone();
    std::array<Rect, 2> regions;
    check(hudtext::NameLayout(cfg.Layout).Regions(&erased, "vs", regions), "erasure footer geometry");
    for (int y = regions[0].Min.Y; y < regions[0].Max.Y; ++y)
        for (int x = regions[0].Min.X; x < regions[0].Max.X; ++x) erased.SetRGBA(x, y, Color{0, 0, 0, 255});
    auto changed = p.Analyze(&erased, base + 464 * Millisecond, base + 464 * Millisecond, frame::MethodBuffer);
    check(changed.Res.Scene == "vs" && changed.Res.PlayerSide.empty(), "accepted footer revoked when current glyphs disappear");
    p.SetPreMatchSideRecognition(false);
    idle();
}

void suffixCases(const RGBA& img, const std::string& repo) {
    std::vector<uint8_t> model;
    check(readFile(repo + "/internal/ocr/neuraldata/recognizer.onnx", model), "suffix test model");
    std::string err;
    auto recognizer = ocr::Recognizer::Load(model.data(), model.size(), &err);
    check(recognizer != nullptr, "suffix test recognizer");
    const std::vector<std::string> names{"白方小"};
    config::Config cfg = config::Default(); cfg.UI.PlayerNames = names; cfg.UI.PreMatchSideRecognition = true;
    std::array<Rect, 2> regions;
    check(hudtext::NameLayout(cfg.Layout).Regions(&img, "vs", regions), "suffix footer geometry");
    std::array<RGBA, 2> strips{match::CropRGBA(&img, regions[0]), match::CropRGBA(&img, regions[1])};
    hudtext::ReadResult read;
    check(hudtext::ReadFooterAccounts(*recognizer, strips, regions, names, read), "real footer OCR");
    check(read.values[0] == "白方小" && read.proofs[0] == nullptr && read.proofs[1] == nullptr,
          "footer deliberately has NO local glyph proof");
    const auto rows = hudtext::BuildRows(strips);
    std::vector<ocr::Recognizer::RowImage> images;
    for (const auto& row : rows) images.push_back({&row.image, row.rect});
    std::vector<ocr::Line> lines;
    check(recognizer->ReadRows(images, lines, &err, 2), "locate original prefix glyphs");
    // Reconstruct the OLD local-proof behavior solely to prove that this fixture
    // appends outside the old account and would have passed its glyph Matches.
    const auto prefixProof = hudtext::Evidence(rows, lines, 0, "白方小", strips[0], regions[0]);
    check(prefixProof != nullptr, "original local prefix proof");
    const int x0 = prefixProof->bounds.Max.X + 3;
    check(x0 + 8 < regions[0].Max.X, "space for a synthetic suffix inside the clean ROI");
    RGBA suffix = img.Clone();
    for (int y = prefixProof->bounds.Min.Y; y < prefixProof->bounds.Max.Y; ++y)
        for (int x = x0; x < x0 + 8; ++x)
            suffix.SetRGBA(x, y, x % 4 < 2 ? Color{255, 255, 255, 255} : Color{0, 0, 0, 255});
    int white = 0;
    check(prefixProof->Matches(&suffix), "suffix leaves every original prefix glyph intact");
    check(hudtext::Lettering(img, regions[0], white) != hudtext::Lettering(suffix, regions[0], white), "whole-footer signature sees suffix");
    for (bool accepted : {false, true}) {
        frame::Pipeline p(cfg, true); p.SetTextEnabled(false);
        auto feed = [&](const RGBA& frame, int ms) {
            return p.Analyze(&frame, base + ms * Millisecond, base + ms * Millisecond, frame::MethodBuffer);
        };
        feed(img, 0); idle();
        check(feed(img, 16).Res.PlayerSide.empty(), "suffix test first vote pending");
        feed(img, 400); idle();
        if (accepted) check(feed(img, 416).Res.PlayerSide == "left", "suffix test account accepted first");
        const auto changed = feed(suffix, accepted ? 432 : 416);
        check(changed.Res.Scene == "vs" && changed.Hold && changed.Res.PlayerSide.empty(),
              accepted ? "suffix revokes an accepted shorter account" : "suffix rejects a pending old completion's second vote");
        p.SetPreMatchSideRecognition(false);
        idle();
    }
    std::printf("PASS suffix after pending/accepted: prefix pixels unchanged, whole-ROI signature revokes both\n");
}

void pause() {
    config::Config cfg = config::Default();
    RGBA img = RGBA::New(MakeRect(0, 0, 960, 540));
    std::array<Rect, 2> regions;
    check(hudtext::NameLayout(cfg.Layout).Regions(&img, "camp", regions), "test HUD geometry");
    // Deliberate synthetic glyphs, only a scheduler test (not an OCR/scene accuracy claim).
    for (int y = regions[0].Min.Y + 2; y < regions[0].Max.Y - 2; ++y)
        for (int x = regions[0].Min.X + 2; x < regions[0].Min.X + 60; ++x)
            if (x % 4 < 2) img.SetRGBA(x, y, Color{255, 255, 255, 255});
    auto inner = std::make_shared<FixedEngine>();
    inner->result.Fighting = true; inner->result.Scene = "fight"; inner->result.LayoutProfile = "camp";
    hudtext::Engine eng(inner, cfg, [] { return std::vector<std::string>{"白方小"}; }, [](char32_t) { return true; }, &hudtext::Worker::Instance());
    eng.AnalyzeAt(&img, base);
    idle();
    inner->result.Uncertain = true;
    eng.AnalyzeAt(&img, base + 16 * Millisecond); // completed job held once
    check(!eng.NeedsAnalysis(base + 32 * Millisecond), "held completion cannot wake every duplicate");
    check(!eng.NeedsAnalysis(base + 2016 * Millisecond), "pause TTL boundary");
    check(eng.NeedsAnalysis(base + 2017 * Millisecond), "pause expiry wakes unchanged pixels");
    eng.AnalyzeAt(&img, base + 2017 * Millisecond);
    check(!eng.NeedsAnalysis(base + 2032 * Millisecond), "expired pause sleeps after discarding evidence");
}
} // namespace

int main(int argc, char** argv) {
    check(argc == 2, "usage: hudtext_vs_test <repo>");
    const std::string repo = argv[1];
    rules();
    registerAssets(repo);
    hudtext::Worker::Instance().SetLoader([repo](std::vector<uint8_t>& out, std::string* err) {
        if (readFile(repo + "/internal/ocr/neuraldata/recognizer.onnx", out)) return true;
        if (err) *err = "missing test model";
        return false;
    });
    for (const std::string label : {"opening", "swap"}) {
        RGBA img;
        check(loadFrame(repo + "/inbox/regressions/vs-account-side-20261107/" + label + ".png", img), "required user fixture " + label);
        config::Config calibrated = config::Default();
        config::RegionCalibration c; c.Profile = "camp"; c.LeftBeads = config::NormalizedRect{.4, .4, .05, .03};
        check(config::CalibrationRectIssue(false, *c.LeftBeads) == config::CalibrationIssue::None, "test calibration is actually usable");
        calibrated.Layout.Calibration = c;
        std::array<Rect, 2> automatic, changed;
        check(hudtext::NameLayout(config::Default().Layout).Regions(&img, "vs", automatic) &&
              hudtext::NameLayout(calibrated.Layout).Regions(&img, "vs", changed) && automatic == changed, "footer ignores battle calibration");
        staticPipeline(img, label);
        if (label == "opening") {
            gates(img); timeout(img); cancellation(img, false); cancellation(img, true); suffixCases(img, repo);
        }
    }
    pause();
    idle();
    std::printf("PASS hudtext_vs_test: %d assertions (real OCR/PNG + explicit scheduler unit tests)\n", checks);
}
