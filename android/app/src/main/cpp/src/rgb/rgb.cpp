// Owner: cpp-rgb
// Port of: internal/engine/rgb/rgb.go
// Contract: include/nt/rgb_internal.h — see android/ARCHITECTURE.md.
//
// Package rgb 实现 RGBEngine：基于纯 RGB 区间判色的检测引擎。
// 配置布局使用固定菱形核心，旧校准工具保留兼容采样接口。
//
// Go 的 Engine.mu 保护 prefer/locked：Android 上同一 Engine 只在单一分析线程上调用
// （jni.cpp 全局锁），按契约省略。camp‖duel 并行时两个并行体都不读写 prefer_/locked_。
#include <algorithm>
#include <cmath>
#include <utility>

#include "nt/parallel.h"
#include "nt/rgb.h"
#include "nt/rgb_internal.h"

namespace nt::rgb {

namespace {

const char* const kSideNames[2] = {"left", "right"};
constexpr engine::Side kSides[2] = {engine::Side::Left, engine::Side::Right};

}  // namespace

Engine::Engine(std::shared_ptr<engine::LayoutProvider> layout) : Engine(std::move(layout), config::Default().Vision) {}

Engine::Engine(std::shared_ptr<engine::LayoutProvider> layout, const config::VisionConfig& vision)
    : layout_(std::move(layout)), vision_(vision), names_(ninja::Reader::NewReader()) {}

// Prefer receives a template's explicit layout binding, or clears it at an end scene.
void Engine::Prefer(const std::string& kind) {
    prefer_ = kind;
    locked_ = kind;
}

// AnalyzeAt uses acquisition time for retry scheduling so live and replay frames
// make the same name decisions, independent of analysis speed.
// Analyze 实现 engine.Engine：RGB 区间判色 + 递增规则（基类 Analyze(img) = AnalyzeAt(img, NowNs())）。
// A scene-bound profile samples only its own calibrated slots.
engine::Result Engine::AnalyzeAt(const RGBA* img, TimeNs at) {
    if (at == 0) {
        at = NowNs();
    }
    engine::Result res;
    res.Name = "rgb";
    if (img == nullptr) {
        return res;
    }
    const int w = img->Bounds().Dx(), h = img->Bounds().Dy();
    detect::ContentArea area = detect::ComputeContentArea(w, h, layout_->Mode());
    // Go 类型断言 e.layout.(*engine.ConfiguredLayout) / e.layout.(engine.ProfiledLayout)。
    const auto* calibrated = dynamic_cast<const engine::ConfiguredLayout*>(layout_.get());
    const auto* profiled = dynamic_cast<const engine::ProfiledLayout*>(layout_.get());
    config::VisionConfig vision = vision_;
    if (calibrated != nullptr) {
        auto [contentArea, supported] = calibrated->ContentArea(img);
        area = contentArea;
        if (!supported) {
            res.Uncertain = true;
            res.Scene = "unsupported-resolution";
            return res;
        }
        Point ref = calibrated->ReferenceSize();
        vision.SampleWidthReferencePX *= double(detect::LogicWidth) / double(ref.X);
        vision.SampleHeightReferencePX *= double(detect::LogicHeight) / double(ref.Y);
    }
    std::string prefer = prefer_;
    auto positions = [&](const std::string& name) -> std::vector<detect::BeadPosition> {
        if (calibrated != nullptr) {
            return calibrated->PositionsIn(name, area);
        }
        if (profiled != nullptr) {
            return profiled->PositionsFor(name, w, h);
        }
        if (name == "duel") {
            return detect::Layout(w, h, layout_->Mode(), detect::DuelBeads());
        }
        return layout_->Positions(w, h);
    };
    // sample writes only its own readout and its profile's trackers, so camp
    // and duel can run concurrently. Non-profiled layouts leave it zero.
    auto sample = [&](const std::string& name, const std::vector<detect::BeadPosition>& pos,
                      std::array<ninja::Readout, 2>& readout) -> std::vector<BeadInfo> {
        if (profiled != nullptr) {
            std::array<ninja::Readout, 2> identified{};
            std::vector<detect::BeadPosition> special = specialPositionsForAt(name, img, pos, area, at, identified);
            readout = identified;
            return sampleCalibratedSpecial(img, special, area, vision, identified);
        }
        return sampleLayout(img, pos);
    };
    if (profiled != nullptr) {
        std::string manual = profiled->PreferredProfile();
        if (manual == "camp" || manual == "duel") {
            prefer = manual;
        }
    }
    if (prefer == "camp" || prefer == "duel") {
        res.Name = "rgb-" + prefer;
        res.LayoutProfile = prefer;
        std::array<ninja::Readout, 2> readout{};
        res.Beads = sample(prefer, positions(prefer), readout);
        applyNames(res, readout);
        locked_ = prefer;
        return res;
    }
    // Layout positions are resolved here so only sampling leaves this goroutine.
    const std::vector<detect::BeadPosition> campPositions = positions("camp"), duelPositions = positions("duel");
    std::vector<BeadInfo> camp, duel;
    std::array<ninja::Readout, 2> campReadout{}, duelReadout{};
    // Go: wg.Go(func() { duel = sample("duel", ...) }); camp = sample("camp", ...); wg.Wait()
    // 串行时（SetParallelEnabled(false)）按 camp、duel 的 Go 顺序执行。
    ParallelInvoke([&] { camp = sample("camp", campPositions, campReadout); },
                   [&] { duel = sample("duel", duelPositions, duelReadout); });
    std::string name;
    const std::vector<BeadInfo>& picked = pickLayout(camp, duel, name);
    std::string stuck;
    std::vector<BeadInfo> chosen = stickLayout(camp, duel, picked, name, stuck);
    name = stuck;
    res.Name = "rgb-" + name;
    res.LayoutProfile = name;
    res.Beads = std::move(chosen);
    const std::array<ninja::Readout, 2>& readout = name == "duel" ? duelReadout : campReadout;
    applyNames(res, readout);
    return res;
}

// sampleCalibrated keeps each core inside its calibrated slot. Searching for the
// brightest nearby pixel follows the cyan rim of an EMPTY bead and turns 2 into 4.
// Ambiguous cores stay unknown; sequence constraints never invent missing votes.
std::vector<BeadInfo> sampleCalibrated(const RGBA* img, const std::vector<BeadPosition>& positions,
                                       const detect::ContentArea& area, const config::VisionConfig& cfg) {
    return sampleCalibratedSpecial(img, positions, area, cfg, std::array<ninja::Readout, 2>{});
}

// sampleUnverifiedSpecial reads only strong, current-frame evidence during a
// brief special-name gap. It never reuses an old count. Dark cores remain
// observable for every skin; a bright result is allowed only for a verified
// saturated purple body and still has to pass the current-frame wash guard.
BeadState sampleUnverifiedSpecial(const RGBA* img, const BeadPosition& p, int w, int h, int guard,
                                  ninja::Palette palette, double& conf) {
    if (img == nullptr || w <= 0 || h <= 0) {
        conf = 0;
        return detect::StateUnknown;
    }
    int dark = 0, purple = 0, total = 0;
    for (int dy = -h / 2; dy <= h / 2; dy++) {
        for (int dx = -w / 2; dx <= w / 2; dx++) {
            if (!detect::InBeadDiamond(0, 0, w, h, dx, dy)) {
                continue;
            }
            Point point = Pt(p.X + dx, p.Y + dy);
            if (!point.In(img->Bounds())) {
                continue;
            }
            total++;
            Color c = img->RGBAAt(point.X, point.Y);
            int r = c.R, g = c.G, b = c.B;
            if ((detect::DarkRange.Contains(r, g, b) && b - r >= 15 && b - g >= 8) ||
                (palette == ninja::Purple && specialPixel(ninja::Purple, r, g, b) == detect::StateDark)) {
                dark++;
            }
            if (palette == ninja::Purple && specialPixel(ninja::Purple, r, g, b) == detect::StateLight) {
                purple++;
            }
        }
    }
    if (total == 0) {
        conf = 0;
        return detect::StateUnknown;
    }
    double darkConfidence = double(dark) / double(total);
    if (darkConfidence >= 0.60) {
        conf = darkConfidence;
        return detect::StateDark;
    }
    double purpleConfidence = double(purple) / double(total);
    if (palette == ninja::Purple && purpleConfidence >= 0.60 && !specialWash(img, p, ninja::Purple, std::max(3, guard))) {
        conf = purpleConfidence;
        return detect::StateLight;
    }
    conf = std::max(darkConfidence, purpleConfidence);
    return detect::StateUnknown;
}

std::vector<BeadInfo> sampleCalibratedSpecial(const RGBA* img, const std::vector<BeadPosition>& positions,
                                              const detect::ContentArea& area, const config::VisionConfig& cfg,
                                              const std::array<ninja::Readout, 2>& identified) {
    const int w = std::max(
        2, static_cast<int>(std::round(cfg.SampleWidthReferencePX * cfg.CoreScale * double(area.W) / detect::LogicWidth)));
    const int h = std::max(
        2, static_cast<int>(std::round(cfg.SampleHeightReferencePX * cfg.CoreScale * double(area.H) / detect::LogicHeight)));
    std::vector<BeadInfo> out;
    for (int index = 0; index < 2; index++) {
        const engine::Side side = kSides[index];
        const std::string sideName = engine::SideString(side);
        ninja::Palette palette = identified[index].Palette_;
        if (identified[index].Unverified) {
            palette = identified[index].PaletteHint;
        }
        const bool xiayin = palette == ninja::Xiayin;
        if (xiayin) {
            palette = xiayinPalette(img, positions, sideName, w, h);
        }
        std::vector<BeadPosition> row;
        const size_t start = out.size();
        for (const BeadPosition& p : positions) {
            if (p.Side == sideName) {
                row.push_back(p);
            }
        }
        std::vector<BeadState> states;
        if (identified[index].Unverified) {
            for (const BeadPosition& p : row) {
                // Keep the row topology, but decide each bean from this frame only.
                // Hints permit current saturated purple bodies; Xiayin also permits
                // spatially isolated red cores. White flares/gold remain unknown.
                const int guard = std::max(
                    3, static_cast<int>(std::round(cfg.SampleHeightReferencePX * double(area.H) / detect::LogicHeight * 1.2)));
                ninja::Palette hint = identified[index].PaletteHint;
                if (xiayin) {
                    hint = palette;
                }
                BeadState st;
                double conf = 0;
                if (xiayin && palette == ninja::Red) {
                    st = sampleUnverifiedXiayinRed(img, p, w, h, guard, conf);
                } else {
                    st = sampleUnverifiedSpecial(img, p, w, h, guard, hint, conf);
                }
                states.push_back(st);
                BeadInfo info;
                info.X = p.X;
                info.Y = p.Y;
                info.Label = label(side, p.Idx);
                info.Lit = st == detect::StateLight;
                info.Unknown = st == detect::StateUnknown;
                info.Conf = conf;
                out.push_back(std::move(info));
            }
        } else {
            // A hand-calibrated offset (energy-gauge variants) can drift a few
            // pixels at some window renderings, and the 5x7 calibrated core then
            // misses every diamond. When a whole row stays unknown, re-sample
            // it on a bounded offset grid and keep the best legal row. Identity
            // stays independent; all evidence is still this frame's pixels.
            std::vector<BeadInfo> rowInfos;
            std::vector<BeadState> rowStates;
            sampleCalibratedRow(img, row, area, cfg, palette, xiayin, identified[index], Point{}, rowInfos, rowStates);
            if (allUnknownStates(rowStates) && row.size() >= 2) {
                static const Point kDeltas[] = {{3, 0}, {-3, 0}, {0, -3}, {0, 3}, {6, 0}, {-6, 0}, {0, -6}, {0, 6}};
                for (const Point& delta : kDeltas) {
                    std::vector<BeadInfo> candidateInfos;
                    std::vector<BeadState> candidateStates;
                    sampleCalibratedRow(img, row, area, cfg, palette, xiayin, identified[index], delta, candidateInfos,
                                        candidateStates);
                    if (rowInfosScore(candidateInfos) > rowInfosScore(rowInfos)) {
                        rowInfos = std::move(candidateInfos);
                        rowStates = std::move(candidateStates);
                    }
                    if (!allUnknownStates(rowStates)) {
                        break;
                    }
                }
            }
            out.insert(out.end(), rowInfos.begin(), rowInfos.end());
            states = std::move(rowStates);
        }
        if (!detect::IsPossiblePrefix(states)) {
            for (size_t i = start; i < out.size(); i++) {
                out[i].Lit = false;
                out[i].Gold = false;
                out[i].Unknown = true;
            }
        }
    }
    return out;
}

// sampleCalibratedRow samples one side's complete bean row at calibrated
// centers shifted by delta. It is the strict core sampler; the retry grid only
// re-centers the diamond, every judgment stays at the calibrated thresholds.
// Go 返回 (infos, states)；这里写入 infos/states（调用方传入空向量，函数先清空）。
void sampleCalibratedRow(const RGBA* img, const std::vector<BeadPosition>& row, const detect::ContentArea& area,
                         const config::VisionConfig& cfg, ninja::Palette palette, bool xiayin,
                         const ninja::Readout& identified, Point delta, std::vector<BeadInfo>& infos,
                         std::vector<BeadState>& states) {
    (void)xiayin;  // Go 签名保留该参数但函数体不使用。
    infos.clear();
    states.clear();
    const int w = std::max(
        2, static_cast<int>(std::round(cfg.SampleWidthReferencePX * cfg.CoreScale * double(area.W) / detect::LogicWidth)));
    const int h = std::max(
        2, static_cast<int>(std::round(cfg.SampleHeightReferencePX * cfg.CoreScale * double(area.H) / detect::LogicHeight)));
    const Rect bounds = img->Bounds();
    for (const BeadPosition& orig : row) {
        const BeadPosition p(static_cast<const detect::Bead&>(orig), orig.X + delta.X, orig.Y + delta.Y);
        const int gap = beadHalfPitch(row, p, 2 * w);
        const bool redHighlight = palette == ninja::Red && redLowerBody(img, p, w, h);
        const bool purpleHighlight =
            palette == ninja::Purple && (purpleGlintBody(img, p, w, h, gap) || purplePairedBody(img, p, w, h, gap));
        const bool goldHighlight =
            (palette == ninja::Palette::None || palette == ninja::Warm) && goldGlintBody(img, p, w, h);
        const bool blueHighlight =
            (palette == ninja::Palette::None || palette == ninja::Warm) && blueGlintBody(img, p, w, h, gap);
        // 神驹佑将 uses the established four-slot layout, but its bounded warm
        // body has the same current-pixel white-glint proof as a six-slot warm
        // variant. This stays restricted to the exact, current-frame title;
        // topology hints and other four-slot identities cannot enable it.
        // A wrong topology (Madara with a stale/derived six-slot readout) is
        // never a six-slot warm variant and must not enable white votes.
        const bool warmHighlight = palette == ninja::Warm &&
                                   ((identified.Slots == 6 && identified.Name != ninja::Madara) ||
                                    (identified.Name == ninja::Madara && identified.Slots == 4)) &&
                                   warmGlintBody(img, p, w, h);
        int light = 0, dark = 0, gold = 0, paleGold = 0, blue = 0, total = 0;
        for (int dy = -h / 2; dy <= h / 2; dy++) {
            for (int dx = -w / 2; dx <= w / 2; dx++) {
                if (!detect::InBeadDiamond(0, 0, w, h, dx, dy)) {
                    continue;
                }
                total++;
                if (!Pt(p.X + dx, p.Y + dy).In(bounds)) {
                    continue;
                }
                Color c = img->RGBAAt(p.X + dx, p.Y + dy);
                int r = c.R, g = c.G, b = c.B;
                if (blueHighlight && g >= 190 && b >= 185 && g + 15 >= r && b + 45 >= r) {
                    light++;
                    blue++;
                    continue;
                }
                if (warmHighlight && r >= 235 && g >= 210 && r + 12 >= g && g >= b) {
                    light++;
                    continue;
                }
                if (purpleHighlight && r >= 235 && b >= 235 && g >= 210 && r + 12 >= g && b + 12 >= g) {
                    light++;
                    continue;
                }
                // The gold idle glint clips the middle to white. Accept that
                // white only with BOTH current gold body lobes and bounded
                // spatial contrast; not a nearby rim, bar, or cached count.
                if (goldHighlight && r >= 235 && g >= 210 && r + 12 >= g && g >= b) {
                    light++;
                    gold++;
                    continue;
                }
                // Naruto's charged red bean can flash yellow/white in its core.
                // This exception needs the exact skin and its own red lower body;
                // a bright rim/white cover on an empty core is not enough.
                if (redHighlight && r >= 235 && g >= 190 && r >= g && g >= b) {
                    light++;
                    continue;
                }
                if (BeadState st = specialPixel(palette, r, g, b); st != detect::StateUnknown) {
                    if (st == detect::StateLight) {
                        light++;
                    } else {
                        dark++;
                    }
                    continue;
                }
                if (r >= 190 && g >= 110 && r - b >= 80 && g - b >= 70) {
                    light++;
                    gold++;
                } else if (r >= 210 && g >= 180 && r - b >= 35 && g - b >= 30) {
                    paleGold++;
                } else if (g >= 120 && b >= 150 && b - r >= 20) {
                    light++;
                    blue++;
                } else if (detect::DarkRange.Contains(r, g, b) && b - r >= 15 && b - g >= 8) {
                    dark++;
                }
            }
        }
        // A gold sparkle fades towards pale yellow. Count this highlight
        // only with substantial saturated gold in the same calibrated core;
        // white or pale effects alone remain unknown.
        if (gold * 4 >= total) {
            light += paleGold;
            gold += paleGold;
        }
        double conf = double(std::max(light, dark)) / double(std::max(1, total));
        double margin = 0.0;
        if (total > 0) {
            margin = std::fabs(double(light - dark)) / double(total);
        }
        BeadState st = detect::StateUnknown;
        if (conf >= std::max(0.60, cfg.UnknownBelow) && margin >= std::max(0.15, cfg.MinimumMargin)) {
            if (light > dark) {
                st = detect::StateLight;
            } else {
                st = detect::StateDark;
            }
        }
        if (st == detect::StateUnknown && darkGlintBody(img, p, w, h, palette)) {
            st = detect::StateDark;
            conf = 1;
        }
        const int guard = std::max(
            3, static_cast<int>(std::round(cfg.SampleHeightReferencePX * double(area.H) / detect::LogicHeight * 1.2)));
        // A confirmed Xiayin red core is already bounded by the calibrated
        // sample, confidence/margin, and the wash guard below. Do not require
        // the optional halo-shape proof for every ordinary red bean: scaling and
        // HUD blending can make a real core fail that narrow geometry check.
        // The proof remains an exception for unverified red rows and is still
        // used by specialWash to reject broad red effects.
        if (st == detect::StateLight && specialWash(img, p, palette, guard)) {
            if (!warmHighlight &&
                (palette != ninja::Purple || identified.Name != ninja::SasukeXiayin ||
                 (!purpleHighlight && !isolatedPurpleHalo(img, p, w, h, guard, gap)))) {
                st = detect::StateUnknown;
            }
        }
        if (st == detect::StateLight && blue > light / 2 && !blueBodyVisible(img, p, w, h)) {
            st = detect::StateUnknown;
        }
        if (st == detect::StateLight && gold > light / 2 && !goldHighlight && !goldBodyVisible(img, p, w, h)) {
            st = detect::StateUnknown;
        }
        states.push_back(st);
        engine::Side sideName = engine::Side::Left;
        if (p.Side == "right") {
            sideName = engine::Side::Right;
        }
        BeadInfo info;
        info.X = p.X;
        info.Y = p.Y;
        info.Label = label(sideName, p.Idx);
        info.Lit = st == detect::StateLight;
        info.Gold = st == detect::StateLight && gold > light / 2;
        info.Unknown = st == detect::StateUnknown;
        info.Conf = conf;
        infos.push_back(std::move(info));
    }
}

bool allUnknownStates(const std::vector<BeadState>& states) {
    for (BeadState st : states) {
        if (st != detect::StateUnknown) {
            return false;
        }
    }
    return !states.empty();
}

double rowInfosScore(const std::vector<BeadInfo>& infos) {
    double score = 0.0;
    for (const BeadInfo& info : infos) {
        if (!info.Unknown) {
            score += 1 + info.Conf;
        }
    }
    return score;
}

const std::vector<BeadInfo>& pickLayout(const std::vector<BeadInfo>& camp, const std::vector<BeadInfo>& duel,
                                        std::string& outName) {
    if (legalSides(duel) > legalSides(camp) ||
        (legalSides(duel) == legalSides(camp) && knownCount(duel) > knownCount(camp))) {
        outName = "duel";
        return duel;
    }
    outName = "camp";
    return camp;
}

std::vector<BeadInfo> Engine::stickLayout(const std::vector<BeadInfo>& camp, const std::vector<BeadInfo>& duel,
                                          const std::vector<BeadInfo>& chosen, const std::string& name,
                                          std::string& outName) {
    if (locked_.empty()) {
        if (legalSides(chosen) >= 1) {
            locked_ = name;
        }
        outName = name;
        return chosen;
    }
    const std::vector<BeadInfo>* cur = &camp;
    if (locked_ == "duel") {
        cur = &duel;
    }
    const std::vector<BeadInfo>* alt = &duel;
    std::string altName = "duel";
    if (locked_ == "duel") {
        alt = &camp;
        altName = "camp";
    }
    // 已锁定的布局仍然合法就别换，避免 4/3 来回切把开钟拖成十几秒。
    if (legalSides(*cur) >= 1) {
        outName = locked_;
        return *cur;
    }
    if (legalSides(*alt) >= 1) {
        locked_ = altName;
        outName = altName;
        return *alt;
    }
    outName = locked_;
    return *cur;
}

std::vector<BeadInfo> sampleLayout(const RGBA* img, const std::vector<BeadPosition>& positions) {
    std::vector<BeadInfo> out;
    for (int index = 0; index < 2; index++) {
        const engine::Side side = kSides[index];
        const std::string sideStr = kSideNames[index];
        std::vector<BeadPosition> pos;
        std::vector<BeadState> states;
        for (BeadPosition p : positions) {
            if (p.Side != sideStr) {
                continue;
            }
            int nx = p.X, ny = p.Y;
            BeadState st = locateBead(img, p.X, p.Y, p.Idx, nx, ny);
            p.X = nx;
            p.Y = ny;
            pos.push_back(p);
            states.push_back(st);
        }
        states = detect::ApplyIncrementRule(states);
        for (size_t i = 0; i < pos.size(); i++) {
            const BeadPosition& p = pos[i];
            Color c = img->RGBAAt(p.X, p.Y);
            bool gold = states[i] == detect::StateLight && detect::IsGold(int(c.R), int(c.G), int(c.B));
            BeadInfo info;
            info.X = p.X;
            info.Y = p.Y;
            info.Label = label(side, p.Idx);
            info.Lit = states[i] == detect::StateLight;
            info.Gold = gold;
            info.Unknown = states[i] == detect::StateUnknown || states[i] == detect::StateGone;
            info.Conf = 1.0;
            out.push_back(std::move(info));
        }
    }
    return out;
}

int legalSides(const std::vector<BeadInfo>& beads) {
    int n = 0;
    for (char side : {'L', 'R'}) {
        std::vector<BeadState> st;
        for (const BeadInfo& b : beads) {
            if (b.Label.empty() || b.Label[0] != side) {
                continue;
            }
            if (b.Unknown) {
                st.push_back(detect::StateUnknown);
            } else if (b.Lit) {
                st.push_back(detect::StateLight);
            } else {
                st.push_back(detect::StateDark);
            }
        }
        if (detect::IsLegalPrefix(st)) {
            n++;
        }
    }
    return n;
}

int knownCount(const std::vector<BeadInfo>& beads) {
    int n = 0;
    for (const BeadInfo& b : beads) {
        if (!b.Unknown) {
            n++;
        }
    }
    return n;
}

// label 生成豆子编号（L1..L6 / R1..R6）。
std::string label(engine::Side side, int idx) {
    std::string prefix = "L";
    if (side == engine::Side::Right) {
        prefix = "R";
    }
    return prefix + std::to_string(idx + 1);
}

// locateBead 在标定附近找豆。亮豆中间有一条暗青装饰带，中心常被判暗，
// 所以一旦附近出现亮核就用亮核，不要被中间那条带子带走。
BeadState locateBead(const RGBA* img, int cx, int cy, int idx, int& nx, int& ny) {
    // 第 5/6 槽：4 豆 HUD 这里是血条/木纹。只认标定中心，别漂到邻豆上。
    if (idx >= 4) {
        nx = cx;
        ny = cy;
        return voteBead(img, cx, cy);
    }
    BeadState bestS = voteBead(img, cx, cy);
    int bestN = classifiedAt(img, cx, cy), bx = cx, by = cy;
    BeadState lightS = detect::StateUnknown;
    int lightN = 0, lx = cx, ly = cy;
    if (bestS == detect::StateLight) {
        lightS = bestS;
        lightN = bestN;
        lx = cx;
        ly = cy;
    }
    for (int dy = -6; dy <= 8; dy += 2) {
        for (int dx = -6; dx <= 6; dx += 2) {
            if (dx == 0 && dy == 0) {
                continue;
            }
            BeadState s = voteBead(img, cx + dx, cy + dy);
            int n = classifiedAt(img, cx + dx, cy + dy);
            if (s == detect::StateLight && n > lightN) {
                lightS = s;
                lightN = n;
                lx = cx + dx;
                ly = cy + dy;
            }
            if (s != detect::StateUnknown && n > bestN) {
                bestS = s;
                bestN = n;
                bx = cx + dx;
                by = cy + dy;
            }
        }
    }
    if (lightS == detect::StateLight && lightN >= 5) {
        nx = lx;
        ny = ly;
        return lightS;
    }
    nx = bx;
    ny = by;
    return bestS;
}

int classifiedAt(const RGBA* img, int cx, int cy) {
    constexpr int w = detect::BeadW, h = detect::BeadH;
    int n = 0;
    const Rect b = img->Bounds();
    for (int dy = -h / 2; dy <= h / 2; dy++) {
        for (int dx = -w / 2; dx <= w / 2; dx++) {
            if (!detect::InBeadDiamond(0, 0, w, h, dx, dy)) {
                continue;
            }
            int x = cx + dx, y = cy + dy;
            if (x < b.Min.X || y < b.Min.Y || x >= b.Max.X || y >= b.Max.Y) {
                continue;
            }
            Color c = img->RGBAAt(x, y);
            if (detect::Classify(int(c.R), int(c.G), int(c.B)) != detect::StateUnknown) {
                n++;
            }
        }
    }
    return n;
}

// voteBead 在菱形采样区域内逐像素分类并投票，返回多数状态。
BeadState voteBead(const RGBA* img, int cx, int cy) {
    constexpr int w = detect::BeadW, h = detect::BeadH;
    int light = 0, dark = 0, other = 0;
    const Rect b = img->Bounds();
    for (int dy = -h / 2; dy <= h / 2; dy++) {
        for (int dx = -w / 2; dx <= w / 2; dx++) {
            if (!detect::InBeadDiamond(0, 0, w, h, dx, dy)) {
                continue;
            }
            int x = cx + dx, y = cy + dy;
            if (x < b.Min.X || y < b.Min.Y || x >= b.Max.X || y >= b.Max.Y) {
                continue;
            }
            Color c = img->RGBAAt(x, y);
            switch (detect::Classify(int(c.R), int(c.G), int(c.B))) {
                case detect::StateLight:
                    light++;
                    break;
                case detect::StateDark:
                    dark++;
                    break;
                default:
                    other++;
                    break;
            }
        }
    }
    // 亮豆中部那条暗青装饰带会拉高 dark。附近有成片亮核就当亮。
    if (light >= 4 && light * 2 >= dark && light + dark > other) {
        return detect::StateLight;
    }
    if (light > dark && light > other) {
        return detect::StateLight;
    }
    if (dark > light && dark > other) {
        return detect::StateDark;
    }
    return detect::StateUnknown;
}

}  // namespace nt::rgb
