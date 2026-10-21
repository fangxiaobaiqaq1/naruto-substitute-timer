// Owner: cpp-gated-frame
// Port of: internal/engine/factory/factory.go + factory_plain.go（!opencv 构建）
// Contract: include/nt/factory.h — see android/ARCHITECTURE.md.
//
// Package factory 提供引擎工厂：按配置构造检测引擎（默认 RGB）。
// 独立子包避免 import cycle（engine 接口包不依赖任何引擎实现）。
// Android 只有 plain 构建：defaultKind = KindRGB，opencv/model 返回 UnsupportedError。
// NewLive（hudtext OCR 包装）与 MustDefault 不移植：OCR 已移除，构建失败由 Pipeline 报告。
#include "nt/factory.h"

#include "nt/identity.h"
#include "nt/rgb.h"
#include "nt/scene.h"

namespace nt::factory {

namespace {

const Kind& defaultKind() { return KindRGB; }

// UnsupportedError 表示引擎类型未实现/不支持。
std::string unsupportedError(const std::string& kind) {
    return "引擎类型暂不支持: " + kind + "（当前支持 rgb；opencv 需 -tags opencv）";
}

void setErr(std::string* err, const std::string& msg) {
    if (err != nullptr) {
        *err = msg;
    }
}

std::shared_ptr<engine::Engine> newInner(const Config& cfg, std::string* err) {
    if (cfg.Kind_.empty() || cfg.Kind_ == KindRGB) {
        return std::make_shared<rgb::Engine>(std::make_shared<engine::ConfiguredLayout>(cfg.Mode, cfg.App.Layout),
                                             cfg.App.Vision);
    }
    // KindOpenCV（plain 构建的 newOpenCV）、KindModel 与其它值一律不支持。
    setErr(err, unsupportedError(cfg.Kind_));
    return nullptr;
}

engine::SideGuesser adaptGuesser(const config::Config& cfg) {
    identity::GuessFunc g = identity::Guesser(cfg);
    if (!g) {
        return nullptr;
    }
    return [g](const RGBA* img, const std::string& scene) {
        identity::Readout r = g(img, scene);
        return engine::Identity{r.Side, r.Mine, r.Opp};
    };
}

}  // namespace

// DefaultConfig 返回默认配置。
Config DefaultConfig() { return FromApp(config::Default()); }

// FromApp 用已加载的应用配置构造引擎工厂配置。
Config FromApp(const config::Config& appCfg) {
    detect::ContentMode mode = detect::ModeAuto;
    if (!detect::ParseMode(appCfg.Layout.ContentMode, mode)) {
        mode = detect::ModeAuto;
    }
    Config c;
    c.Kind_ = defaultKind();
    c.Mode = mode;
    c.App = appCfg;
    return c;
}

// New 按配置构造引擎。
std::shared_ptr<engine::Engine> New(const Config& cfg, std::string* err) {
    std::string innerErr;
    std::shared_ptr<engine::Engine> inner = newInner(cfg, &innerErr);
    if (inner == nullptr) {
        setErr(err, innerErr);
        return nullptr;
    }
    std::string gateErr;
    std::shared_ptr<engine::Gate> gate = scene::NewGate(cfg.App, nullptr, &gateErr);
    if (gate == nullptr) {
        // Go 的 scene.NewGate 只有出错时才返回 nil（无模板时回退 ColorGate）；
        // 这里同样把 nullptr 当作错误，绝不悄悄退化成无门闩引擎。
        setErr(err, gateErr.empty() ? std::string("scene gate unavailable") : gateErr);
        return nullptr;
    }
    return engine::NewGatedWithGuess(gate, inner, adaptGuesser(cfg.App));
}

}  // namespace nt::factory
