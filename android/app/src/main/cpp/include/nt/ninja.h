// nt/ninja.h — internal/ninja（owner: cpp-ninja；实现 src/ninja/{rules,reader,tracker,avatar}.cpp）
//
// Package ninja describes visual HUD variants and the user-confirmed cooldown
// policy. Archived skill tables are not authoritative for live timer rules.
//
// 资源（AssetStore key）：
//   名字条模板    "ninja_templates/<file>.png"（Go: internal/ninja/templates/*.png）
//   百战鼬头像    "ninja_templates/itachi_hyakusen_portrait.png"
//   头像图鉴      "avatars/index.json" + "avatars/<id>.png"（+ "avatars/<id>.png.sha256" 校验）
// 所有模板灰度必须用 match::ToGrayAsset（Go 对解码 PNG 的预乘通用路径）。
#pragma once

#include <cstdint>
#include <memory>
#include <mutex>
#include <string>
#include <utility>
#include <vector>

#include "nt/image.h"
#include "nt/match.h"
#include "nt/time.h"

namespace nt::ninja {

// Names are never destroyed (*new): exit() runs static destructors while a
// frame may still compare against them.
inline const std::string& NarutoStudent = *new std::string("漩涡鸣人[忍者学员]");
inline const std::string& FifthMizukage = *new std::string("照美冥[五代目水影]");
inline const std::string& Hashirama = *new std::string("千手柱间[木叶创立]");
inline const std::string& Madara = *new std::string("宇智波斑[神驹佑将]");
// MadaraLegacyAlias is retained for old local fixtures only. It is an exact
// spelling alias, not a relaxed match for other Madara variants.
inline const std::string& MadaraLegacyAlias = *new std::string("宇智波斑[神驹佑祥]");
inline const std::string& Obito = *new std::string("宇智波带土[十尾人柱力]");
inline const std::string& SasukeXiayin = *new std::string("宇智波佐助[侠隐江湖]");
inline const std::string& Naruto = *new std::string("漩涡鸣人[暴怒·第六尾]");
inline const std::string& ItachiHyakusen = *new std::string("宇智波鼬[百战]");
inline const std::string& MinatoKyubi = *new std::string("波风水门[九喇嘛连结]");
inline const std::string& HashiramaEdo = *new std::string("千手柱间[秽土转生]");
// Substitutes on its own like FifthMizukage: 15 s and 10 s.
inline const std::string& HanzoEdo = *new std::string("半藏[秽土转生]");
// EnergyGaugeRowOffset is measured from reviewed native HUDs. The energy
// gauge pushes these ordinary four-diamond rows down 13px at 960x540. It is
// active after a verified complete title or exact portrait (kept briefly as
// a tracker hint); unidentified rows use only rgb's energy-band fallback.
constexpr double EnergyGaugeRowOffset = 13;
constexpr DurationNs DefaultCooldown = 15 * Second;
constexpr DurationNs AlternateCooldown = 10 * Second;

// DualCooldown matches the full version, never a bare name or an ambiguous
// numeric ID from the old extracted table. Bracket typography is insignificant.
bool DualCooldown(const std::string& name);
// rules.go normalize：按 rune 去掉空白与 "[]【】()（）·・"（UTF-8 感知）。
std::string normalize(const std::string& s);
// ShortLabel：特殊变体的短名；其它名字超过 10 个 rune 截成 9 个 + "…"。
std::string ShortLabel(const std::string& name);

// Go: type Palette string（"" / "warm" / "purple" / "red" / "xiayin"）。
enum class Palette : uint8_t {
    None = 0,  // ""
    Warm,      // Blue/orange six-slot variants.
    Purple,
    Red,
    Xiayin,    // Exact Sasuke variant supports purple and red HUD skins.
};
constexpr Palette Warm = Palette::Warm;
constexpr Palette Purple = Palette::Purple;
constexpr Palette Red = Palette::Red;
constexpr Palette Xiayin = Palette::Xiayin;
const char* PaletteString(Palette p);

struct Readout {
    std::string Name;
    // TitleName and AvatarName preserve independent same-frame evidence for
    // diagnostics. Name is set only by ResolveEvidence after their agreement.
    std::string TitleName;
    std::string AvatarName;
    double AvatarScore = 0;
    int Slots = 0;
    Palette Palette_ = Palette::None;  // Go 字段名 Palette
    double Score = 0;
    // RowOffsetY shifts the calibrated bean row in 960x540 reference pixels.
    // It is geometry, not evidence of a current name or a readable bean.
    double RowOffsetY = 0;
    // Unverified retains recently verified slot geometry during a short label
    // gap. Name/Palette/Score are empty; hints alone MUST NOT supply bean votes.
    bool Unverified = false;
    // PaletteHint is a prior verified special-skin classifier hint. It is set
    // only while Unverified and never represents current name evidence.
    Palette PaletteHint = Palette::None;
};

using PreparedPtr = std::shared_ptr<const match::PreparedNCC>;

struct nameTemplate {
    Readout readout;
    Gray gray;
    Gray portrait;  // optional independent HUD portrait evidence（Nil() == Go nil）
    bool requirePortrait = false;
};

// fineTemplate is one prepared size of a title strip.
struct fineTemplate {
    PreparedPtr ncc;
    Point size;
};

// fineVariants are a strip's other fineFactors sizes at one scale, part of
// that scale's cached set. They are prepared on first use: only strips whose
// coarse locate passes ever need them, so the cache stays near its old size.
struct fineVariants {
    std::once_flag once;  // Go sync.Once
    Gray src;             // the source strip (shares its pixels)
    double scale = 0;
    Point nominal;
    std::vector<fineTemplate> list;
    // get returns the distinct sizes in fineFactors order (reader.cpp).
    const std::vector<fineTemplate>& get();
};

struct scaledName {
    Readout readout;
    PreparedPtr ncc;  // nominal size round(src·scale)
    Point size;
    std::shared_ptr<fineVariants> variants;  // nearby sizes, prepared on first use
    PreparedPtr coarse;
    PreparedPtr portrait;
    Point portraitSize;
    bool requirePortrait = false;
};

// Go: type evidence struct { Readout; template; rect; portrait; portraitSize }
struct evidence : Readout {
    PreparedPtr template_;
    Rect rect;
    PreparedPtr portrait;
    Point portraitSize;
};

// AvatarMatch is exclusively current-frame portrait evidence. Candidate is
// diagnostic-only when the strict identity fields are blank.
struct AvatarMatch {
    std::string ID;
    // Name is the canonical identity for this exact portrait asset.
    std::string Name;
    // BaseName is the official base ninja identity, retained independently of
    // the skin-specific Name.
    std::string BaseName;
    double Score = 0;
    double RunnerUp = 0;
    std::string Candidate;
    std::vector<std::string> ids;
};

// AvatarCatalogStats is useful in diagnostics without exposing asset storage.
struct AvatarCatalogStats {
    int Entries = 0;
    int Base = 0;
    int Skins = 0;
    int Bytes = 0;  // Android：解码后像素字节数（Go 为 PNG 字节数，仅诊断）
};

class AvatarCatalog;  // 完整定义见 nt/ninja_internal.h（cpp-ninja 内部）

// portraitAnchor places 百战鼬's fixed right-HUD portrait check on a
// calibrated right side: relative to its avatar window instead of the 16:9
// frame origin. The zero value keeps the built-in frame-origin rect.
struct portraitAnchor {
    Rect window;
    double scale = 0;
    bool operator==(const portraitAnchor& o) const { return window == o.window && scale == o.scale; }
    bool operator!=(const portraitAnchor& o) const { return !(*this == o); }
};

// 内置 A/S 图鉴（AssetStore "avatars/index.json"），进程内只加载一次（sync.OnceValues）。
// 失败返回 nullptr（Reader 此时 AvatarEnabled()==false，等价 Go）。
std::shared_ptr<AvatarCatalog> LoadEmbeddedAvatarCatalog(std::string* err = nullptr);
bool AvatarStats(AvatarCatalogStats& out, std::string* err = nullptr);

// AvatarTracker executes the 242-entry thumbnail filter at most twice per
// second per side. Other frames re-score only the previous six candidates on
// current pixels; no identity is inherited from a prior frame.
class AvatarTracker {
public:
    AvatarMatch Read(AvatarCatalog* c, const RGBA* img, Rect roi, double scale, TimeNs now);

private:
    void remember(const RGBA* img, const Rect& roi, bool full, const AvatarMatch& out);

