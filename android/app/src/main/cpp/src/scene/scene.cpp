// Owner: cpp-scene（由 integrator 补齐）
// Port of: internal/scene/scene.go
// Contract: include/nt/scene.h — see android/ARCHITECTURE.md.
//
// Package scene 用模板清单判断当前画面属于哪个场景。
// 场景名、ROI、阈值全部来自资源文件，代码里不写死「决斗场」。
// 忠实移植 Go 原算法：阈值、ROI、常量、判定顺序逐行对应，保留解释"为什么"的中文注释。
//
// 与 Go 的差异：
//  * Android 永远走「内置清单」分支（AssetStore "templates/manifest.json"）；外置 manifest 不支持。
//  * prepareMu（Engine 级锁）省略：Catalog 只在单一分析线程上调用（JNI 全局锁）。hintMu 保留。
//  * Go 的 scorer 闭包 → 本文件内的 Scorer 类（视图缓存同样按 ROI 共享、加锁）。
#include "nt/scene.h"

#include <algorithm>
#include <cmath>
#include <functional>
#include <map>
#include <memory>
#include <mutex>
#include <string>
#include <vector>

#include "nt/assets.h"
#include "nt/json.h"
#include "nt/parallel.h"

namespace nt::scene {

namespace {

std::string quote(const std::string& s) { return "\"" + s + "\""; }

// readGray：内置分支 assets.Templates.ReadFile("templates/"+name) → image.Decode → match.ToGray。
// 不存在返回 false 且 notExist=true（Go os.IsNotExist → 跳过该模板）。
bool readGray(const std::string& name, Gray& out, bool& notExist) {
    notExist = false;
    auto img = AssetStore::Instance().Image("templates/" + name);
    if (!img) {
        notExist = true;
        return false;
    }
    out = match::ToGrayAsset(*img);
    return true;
}

// Go encoding/json 语义：字段缺失或为 null 保持零值；类型不符是解码错误。
// （Go 的键名匹配不区分大小写；内置清单键名全部规范，这里按精确键名读取。）
template <class Pred>
bool fieldOK(const json& obj, const char* key, Pred pred) {
    auto it = obj.find(key);
    return it == obj.end() || it->is_null() || pred(*it);
}

bool isNumber(const json& v) { return v.is_number(); }
bool isInteger(const json& v) { return v.is_number_integer(); }
bool isBool(const json& v) { return v.is_boolean(); }
bool isString(const json& v) { return v.is_string(); }
bool isObject(const json& v) { return v.is_object(); }
bool isArray(const json& v) { return v.is_array(); }

bool parseRect(const json& obj, config::NormalizedRect& r) {
    auto it = obj.find("roi");
    if (it == obj.end() || it->is_null()) return true;
    if (!it->is_object()) return false;
    for (const char* k : {"x", "y", "width", "height"}) {
        if (!fieldOK(*it, k, isNumber)) return false;
    }
    r.X = JsonNumber(*it, "x");
    r.Y = JsonNumber(*it, "y");
    r.Width = JsonNumber(*it, "width");
    r.Height = JsonNumber(*it, "height");
    return true;
}

bool parseSpec(const json& t, TemplateSpec& s) {
    if (t.is_null()) return true;  // Go：null 元素解码为零值 TemplateSpec。
    if (!t.is_object()) return false;
    if (!fieldOK(t, "templateScale", isNumber) || !fieldOK(t, "continuationOnly", isBool) ||
        !fieldOK(t, "roundOpening", isBool) || !fieldOK(t, "layoutProfile", isString) ||
        !fieldOK(t, "id", isString) || !fieldOK(t, "file", isString) || !fieldOK(t, "mask", isString) ||
        !fieldOK(t, "scene", isString) || !fieldOK(t, "threshold", isNumber)) {
        return false;
    }
    s.TemplateScale = JsonNumber(t, "templateScale");
    s.Anchor = JsonString(t, "anchor");
    s.ContinuationOnly = JsonBool(t, "continuationOnly");
    s.RoundOpening = JsonBool(t, "roundOpening");
    s.LayoutProfile = JsonString(t, "layoutProfile");
    s.ID = JsonString(t, "id");
    s.File = JsonString(t, "file");
    s.Mask = JsonString(t, "mask");
    s.Scene = JsonString(t, "scene");
    if (!parseRect(t, s.ROI)) return false;
    s.Threshold = JsonNumber(t, "threshold");
    return true;
}

bool parseManifest(const std::string& data, Manifest& man, std::string& why) {
    json doc = json::parse(data, nullptr, /*allow_exceptions=*/false);
    if (doc.is_discarded()) {
        why = "invalid JSON";
        return false;
    }
    if (doc.is_null()) return true;  // Go：json.Unmarshal("null") 不报错，清单保持零值。
    if (!doc.is_object() || !fieldOK(doc, "minimumRegions", isObject) || !fieldOK(doc, "reference", isObject) ||
        !fieldOK(doc, "templates", isArray)) {
        why = "json: cannot unmarshal into scene.Manifest";
        return false;
    }
    auto mr = doc.find("minimumRegions");
    if (mr != doc.end() && mr->is_object()) {
        for (auto it = mr->begin(); it != mr->end(); ++it) {
            if (it.value().is_null()) {
                man.MinimumRegions[it.key()] = 0;
                continue;
            }
            if (!it.value().is_number_integer()) {
                why = "json: cannot unmarshal minimumRegions";
                return false;
            }
            man.MinimumRegions[it.key()] = it.value().get<int>();
        }
    }
    auto ref = doc.find("reference");
    if (ref != doc.end() && ref->is_object()) {
        if (!fieldOK(*ref, "width", isInteger) || !fieldOK(*ref, "height", isInteger)) {
            why = "json: cannot unmarshal reference";
            return false;
        }
        man.ReferenceWidth = JsonInt(*ref, "width");
        man.ReferenceHeight = JsonInt(*ref, "height");
    }
    auto ts = doc.find("templates");
    if (ts != doc.end() && ts->is_array()) {
        for (const auto& t : *ts) {
            TemplateSpec s;
            if (!parseSpec(t, s)) {
                why = "json: cannot unmarshal template";
                return false;
            }
            man.Templates.push_back(std::move(s));
        }
    }
    return true;
}

const Gray* maskOrNull(const Gray& m) { return m.Nil() ? nullptr : &m; }

bool matchIn(const Catalog& c, const RGBA& img, const Gray& gray, const Rect& roi, const preparedTemplate& t,
             match::Score& s) {
    match::Query q;
    q.Image = &img;
    q.Gray = &gray;
    q.ROI = roi;
    q.Template = &t.gray;
    q.Mask = maskOrNull(t.mask);
    q.Prepared = t.ncc.get();
    if (!c.matcher.Match(q, s)) return false;
    if (!t.spec.ContinuationOnly || t.spec.Anchor != "screen" || s.Value < .65 || s.Value >= t.spec.Threshold) return true;
    const Point peak = s.Peak;
    // The +/-2px size variants are pure functions of the prepared template, so
    // they are scaled and prepared once per geometry (refineVariants) instead
    // of on every refinement; Match with Prepared == PrepareNCC(g, mask) is
    // exactly Match with Template = g, Mask = mask.
    const refineVariants* variants = t.refine ? &t.refine->get(t) : nullptr;
    for (int dw = -2; dw <= 2; ++dw) {
        for (int dh = -2; dh <= 2; ++dh) {
            const int w = t.gray.Bounds().Dx() + dw, h = t.gray.Bounds().Dy() + dh;
            if ((dw == 0 && dh == 0) || w < 4 || h < 4) continue;
            Gray g, mask;
            std::shared_ptr<const match::PreparedNCC> owned;
            const match::PreparedNCC* variant = nullptr;
            if (variants != nullptr) {
                variant = variants->at(dw, dh).get();
            } else {
                g = match::ScaleGray(&t.gray, w, h);
                if (!t.mask.Nil()) mask = match::ScaleGray(&t.mask, w, h);
                owned = match::PrepareNCC(&g, maskOrNull(mask));
                variant = owned.get();
            }
            q.ROI = Rect{peak, peak.Add(Pt(w, h))}.Inset(-2).Intersect(roi);
            q.Template = nullptr;
            q.Mask = nullptr;
            q.Prepared = variant;
            match::Score candidate;
            if (c.matcher.Match(q, candidate) && candidate.Value > s.Value) s = candidate;
        }
    }
    return true;
}

// scoreHinted searches roi (t.roi, or a remembered peak window when windowed)
// and remembers the peak when the template is present, or clears a stale hint.
// Every value is a real NCC score on this frame; the diagnostics path
// (scoreOne) never touches hints.
Hit scoreHinted(Catalog& c, const RGBA& img, const Gray& gray, const Rect& roi, bool windowed,
                const preparedTemplate& t) {
    if (t.gray.Nil()) return Hit{t.spec.ID, t.spec.Scene, 0};
    match::Score s;
    if (!matchIn(c, img, gray, roi, t, s)) {
        if (!windowed) c.setHint(t.index, Point{}, false);
        return Hit{t.spec.ID, t.spec.Scene, 0};
    }
    if (s.Value >= t.spec.Threshold) {
        c.setHint(t.index, s.Peak, true);
    } else if (!windowed) {
        c.setHint(t.index, Point{}, false);  // Only a full-ROI miss forgets the peak.
    }
    return Hit{t.spec.ID, t.spec.Scene, s.Value, Rect{s.Peak, s.Peak.Add(t.gray.Bounds().Size())}};
}

// scoreOne is the exhaustive form used by diagnostics; it never reads hints.
Hit scoreOne(const Catalog& c, const RGBA& img, const Gray& gray, const preparedTemplate& t) {
    if (t.gray.Nil()) return Hit{t.spec.ID, t.spec.Scene, 0};
    match::Score s;
    if (!matchIn(c, img, gray, t.roi, t, s)) return Hit{t.spec.ID, t.spec.Scene, 0};
    return Hit{t.spec.ID, t.spec.Scene, s.Value, Rect{s.Peak, s.Peak.Add(t.gray.Bounds().Size())}};
}

// grayView is one searched rectangle converted to gray, shared by every
// template on that rectangle. (Prefix sums stay per NCC call in a reused
// per-thread buffer: per-view prefixes would allocate megabytes per frame.)
struct grayView {
    RGBA img;
    Gray gray;
};

std::shared_ptr<grayView> newGrayView(const RGBA* img, const Rect& roi) {
    auto v = std::make_shared<grayView>();
    v->img = img->SubImage(roi);
    v->gray = match::ToGray(&v->img);
    return v;
}

// scorer converts only searched rectangles, not the animation/background of
// an entire frame. Variants with the same ROI share one gray image. Every view
// belongs to this call; concurrent catalogs cannot race on reused pixel buffers.
// The view map is shared by parallel template scoring; a view converted twice
// for the same ROI holds identical pixels, so either copy yields the same score.
class Scorer {
public:
    Scorer(Catalog& c, const RGBA* img) : c_(c), img_(img) {}

