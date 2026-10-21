// nt/ninja_internal.h — internal/ninja/avatar.go 的未导出类型（owner: cpp-ninja，仅 ninja 模块内部使用）。
// cpp-ninja 可以自由修改本文件；其它模块不得包含它。
#pragma once

#include <map>
#include <memory>
#include <mutex>
#include <string>
#include <unordered_map>
#include <vector>

#include "nt/assets.h"
#include "nt/image.h"
#include "nt/json.h"
#include "nt/match.h"
#include "nt/ninja.h"

namespace nt::ninja {

struct avatarIndexEntry {
    std::string ID;            // "id"
    bool IsSkin = false;       // "isSkin"
    std::string Ninja;         // "ninja"
    std::string Form;          // "form"
    std::string SkinTitle;     // "skinTitle"
    std::string BaseNinjaID;   // "baseNinjaId"
    std::string BaseNinjaName; // "baseNinjaName"
    std::string CanonicalName; // "canonicalName"
    std::string AvatarSHA256;  // "avatar": {"sha256"}
    std::string SHA256;        // "sha256"（bundled manifest format）
};

struct avatarEntry {
    std::string id, name, baseName;
    // Go: PNG 字节；这里是注册时转换好的资源图（亮度 + alpha>=128 位图，见 nt/assets.h）。
    std::shared_ptr<const AssetImage> data;
    // Go 的 16² thumb 从未被读取，Android 不再生成（省内存与加载时间）。
    Gray coarseGray, coarseMask;
    // Go decode() 的灰度 = data->Luma 的共享视图。全分辨率脸部掩膜（alpha ∩ 菱形）不常驻：
    // 模板缓存未命中时由 avatarFaceMask(*data) 现算（与原掩膜逐位相同）。
    Gray gray;
};

// avatarTemplateKey keys the catalog's scaled-template cache: one entry at one
// pixel size. The prepared template is a pure function of (entry, w, h), so a
// cached template is valid for every scale/side that asks for that size.
struct avatarTemplateKey {
    const avatarEntry* entry = nullptr;
    int w = 0, h = 0;
    bool operator==(const avatarTemplateKey& o) const { return entry == o.entry && w == o.w && h == o.h; }
};

struct avatarTemplateKeyHash {
    size_t operator()(const avatarTemplateKey& k) const {
        return std::hash<const void*>()(k.entry) ^ (static_cast<size_t>(k.w) * 0x9e3779b1u) ^
               (static_cast<size_t>(k.h) * 0x85ebca6bu);
    }
};

struct avatarFinalist {
    avatarEntry* entry = nullptr;
    double score = 0;
};

struct stageEntry {
    avatarEntry* entry = nullptr;
    double score = 0;
};

// 线程：与 Go 相同，mu 只保护可变的模板状态（gray/mask 懒解码、scaled、cacheScale、
// cachedEntries、cacheWipes）；NCC 打分在锁外进行（可用 nt::ParallelFor，按原始
// entry/factor 顺序以严格 '>' 归约，结果与串行一致）。
// avatarScores is the best NCC score per identity over everything one match
// scored (Go avatarScores), with the identity names. order lists the scored
// entries (the scan's list, then the second look's): decide() walks it, first
// occurrence of an id only, instead of Go's random map order (see avatar.cpp's
// header for the only case where it matters).
struct avatarScores {
    std::unordered_map<std::string, double> best;
    std::unordered_map<std::string, std::string> name, base;
    std::vector<avatarEntry*> order;
    // note registers a decoded entry's names.
    void note(avatarEntry* entry);
    // decide fills out's winner, runner-up and candidate and applies the strict identity gate.
    AvatarMatch decide(AvatarMatch out) const;
};

// avatarView is one match's window: its pixels, their gray and the shared
// prefix sums of that gray (nullptr: each NCC builds its own).
struct avatarView {
    const RGBA& view;
    const Gray* gray;
    const match::grayPrefix* prefix;
};

class AvatarCatalog {
public:
    AvatarMatch Match(const RGBA* img, Rect roi, double scale);
    // gray 为 nullptr 时内部转换（Go: gray == nil）。
    AvatarMatch matchEntries(const RGBA& view, const Gray* gray, double scale, const std::vector<avatarEntry*>& entries);
    PreparedPtr prepared(avatarEntry* entry, int w, int h);

    std::vector<std::unique_ptr<avatarEntry>> entries;
    std::unordered_map<std::string, avatarEntry*> byID;
    // Scaled-template cache (caller holds mu): keyed by (entry, w, h) instead of
    // one global scale, so calibrated sides with different portrait scales (or
    // camp/duel) never wipe each other's templates. Templates unused for
    // templateIdleCalls matchEntries calls (an opponent seen rounds ago) are
    // dropped; templateCacheCap bytes bound the rest, least recently used first
    // (the scan in progress holds the newest). Typical: ~1-2 MiB at 1080p-1600p.
    struct cachedTemplate {
        PreparedPtr prepared;
        size_t bytes = 0;
        uint64_t used = 0;   // templateClock at last use (LRU order)
        uint64_t epoch = 0;  // templateEpoch at last use (idle eviction)
    };
    std::unordered_map<avatarTemplateKey, cachedTemplate, avatarTemplateKeyHash> templates;
    size_t templateBytes = 0;
    size_t templateCacheCap = static_cast<size_t>(8) << 20;
    uint64_t templateClock = 0;
    uint64_t templateEpoch = 0;  // matchEntries calls
    static constexpr uint64_t templateIdleCalls = 2048;
    // templateEvictions counts evicted templates (diagnostics/tests).
    int templateEvictions = 0;
    // beginScan advances the epoch and drops idle templates (caller holds mu).
    void beginScan();
    // 注意：Match / matchEntries 可能被 rgb 的两侧并行体同时调用，因此不使用成员级
    // 临时缓冲（Go 每次调用都新分配）。
    std::mutex mu;  // Go c.mu