    TimeNs next_ = 0;
    std::vector<std::string> ids_;
    AvatarCatalog* catalog_ = nullptr;
    Rect bounds_, roi_;
    double scale_ = 0;
    TimeNs lastAt_ = 0;
    // Pixel memo: the last ROI pixels (packed RGBA rows) and the result they
    // produced on the given path.
    std::vector<uint8_t> memoPix_;
    bool memoFull_ = false;
    std::vector<std::string> memoIDs_;
    AvatarMatch memoOut_;
    bool memoOK_ = false;
};

// 线程：与 Go 相同，Reader 可被多个 Tracker 并发调用（rgb 两侧 / camp+duel 并行），
// read() 内对缩放缓存的访问用 mu 保护（Go r.mu 的同一临界区）。
class Reader {
public:
    // NewReader uses the embedded A/S catalog（AssetStore 必须已就绪）。
    static std::shared_ptr<Reader> NewReader();

    // AvatarEnabled reports whether a validated catalog is available.
    bool AvatarEnabled() const { return avatars != nullptr; }

    // Read uses scale relative to the supplied 960-wide HUD reference (15px
    // nominal slot pitch), never the arbitrary width of a tightly cropped image.
    Readout Read(const RGBA* img, Rect roi, double scale);
    evidence read(const RGBA* img, Rect roi, double scale);
    evidence readAnchored(const RGBA* img, Rect roi, double scale, const portraitAnchor& anchor);

