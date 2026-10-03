// nt/scene.h — internal/scene（owner: cpp-scene；实现 src/scene/scene.cpp）
//
// Package scene 用模板清单判断当前画面属于哪个场景。
// 场景名、ROI、阈值全部来自资源文件，代码里不写死「决斗场」。
//
// 资源：清单 AssetStore::Text("templates/manifest.json")，模板/掩膜
// AssetStore::Image("templates/" + file)。模板灰度用 match::ToGrayAsset。
// Android 上永远是「内置清单」路径（cfg.Scene.Manifest 被忽略，等价 Go embedded 分支）。
#pragma once

#include <map>
#include <memory>
#include <mutex>
#include <set>
#include <string>
#include <utility>
#include <vector>

#include "nt/config.h"
#include "nt/detect.h"
#include "nt/engine.h"
#include "nt/match.h"
#include "nt/time.h"

namespace nt::scene {

inline const std::string DefaultManifest = "assets/templates/manifest.json";
// AssetStore key of the embedded manifest.
inline const std::string ManifestAsset = "templates/manifest.json";

struct TemplateSpec {
    double TemplateScale = 0;     // "templateScale"
    bool ContinuationOnly = false;  // "continuationOnly"
    bool RoundOpening = false;    // "roundOpening"
    std::string LayoutProfile;    // "layoutProfile"
    std::string ID;               // "id"
    std::string File;             // "file"
    std::string Mask;             // "mask"
    std::string Scene;            // "scene"
    config::NormalizedRect ROI;   // "roi": {x,y,width,height}
    double Threshold = 0;         // "threshold"
};

struct Manifest {
    std::map<std::string, int> MinimumRegions;  // "minimumRegions"
    int ReferenceWidth = 0;                     // "reference": {"width"}
    int ReferenceHeight = 0;                    // "reference": {"height"}
    std::vector<TemplateSpec> Templates;        // "templates"
};

struct template_ {
    TemplateSpec spec;
    Gray gray;
    Gray mask;  // Nil() == 无掩膜
    int refW = 0;
    int refH = 0;
};

// Prepared images are immutable after construction. Only the current content
// geometry is retained so resizing cannot grow the cache indefinitely.
struct preparedTemplate {
    TemplateSpec spec;
    Rect roi;
    Gray gray, mask;  // gray.Nil() == Go t.gray == nil（模板太小/ROI 太小，不参与匹配）
    std::shared_ptr<const match::PreparedNCC> ncc;
    int index = 0;          // Position in Catalog.templates; keys the peak hint.
    bool windowed = false;  // ROI narrowed to the remembered peak; a miss keeps the hint.
};

struct templateHint {
    Point peak;
    bool valid = false;
};

// Hit 是单条模板的分数，供诊断打印。
struct Hit {
    std::string ID;
    std::string Scene;
    double Value = 0;
};

constexpr int coldScanEvery = 3;
constexpr DurationNs coldScanInterval = 100 * Millisecond;
// hintRadius bounds the window checked around a remembered peak, in pixels.
constexpr int hintRadius = 2;

// Catalog 是已加载的模板库。实现 engine::Gate（DecideAt）与 engine::FightSupportGate。
// Go 的 scorer / scoreAtHint / scoreHinted / scoreOne / matchIn / forEach 由 cpp-scene 在
// scene.cpp 内自行声明（可加为私有成员）；forEach 用 nt::ParallelFor，hints 的读写按 Go hintMu 加锁。
class Catalog final : public engine::Gate, public engine::FightSupportGate {
public:
    // DecideAt implements engine.TimedGate.
    engine::GateDecision DecideAt(const RGBA* img, TimeNs at) override;
    // SupportsFight checks a separate, static battle control.
    bool SupportsFight(const RGBA* img, const std::string& profile) override;
    // ScoreAll 返回每条模板的分数，不表决（诊断用，不读写 hint）。
    std::vector<Hit> ScoreAll(const RGBA* img);
    int TemplateCount() const { return static_cast<int>(templates.size()); }

    // --- 内部（与 Go 未导出字段/方法同名） ---
    bool hasOther() const;
    std::pair<detect::ContentArea, bool> contentArea(const RGBA* img) const;
    const std::vector<preparedTemplate>& prepare(const detect::ContentArea& ca);
    bool coldScanDue(TimeNs at, bool hotFight);
    void recordColdScan(TimeNs at, const std::vector<Hit>& hits);
    Hit lastColdHit(int index) const;
    bool hint(int index, templateHint& out) const;
    void setHint(int index, Point peak, bool valid);
    std::vector<Hit> scoreAll(const RGBA* img, const std::vector<preparedTemplate>& prepared, TimeNs at);

    std::map<std::string, int> minimumRegions;
    std::vector<template_> templates;
    config::SceneConfig cfg;
    detect::ContentMode mode = detect::ModeAuto;
    config::LayoutConfig layoutCfg;
    match::NCC matcher;
    std::set<std::string> fightSet, endSet, holdSet;
    bool preparedValid = false;
    detect::ContentArea preparedArea;
    std::vector<preparedTemplate> prepared_;
    mutable std::mutex hintMu;  // Go hintMu：并行打分时保护 hints
    std::vector<templateHint> hints;
    TimeNs coldAt = 0;
    int coldFrames = 0;
    std::vector<Hit> lastCold;
};

// Load 读取内置清单。返回 nullptr 且 err 为空 == Go (nil, nil)（未启用 / 无模板）；
// 返回 nullptr 且 err 非空 == Go 的 error。
std::shared_ptr<Catalog> Load(const config::Config& cfg, std::string* err = nullptr);
detect::ContentMode parseMode(const std::string& s);

// prepareOne / mapRect / lumaBlank：同 Go。
preparedTemplate prepareOne(const detect::ContentArea& ca, const template_& t);
Rect mapRect(const detect::ContentArea& ca, const config::NormalizedRect& r);
// 返回 blank；kind 写入门闩结论（GateUncertain / GateBlank）。
bool lumaBlank(const RGBA* img, engine::GateKind& kind);

// ColorGate 把旧的 ClassifyScreen 包成 Gate，供无模板时回退。
class ColorGate final : public engine::Gate {
public:
    explicit ColorGate(detect::ContentMode mode) : Mode(mode) {}
    engine::GateDecision DecideAt(const RGBA* img, TimeNs at) override;
    detect::ContentMode Mode;
};

// NewGate 优先用模板目录；没有可用模板则回退颜色密度。
// description 写入 "templates:N" 或 "color-fallback"。失败返回 nullptr 并写 err。
std::shared_ptr<engine::Gate> NewGate(const config::Config& cfg, std::string* description = nullptr,
                                      std::string* err = nullptr);

}  // namespace nt::scene
