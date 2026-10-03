// nt/engine.h — internal/engine（owner: cpp-gated-frame；实现
//   src/engine/layout.cpp（DefaultLayout）、src/engine/configured_layout.cpp、src/engine/gated.cpp）
//
// Package engine 定义检测引擎抽象：把"给一张截图 → 返回画面与各豆状态"
// 抽象为接口，判色/模型推理都是接口的实现，可热插拔。
//
// Go 接口 → C++ 抽象类映射：
//   Engine + TimedEngine   → Engine（AnalyzeAt 为主入口；Analyze(img) = AnalyzeAt(img, NowNs())）
//   Gate + TimedGate       → Gate（DecideAt 为主入口；Decide(img) = DecideAt(img, NowNs())）
//   FightSupportGate       → FightSupportGate（Gated 用 dynamic_cast 检测，等价 Go 类型断言）
//   LayoutPreferrer        → LayoutPreferrer（同上，dynamic_cast）
//   LayoutProvider / ProfiledLayout → 同名抽象类；ConfiguredLayout 由 rgb 用 dynamic_cast 识别
//
// 线程模型：同一 Engine 实例只在单一分析线程上调用（JNI 层加全局锁），
// 因此 Gated / rgb::Engine 上 Go 的 sync.Mutex 在 C++ 中可以省略。
// 帧内并行（Go 的 goroutine：rgb camp/duel 与左右两侧、scene 模板打分、头像打分）
// 一律走 nt/parallel.h 的 ParallelFor/ParallelInvoke，结果必须与串行逐位一致。
#pragma once

#include <functional>
#include <map>
#include <memory>
#include <string>
#include <utility>
#include <vector>

#include "nt/config.h"
#include "nt/detect.h"
#include "nt/image.h"
#include "nt/result.h"
#include "nt/time.h"

namespace nt::engine {

// LayoutPreferrer receives only a template's explicit camp/duel binding.
class LayoutPreferrer {
public:
    virtual ~LayoutPreferrer() = default;
    virtual void Prefer(const std::string& kind) = 0;
};

// Engine 是检测引擎接口：输入一张客户区截图，输出画面与豆状态。
//  - img 为截图（可为 nullptr = Go nil）；返回的 BeadInfo 坐标必须是截图像素坐标。
//  - at 为采集时刻（CLOCK_MONOTONIC ns；0 = 零时刻，实现里按 Go 规则换成 NowNs()）。
class Engine {
public:
    virtual ~Engine() = default;
    virtual Result AnalyzeAt(const RGBA* img, TimeNs at) = 0;
    Result Analyze(const RGBA* img) { return AnalyzeAt(img, NowNs()); }
};

// LayoutProvider 提供豆位标定（逻辑坐标 → 截图像素坐标）。
class LayoutProvider {
public:
    virtual ~LayoutProvider() = default;
    // Positions 返回当前截图尺寸下所有豆位的实际像素坐标。
    virtual std::vector<detect::BeadPosition> Positions(int w, int h) const = 0;
    // Mode 返回当前内容模式（auto/stretch/letterbox）。
    virtual detect::ContentMode Mode() const = 0;
};

// ProfiledLayout supplies independently calibrated camp/duel positions.
class ProfiledLayout : public LayoutProvider {
public:
    virtual std::vector<detect::BeadPosition> PositionsFor(const std::string& profile, int w, int h) const = 0;
    virtual std::string PreferredProfile() const = 0;
};

// DefaultLayout 是默认豆位提供者：基于 detect 的 1920×1080 逻辑标定 +
// ComputeContentArea 等比映射。
class DefaultLayout final : public LayoutProvider {
public:
    // NewDefaultLayout 用默认豆位标定 + 内容模式构造提供者。
    explicit DefaultLayout(detect::ContentMode mode);
    // NewLayout 用自定义豆位标定构造提供者（空则用默认）。
    DefaultLayout(detect::ContentMode mode, std::vector<detect::Bead> beads);
    std::vector<detect::BeadPosition> Positions(int w, int h) const override;
    detect::ContentMode Mode() const override { return mode_; }

private:
    detect::ContentMode mode_;
    std::vector<detect::Bead> beads_;
};

// ConfiguredLayout is immutable after construction and shared by both engines.
class ConfiguredLayout final : public ProfiledLayout {
public:
    // NewConfiguredLayout
    ConfiguredLayout(detect::ContentMode mode, const config::LayoutConfig& cfg);