    // ResolveEvidence decides identity from two independent CURRENT-frame sources.
    Readout ResolveEvidence(const RGBA* img, Rect titleROI, Rect avatarROI, double scale, const Readout& title,
                            AvatarTracker* avatar, TimeNs now);

    std::vector<nameTemplate> source;
    // Go 只缓存一个 scale（prepared + scale）；这里按 scale 缓存最近 maxPreparedScales 组不可变
    // 模板（最近使用在前），校准后左右/camp/duel 各自的 scale 不再互相清空、每次重建。
    static constexpr size_t maxPreparedScales = 4;
    std::vector<std::pair<double, std::shared_ptr<const std::vector<scaledName>>>> prepared;
    std::shared_ptr<AvatarCatalog> avatars;
    std::mutex mu;  // Go r.mu
};

// Tracker keeps a name-search off the per-frame hot path.
// 每个 Tracker / AvatarTracker 只被一个并行体使用，因此不需要 Go 的 t.mu。
class Tracker {
public:
    Readout Read(Reader* reader, const RGBA* img, Rect roi, double scale, TimeNs now);
    // ReadWithAvatar keeps title and portrait evidence independent until a current
    // frame resolves them.
    Readout ReadWithAvatar(Reader* reader, AvatarTracker* avatar, const RGBA* img, Rect titleROI, Rect avatarROI,
                           double scale, TimeNs now);
    // ReadCalibrated is ReadWithAvatar for a player-calibrated side: the portrait
    // window has its own scale, and on the right side 百战鼬's fixed portrait
    // check follows that window instead of the 16:9 frame origin.
    Readout ReadCalibrated(Reader* reader, AvatarTracker* avatar, const RGBA* img, Rect titleROI, Rect avatarROI,
                           double scale, double avatarScale, bool right, TimeNs now);

private:
    Readout read(Reader* reader, const RGBA* img, Rect roi, double scale, const portraitAnchor& anchor, TimeNs now);

    Reader* reader_ = nullptr;
    evidence hint_;
    Rect bounds_, roi_;
    double scale_ = 0;
    portraitAnchor anchor_;
    TimeNs retryAfter_ = 0;
    TimeNs lastAt_ = 0;
    TimeNs verifiedAt_ = 0;
};

// NameRegion is deliberately above the calibrated bean row.
Rect NameRegion(Point first, double scale, bool left);
// AvatarRegion maps one normalized 960-wide reference ROI from an existing first-bead anchor.
Rect AvatarRegion(Point first, double scale, bool left);
// AvatarRegionFromBox converts a calibrated portrait box (frame pixels) into
// the AvatarRegion window; outScale receives the 960-wide scale (Go returns both).
Rect AvatarRegionFromBox(double x, double y, double w, double h, bool left, double& outScale);
// AvatarBoxFromRegion inverts AvatarRegionFromBox: the square portrait box
// (frame pixels) giving window and scale back (Go returns x, y, w, h).
void AvatarBoxFromRegion(const Rect& window, double scale, bool left, double& x, double& y, double& w, double& h);

// reader.go 内部 helper（跨 .cpp 共享，故在此声明）。
scaledName scaledNameForEvidence(const std::vector<scaledName>& prepared, const evidence& found);
bool sameAvatarEvidence(const std::string& title, const AvatarMatch& m);
std::string avatarTitleRole(const std::string& title);
Readout avatarReadout(const AvatarMatch& m);
bool itachiPortraitEvidence(const RGBA* img, double scale, const match::PreparedNCC* portrait, Point size);
bool itachiPortraitEvidenceAt(const RGBA* img, double scale, const match::PreparedNCC* portrait, Point size,
                              const portraitAnchor& anchor);

// avatar.go 名字 helper。
std::string canonicalAvatarName(const std::string& s);
bool sameAvatarVariant(const std::string& a, const std::string& b);
// normalizeAvatarName：去掉空白与 "[]【】「」()（）·・"。
std::string normalizeAvatarName(const std::string& s);

}  // namespace nt::ninja
