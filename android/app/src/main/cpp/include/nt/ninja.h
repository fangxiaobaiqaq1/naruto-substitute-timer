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
#include <vector>

#include "nt/image.h"
#include "nt/match.h"
#include "nt/time.h"

namespace nt::ninja {

inline const std::string NarutoStudent = "漩涡鸣人[忍者学员]";
inline const std::string FifthMizukage = "照美冥[五代目水影]";
inline const std::string Hashirama = "千手柱间[木叶创立]";
inline const std::string Madara = "宇智波斑[神驹佑将]";
// MadaraLegacyAlias is retained for old local fixtures only. It is an exact
// spelling alias, not a relaxed match for other Madara variants.
inline const std::string MadaraLegacyAlias = "宇智波斑[神驹佑祥]";
inline const std::string Obito = "宇智波带土[十尾人柱力]";
inline const std::string SasukeXiayin = "宇智波佐助[侠隐江湖]";
inline const std::string Naruto = "漩涡鸣人[暴怒·第六尾]";
inline const std::string ItachiHyakusen = "宇智波鼬[百战]";
inline const std::string MinatoKyubi = "波风水门[九喇嘛连结]";
inline const std::string HashiramaEdo = "千手柱间[秽土转生]";
// EnergyGaugeRowOffset is measured from reviewed native HUDs. The energy
// gauge pushes these ordinary four-diamond rows down 13px at 960x540. It is
// active only after the complete title template is verified.
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

struct scaledName {
    Readout readout;
    PreparedPtr ncc;
    Point size;
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

    // ResolveEvidence decides identity from two independent CURRENT-frame sources.
    Readout ResolveEvidence(const RGBA* img, Rect titleROI, Rect avatarROI, double scale, const Readout& title,
                            AvatarTracker* avatar, TimeNs now);

    std::vector<nameTemplate> source;
    double scale = 0;
    std::vector<scaledName> prepared;  // Go: nil 时需要重新准备（empty == nil）
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

private:
    Reader* reader_ = nullptr;
    evidence hint_;
    Rect bounds_, roi_;
    double scale_ = 0;
    TimeNs retryAfter_ = 0;
    TimeNs lastAt_ = 0;
    TimeNs verifiedAt_ = 0;
};

// NameRegion is deliberately above the calibrated bean row.
Rect NameRegion(Point first, double scale, bool left);
// AvatarRegion maps one normalized 960-wide reference ROI from an existing first-bead anchor.
Rect AvatarRegion(Point first, double scale, bool left);

// reader.go 内部 helper（跨 .cpp 共享，故在此声明）。
scaledName scaledNameForEvidence(const std::vector<scaledName>& prepared, const evidence& found);
bool sameAvatarEvidence(const std::string& title, const AvatarMatch& m);
std::string avatarTitleRole(const std::string& title);
Readout avatarReadout(const AvatarMatch& m);
bool itachiPortraitEvidence(const RGBA* img, double scale, const match::PreparedNCC* portrait, Point size);

// avatar.go 名字 helper。
std::string canonicalAvatarName(const std::string& s);
bool sameAvatarVariant(const std::string& a, const std::string& b);
// normalizeAvatarName：去掉空白与 "[]【】「」()（）·・"。
std::string normalizeAvatarName(const std::string& s);

}  // namespace nt::ninja
