// Owner: cpp-ninja
// Port of: internal/ninja/avatar.go
// Contract: include/nt/ninja_internal.h — see android/ARCHITECTURE.md.
//
// 忠实移植 Go 原算法：阈值、ROI、常量、判定顺序逐行对应，保留解释"为什么"的中文注释。
//
// 与 Go 的差异：
//  * 图鉴 PNG 由 Kotlin 解码进 AssetStore；完整性校验对照 "<path>.sha256"（Kotlin 对原始 PNG
//    字节算的 SHA-256），等价 Go 对 PNG 字节的 sha256.Sum256。
//  * avatarGrayMask 输入是非预乘 RGBA8：Go 对 RGB PNG（*image.RGBA，a=255）、
//    RGBA PNG（*image.NRGBA）与灰度 PNG（At().RGBA()）三条路径在 8 位 PNG 上逐位等价于
//    GoLumaFromNRGBA + (a16 >= 0x8000)。
//  * matchEntries 里 Go 的 `for id, score := range bestByID` 是随机顺序；分数互不相同时
//    最高分/次高分与顺序无关。只有浮点分数恰好并列时才依赖顺序：并列最高分时 margin=0
//    必然清空身份（只影响 Candidate 诊断字段）；并列次高分时 runnerName 可能不同，从而影响
//    放宽通道的 avatarVariantPolicy(runnerName) 判断（Go 本身在这种情况下就不确定）。
//    这里按 entries 顺序遍历，使结果确定。
//  * LoadEmbeddedAvatarCatalog：成功结果进程内只加载一次（Go sync.OnceValues）；失败时若
//    AssetStore 之后有变化（资源尚未注册完）允许重试，避免启动顺序把失败永久缓存。
//  * AvatarOptions / LoadAvatarCatalog（外部开发目录）不移植。
#include <algorithm>
#include <cctype>
#include <cmath>
#include <cstdlib>
#include <cstring>
#include <mutex>
#include <string>
#include <unordered_map>
#include <unordered_set>
#include <vector>

#include "nt/assets.h"
#include "nt/json.h"
#include "nt/ninja_internal.h"
#include "nt/parallel.h"
#include "nt/utf8.h"