    // avatarJob is one entry at one display factor. prepared is nullptr when the
    // template is larger than its search area (keeps the zero score). roi is the
    // search area; empty = the whole view.
    struct avatarJob {
        avatarEntry* entry = nullptr;
        PreparedPtr prepared;
        Rect roi;
        double score = 0;
    };
    // appendJobs fetches or prepares the entry's template at each factor (caller holds mu).
    void appendJobs(std::vector<avatarJob>& jobs, avatarEntry* entry, double scale, const Rect& bounds,
                    const std::vector<double>& factors);
    // appendPortraitJobs: the entry's generation factors, searched in the portrait band (caller holds mu).
    void appendPortraitJobs(std::vector<avatarJob>& jobs, avatarEntry* entry, double scale, const Rect& bounds);
    // scoreEntries is matchEntries that also returns the scores behind its result.
    AvatarMatch scoreEntries(const avatarView& v, double scale, const std::vector<avatarEntry*>& entries,
                             avatarScores& scores);
    // rescue is the second look of a full scan that found no identity (avatar.cpp).
    AvatarMatch rescue(const avatarView& v, double scale, avatarScores& scores);
    // addScores scores jobs (in parallel) and keeps each identity's best score.
    static void addScores(const avatarView& v, std::vector<avatarJob>& jobs, avatarScores& scores);
    // decode fills the full-resolution gray/mask lazily (caller holds mu).
    bool decode(avatarEntry* entry);
};

// avatarJobScore only reads the immutable template and the caller's pixels
// (prefix: optional shared prefix sums of *gray, see match::Query::Prefix).
// roi is the search area; empty = the whole view.
double avatarJobScore(const RGBA& view, const Gray* gray, const match::PreparedNCC* prepared,
                      const match::grayPrefix* prefix = nullptr, const Rect& roi = Rect{});

// loadAvatarCatalog：解析 index.json，逐条校验 id / sha256（对照 AssetStore 的 "<path>.sha256"），
// 生成 36² coarse。任何一条失败返回 nullptr（Go 返回 error）。
std::shared_ptr<AvatarCatalog> loadAvatarCatalog(const std::string& indexJSON, const std::string& dir,
                                                 std::string* err);

// loadAvatarEntry reads, verifies and decodes one indexed asset（只写自己的 entry，可并发）。
std::unique_ptr<avatarEntry> loadAvatarEntry(const avatarIndexEntry& item, const std::string& sum,
                                             const std::string& dir, std::string* err);

bool validAvatarID(const std::string& id);
std::string avatarBaseName(const avatarIndexEntry& item);
std::string avatarName(const avatarIndexEntry& item);
// avatarFaceMask keeps alpha ∩ the inner diamond.
Gray avatarFaceMask(const Rect& bounds, const Gray& alpha);
// avatarFaceMask(src): the same mask from the asset's alpha >= 128 bits.
Gray avatarFaceMask(const AssetImage& src);
// avatarGrayMask：预乘亮度 + (a16 >= 0x8000) 掩膜。
void avatarGrayMask(const AssetImage& src, Gray& gray, Gray& mask);
Rect avatarAnchorRect(const Rect& roi, double scale);
double coarseAvatarScore(const Gray* query, const Gray* templ, const Gray* mask);
// avatarFactorsFull is the strict multi-scale display-factor sweep (Go avatar.go:
// the HUD renders an 85px icon near 0.55x, a 70px icon near 0.72x, see avatarClassFactors70).
inline const std::vector<double>& avatarFactorsFull = *new std::vector<double>{.55, .60, .65, .70, .75, .85, 1.0, 1.15, 1.30, 1.45};
// avatarFactorPrimary is the full-scan locator factor; avatarFactorsFine is the
// full sweep without it, in the same order, for the re-scored leaders.
inline const std::vector<double>& avatarFactorPrimary = *new std::vector<double>{.60};
inline const std::vector<double>& avatarFactorsFine = *new std::vector<double>{.55, .65, .70, .75, .85, 1.0, 1.15, 1.30, 1.45};
// The two catalog icon generations render at different HUD portrait sizes:
// avatarClassFactors70 (70px icons) and avatarClassFactors85 (85px icons); see
// the comment in avatar.cpp (Go avatar.go).
inline const std::vector<double>& avatarClassFactors70 = *new std::vector<double>{.64, .68, .72, .76, .80, .84};
inline const std::vector<double>& avatarClassFactors85 = *new std::vector<double>{.49, .52, .55, .58};
constexpr int avatarRescueEntries = 6;
constexpr double avatarPortraitSide = 48.0;
constexpr double avatarPortraitSlack = 6.0;
// avatarClassFactors are the display factors of the entry's icon generation.
const std::vector<double>& avatarClassFactors(const avatarEntry* entry);
// avatarPortraitRect is the portrait square around a diamond centre at
// (cx, avatarBoxCentreY) window units, in the window's own pixels (origin 0).
Rect avatarPortraitRect(double cx, double scale);
// avatarPortraitBand is the search area of a w x h portrait template in the window bounds.
Rect avatarPortraitBand(const Rect& bounds, double scale, int w, int h);
bool avatarVariantPolicy(const std::string& name);
void avatarFactorSize(const avatarEntry* entry, double scale, double factor, int& w, int& h);
bool samePackedPix(const std::vector<uint8_t>& packed, const RGBA* img, const Rect& roi);

}  // namespace nt::ninja
