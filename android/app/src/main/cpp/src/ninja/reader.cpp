// Owner: cpp-ninja
// Port of: internal/ninja/reader.go
// Contract: include/nt/ninja.h — see android/ARCHITECTURE.md.
//
// Name strips come from user-supplied HUD crops. Special strips establish the
// exact variant; ordinary strips establish only a display name (Slots == 0).
// Neither kind establishes the scene or the player's side.
//
// 模板：Go 用 //go:embed templates/*.png；Android 由 AssetStore 提供
// "ninja_templates/<file>.png"（Kotlin 解码的非预乘 RGBA8），灰度走 match::ToGrayAsset
// （等价 Go 对 png.Decode 结果调用 match.ToGray）。
#include <algorithm>
#include <cmath>
#include <map>
#include <memory>
#include <mutex>
#include <string>
#include <vector>

#include "nt/assets.h"
#include "nt/match.h"
#include "nt/ninja.h"
#include "nt/ninja_internal.h"
#include "nt/parallel.h"
#include "nt/utf8.h"

namespace nt::ninja {

namespace {

inline int iround(double v) { return static_cast<int>(std::round(v)); }

// strings.IndexAny(title, "[【「(")：返回第一个命中 rune 的字节下标，-1 = 无。
int indexTitleBracket(const std::string& s) {
    for (size_t i = 0, w = 0; i < s.size(); i += w) {
        char32_t r = utf8::DecodeRune(s, i, w);
        if (r == U'[' || r == U'【' || r == U'「' || r == U'(') return static_cast<int>(i);
    }
    return -1;
}

struct templateSpec {
    const char* file;
    const std::string* name;
    int slots;
    Palette palette;
    double rowOffsetY;
    bool requirePortrait;
};

}  // namespace

// NewReader uses the embedded A/S catalog by default.（Go NewReaderWithAvatars(AvatarOptions{})：
// Android 没有外部开发图鉴，只用内置图鉴；图鉴不可用时 avatars == nullptr。）
std::shared_ptr<Reader> Reader::NewReader() {
    auto r = std::make_shared<Reader>();
    r->avatars = LoadEmbeddedAvatarCatalog(nullptr);
    const templateSpec specs[] = {
        {"hashirama", &Hashirama, 6, Warm, 0, false},
        {"hashirama_alt", &Hashirama, 6, Warm, 0, false},
        {"madara", &Madara, 4, Warm, 0, false},
        {"obito", &Obito, 4, Purple, 0, false},
        {"naruto", &Naruto, 4, Red, 0, false},
        {"naruto_right", &Naruto, 4, Red, 0, false},
        {"obito_current", &Obito, 4, Purple, 0, false},
        {"sasuke_xiayin", &SasukeXiayin, 4, Xiayin, 9, false},
        {"sasuke_xiayin_duel", &SasukeXiayin, 4, Xiayin, 9, false},
        // User-provided native captures. These full-title templates establish
        // a display identity; they retain the ordinary four-slot/default-color
        // policy. Minato is the reviewed energy-gauge row-offset exception.
        {"itachi_hyakusen", &ItachiHyakusen, 4, Palette::None, EnergyGaugeRowOffset, true},
        // MuMu 1280x720 capture requires independent character HUD portrait evidence.
        {"itachi_hyakusen_mumu", &ItachiHyakusen, 4, Palette::None, EnergyGaugeRowOffset, true},
        {"minato_kyubi", &MinatoKyubi, 0, Palette::None, EnergyGaugeRowOffset, false},
        {"hashirama_edo", &HashiramaEdo, 0, Palette::None, 0, false},
        {"naruto_student", &NarutoStudent, 0, Palette::None, 0, false},
    };
    AssetStore& store = AssetStore::Instance();
    for (const auto& spec : specs) {
        auto img = store.Image(std::string("ninja_templates/") + spec.file + ".png");
        if (img == nullptr || img->Width <= 0 || img->Height <= 0) continue;  // Go: ReadFile/Decode 失败 → continue
        Gray portrait;  // Nil() == Go nil
        if (*spec.name == ItachiHyakusen) {
            auto portraitImage = store.Image("ninja_templates/itachi_hyakusen_portrait.png");
            if (portraitImage != nullptr && portraitImage->Width > 0 && portraitImage->Height > 0) {
                portrait = match::ToGrayAsset(*portraitImage);
            }
        }
        nameTemplate t;
        t.readout.Name = *spec.name;
        t.readout.Slots = spec.slots;
        t.readout.Palette_ = spec.palette;
        t.readout.RowOffsetY = spec.rowOffsetY;
        t.gray = match::ToGrayAsset(*img);
        t.portrait = portrait;
        t.requirePortrait = spec.requirePortrait;
        r->source.push_back(std::move(t));
    }
    return r;
}

// Read uses scale relative to the supplied 960-wide HUD reference (15px
// nominal slot pitch), never the arbitrary width of a tightly cropped image.
// The cache holds only one scale and owns immutable templates.
Readout Reader::Read(const RGBA* img, Rect roi, double scale) {
    evidence e = read(img, roi, scale);
    return static_cast<const Readout&>(e);
}

evidence Reader::read(const RGBA* img, Rect roi, double scale) {
    if (img == nullptr || scale <= 0 || std::isnan(scale) || std::isinf(scale)) return evidence{};
    std::vector<scaledName> preparedCopy;
    {
        std::lock_guard<std::mutex> lock(mu);
        if (prepared.empty() || this->scale != scale) {
            prepared.clear();
            this->scale = scale;
            for (const auto& t : source) {
                int w = iround(static_cast<double>(t.gray.Bounds().Dx()) * scale);
                int h = iround(static_cast<double>(t.gray.Bounds().Dy()) * scale);
                if (w < 8 || h < 8) continue;
                Gray gray = match::ScaleGray(&t.gray, w, h);
                double coarseScale = std::min(scale, .5);
                Gray small = match::ScaleGray(&t.gray, iround(static_cast<double>(t.gray.Bounds().Dx()) * coarseScale),
                                              iround(static_cast<double>(t.gray.Bounds().Dy()) * coarseScale));
                scaledName p;
                p.readout = t.readout;
                p.ncc = match::PrepareNCC(&gray, nullptr);
                p.size = gray.Bounds().Size();
                p.coarse = match::PrepareNCC(&small, nullptr);
                p.requirePortrait = t.requirePortrait;
                if (!t.portrait.Nil()) {
                    int pw = iround(static_cast<double>(t.portrait.Bounds().Dx()) * scale);
                    int ph = iround(static_cast<double>(t.portrait.Bounds().Dy()) * scale);
                    Gray portrait = match::ScaleGray(&t.portrait, pw, ph);
                    p.portrait = match::PrepareNCC(&portrait, nullptr);
                    p.portraitSize = portrait.Bounds().Size();
                }
                prepared.push_back(std::move(p));
            }
        }
        // Go 复制的是切片头（共享不可变模板）；这里复制 shared_ptr，模板本身不可变。
        preparedCopy = prepared;
    }
    const std::vector<scaledName>& templates = preparedCopy;
    // 百战鼬 requires two independent, current-frame signals: its complete title
    // and its fixed right-HUD portrait. The portrait alone cannot move the bean
    // row or turn a base Itachi into the 百战 skin.
    roi = roi.Intersect(img->Bounds());
    if (roi.Empty()) return evidence{};
    RGBA view = img->SubImage(roi);
    Gray gray = match::ToGray(&view);
    // Unknown names used to scan the whole corner at native resolution. Keep
    // the locator bounded at the 480-wide HUD scale, then validate candidate
    // locations using ALL original-resolution pixels and the original threshold.
    double factor = std::min(scale, .5) / scale;
    Gray smallGray = match::ScaleGray(&gray, std::max(1, iround(static_cast<double>(gray.Bounds().Dx()) * factor)),
                                      std::max(1, iround(static_cast<double>(gray.Bounds().Dy()) * factor)));
    // NCC reads only the Image bounds when Gray is supplied.
    RGBA smallImage(nullptr, 0, smallGray.Bounds());
    // Each template's locate+refine pair is independent and only reads the
    // shared pixels and its immutable prepared templates, so they run
    // concurrently. The reduction below stays sequential in prepared order with
    // the same strict '>', so ties still go to the first template.
    struct titleResult {
        match::Score score;
        bool ok = false;
    };
    std::vector<titleResult> results(templates.size());
    ParallelFor(static_cast<int>(templates.size()), [&](int i) {
        const scaledName& t = templates[i];
        match::Query cq;
        cq.Image = &smallImage;
        cq.Gray = &smallGray;
        cq.ROI = smallImage.Bounds();
        cq.Prepared = t.coarse.get();
        match::Score coarse;
        if (!match::NCC{}.Match(cq, coarse) || coarse.Value < .55) return;
        Point point(roi.Min.X + iround(static_cast<double>(coarse.Peak.X) / factor),
                    roi.Min.Y + iround(static_cast<double>(coarse.Peak.Y) / factor));
        int radius = static_cast<int>(std::ceil(2 / factor));
        Rect candidate = Rect(point, point.Add(t.size)).Inset(-radius).Intersect(roi);
        match::Query fq;
        fq.Image = &view;
        fq.Gray = &gray;
        fq.ROI = candidate;
        fq.Prepared = t.ncc.get();
        match::Score score;
        if (!match::NCC{}.Match(fq, score)) return;
        results[i].score = score;
        results[i].ok = true;
    });
    evidence best;
    // Go map[string]float64：只用于逐名取最大值，结果与迭代顺序无关。
    std::map<std::string, double> scores;
    for (size_t i = 0; i < templates.size(); ++i) {
        if (!results[i].ok) continue;
        const scaledName& t = templates[i];
        const match::Score& score = results[i].score;
        auto it = scores.find(t.readout.Name);
        double prev = it == scores.end() ? 0 : it->second;
        scores[t.readout.Name] = std::max(prev, score.Value);
        if (score.Value > best.Score) {
            best = evidence{};
            static_cast<Readout&>(best) = t.readout;
            best.template_ = t.ncc;
            best.rect = Rect(score.Peak, score.Peak.Add(t.size));
            best.Score = score.Value;
        }
    }
    double runnerUp = 0.0;
    for (const auto& [name, score] : scores) {
        if (name != best.Name) runnerUp = std::max(runnerUp, score);
    }
    if (best.Score < 0.80 || best.Score - runnerUp < 0.08) return evidence{};
    // 百战鼬 requires two independent, current-frame signals: its complete title
    // and its fixed right-HUD portrait. Keep the verified portrait template on
    // the evidence so the Tracker's fast path can re-check it on later frames.
    if (best.Name == ItachiHyakusen) {
        scaledName t = scaledNameForEvidence(templates, best);
        if (!itachiPortraitEvidence(img, scale, t.portrait.get(), t.portraitSize)) return evidence{};
        best.portrait = t.portrait;
        best.portraitSize = t.portraitSize;
    }
    return best;
}

// ResolveEvidence decides identity from two independent CURRENT-frame sources.
// Exact complete titles win when present; an avatar can independently confirm a
// title or produce a bounded candidate only if it identifies an exact catalog
// variant. A base title never turns an avatar into a special variant.
Readout Reader::ResolveEvidence(const RGBA* img, Rect titleROI, Rect avatarROI, double scale, const Readout& title,
                                AvatarTracker* avatar, TimeNs now) {
    (void)titleROI;
    Readout out = title;
    out.TitleName = title.Name;
    // Go 对 nil *AvatarTracker 会 panic；这里视同没有头像证据。
    if (avatars == nullptr || avatarROI.Empty() || avatar == nullptr) return out;
    AvatarMatch m = avatar->Read(avatars.get(), img, avatarROI, scale, now);
    out.AvatarName = m.Name;
    out.AvatarScore = m.Score;
    if (!title.Name.empty()) {
        if (!m.Name.empty() && !sameAvatarEvidence(title.Name, m)) {
            // A disagreement never enables a version-dependent geometry rule.
            Readout r;
            r.TitleName = title.Name;
            r.AvatarName = m.Name;
            r.AvatarScore = m.Score;
            return r;
        }
        // Current complete title is still independently sufficient. Portrait
        // absence (crop/skin mismatch) does not regress reviewed title paths.
        out.Name = title.Name;
        return out;
    }
    if (!m.Name.empty()) {
        // A strong, separated current avatar can supply a bounded exact variant
        // when a long account name covers the title. Only explicit variant
        // policies below alter slots/palette/row geometry; other catalog entries
        // remain display identity only.
        return avatarReadout(m);
    }
    return out;
}

scaledName scaledNameForEvidence(const std::vector<scaledName>& prepared, const evidence& found) {
    for (const auto& t : prepared) {
        if (t.readout.Name == found.Name && t.requirePortrait) return t;
    }
    return scaledName{};
}

bool sameAvatarEvidence(const std::string& title, const AvatarMatch& m) {
    if (sameAvatarVariant(title, m.Name)) return true;
    // The HUD keeps the base form in the title while an equipped seasonal skin
    // changes only the portrait (骥玄凌霄 over 宇智波斑[神驹佑将]). The title's
    // base role may therefore corroborate that skin, but the full title keeps
    // authority: this must never turn the skin into the base variant or enable
    // a skin-only geometry rule.
    return !m.BaseName.empty() && normalizeAvatarName(avatarTitleRole(title)) == normalizeAvatarName(m.BaseName);
}

// avatarTitleRole extracts the ninja role before the first variant bracket.
std::string avatarTitleRole(const std::string& title) {
    int i = indexTitleBracket(title);
    if (i >= 0) return title.substr(0, static_cast<size_t>(i));
    return title;
}

Readout avatarReadout(const AvatarMatch& m) {
    std::string name = canonicalAvatarName(m.Name);
    Readout out;
    out.Name = name;
    out.AvatarName = name;
    out.AvatarScore = m.Score;
    const std::string key = normalizeAvatarName(name);
    if (key == normalizeAvatarName(Obito)) {
        out.Slots = 4;
        out.Palette_ = Purple;
    } else if (key == normalizeAvatarName(SasukeXiayin)) {
        out.Slots = 4;
        out.Palette_ = Xiayin;
        out.RowOffsetY = 9;
    } else if (key == normalizeAvatarName(ItachiHyakusen)) {
        // Corpus ID 920541 is 宇智波鼬[百战]. Only that exact current avatar
        // variant gets the ordinary four-slot row and its 13px energy shift.
        out.Slots = 4;
        out.RowOffsetY = EnergyGaugeRowOffset;
    }
    return out;
}

namespace {

// itachiPortraitScore converts only the portrait rect to gray. NCC offsets
// are relative to the view's bounds and its peak stays in frame coordinates,
// so the score equals matching the full-frame gray over the same rect.
bool itachiPortraitScore(const RGBA* img, const Rect& rect, const match::PreparedNCC* portrait, double& value) {
    RGBA view = img->SubImage(rect);
    Gray gray = match::ToGray(&view);
    match::Query q;
    q.Image = &view;
    q.Gray = &gray;
    q.ROI = rect;
    q.Prepared = portrait;
    match::Score score;
    bool ok = match::NCC{}.Match(q, score);
    value = ok ? score.Value : 0;
    return ok;
}

}  // namespace

bool itachiPortraitEvidence(const RGBA* img, double scale, const match::PreparedNCC* portrait, Point size) {
    if (portrait == nullptr || size.X <= 0 || size.Y <= 0) return false;
    Rect bounds = img->Bounds();
    // The supplied portrait is a right-HUD visual anchor. Do not mirror it to
    // the left: insufficient evidence must remain unknown, never a guessed
    // special offset. Coordinates are normalized to the 960-wide HUD reference.
    const double x0 = 847.5, x1 = 918.75, y0 = 7.5;
    Rect rect = MakeRect(bounds.Min.X + iround(x0 * scale), bounds.Min.Y + iround(y0 * scale),
                         bounds.Min.X + iround(x1 * scale), bounds.Min.Y + iround((y0 + 71) * scale))
                    .Intersect(bounds);
    if (rect.Size() != size) return false;
    double score = 0;
    bool ok = itachiPortraitScore(img, rect, portrait, score);
    return ok && score >= .78;
}

// NameRegion is deliberately above the calibrated bean row. A small extra
// upper band keeps the search stable for compact duel HUDs whose title baseline
// sits above the normal blood-bar offset; it does not move any bean center or
// authorize a slot/palette rule. Blood bars and full-screen effects do not get
// to move the sampling centers.
Rect NameRegion(Point first, double scale, bool left) {
    double x0 = -6.0, x1 = 340.0;
    if (!left) {
        // MuMu 1280x720 right titles can extend a few pixels beyond the
        // right-side first-bead anchor; keep the bounded ROI symmetric enough
        // to include the complete current title without searching the HUD.
        x0 = -340.0;
        x1 = 16.0;
    }
    return MakeRect(first.X + iround(x0 * scale), first.Y - iround(64 * scale), first.X + iround(x1 * scale),
                    first.Y - iround(13 * scale));
}

}  // namespace nt::ninja