    // window (optional) narrows the search to a remembered peak window.
    Hit operator()(const preparedTemplate& t, const Rect* window = nullptr) {
        Rect roi = (window != nullptr ? *window : t.roi).Intersect(img_->Bounds());
        if (roi.Empty() || t.gray.Nil()) return Hit{t.spec.ID, t.spec.Scene, 0};
        std::shared_ptr<const grayView> view = viewFor(roi);
        return scoreHinted(c_, view->img, view->gray, roi, window != nullptr, t);
    }

    // one is the diagnostics form (scoreOne): whole ROI, hints untouched.
    Hit one(const preparedTemplate& t) {
        Rect roi = t.roi.Intersect(img_->Bounds());
        if (roi.Empty() || t.gray.Nil()) return Hit{t.spec.ID, t.spec.Scene, 0};
        std::shared_ptr<const grayView> view = viewFor(roi);
        return scoreOne(c_, view->img, view->gray, t);
    }

private:
    std::shared_ptr<const grayView> viewFor(const Rect& roi) {
        std::shared_ptr<const grayView> view;
        {
            std::lock_guard<std::mutex> lk(mu_);
            for (const auto& kv : views_) {
                if (kv.first == roi) {
                    view = kv.second;
                    break;
                }
            }
        }
        if (!view) {
            std::shared_ptr<grayView> v = newGrayView(img_, roi);
            std::lock_guard<std::mutex> lk(mu_);
            for (const auto& kv : views_) {
                if (kv.first == roi) {
                    view = kv.second;
                    break;
                }
            }
            if (!view) {
                views_.emplace_back(roi, v);
                view = std::move(v);
            }
        }
        return view;
    }

