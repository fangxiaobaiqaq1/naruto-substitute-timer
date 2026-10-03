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

// avatarSize keys the per-entry scaled template cache.
struct avatarSize {
    int w = 0, h = 0;
    bool operator<(const avatarSize& o) const { return w != o.w ? w < o.w : h < o.h; }
};

struct avatarEntry {
    std::string id, name, baseName;
    std::shared_ptr<const AssetImage> data;  // Go: PNG 字节；这里是已解码像素，decode() 只做灰度/掩膜
    PreparedPtr thumb;
    Gray thumbGray, thumbMask;
    Gray coarseGray, coarseMask;
    Gray gray, mask;  // 懒计算（Go decode()）
    bool scaledValid = false;  // Go: scaled == nil
    std::map<avatarSize, PreparedPtr> scaled;
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
class AvatarCatalog {
public:
    AvatarMatch Match(const RGBA* img, Rect roi, double scale);
    // gray 为 nullptr 时内部转换（Go: gray == nil）。
    AvatarMatch matchEntries(const RGBA& view, const Gray* gray, double scale, const std::vector<avatarEntry*>& entries);
    void evictTemplates(double scale);
    PreparedPtr prepared(avatarEntry* entry, int w, int h);
    int templateCacheCap() const;

    std::vector<std::unique_ptr<avatarEntry>> entries;
    std::unordered_map<std::string, avatarEntry*> byID;
    double cacheScale = 0;
    int cachedEntries = 0;
    // cacheWipes counts overflow wipes of the scaled template cache; a single
    // scan must never trigger one.
    int cacheWipes = 0;
    // 注意：Match / matchEntries 可能被 rgb 的两侧并行体同时调用，因此不使用成员级
    // 临时缓冲（Go 每次调用都新分配）。
    std::mutex mu;  // Go c.mu

    // avatarJob is one entry at one display factor. prepared is nullptr when the
    // template is larger than the ROI (keeps the zero score).
    struct avatarJob {
        avatarEntry* entry = nullptr;
        PreparedPtr prepared;
        double score = 0;
    };
    // appendJobs fetches or prepares the entry's template at each factor (caller holds mu).
    void appendJobs(std::vector<avatarJob>& jobs, avatarEntry* entry, double scale, const Rect& bounds,
                    const std::vector<double>& factors);
    // decode fills the full-resolution gray/mask lazily (caller holds mu).
    bool decode(avatarEntry* entry);
};

// avatarJobScore only reads the immutable template and the caller's pixels.
double avatarJobScore(const RGBA& view, const Gray* gray, const match::PreparedNCC* prepared);

// loadAvatarCatalog：解析 index.json，逐条校验 id / sha256（对照 AssetStore 的 "<path>.sha256"），
// 生成 16² thumb 与 36² coarse。任何一条失败返回 nullptr（Go 返回 error）。
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
// avatarGrayMask：预乘亮度 + (a16 >= 0x8000) 掩膜。
void avatarGrayMask(const AssetImage& src, Gray& gray, Gray& mask);
Rect avatarAnchorRect(const Rect& roi, double scale);
double coarseAvatarScore(const Gray* query, const Gray* templ, const Gray* mask);
// avatarFactorsFull is the strict multi-scale display-factor sweep.
inline const std::vector<double> avatarFactorsFull = {.55, .60, .65, .70, .75, .85, 1.0, 1.15, 1.30, 1.45};
// avatarFactorPrimary is the full-scan locator factor; avatarFactorsFine is the
// full sweep without it, in the same order, for the re-scored leaders.
inline const std::vector<double> avatarFactorPrimary = {.60};
inline const std::vector<double> avatarFactorsFine = {.55, .65, .70, .75, .85, 1.0, 1.15, 1.30, 1.45};
bool avatarVariantPolicy(const std::string& name);
void avatarFactorSize(const avatarEntry* entry, double scale, double factor, int& w, int& h);
bool samePackedPix(const std::vector<uint8_t>& packed, const RGBA* img, const Rect& roi);

}  // namespace nt::ninja