namespace nt::ninja {

namespace {

std::string quote(const std::string& s) { return "\"" + s + "\""; }

bool equalFold(const std::string& a, const std::string& b) {
    if (a.size() != b.size()) return false;
    for (size_t i = 0; i < a.size(); ++i) {
        if (std::tolower(static_cast<unsigned char>(a[i])) != std::tolower(static_cast<unsigned char>(b[i]))) {
            return false;
        }
    }
    return true;
}

bool validHex(const std::string& s) {
    if (s.size() % 2 != 0) return false;
    for (char c : s) {
        if (!std::isxdigit(static_cast<unsigned char>(c))) return false;
    }
    return true;
}

// strings.IndexAny(base, "「[")
size_t indexAnyBracket(const std::string& s) {
    size_t a = s.find("「");
    size_t b = s.find('[');
    return std::min(a, b);
}

bool parseIndexEntry(const json& v, avatarIndexEntry& e) {
    if (!v.is_object()) return false;
    e.ID = JsonString(v, "id");
    e.IsSkin = JsonBool(v, "isSkin");
    e.Ninja = JsonString(v, "ninja");
    e.Form = JsonString(v, "form");
    e.SkinTitle = JsonString(v, "skinTitle");
    e.BaseNinjaID = JsonString(v, "baseNinjaId");
    e.BaseNinjaName = JsonString(v, "baseNinjaName");
    e.CanonicalName = JsonString(v, "canonicalName");
    auto av = v.find("avatar");
    if (av != v.end() && av->is_object()) e.AvatarSHA256 = JsonString(*av, "sha256");
    e.SHA256 = JsonString(v, "sha256");
    return true;
}

bool sortStage(const stageEntry& a, const stageEntry& b) {
    if (a.score != b.score) return a.score > b.score;
    return a.entry->id < b.entry->id;
}

}  // namespace

// ---------------------------------------------------------------------------
// 内置图鉴

namespace {
std::mutex g_embeddedMu;
std::shared_ptr<AvatarCatalog> g_embedded;
bool g_embeddedTried = false;
uint64_t g_embeddedRevision = 0;
std::string g_embeddedErr;
}  // namespace

std::shared_ptr<AvatarCatalog> LoadEmbeddedAvatarCatalog(std::string* err) {
    std::lock_guard<std::mutex> lk(g_embeddedMu);
    AssetStore& store = AssetStore::Instance();
    uint64_t rev = store.Revision();
    if (!g_embedded && (!g_embeddedTried || rev != g_embeddedRevision)) {
        g_embeddedTried = true;
        g_embeddedRevision = rev;
        g_embeddedErr.clear();
        auto index = store.Text("avatars/index.json");
        if (!index) {
            g_embeddedErr = "read avatar index: avatars/index.json: file does not exist";
        } else {
            g_embedded = loadAvatarCatalog(*index, "avatars/", &g_embeddedErr);
        }
    }
    if (err) *err = g_embedded ? std::string() : g_embeddedErr;
    return g_embedded;
}

bool AvatarStats(AvatarCatalogStats& out, std::string* err) {
    out = AvatarCatalogStats{};
    auto c = LoadEmbeddedAvatarCatalog(err);
    if (!c) return false;
    out.Entries = static_cast<int>(c->entries.size());
    for (const auto& e : c->entries) {
        if (e->data) out.Bytes += static_cast<int>(e->data->Pixels.size());
        if (e->id.size() == 5) {
            out.Base++;
        } else {
            out.Skins++;
        }
    }
    return true;
}

std::shared_ptr<AvatarCatalog> loadAvatarCatalog(const std::string& indexJSON, const std::string& dir,
                                                 std::string* err) {
    json doc = json::parse(indexJSON, nullptr, /*allow_exceptions=*/false);
    if (doc.is_discarded() || !doc.is_object()) {
        if (err) *err = "parse avatar index: invalid JSON";
        return nullptr;
    }
    std::vector<avatarIndexEntry> index;
    auto it = doc.find("entries");
    if (it != doc.end() && it->is_array()) {
        for (const auto& v : *it) {
            avatarIndexEntry e;
            if (!parseIndexEntry(v, e)) {
                if (err) *err = "parse avatar index: invalid entry";
                return nullptr;
            }
            index.push_back(std::move(e));
        }
    }
    if (index.empty()) {
        if (err) *err = "avatar index has no entries";
        return nullptr;
    }
    // Cheap index validation stays sequential (duplicates depend on order);
    // the per-asset read/verify/decode work runs on a bounded pool into an
    // index-ordered slice. The error reported is the one the sequential loop
    // would have stopped at: the first failing index, validation first.
    const int n = static_cast<int>(index.size());
    std::vector<std::string> errs(n);
    std::vector<char> failed(n, 0);
    std::vector<std::string> sums(n);
    std::unordered_set<std::string> seen;
    for (int i = 0; i < n; ++i) {
        const avatarIndexEntry& item = index[i];
        std::string sum = item.SHA256;
        if (sum.empty()) sum = item.AvatarSHA256;
        if (!validAvatarID(item.ID) || seen.count(item.ID) || sum.size() != 64) {
            failed[i] = 1;
            errs[i] = "invalid or duplicate avatar id " + quote(item.ID);
        } else if (!validHex(sum)) {
            failed[i] = 1;
            errs[i] = "invalid avatar sha256 for " + item.ID;
        }
        seen.insert(item.ID);
        sums[i] = sum;
    }
    std::vector<std::unique_ptr<avatarEntry>> entries(n);
    ParallelFor(n, [&](int i) {
        if (!failed[i]) {
            std::string e;
            entries[i] = loadAvatarEntry(index[i], sums[i], dir, &e);
            if (!entries[i]) {
                failed[i] = 1;
                errs[i] = e;
            }
        }
    });
    for (int i = 0; i < n; ++i) {
        if (failed[i]) {
            if (err) *err = errs[i];
            return nullptr;
        }
    }
    auto catalog = std::make_shared<AvatarCatalog>();
    catalog->entries = std::move(entries);
    for (auto& entry : catalog->entries) catalog->byID[entry->id] = entry.get();
    return catalog;
}

// loadAvatarEntry reads, verifies and decodes one indexed asset. It touches
// only its own entry, so the catalog loader may run it concurrently.
std::unique_ptr<avatarEntry> loadAvatarEntry(const avatarIndexEntry& item, const std::string& sum,
                                             const std::string& dir, std::string* err) {
    AssetStore& store = AssetStore::Instance();
    const std::string path = dir + item.ID + ".png";
    auto img = store.Image(path);
    auto got = store.Text(path + ".sha256");
    if (!img || !got) {
        if (err) *err = "read avatar " + item.ID + ": " + path + ": file does not exist";
        return nullptr;
    }
    if (!equalFold(*got, sum)) {
        if (err) *err = "avatar " + item.ID + " sha256 does not match index";
        return nullptr;
    }
    Gray gray, alphaMask;
    avatarGrayMask(*img, gray, alphaMask);
    if (gray.Bounds().Dx() < 16 || gray.Bounds().Dy() < 16) {
        if (err) *err = "avatar " + item.ID + " too small";
        return nullptr;
    }
    // The catalog PNG carries a baked rank badge/frame/background. Those
    // pixels vary in the live HUD, while the face inside the diamond is
    // stable. Recognition uses alpha ∩ inner-diamond only.
    Gray mask = avatarFaceMask(gray.Bounds(), alphaMask);
    auto e = std::make_unique<avatarEntry>();
    e->id = item.ID;
    e->name = avatarName(item);
    e->baseName = avatarBaseName(item);
    e->data = img;
    e->thumbGray = match::ScaleGray(&gray, 16, 16);
    e->thumbMask = match::ScaleGray(&mask, 16, 16);
    e->thumb = match::PrepareNCC(&e->thumbGray, &e->thumbMask);
    e->coarseGray = match::ScaleGray(&gray, 36, 36);
    e->coarseMask = match::ScaleGray(&mask, 36, 36);
    // The full-resolution gray/mask are exactly what matchEntries would
    // otherwise re-decode lazily from data; keep them so a PNG is decoded once.
    e->gray = std::move(gray);
    e->mask = std::move(mask);
    return e;
}

bool validAvatarID(const std::string& id) {
    if (id.size() != 5 && id.size() != 6) return false;
    for (char r : id) {
        if (r < '0' || r > '9') return false;
    }
    return true;
}

std::string avatarBaseName(const avatarIndexEntry& item) {
    std::string base = item.Ninja;
    if (item.IsSkin) base = item.BaseNinjaName;
    size_t i = indexAnyBracket(base);
    if (i != std::string::npos) base = base.substr(0, i);
    if (base.empty()) base = item.Ninja;
    return base;
}

std::string avatarName(const avatarIndexEntry& item) {
    std::string base = item.Ninja, form = item.Form;
    if (item.IsSkin) {
        base = item.BaseNinjaName;
        form = item.SkinTitle;
    }
    size_t i = indexAnyBracket(base);
    if (i != std::string::npos) base = base.substr(0, i);
    if (base.empty()) base = item.Ninja;
    if (form.empty()) return base;
    return base + "[" + form + "]";
}

// avatarFaceMask keeps alpha ∩ the inner diamond: the baked rank badge, frame,
// and background around the portrait vary in the live HUD, the face does not.
Gray avatarFaceMask(const Rect& bounds, const Gray& alpha) {
    Gray mask = Gray::New(MakeRect(0, 0, bounds.Dx(), bounds.Dy()));
    double cx = static_cast<double>(bounds.Dx() - 1) / 2, cy = static_cast<double>(bounds.Dy() - 1) / 2;
    double radius = static_cast<double>(std::min(bounds.Dx(), bounds.Dy())) * 0.43;
    for (int y = 0; y < bounds.Dy(); ++y) {
        for (int x = 0; x < bounds.Dx(); ++x) {
            if (alpha.GrayAt(alpha.Bounds().Min.X + x, alpha.Bounds().Min.Y + y) >= 128 &&
                std::fabs(static_cast<double>(x) - cx) + std::fabs(static_cast<double>(y) - cy) <= radius) {
                mask.SetGray(x, y, 255);
            }
        }
    }
    return mask;
}

void avatarGrayMask(const AssetImage& src, Gray& gray, Gray& mask) {
    const int w = src.Width, h = src.Height;
    gray = Gray::New(MakeRect(0, 0, w, h));
    mask = Gray::New(MakeRect(0, 0, w, h));
    // The direct paths reproduce color.NRGBA.RGBA / color.RGBA.RGBA (16-bit
    // expansion, NRGBA alpha premultiply) and the same >>8 luma byte for byte.
    for (int y = 0; y < h; ++y) {
        const uint8_t* row = src.Pixels.data() + static_cast<size_t>(y) * 4 * w;
        for (int x = 0; x < w; ++x) {
            const uint8_t* p = row + 4 * x;
            gray.Pix[y * gray.Stride + x] = GoLumaFromNRGBA(p[0], p[1], p[2], p[3]);
            if (GoAlpha16(p[3]) >= 0x8000) mask.Pix[y * mask.Stride + x] = 255;
        }
    }
}

AvatarMatch AvatarCatalog::Match(const RGBA* img, Rect roi, double scale) {
    if (img == nullptr || scale <= 0) return AvatarMatch{};
    roi = roi.Intersect(img->Bounds());
    if (roi.Empty()) return AvatarMatch{};
    // The coarse locator reads only entries and their load-time immutable
    // fields (id, coarseGray, coarseMask), so it runs without the catalog lock;
    // matchEntries takes it only for its template cache.
    RGBA view = img->SubImage(roi);
    Gray gray = match::ToGray(&view);
    Gray coarse = match::ScaleGray(&gray, 36, 36);
    // The portrait is a fixed HUD element: relative to the canonical
    // AvatarRegion window its diamond center sits near (35.5, 48) reference
    // pixels and renders at roughly 0.6x the 85px catalog asset. A square crop
    // around that anchor removes the ROI's background and vertical squash from
    // the 36x36 locator. The unanchored crop stays as a fallback for callers
    // that pass a synthetic centered portrait rather than the HUD ROI.
    // gray has origin (0,0) at roi.Min; cropping it equals converting the
    // cropped RGBA because luma is per pixel.
    Rect anchor = avatarAnchorRect(roi, scale).Intersect(roi).Sub(roi.Min);
    Gray anchored = gray.SubImage(anchor);
    Gray hudCoarse = match::ScaleGray(&anchored, 36, 36);
    std::vector<stageEntry> best;
    best.reserve(entries.size());
    for (const auto& entry : entries) {
        // Two aligned 36² thumbnail queries (HUD anchor + whole ROI) keep
        // recall for both real HUD frames and centered synthetic portraits.
        // Only the retained candidates do full-resolution NCC.
        double score = coarseAvatarScore(&coarse, &entry->coarseGray, &entry->coarseMask);
        double hud = coarseAvatarScore(&hudCoarse, &entry->coarseGray, &entry->coarseMask);
        if (hud > score) score = hud;
        best.push_back(stageEntry{entry.get(), score});
    }
    std::stable_sort(best.begin(), best.end(), sortStage);
    // Coarse matching is only a locator. A real HUD portrait can be partially
    // clipped by the capture boundary and its render scale is not guaranteed to
    // equal the bead-derived scale, so the true entry may rank below the first
    // few color-similar portraits (measured worst case: 水门 at rank 10). Keep
    // this bounded recall set; strict multi-scale NCC and the score margin
    // still decide identity.
    if (best.size() > 24) best.resize(24);
    std::vector<avatarEntry*> list;
    list.reserve(best.size());
    for (const auto& b : best) list.push_back(b.entry);
    return matchEntries(view, &gray, scale, list);
}

// avatarAnchorRect maps the measured HUD diamond neighborhood into img pixels.
// Coordinates are relative to the AvatarRegion window and normalized to the
// 960-wide HUD reference.
Rect avatarAnchorRect(const Rect& roi, double scale) {
    const double cx = 35.5, cy = 48.0, half = 34.0;
    return MakeRect(roi.Min.X + static_cast<int>(std::round((cx - half) * scale)),
                    roi.Min.Y + static_cast<int>(std::round((cy - half) * scale)),
                    roi.Min.X + static_cast<int>(std::round((cx + half) * scale)),
                    roi.Min.Y + static_cast<int>(std::round((cy + half) * scale)));
}

double coarseAvatarScore(const Gray* query, const Gray* templ, const Gray* mask) {
    if (query == nullptr || templ == nullptr || mask == nullptr ||
        query->Bounds().Size() != templ->Bounds().Size() || templ->Bounds().Size() != mask->Bounds().Size()) {
        return 0;
    }
    long long difference = 0, count = 0;
    for (int y = 0; y < templ->Bounds().Dy(); ++y) {
        for (int x = 0; x < templ->Bounds().Dx(); ++x) {
            int i = y * templ->Stride + x;
            if (mask->Pix[y * mask->Stride + x] < 128) continue;
            int d = static_cast<int>(query->Pix[y * query->Stride + x]) - static_cast<int>(templ->Pix[i]);
            if (d < 0) d = -d;
            difference += d;
            count++;
        }
    }
    if (count == 0) return 0;
    return 1 - static_cast<double>(difference) / static_cast<double>(255 * count);
}

// gray is ToGray(view), converted once by the caller and shared by every NCC
// query instead of being rebuilt per entry/factor; nullptr converts it here.
//
// The caller must not hold mu. The lock covers only the lazy decode and the
// template cache while the jobs are built; NCC scoring runs unlocked (and in
// parallel) on immutable prepared templates and the caller's pixels. Scores
// are reduced in the original entry/factor order with the same strict '>', so
// the result equals the serial scan exactly.
AvatarMatch AvatarCatalog::matchEntries(const RGBA& view, const Gray* grayIn, double scale,
                                        const std::vector<avatarEntry*>& list) {
    AvatarMatch out;
    Gray owned;
    const Gray* gray = grayIn;
    if (gray == nullptr) {
        owned = match::ToGray(&view);
        gray = &owned;
    }
    // Keep the best score per identity before calculating the runner-up. A
    // single identity is evaluated at several display scales; treating its
    // second scale as a different candidate can reject an otherwise unambiguous
    // portrait (best=.90, same-ID runner-up=.85).
    std::unordered_map<std::string, double> bestByID;
    std::unordered_map<std::string, std::string> nameByID, baseByID;
    const Rect bounds = view.Bounds();
    auto scoreAll = [&](std::vector<avatarJob>& jobs) {
        ParallelFor(static_cast<int>(jobs.size()),
                    [&](int i) { jobs[i].score = avatarJobScore(view, gray, jobs[i].prepared.get()); });
        for (const auto& job : jobs) {
            double& b = bestByID[job.entry->id];  // Go map 读缺省 0
            if (job.score > b) b = job.score;
        }
    };
    std::vector<avatarJob> jobs;
    std::unique_lock<std::mutex> lk(mu);
    evictTemplates(scale);
    for (avatarEntry* entry : list) {
        if (!decode(entry)) continue;
        out.ids.push_back(entry->id);
        nameByID[entry->id] = entry->name;
        baseByID[entry->id] = entry->baseName;
    }
    if (list.size() > 6) {
        // Full scan, two-stage: the primary display factor (the live HUD
        // renders near 0.6x bead scale) locates the portrait cheaply over the
        // whole shortlist; the full multi-scale sweep below then re-runs only
        // the leading candidates plus the coarse locator's first entries. The
        // shortlist order itself is the recall fallback for render scales that
        // are not the common HUD factor (exact-scale test callers).
        jobs.reserve(list.size());
        for (avatarEntry* entry : list) appendJobs(jobs, entry, scale, bounds, avatarFactorPrimary);
        lk.unlock();
        scoreAll(jobs);
        std::vector<stageEntry> primary;
        primary.reserve(list.size());
        for (avatarEntry* entry : list) {
            if (entry != nullptr) {
                auto it = bestByID.find(entry->id);
                primary.push_back(stageEntry{entry, it == bestByID.end() ? 0 : it->second});
            }
        }
        std::stable_sort(primary.begin(), primary.end(), sortStage);
        std::unordered_set<std::string> fine;
        for (size_t i = 0; i < primary.size(); ++i) {
            if (i < 6) fine.insert(primary[i].entry->id);
        }
        for (size_t i = 0; i < list.size(); ++i) {
            if (list[i] != nullptr && i < 6) fine.insert(list[i]->id);
        }
        // The primary factor's score is already in bestByID, exactly as a
        // repeat would compute it (same template, same pixels), and an equal
        // score never wins the strict '>', so the fine sweep skips it.
        jobs.clear();
        lk.lock();
        evictTemplates(scale);
        for (avatarEntry* entry : list) {
            if (entry != nullptr && fine.count(entry->id)) appendJobs(jobs, entry, scale, bounds, avatarFactorsFine);
        }
        lk.unlock();
        scoreAll(jobs);
    } else {
        jobs.reserve(list.size() * avatarFactorsFull.size());
        for (avatarEntry* entry : list) appendJobs(jobs, entry, scale, bounds, avatarFactorsFull);
        lk.unlock();
        scoreAll(jobs);
    }
    // Go 遍历 map（随机顺序）；这里按 entries 顺序，结果只在分数恰好并列时才会不同（见文件头）。
    std::string runnerName;
    std::unordered_set<std::string> visited;
    for (avatarEntry* entry : list) {
        if (entry == nullptr || !visited.insert(entry->id).second) continue;
        auto it = bestByID.find(entry->id);
        if (it == bestByID.end()) continue;
        const std::string& id = it->first;
        double score = it->second;
        if (score > out.Score) {
            out.RunnerUp = out.Score;
            runnerName = out.Name;
            out.ID = id;
            out.Name = nameByID[id];
            out.BaseName = baseByID[id];
            out.Score = score;
        } else if (id != out.ID && score > out.RunnerUp) {
            out.RunnerUp = score;
            runnerName = nameByID[id];
        }
    }
    out.Candidate = out.Name;
    if (out.Score < .78 || out.Score - out.RunnerUp < .08) {
        // A lookalike skin pair of the same cosmetic family (both golden Naruto
        // forms under a glare) can compress the margin below the strict gate.
        // Accept a very strong winner only when NEITHER candidate carries a
        // special variant policy: geometry rules (slots/palette/row offset)
        // stay strictly margined, so special-bead safety is unchanged.
        if (!(out.Score >= .90 && out.Score - out.RunnerUp >= .02 && !avatarVariantPolicy(out.Name) &&
              !avatarVariantPolicy(runnerName))) {
            out.ID.clear();
            out.Name.clear();
            out.BaseName.clear();
        }
    }
    return out;
}

// appendJobs fetches or prepares the entry's template at each factor, in
// factor order (caller holds mu).
void AvatarCatalog::appendJobs(std::vector<avatarJob>& jobs, avatarEntry* entry, double scale, const Rect& bounds,
                               const std::vector<double>& factors) {
    for (double factor : factors) {
        avatarJob job;
        job.entry = entry;
        int w = 0, h = 0;
        avatarFactorSize(entry, scale, factor, w, h);
        if (w <= bounds.Dx() && h <= bounds.Dy()) job.prepared = prepared(entry, w, h);
        jobs.push_back(std::move(job));
    }
}

// avatarJobScore only reads the immutable template and the caller's pixels,
// so jobs may be scored concurrently.
double avatarJobScore(const RGBA& view, const Gray* gray, const match::PreparedNCC* prepared) {
    if (prepared == nullptr) return 0;
    match::Query q;
    q.Image = &view;
    q.Gray = gray;
    q.ROI = view.Bounds();
    q.Prepared = prepared;
    match::Score score;
    if (!match::NCC{}.Match(q, score)) return 0;
    return score.Value;
}

// decode fills the full-resolution gray/mask lazily for entries that were not
// built by loadAvatarCatalog (caller holds mu).
bool AvatarCatalog::decode(avatarEntry* entry) {
    if (entry == nullptr) return false;
    if (entry->gray.Nil()) {
        if (!entry->data) return false;
        Gray alpha;
        avatarGrayMask(*entry->data, entry->gray, alpha);
        entry->mask = avatarFaceMask(entry->gray.Bounds(), alpha);
    }
    return true;
}

// avatarVariantPolicy reports whether this catalog display name carries a
// special slot/palette/geometry policy. Such identities must never be decided
// by the relaxed same-role margin path.
bool avatarVariantPolicy(const std::string& name) {
    static const std::string obito = normalizeAvatarName(Obito);
    static const std::string xiayin = normalizeAvatarName(SasukeXiayin);
    static const std::string itachi = normalizeAvatarName(ItachiHyakusen);
    const std::string key = normalizeAvatarName(canonicalAvatarName(name));
    return key == obito || key == xiayin || key == itachi;
}

void avatarFactorSize(const avatarEntry* entry, double scale, double factor, int& w, int& h) {
    w = std::max(12, static_cast<int>(std::round(static_cast<double>(entry->gray.Bounds().Dx()) * scale * factor)));
    h = std::max(12, static_cast<int>(std::round(static_cast<double>(entry->gray.Bounds().Dy()) * scale * factor)));
}

// evictTemplates clears the per-entry scaled template cache whenever the HUD
// scale changes; cached templates are only valid for one capture geometry.
void AvatarCatalog::evictTemplates(double scale) {
    if (cacheScale != scale) {
        cacheScale = scale;
        for (auto& e : entries) {
            e->scaled.clear();
            e->scaledValid = false;
        }
        cachedEntries = 0;
    }
}

// prepared returns the cached scaled template for the entry at this size,
// preparing it on first use. All catalog template state lives under the
// catalog lock; the cache only ever holds templates at the current scale.
PreparedPtr AvatarCatalog::prepared(avatarEntry* entry, int w, int h) {
    avatarSize key{w, h};
    if (!entry->scaledValid) {
        entry->scaled.clear();
        entry->scaledValid = true;
    } else {
        auto it = entry->scaled.find(key);
        if (it != entry->scaled.end()) return it->second;
    }
    Gray g = match::ScaleGray(&entry->gray, w, h);
    Gray m = match::ScaleGray(&entry->mask, w, h);
    PreparedPtr p = match::PrepareNCC(&g, &m);
    entry->scaled[key] = p;
    cachedEntries++;
    if (cachedEntries > templateCacheCap()) {
        for (auto& e : entries) {
            e->scaled.clear();
            e->scaledValid = false;
        }
        cachedEntries = 0;
        cacheWipes++;
    }
    return p;
}

// templateCacheCap covers every size of one complete scan (each entry at
// every display factor) plus headroom, so a scan never wipes its own cache.
// Sizes depend only on the scale, which evictTemplates already keys.
int AvatarCatalog::templateCacheCap() const {
    return static_cast<int>(entries.size() * avatarFactorsFull.size()) + 64;
}

AvatarMatch AvatarTracker::Read(AvatarCatalog* c, const RGBA* img, Rect roi, double scale, TimeNs now) {
    if (c == nullptr || img == nullptr || scale <= 0 || std::isnan(scale) || std::isinf(scale)) return AvatarMatch{};
    roi = roi.Intersect(img->Bounds());
    if (roi.Empty()) return AvatarMatch{};
    // The candidate shortlist is only a geometry hint: any window, scale, or
    // catalog change (and backwards time) invalidates it. The very next frame
    // runs the bounded full scan again; identity itself is never inherited.
    bool changed = catalog_ != c || bounds_ != img->Bounds() || roi_ != roi || scale_ != scale ||
                   (lastAt_ != 0 && now < lastAt_);
    lastAt_ = now;
    if (changed) {
        ids_.clear();
        next_ = 0;
        memoOK_ = false;
    }
    catalog_ = c;
    bounds_ = img->Bounds();
    roi_ = roi;
    scale_ = scale;
    bool same = memoOK_ && samePackedPix(memoPix_, img, roi);
    if (now < next_ && !ids_.empty()) {
        if (same && !memoFull_ && memoIDs_ == ids_) return memoOut_;
        // byID is written only by loadAvatarCatalog, so it is read lock-free;
        // matchEntries takes c.mu for its template cache itself.
        std::vector<avatarEntry*> list;
        list.reserve(ids_.size());
        for (const auto& id : ids_) {
            auto it = c->byID.find(id);
            if (it != c->byID.end() && it->second != nullptr) list.push_back(it->second);
        }
        AvatarMatch out = c->matchEntries(img->SubImage(roi), nullptr, scale, list);
        remember(img, roi, false, out);
        return out;
    }
    AvatarMatch out;
    if (same && memoFull_) {
        out = memoOut_;
    } else {
        out = c->Match(img, roi, scale);
    }
    // The documented between-scan cost is the previous SIX candidates, never a
    // whole shortlist: an ambiguous full scan must not turn every intermediate
    // frame into a 24-entry NCC sweep.
    if (out.ids.size() > 6) out.ids.resize(6);
    ids_ = out.ids;
    if (!out.ID.empty()) ids_ = {out.ID};
    next_ = now + 500 * Millisecond;
    remember(img, roi, true, out);
    return out;
}

// remember stores the result for the ROI pixels; ids is the shortlist the
// non-full path matched against (ids_ at call time).
void AvatarTracker::remember(const RGBA* img, const Rect& roi, bool full, const AvatarMatch& out) {
    memoPix_.clear();
    const size_t row = static_cast<size_t>(4 * roi.Dx());
    memoPix_.reserve(row * static_cast<size_t>(roi.Dy()));
    for (int y = roi.Min.Y; y < roi.Max.Y; ++y) {
        const uint8_t* p = img->Pix + img->PixOffset(roi.Min.X, y);
        memoPix_.insert(memoPix_.end(), p, p + row);
    }
    memoFull_ = full;
    memoOut_ = out;
    memoOK_ = true;
    memoIDs_ = ids_;
}

bool samePackedPix(const std::vector<uint8_t>& packed, const RGBA* img, const Rect& roi) {
    const size_t row = static_cast<size_t>(4 * roi.Dx());
    if (packed.size() != row * static_cast<size_t>(roi.Dy())) return false;
    for (int y = roi.Min.Y; y < roi.Max.Y; ++y) {
        const uint8_t* p = img->Pix + img->PixOffset(roi.Min.X, y);
        size_t o = static_cast<size_t>(y - roi.Min.Y) * row;
        if (row > 0 && std::memcmp(packed.data() + o, p, row) != 0) return false;
    }
    return true;
}

// AvatarRegion maps one normalized 960-wide reference ROI from an existing
// first-bead anchor; it works for 720p..1440p and letterboxed content areas.
Rect AvatarRegion(Point first, double scale, bool left) {
    // The avatar asset is an 85x85 HUD diamond. Its bottom is below the bead
    // anchor by roughly 35 reference pixels; the previous -82..-5 window cut
    // off the lower half at the real 960-wide anchor (first.Y≈61), leaving a
    // badly warped top-only portrait. Keep the full diamond and allow the HUD
    // frame/health bar to be clipped by the image bounds at the call site.
    double x0 = -80.0, x1 = -10.0;
    if (!left) {
        x0 = 5;
        x1 = 75;
    }
    return MakeRect(first.X + static_cast<int>(std::round(x0 * scale)),
                    first.Y - static_cast<int>(std::round(62 * scale)),
                    first.X + static_cast<int>(std::round(x1 * scale)),
                    first.Y + static_cast<int>(std::round(38 * scale)));
}

// canonicalAvatarName maps only documented historical spellings onto a current
// canonical catalog title. It deliberately does not collapse distinct forms.
std::string canonicalAvatarName(const std::string& s) {
    static const std::string legacy = normalizeAvatarName(MadaraLegacyAlias);
    if (normalizeAvatarName(s) == legacy) return Madara;
    return s;
}

bool sameAvatarVariant(const std::string& a, const std::string& b) {
    return normalizeAvatarName(canonicalAvatarName(a)) == normalizeAvatarName(canonicalAvatarName(b));
}

std::string normalizeAvatarName(const std::string& s) {
    static const std::u32string drop = U"[]【】「」()（）·・";
    std::string out;
    out.reserve(s.size());
    for (size_t i = 0, w = 0; i < s.size(); i += w) {
        char32_t r = utf8::DecodeRune(s, i, w);
        if (utf8::IsSpace(r) || drop.find(r) != std::u32string::npos) continue;
        utf8::AppendRune(out, r);
    }
    return out;
}

}  // namespace nt::ninja