    Catalog& c_;
    const RGBA* img_;
    std::mutex mu_;
    // A handful of rectangles per frame: a flat list beats a tree here.
    std::vector<std::pair<Rect, std::shared_ptr<const grayView>>> views_;
};

// scoreAtHint checks only the remembered peak window. A template that still
// scores at or above its threshold there is present and its peak is refreshed;
// the boolean is false when there is no hint or the window misses.
bool scoreAtHint(Catalog& c, const RGBA* img, Scorer& score, const preparedTemplate& t, Hit& out) {
    templateHint hint;
    if (!c.hint(t.index, hint) || t.gray.Nil()) return false;
    Rect roi = t.roi.Intersect(img->Bounds());
    if (roi.Empty()) return false;
    Point size = t.gray.Bounds().Size();
    Rect window = Rect(hint.peak.Sub(Point(hintRadius, hintRadius)),
                       hint.peak.Add(Point(hintRadius, hintRadius)).Add(size))
                      .Intersect(roi);
    if (window.Dx() < size.X || window.Dy() < size.Y) return false;
    Hit hit = score(t, &window);
    if (hit.Value < t.spec.Threshold) return false;
    out = hit;
    return true;
}

// forEach calls fn for 0..n-1 on a bounded worker pool, or inline when there is
// too little work to share. 内置 NCC 无状态，所以总是允许并行（Go parallelScoring）。
void forEach(int n, const std::function<void(int)>& fn) { ParallelFor(n, fn); }

}  // namespace

// Load 默认读取随 EXE 更新的内置资源。Android 上永远是内置清单。
// 文件不存在或 templates 为空时返回 (nil, nil)，由上层回退旧门闩。
std::shared_ptr<Catalog> Load(const config::Config& cfg, std::string* err) {
    if (err) err->clear();
    const config::SceneConfig& sc = cfg.Scene;
    if (!sc.Enabled) return nullptr;
    std::string path = sc.Manifest;
    if (path.empty()) path = DefaultManifest;
    auto data = AssetStore::Instance().Text(ManifestAsset);
    if (!data) return nullptr;  // os.IsNotExist → (nil, nil)
    Manifest man;
    std::string why;
    if (!parseManifest(*data, man, why)) {
        if (err) *err = "scene: decode manifest " + quote(path) + ": " + why;
        return nullptr;
    }
    for (const auto& kv : man.MinimumRegions) {
        if (kv.first.empty() || kv.second < 1 || kv.second > 8) {
            if (err) *err = "scene: invalid minimumRegions " + quote(kv.first) + "=" + std::to_string(kv.second);
            return nullptr;
        }
    }
    int refW = man.ReferenceWidth, refH = man.ReferenceHeight;
    if (refW <= 0) refW = cfg.Layout.ReferenceWidth;
    if (refH <= 0) refH = cfg.Layout.ReferenceHeight;
    if (refW <= 0) refW = detect::LogicWidth;
    if (refH <= 0) refH = detect::LogicHeight;

    auto out = std::make_shared<Catalog>();
    out->minimumRegions = man.MinimumRegions;
    out->cfg = sc;
    out->mode = parseMode(cfg.Layout.ContentMode);
    out->layoutCfg = cfg.Layout;
    for (const auto& id : sc.FightScenes) out->fightSet.insert(id);
    for (const auto& id : sc.EndScenes) out->endSet.insert(id);
    for (const auto& id : sc.HoldScenes) out->holdSet.insert(id);

    for (TemplateSpec spec : man.Templates) {
        if (spec.Anchor != "" && spec.Anchor != "legacy" && spec.Anchor != "screen" && spec.Anchor != "center" && spec.Anchor != "left" && spec.Anchor != "right") {
            if (err) *err = "scene: template " + spec.ID + ": unsupported anchor " + quote(spec.Anchor);
            return nullptr;
        }
        if (spec.TemplateScale != 0 && (spec.TemplateScale < 0.5 || spec.TemplateScale > 2)) {
            if (err) *err = "scene: template " + spec.ID + ": templateScale must be between 0.5 and 2";
            return nullptr;
        }
        if (!spec.LayoutProfile.empty() && spec.LayoutProfile != "camp" && spec.LayoutProfile != "duel") {
            if (err) *err = "scene: template " + spec.ID + ": unsupported layoutProfile " + quote(spec.LayoutProfile);
            return nullptr;
        }
        if (spec.RoundOpening && (!out->fightSet.count(spec.Scene) || spec.LayoutProfile.empty())) {
            if (err) *err = "scene: template " + spec.ID + ": roundOpening requires a fight scene and layout profile";
            return nullptr;
        }
        if (spec.ID.empty() || spec.File.empty() || spec.Scene.empty()) {
            if (err) *err = "scene: template missing id/file/scene";
            return nullptr;
        }
        if (spec.Threshold <= 0) {
            spec.Threshold = sc.MinFightScore;
            if (!out->fightSet.count(spec.Scene) && sc.MinOtherScore > 0) spec.Threshold = sc.MinOtherScore;
            if (spec.Threshold <= 0) spec.Threshold = 0.82;
        }
        Gray gray;
        bool notExist = false;
        if (!readGray(spec.File, gray, notExist)) {
            if (notExist) continue;
            if (err) *err = "scene: template " + spec.ID + ": decode failed";
            return nullptr;
        }
        Gray mask;
        if (!spec.Mask.empty()) {
            if (!readGray(spec.Mask, mask, notExist)) {
                if (notExist) continue;
                if (err) *err = "scene: mask " + spec.ID + ": decode failed";
                return nullptr;
            }
        }
        template_ t;
        t.spec = spec;
        t.gray = std::move(gray);
        t.mask = std::move(mask);
        t.refW = refW;
        t.refH = refH;
        out->templates.push_back(std::move(t));
    }
    if (out->templates.empty()) return nullptr;
    return out;
}

detect::ContentMode parseMode(const std::string& s) {
    detect::ContentMode mode = detect::ModeAuto;
    if (!detect::ParseMode(s, mode, nullptr)) return detect::ModeAuto;
    return mode;
}

// DecideAt implements engine.TimedGate. Acquisition time bounds how long
// templates without a remembered peak may go unscanned during a confirmed fight.
engine::GateDecision Catalog::DecideAt(const RGBA* img, TimeNs at) {
    using engine::GateDecision;
    if (img == nullptr) return GateDecision{engine::GateUncertain, "", 0, "", false};
    engine::GateKind blankKind = engine::GateFight;
    if (lumaBlank(img, blankKind)) return GateDecision{blankKind, "blank", 0, "", false};
    auto [ca, supported] = contentArea(img);
    if (!supported) return GateDecision{engine::GateUncertain, "unsupported-resolution", 0, "", false};
    if (at == 0) at = NowNs();
    const std::vector<preparedTemplate>& prepared = prepare(ca);
    std::vector<Hit> hits = scoreAll(img, prepared, at);
    double bestFight = 0, bestOther = 0;
    double bestRaw = 0;
    std::map<std::string, double> profileScores;
    std::map<std::string, std::vector<Rect>> otherRegions;
    std::string fightID, otherID;
    std::string fightLayout;
    double openingScore = 0;
    std::string openingLayout;
    for (size_t i = 0; i < prepared.size(); ++i) {
        const preparedTemplate& t = prepared[i];
        if (t.spec.ContinuationOnly) continue;
        const Hit& hit = hits[i];
        bestRaw = std::max(bestRaw, hit.Value);
        if (hit.Value < t.spec.Threshold) continue;
        if (fightSet.count(t.spec.Scene)) {
            double& ps = profileScores[t.spec.LayoutProfile];
            ps = std::max(ps, hit.Value);
            if (t.spec.RoundOpening && hit.Value > openingScore) {
                openingScore = hit.Value;
                openingLayout = t.spec.LayoutProfile;
            }
            if (hit.Value > bestFight) {
                bestFight = hit.Value;
                fightID = t.spec.Scene;
                fightLayout = t.spec.LayoutProfile;
            }
        } else {
            bool independent = true;
            std::vector<Rect>& regions = otherRegions[t.spec.Scene];
            for (const Rect& previous : regions) {
                Rect overlap = previous.Intersect(t.roi);
                if (overlap.Dx() * overlap.Dy() * 4 >
                    std::min(previous.Dx() * previous.Dy(), t.roi.Dx() * t.roi.Dy())) {
                    independent = false;
                }
            }
            if (independent) regions.push_back(t.roi);
            if (hit.Value > bestOther) {
                bestOther = hit.Value;
                otherID = t.spec.Scene;
            }
        }
        // Scan competing templates before selecting a scene or coordinate profile.
    }
    double minFight = cfg.MinFightScore;
    double minOther = cfg.MinOtherScore;
    double margin = cfg.Margin;
    if (minFight <= 0) minFight = 0.82;
    if (minOther <= 0) minOther = 0.85;
    if (margin < 0) margin = 0;

    if (!fightID.empty() && bestFight >= minFight && bestFight >= bestOther + margin) {
        // Go 遍历 map 时任一命中即返回同一结论，与迭代顺序无关。
        for (const auto& kv : profileScores) {
            const std::string& profile = kv.first;
            double score = kv.second;
            if (!profile.empty() && profile != fightLayout && score >= minFight && bestFight - score <= margin) {
                return GateDecision{engine::GateUncertain, "", bestFight, "", false};
            }
        }
        GateDecision d;
        d.Kind = engine::GateFight;
        d.SceneID = fightID;
        d.Confidence = bestFight;
        d.LayoutProfile = fightLayout;
        // The opening marker is a second accepted fight template, not a
        // heuristic inferred from bean colors. Do not apply it to a different
        // calibrated HUD profile.
        d.RoundOpening = openingScore > 0 && openingLayout == fightLayout;
        return d;
    }
    // Result/selection overlays can leave the round label visible behind them.
    // Two separate accepted page controls establish the foreground page even
    // when the old label is within the normal ambiguity margin.
    size_t otherCount = 0;
    {
        auto it = otherRegions.find(otherID);
        if (it != otherRegions.end()) otherCount = it->second.size();
    }
    bool pageConsensus = otherCount >= 2 && bestOther >= bestFight;
    if (!otherID.empty() && bestOther >= minOther && (bestOther >= bestFight + margin || pageConsensus)) {
        // A cutscene can resemble one result label. Only independently located
        // page controls satisfy a multi-region rule; duplicate templates do not.
        // Insufficient evidence holds observation, never resets the match.
        int minimum = 0;
        auto mit = minimumRegions.find(otherID);
        if (mit != minimumRegions.end()) minimum = mit->second;
        if (static_cast<int>(otherCount) < std::max(1, minimum)) {
            return GateDecision{engine::GateUncertain, "", bestOther, "", false};
        }
        engine::GateKind kind = engine::GateNotFight;
        if (holdSet.count(otherID) || !endSet.count(otherID)) {
            // 死亡换人 / VS / 选人 / 匹配：对局没打完，豆和钟必须保住。
            kind = engine::GateUncertain;
        }
        return GateDecision{kind, otherID, bestOther, "", false};
    }
    return GateDecision{engine::GateUncertain, "", bestRaw, "", false};
}

bool Catalog::hasOther() const {
    for (const auto& t : templates) {
        if (!fightSet.count(t.spec.Scene)) return true;
    }
    return false;
}

// ScoreAll 返回每条模板的分数，不表决。
std::vector<Hit> Catalog::ScoreAll(const RGBA* img) {
    if (img == nullptr) return {};
    auto [ca, supported] = contentArea(img);
    if (!supported) return {};
    // Templates are independent and scoreOne has no side effects (no hints),
    // so they are scored concurrently into their own slots; views of equal
    // ROIs are still converted once and shared.
    const std::vector<preparedTemplate>& prepared = prepare(ca);
    Scorer score(*this, img);
    std::vector<Hit> out(prepared.size());
    forEach(static_cast<int>(prepared.size()), [&](int i) { out[i] = score.one(prepared[i]); });
    return out;
}

std::pair<detect::ContentArea, bool> Catalog::contentArea(const RGBA* img) const {
    int w = layoutCfg.ReferenceWidth, h = layoutCfg.ReferenceHeight;
    if (w <= 0 || h <= 0) {
        w = detect::LogicWidth;
        h = detect::LogicHeight;
    }
    return detect::ResolveContentArea(img, mode, w, h, layoutCfg.AutoAspectTolerance);
}

// SupportsFight checks a separate, static battle control for an established
// match. A cold start instead needs StartFight's independent pair of controls
// plus Gated's complete, legal current HUD.
bool Catalog::SupportsFight(const RGBA* img, const std::string& profile) {
    if (img == nullptr) return false;
    auto [ca, supported] = contentArea(img);
    if (!supported) return false;
    Scorer score(*this, img);
    for (const preparedTemplate& t : prepare(ca)) {
        if (!t.spec.ContinuationOnly || (!t.spec.LayoutProfile.empty() && t.spec.LayoutProfile != profile)) continue;
        Hit hit;
        if (scoreAtHint(*this, img, score, t, hit) || score(t).Value >= t.spec.Threshold) return true;
    }
    return false;
}

// StartFight is an alternative to a covered round/toolbar label, not a relaxed
// bean-color gate. Both controls must pass their original template thresholds
// in independent regions. Any accepted other-page or competing-profile marker
// vetoes entry. Gated must verify both complete bean rows before calling this.
engine::GateDecision Catalog::StartFight(const RGBA* img, const std::string& profile) {
    const engine::GateDecision blocked{engine::GateUncertain, "", 0, "", false};
    if (profile != "camp" && profile != "duel") return blocked;
    auto [ca, supported] = contentArea(img);
    if (!supported) return blocked;
    const std::vector<preparedTemplate>& prepared = prepare(ca);
    const std::vector<Hit> hits = ScoreAll(img);
    std::vector<Rect> regions;
    std::string id;
    double confidence = 1.0;
    for (size_t i = 0; i < prepared.size(); ++i) {
        const preparedTemplate& t = prepared[i];
        if (hits[i].Value < t.spec.Threshold) continue;
        if (!fightSet.count(t.spec.Scene) || (!t.spec.LayoutProfile.empty() && t.spec.LayoutProfile != profile)) {
            return blocked;
        }
        if (!t.spec.ContinuationOnly) continue;
        const Rect bounds = hits[i].Bounds;
        bool independent = !bounds.Empty();
        for (const Rect& previous : regions) {
            const Rect overlap = previous.Intersect(bounds);
            if (overlap.Dx() * overlap.Dy() * 4 >
                std::min(previous.Dx() * previous.Dy(), bounds.Dx() * bounds.Dy())) {
                independent = false;
            }
        }
        if (independent) {
            regions.push_back(bounds);
            id = t.spec.Scene;
            confidence = std::min(confidence, hits[i].Value);
        }
    }
    if (regions.size() < 2 || confidence < cfg.MinFightScore) return blocked;
    return engine::GateDecision{engine::GateFight, id, confidence, profile, false};
}

const std::vector<preparedTemplate>& Catalog::prepare(const detect::ContentArea& ca) {
    if (preparedValid && preparedArea == ca) return prepared_;
    std::vector<preparedTemplate> out(templates.size());
    for (size_t i = 0; i < templates.size(); ++i) {
        out[i] = prepareOne(ca, templates[i]);
        out[i].index = static_cast<int>(i);
    }
    preparedArea = ca;
    prepared_ = std::move(out);
    preparedValid = true;
    std::lock_guard<std::mutex> lk(hintMu);
    hints.assign(prepared_.size(), templateHint{});  // Peaks belong to one geometry only.
    lastCold.assign(prepared_.size(), Hit{});
    coldAt = 0;
    coldFrames = 0;
    return prepared_;
}

// coldScanDue reports whether templates without a remembered peak must be
// searched on this frame. Time moving backwards (replay seek) always rescans.
bool Catalog::coldScanDue(TimeNs at, bool hotFight) {
    std::lock_guard<std::mutex> lk(hintMu);
    if (!hotFight || coldAt == 0 || at < coldAt || at - coldAt >= coldScanInterval || coldFrames >= coldScanEvery) {
        return true;
    }
    coldFrames++;
    return false;
}

void Catalog::recordColdScan(TimeNs at, const std::vector<Hit>& hitsIn) {
    std::lock_guard<std::mutex> lk(hintMu);
    coldAt = at;
    coldFrames = 0;
    if (hitsIn.size() == lastCold.size()) std::copy(hitsIn.begin(), hitsIn.end(), lastCold.begin());
}

Hit Catalog::lastColdHit(int index) const {
    std::lock_guard<std::mutex> lk(hintMu);
    if (index < 0 || index >= static_cast<int>(lastCold.size())) return Hit{};
    return lastCold[index];
}

bool Catalog::hint(int index, templateHint& out) const {
    std::lock_guard<std::mutex> lk(hintMu);
    if (index < 0 || index >= static_cast<int>(hints.size())) {
        out = templateHint{};
        return false;
    }
    out = hints[index];
    return hints[index].valid;
}

void Catalog::setHint(int index, Point peak, bool valid) {
    std::lock_guard<std::mutex> lk(hintMu);
    if (index < 0 || index >= static_cast<int>(hints.size())) return;
    hints[index] = templateHint{peak, valid};
}

preparedTemplate prepareOne(const detect::ContentArea& ca, const template_& t) {
    Rect roi = mapRect(ca, t.spec.ROI, t.spec.Anchor, t.refW, t.refH);
    preparedTemplate out;
    out.spec = t.spec;
    out.roi = roi;
    double scale = t.spec.TemplateScale;
    if (scale == 0) scale = 1;
    double twScale = static_cast<double>(ca.W) / static_cast<double>(t.refW);
    double thScale = static_cast<double>(ca.H) / static_cast<double>(t.refH);
    if (!t.spec.Anchor.empty() && t.spec.Anchor != "legacy") {
        // Fit the HUD reference without stretching widgets on 16:10 tablets.
        twScale = ca.HUDScale(t.refW, t.refH);
        thScale = twScale;
    }
    int tw = static_cast<int>(std::round(static_cast<double>(t.gray.Bounds().Dx()) * twScale * scale));
    int th = static_cast<int>(std::round(static_cast<double>(t.gray.Bounds().Dy()) * thScale * scale));
    if (tw < 4) tw = 4;
    if (th < 4) th = 4;
    if (tw > roi.Dx()) tw = roi.Dx();
    if (th > roi.Dy()) th = roi.Dy();
    if (tw < 4 || th < 4) return out;
    out.gray = match::ScaleGray(&t.gray, tw, th);
    if (!t.mask.Nil()) out.mask = match::ScaleGray(&t.mask, tw, th);
    out.ncc = match::PrepareNCC(&out.gray, maskOrNull(out.mask));
    // Only continuation-only screen templates refine their size (matchIn).
    if (out.spec.ContinuationOnly && out.spec.Anchor == "screen") out.refine = std::make_shared<refineCache>();
    return out;
}

// get builds the +/-2px size variants of t once (first refinement); they are
// exactly what matchIn used to scale and prepare on every refinement.
const refineVariants& refineCache::get(const preparedTemplate& t) {
    std::call_once(once, [&] {
        for (int dw = -2; dw <= 2; ++dw) {
            for (int dh = -2; dh <= 2; ++dh) {
                const int w = t.gray.Bounds().Dx() + dw, h = t.gray.Bounds().Dy() + dh;
                if ((dw == 0 && dh == 0) || w < 4 || h < 4) continue;
                Gray g = match::ScaleGray(&t.gray, w, h);
                Gray mask;
                if (!t.mask.Nil()) mask = match::ScaleGray(&t.mask, w, h);
                variants.v[static_cast<size_t>((dw + 2) * 5 + dh + 2)] = match::PrepareNCC(&g, maskOrNull(mask));
            }
        }
    });
    return variants;
}

// scoreAll scores every primary template for one frame. Templates with a
// remembered peak are checked first at that peak; when one of them confirms a
// fight marker, the remaining cold templates are rescanned only on a bounded
// cadence and otherwise keep their last full-scan score.
//
// Each stage may score its templates concurrently: a template writes only its
// own slot and its own hint, and every reduction runs afterwards in template
// order, so hits, hints and cold-scan bookkeeping match a sequential pass.
std::vector<Hit> Catalog::scoreAll(const RGBA* img, const std::vector<preparedTemplate>& prepared, TimeNs at) {
    Scorer score(*this, img);
    const int n = static_cast<int>(prepared.size());
    std::vector<Hit> hitsOut(n);
    std::vector<char> done(n, 0);
    std::vector<int> work;
    work.reserve(n);
    for (int i = 0; i < n; ++i) {
        const preparedTemplate& t = prepared[i];
        if (!t.spec.ContinuationOnly) {
            templateHint h;
            if (hint(t.index, h)) work.push_back(i);
        }
    }
    forEach(static_cast<int>(work.size()), [&](int k) {
        int i = work[k];
        Hit h;
        bool ok = scoreAtHint(*this, img, score, prepared[i], h);
        hitsOut[i] = ok ? h : Hit{};
        done[i] = ok ? 1 : 0;
    });
    bool hotFight = false;
    for (int i = 0; i < n; ++i) {
        if (done[i] && fightSet.count(prepared[i].spec.Scene)) hotFight = true;
    }
    if (!coldScanDue(at, hotFight)) {
        for (int i = 0; i < n; ++i) {
            if (!done[i] && !prepared[i].spec.ContinuationOnly) hitsOut[i] = lastColdHit(i);
        }
        return hitsOut;
    }
    work.clear();
    for (int i = 0; i < n; ++i) {
        if (!done[i] && !prepared[i].spec.ContinuationOnly) work.push_back(i);
    }
    forEach(static_cast<int>(work.size()), [&](int k) {
        int i = work[k];
        hitsOut[i] = score(prepared[i]);
    });
    std::vector<Hit> cold(n);
    for (int i = 0; i < n; ++i) {
        if (!done[i]) cold[i] = hitsOut[i];
    }
    recordColdScan(at, cold);
    return hitsOut;
}

Point mapAnchoredPoint(const detect::ContentArea& ca, double x, double y, int refW, int refH,
                       const std::string& anchor);

Rect mapRect(const detect::ContentArea& ca, const config::NormalizedRect& r, const std::string& anchor,
             int refW, int refH) {
    Point p0;
    Point p1;
    // Screen-relative control search, with fitted (not stretched) HUD templates.
    if (anchor.empty() || anchor == "legacy" || anchor == "screen") {
        p0 = ca.Map(r.X * detect::LogicWidth, r.Y * detect::LogicHeight);
        p1 = ca.Map((r.X + r.Width) * detect::LogicWidth, (r.Y + r.Height) * detect::LogicHeight);
    } else {
        p0 = mapAnchoredPoint(ca, r.X, r.Y, refW, refH, anchor);
        p1 = mapAnchoredPoint(ca, r.X + r.Width, r.Y + r.Height, refW, refH, anchor);
    }
    int x0 = p0.X, y0 = p0.Y, x1 = p1.X, y1 = p1.Y;
    if (x1 <= x0) x1 = x0 + 1;
    if (y1 <= y0) y1 = y0 + 1;
    return MakeRect(x0, y0, x1, y1);
}

Point mapAnchoredPoint(const detect::ContentArea& ca, double x, double y, int refW, int refH,
                       const std::string& anchor) {
    const double s = ca.HUDScale(refW, refH);
    const double logicalX = x * static_cast<double>(refW);
    double px = static_cast<double>(ca.X) + logicalX * s;
    if (anchor == "right") {
        px = static_cast<double>(ca.X + ca.W) - (static_cast<double>(refW) - logicalX) * s;
    } else if (anchor == "center") {
        px = static_cast<double>(ca.X) + static_cast<double>(ca.W) / 2.0 +
             (logicalX - static_cast<double>(refW) / 2.0) * s;
    }
    return Pt(static_cast<int>(px), ca.Y + static_cast<int>(y * static_cast<double>(refH) * s));
}

bool lumaBlank(const RGBA* img, engine::GateKind& kind) {
    kind = engine::GateFight;  // Go 零值 0
    const Rect& b = img->Bounds();
    if (b.Dx() <= 0 || b.Dy() <= 0) {
        kind = engine::GateUncertain;
        return true;
    }
    double sum = 0, n = 0;
    double low = 255.0, high = 0.0;
    for (int y = b.Min.Y; y < b.Max.Y; y += 8) {
        for (int x = b.Min.X; x < b.Max.X; x += 8) {
            Color c = img->RGBAAt(x, y);
            double value = 0.299 * static_cast<double>(c.R) + 0.587 * static_cast<double>(c.G) +
                           0.114 * static_cast<double>(c.B);
            sum += value;
            low = std::min(low, value);
            high = std::max(high, value);
            n++;
        }
    }
    if (n == 0) {
        kind = engine::GateUncertain;
        return true;
    }
    double avg = sum / n;
    // A dark/white stage or full-screen effect can still leave readable HUD
    // landmarks. Mean brightness alone is not evidence of a blank capture.
    if ((avg < 25 || avg > 235) && high - low <= 12) {
        kind = engine::GateBlank;
        return true;
    }
    return false;
}

engine::GateDecision ColorGate::DecideAt(const RGBA* img, TimeNs /*at*/) {
    using engine::GateDecision;
    if (img == nullptr) return GateDecision{engine::GateUncertain, "", 0, "", false};
    switch (detect::ClassifyScreen(img, Mode)) {
        case detect::ScreenState::ScreenFighting:
            return GateDecision{engine::GateFight, "color-fight", 1, "", false};
        case detect::ScreenState::ScreenBlank:
            return GateDecision{engine::GateBlank, "blank", 1, "", false};
        case detect::ScreenState::ScreenUnknown:
            return GateDecision{engine::GateUncertain, "unknown", 0, "", false};
        default:
            return GateDecision{engine::GateNotFight, "color-other", 1, "", false};
    }
}

// NewGate 优先用模板目录；没有可用模板则回退颜色密度。
std::shared_ptr<engine::Gate> NewGate(const config::Config& cfg, std::string* description, std::string* err) {
    std::string loadErr;
    std::shared_ptr<Catalog> cat = Load(cfg, &loadErr);
    if (!loadErr.empty()) {
        if (err) *err = loadErr;
        if (description) description->clear();
        return nullptr;
    }
    if (err) err->clear();
    if (cat) {
        if (description) *description = "templates:" + std::to_string(cat->templates.size());
        return cat;
    }
    if (description) *description = "color-fallback";
    return std::make_shared<ColorGate>(parseMode(cfg.Layout.ContentMode));
}

}  // namespace nt::scene