    detect::ContentMode Mode() const override { return mode_; }
    std::string PreferredProfile() const override { return preferred_; }
    std::vector<detect::BeadPosition> Positions(int w, int h) const override;
    std::vector<detect::BeadPosition> PositionsFor(const std::string& name, int w, int h) const override;

    // ContentArea is shared with scene matching. A size alone cannot prove bars.
    std::pair<detect::ContentArea, bool> ContentArea(const RGBA* img) const;
    Point ReferenceSize() const { return reference_; }
    std::vector<detect::BeadPosition> PositionsIn(const std::string& name, const detect::ContentArea& ca) const;

private:
    detect::ContentMode mode_;
    std::string preferred_;
    std::map<std::string, config::LayoutProfile> profiles_;  // "camp", "duel"
    int count_ = 0;
    Point reference_;
    double tolerance_ = 0;
};

// GateKind 是场景门闩的四种结论。场景名本身来自资源，不写死在代码里。
enum class GateKind { GateFight = 0, GateNotFight, GateUncertain, GateBlank };
constexpr GateKind GateFight = GateKind::GateFight;
constexpr GateKind GateNotFight = GateKind::GateNotFight;
constexpr GateKind GateUncertain = GateKind::GateUncertain;
constexpr GateKind GateBlank = GateKind::GateBlank;

// GateDecision 是门闩对一帧画面的判断。
struct GateDecision {
    GateKind Kind = GateFight;  // Go 零值 = GateFight(0)
    std::string SceneID;
    double Confidence = 0;
    std::string LayoutProfile;
    bool RoundOpening = false;
};

// Gate 只负责「这帧是不是对局」，不采豆。
class Gate {
public:
    virtual ~Gate() = default;
    // DecideAt receives the acquisition time so bounded rescans follow frame time.
    virtual GateDecision DecideAt(const RGBA* img, TimeNs at) = 0;
    GateDecision Decide(const RGBA* img) { return DecideAt(img, NowNs()); }
};

class FightSupportGate {
public:
    virtual ~FightSupportGate() = default;
    virtual bool SupportsFight(const RGBA* img, const std::string& profile) = 0;
};

// Identity 是 VS / 换人画面上认出的双方账号。
struct Identity {
    std::string Side;
    std::string Mine;
    std::string Opp;
};

// SideGuesser 在 VS / 换人画面上认人。空 function == Go nil。
using SideGuesser = std::function<Identity(const RGBA* img, const std::string& scene)>;

constexpr DurationNs profileSwitchConfirmation = 300 * Millisecond;
constexpr DurationNs profileEvidenceMaxGap = 300 * Millisecond;

// Gated 先过门闩，再把对局帧交给内层引擎采豆。
class Gated final : public Engine {
public:
    Gated(std::shared_ptr<Gate> gate, std::shared_ptr<Engine> inner, SideGuesser guess);
    Result AnalyzeAt(const RGBA* img, TimeNs at) override;

    std::shared_ptr<Gate> Gate_;
    std::shared_ptr<Engine> Inner;
    SideGuesser Guess;

private:
    void clearScene();
    Result analyzeInner(const RGBA* img, TimeNs at);
    bool supportsCurrent(const RGBA* img, const Result& res);

    std::string name_;
    GateDecision lastFight_;
    TimeNs lastAt_ = 0;
    Rect lastBounds_;
    std::string pendingProfile_;
    TimeNs pendingAt_ = 0;
    uint64_t pendingImage_ = 0;
};

// pixelHash distinguishes two captures during profile-switch confirmation
// (process-local 64-bit hash of the visible pixel rows, nt/hash.h).
uint64_t pixelHash(const RGBA* img);

bool hasBilateralHUD(const std::vector<BeadInfo>& beads);
int readyBeads(const std::vector<BeadInfo>& beads);

// NewGated 包装任意 Engine。inner 为 nullptr 时返回 nopEngine（Name "nop"），
// gate 为 nullptr 时直接返回 inner。
std::shared_ptr<Engine> NewGated(std::shared_ptr<Gate> gate, std::shared_ptr<Engine> inner);
std::shared_ptr<Engine> NewGatedWithGuess(std::shared_ptr<Gate> gate, std::shared_ptr<Engine> inner,
                                          SideGuesser guess);

}  // namespace nt::engine
