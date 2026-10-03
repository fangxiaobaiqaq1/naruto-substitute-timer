// Owner: cpp-gated-frame
// Port of: internal/engine/gated.go
// Contract: include/nt/engine.h — see android/ARCHITECTURE.md.
//
// Gated 先过门闩，再把对局帧交给内层引擎采豆。
// Go 的 g.mu（Prefer and Analyze must belong to the same frame）在 Android 上由 JNI 全局锁
// 保证（单分析线程），这里省略。
// Go 的 TimedGate / TimedEngine 类型断言：C++ 抽象类只有 DecideAt / AnalyzeAt 入口，
// 非 timed 的 Go 实现等价于传入 time.Now()，而它们本来就不使用时间参数。
#include "nt/engine.h"

#include <set>
#include <utility>

#include "nt/hash.h"

namespace nt::engine {

namespace {

// pixelHashSeed：进程内种子（Go maphash.MakeSeed()）。与帧指纹的种子区分开。
uint64_t pixelHashSeed() {
    static const uint64_t seed = ProcessHashSeed() ^ 0x7069786568617368ull;  // "pixehash"
    return seed;
}

class nopEngine final : public Engine {
public:
    Result AnalyzeAt(const RGBA*, TimeNs) override {
        Result r;
        r.Name = "nop";
        return r;
    }
};

}  // namespace

// pixelHash distinguishes two captures during profile-switch confirmation; a
// process-local 64-bit hash is sufficient and far cheaper than a digest.
// Go 哈希整个 img.Pix；这里只哈希可见像素行（stride 填充不是观测），逐行组合，
// 使得紧凑缓冲与带填充的 HardwareBuffer 得到同一结果。
uint64_t pixelHash(const RGBA* img) {
    uint64_t seed = pixelHashSeed();
    if (img == nullptr || img->Pix == nullptr) {
        return HashBytes(seed, nullptr, 0);
    }
    const Rect r = img->Bounds();
    const int rowBytes = r.Dx() * 4;
    if (rowBytes <= 0 || r.Dy() <= 0) {
        return HashBytes(seed, nullptr, 0);
    }
    uint64_t sum = 0;
    for (int y = 0; y < r.Dy(); ++y) {
        const uint8_t* row = img->Pix + static_cast<ptrdiff_t>(y) * img->Stride;
        sum = MixHash(sum, HashBytes(seed, row, static_cast<size_t>(rowBytes)));
    }
    return sum;
}

// NewGated 包装任意 Engine。gate 或 inner 为 nil 时退回 inner / 空结果。
std::shared_ptr<Engine> NewGated(std::shared_ptr<Gate> gate, std::shared_ptr<Engine> inner) {
    return NewGatedWithGuess(std::move(gate), std::move(inner), nullptr);
}

std::shared_ptr<Engine> NewGatedWithGuess(std::shared_ptr<Gate> gate, std::shared_ptr<Engine> inner,
                                          SideGuesser guess) {
    if (inner == nullptr) {
        return std::make_shared<nopEngine>();
    }
    if (gate == nullptr) {
        return inner;
    }
    return std::make_shared<Gated>(std::move(gate), std::move(inner), std::move(guess));
}

Gated::Gated(std::shared_ptr<Gate> gate, std::shared_ptr<Engine> inner, SideGuesser guess)
    : Gate_(std::move(gate)), Inner(std::move(inner)), Guess(std::move(guess)) {
    // Go: name := inner.Analyze(nil).Name
    name_ = Inner ? Inner->Analyze(nullptr).Name : std::string();
    if (name_.empty()) {
        name_ = "engine";
    }
}

Result Gated::AnalyzeAt(const RGBA* img, TimeNs at) {
    if (at == 0) {
        at = NowNs();
    }
    if (img == nullptr) {
        pendingProfile_.clear();  // An explicit missing frame breaks confirmation.
        Result r;
        r.Name = name_ + "+gate";
        r.Uncertain = true;
        return r;
    }
    if (img->Bounds() != lastBounds_ || (lastAt_ != 0 && at < lastAt_)) {
        clearScene();
        lastBounds_ = img->Bounds();
    }
    // Capture failures can bypass the engine altogether. Preserve the last
    // established scene, but never count missing time as candidate persistence.
    if (lastAt_ != 0 && (!(at > lastAt_) || at - lastAt_ > profileEvidenceMaxGap)) {
        pendingProfile_.clear();
    }
    lastAt_ = at;
    GateDecision d = Gate_->DecideAt(img, at);

    bool haveSampled = false;
    Result sampled;
    // Compare a competing label against fresh evidence in the established
    // coordinate system BEFORE changing it. Animated backgrounds may match a
    // different label twice; that is not evidence that a readable HUD moved.
    if (d.Kind == GateFight && !lastFight_.SceneID.empty() && d.LayoutProfile != lastFight_.LayoutProfile) {
        sampled = analyzeInner(img, at);
        haveSampled = true;
        if (supportsCurrent(img, sampled)) {
            d = lastFight_;
            d.Confidence = 0;  // Continuation, not a fresh primary-template score.
        }
    }
    // A single competing marker cannot switch coordinate profiles and erase
    // active clocks. Require a second independent image of the new profile.
    if (d.Kind == GateFight && !lastFight_.SceneID.empty() && d.LayoutProfile != lastFight_.LayoutProfile) {
        // The pending image is hashed once when the candidate starts; later
        // frames are hashed only once the confirmation window has elapsed, as
        // earlier frames stay uncertain regardless of their pixels.
        if (pendingProfile_ != d.LayoutProfile) {
            pendingProfile_ = d.LayoutProfile;
            pendingAt_ = at;
            pendingImage_ = pixelHash(img);
            d = GateDecision{};
            d.Kind = GateUncertain;
        } else if (at - pendingAt_ < profileSwitchConfirmation || pixelHash(img) == pendingImage_) {
            d = GateDecision{};
            d.Kind = GateUncertain;
        } else {
            pendingProfile_.clear();
        }
    } else {
        pendingProfile_.clear();
    }
    // Explicit ordinary pages, including VS and result, take priority over any
    // old battle context. They must never be hidden by a sticky fight state.
    if (d.Kind == GateNotFight || (d.Kind != GateFight && !d.SceneID.empty() && d.SceneID != "blank")) {
        clearScene();
    }
    Identity id;
    if (Guess) {
        id = Guess(img, d.SceneID);
    }

    if (auto* p = dynamic_cast<LayoutPreferrer*>(Inner.get())) {
        if (d.Kind == GateNotFight) {
            p->Prefer("");
        } else if (d.Kind == GateFight && (d.LayoutProfile == "camp" || d.LayoutProfile == "duel")) {
            if (haveSampled && sampled.LayoutProfile != d.LayoutProfile) {
                haveSampled = false;  // A confirmed transition needs the new coordinates.
            }
            p->Prefer(d.LayoutProfile);
        }
    }

    // 大厅/结算：明确不在对局，不采豆。
    if (d.Kind == GateNotFight) {
        Result r;
        r.Name = name_ + "+gate";
        r.Fighting = false;
        r.Scene = d.SceneID;
        r.GateScore = d.Confidence;
        r.RoundOpening = d.RoundOpening;
        r.PlayerSide = id.Side;
        r.PlayerName = id.Mine;
        r.OppName = id.Opp;
        return r;
    }

    // 对局、换人、看不清：都采豆。模板偶发认丢不能把豆清空。
    Result res;
    if (haveSampled) {
        res = std::move(sampled);
    } else {
        res = analyzeInner(img, at);
    }
    if (res.Name.empty()) {
        res.Name = name_;
    }
    res.Name += "+gate";
    res.Scene = d.SceneID;
    res.GateScore = d.Confidence;
    res.RoundOpening = d.RoundOpening;
    res.PlayerSide = id.Side;
    res.PlayerName = id.Mine;
    res.OppName = id.Opp;
    if (d.Kind == GateFight) {
        lastFight_ = d;
        res.Fighting = true;
        if (res.Beads.empty()) {
            res.Uncertain = true;
        }
        return res;
    }
    if ((d.SceneID.empty() || d.SceneID == "blank") && !lastFight_.SceneID.empty()) {
        // Require current evidence in three separate areas: a battle control
        // and readable calibrated beads at BOTH corners. Never reuse old beads.
        if (d.Kind != GateBlank && supportsCurrent(img, res)) {
            res.Scene = lastFight_.SceneID;
            res.Fighting = true;
            return res;
        }
        // Retain context, NEVER permission to count. An occlusion is not a
        // scene transition, regardless of duration. Only current bilateral HUD
        // + control can resume observation; explicit pages/geometry clear it.
        res.Scene = lastFight_.SceneID;
    }
    if (!d.SceneID.empty()) {
        // VS / 换人 / 选人：场景认出来了，冻结，不开钟。
        res.Fighting = false;
        res.Uncertain = true;
        return res;
    }
    // No scene evidence: colored pixels cannot prove that a match is in progress.
    res.Fighting = false;
    res.Uncertain = true;
    return res;
}

void Gated::clearScene() {
    lastFight_ = GateDecision{};
    pendingProfile_.clear();
}

Result Gated::analyzeInner(const RGBA* img, TimeNs at) { return Inner->AnalyzeAt(img, at); }

bool Gated::supportsCurrent(const RGBA* img, const Result& res) {
    if (res.Uncertain || !hasBilateralHUD(res.Beads) || res.LayoutProfile != lastFight_.LayoutProfile) {
        return false;
    }
    auto* support = dynamic_cast<FightSupportGate*>(Gate_.get());
    return support != nullptr && support->SupportsFight(img, lastFight_.LayoutProfile);
}

bool hasBilateralHUD(const std::vector<BeadInfo>& beads) {
    int known[2] = {0, 0};
    std::set<std::string> seen;
    for (const BeadInfo& b : beads) {
        if (b.Unknown || b.Conf < .6 || b.Label.size() < 2 || seen.count(b.Label) != 0) {
            continue;
        }
        seen.insert(b.Label);
        if (b.Label[0] == 'L') {
            known[0]++;
        }
        if (b.Label[0] == 'R') {
            known[1]++;
        }
    }
    return known[0] >= 3 && known[1] >= 3;
}

int readyBeads(const std::vector<BeadInfo>& beads) {
    int n = 0;
    for (const BeadInfo& b : beads) {
        if (b.Lit && !b.Unknown) {
            n++;
        }
    }
    return n;
}

}  // namespace nt::engine
