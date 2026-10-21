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

// The calling thread's prefix sums of its avatar window (matchEntries shares
// them read-only with its ParallelFor helpers; AvatarTracker::Read trims them).
thread_local match::grayPrefix t_viewPrefix;

// viewPrefixFor builds the calling thread's prefix sums of a window's gray
// (t_viewPrefix): every whole-window job scans all of it, so they share one
// build instead of rebuilding it in each of up to ~130 NCC calls (band jobs
// reuse it too: it covers their search area). nullptr when the rows are too
// wide for uint32 sums. The two sides may run concurrently: one buffer per thread.
const match::grayPrefix* viewPrefixFor(const Gray& gray) {
    if (gray.Bounds().Dx() > match::maxDotRun) return nullptr;
    match::newGrayPrefix(t_viewPrefix, gray, 0, 0, gray.Bounds().Dx(), gray.Bounds().Dy());
    return &t_viewPrefix;
}

}  // namespace

// Player-drawn portrait box (docs/区域校准.md): the bounding box of the
// visible diamond portrait, frame included. Measured on 13 native portraits
// (1280x720..2880x1800, camp and duel): its side is 68.5 reference units
// (960-wide) and its centre sits at (42, 49.5) / (36, 49.5) inside the
// automatic left / right AvatarRegion window.
namespace {
constexpr double avatarBoxSide = 68.5;
constexpr double avatarBoxLeftX = 42.0;
constexpr double avatarBoxRightX = 36.0;
constexpr double avatarBoxCentreY = 49.5;
}  // namespace

// ---------------------------------------------------------------------------
// 内置图鉴

namespace {
// Never destroyed: exit() runs static destructors while a frame may still use them.
std::mutex& g_embeddedMu = *new std::mutex;
std::shared_ptr<AvatarCatalog>& g_embedded = *new std::shared_ptr<AvatarCatalog>;
bool g_embeddedTried = false;
uint64_t g_embeddedRevision = 0;
std::string& g_embeddedErr = *new std::string;
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
        if (e->data) out.Bytes += static_cast<int>(e->data->Bytes());
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
    // The asset's luma is exactly the Go decode() gray; it is shared, not copied.
    const Gray& gray = img->Luma;
    if (gray.Bounds().Dx() < 16 || gray.Bounds().Dy() < 16) {
        if (err) *err = "avatar " + item.ID + " too small";
        return nullptr;
    }
    // The catalog PNG carries a baked rank badge/frame/background. Those
    // pixels vary in the live HUD, while the face inside the diamond is
    // stable. Recognition uses alpha ∩ inner-diamond only.
    Gray mask = avatarFaceMask(*img);
    auto e = std::make_unique<avatarEntry>();
    e->id = item.ID;
    e->name = avatarName(item);
    e->baseName = avatarBaseName(item);
    e->data = img;
    // Go also builds a 16² thumb that nothing reads; it is not kept here.
    e->coarseGray = match::ScaleGray(&gray, 36, 36);
    e->coarseMask = match::ScaleGray(&mask, 36, 36);
    // The full-resolution face mask is not kept: the template cache rebuilds it
    // on a miss from the asset's alpha bits (avatarFaceMask is cheap).
    e->gray = gray;
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

// avatarFaceMask(src) is avatarFaceMask(bounds, alpha) for the asset's own
// alpha >= 128 bits (AssetImage::Opaque), without materializing the alpha mask.
Gray avatarFaceMask(const AssetImage& src) {
    const int w = src.Width, h = src.Height;
    Gray mask = Gray::New(MakeRect(0, 0, w, h));
    double cx = static_cast<double>(w - 1) / 2, cy = static_cast<double>(h - 1) / 2;
    double radius = static_cast<double>(std::min(w, h)) * 0.43;
    for (int y = 0; y < h; ++y) {
        for (int x = 0; x < w; ++x) {
            if (src.Opaque(x, y) &&
                std::fabs(static_cast<double>(x) - cx) + std::fabs(static_cast<double>(y) - cy) <= radius) {
                mask.Pix[y * mask.Stride + x] = 255;
            }
        }
    }
    return mask;
}

void avatarGrayMask(const AssetImage& src, Gray& gray, Gray& mask) {
    const int w = src.Width, h = src.Height;
    // AddImage already computed color.NRGBA.RGBA / color.RGBA.RGBA (16-bit
    // expansion, NRGBA alpha premultiply) and the same >>8 luma byte for byte.
    gray = src.Luma;
    mask = Gray::New(MakeRect(0, 0, w, h));
    for (int y = 0; y < h; ++y) {
        for (int x = 0; x < w; ++x) {
            if (src.Opaque(x, y)) mask.Pix[y * mask.Stride + x] = 255;
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
    const avatarView v{view, &gray, viewPrefixFor(gray)};
    avatarScores scores;
    AvatarMatch out = scoreEntries(v, scale, list, scores);
    if (!out.ID.empty()) return out;
    // Nothing passed: take the second look below. Only an identity it finds
    // replaces this result; otherwise every field, the shortlist the tracker
    // keeps included, stays exactly what the scan above produced.
    AvatarMatch rescued = rescue(v, scale, scores);
    if (!rescued.ID.empty()) {
        rescued.ids = out.ids;
        return rescued;
    }
    return out;
}

// The two catalog icon generations render at different sizes in the live HUD
// portrait. Best factor of the true entry on native portraits (parity corpus
// and two user videos, 1024x461..2880x1800): the 70px icons (161 of the 242
// entries) 0.64-0.83, about 0.72, on 12 portraits of 9 ninjas; the 85px icons
// 0.48-0.56 on 8 portraits of 5 ninjas. The full scan above locates with one
// 0.60 factor and a 36² thumbnail of the whole 68-unit diamond neighbourhood
// (centred for the right window), which suits neither: 神秘面具男 (90174, 70px)
// ranked 34-37 of 242 in that locator, which keeps 24, and scored 0.41 at
// 0.60 against 0.96 at 0.72; the left 宇智波佐助[侠隐江湖] (90464, 85px) ranked
// 23-24. So when the scan finds no identity, a second look ranks every entry
// by a thumbnail of the portrait itself (avatarPortraitSide units around the
// measured diamond centre of the left or of the right window, the better of
// the two) and scores the leading avatarRescueEntries at the display factors
// of their generation (avatarClassFactors70/85, nt/ninja_internal.h). The
// verdict is the same strict gate over everything scored for the frame, so an
// identity the scan finds is never changed.
const std::vector<double>& avatarClassFactors(const avatarEntry* entry) {
    if (entry->gray.Bounds().Dx() >= 80) return avatarClassFactors85;
    return avatarClassFactors70;
}

// avatarPortraitRect is the portrait square around a diamond centre at
// (cx, avatarBoxCentreY) window units, in the window's own pixels (origin 0).
Rect avatarPortraitRect(double cx, double scale) {
    const double half = avatarPortraitSide / 2;
    return MakeRect(static_cast<int>(std::round((cx - half) * scale)),
                    static_cast<int>(std::round((avatarBoxCentreY - half) * scale)),
                    static_cast<int>(std::round((cx + half) * scale)),
                    static_cast<int>(std::round((avatarBoxCentreY + half) * scale)));
}

// rescue is the second look of a full scan that found no identity (see
// avatarClassFactors above). v.gray is the window's gray (origin at the
// window's top left); scores holds everything the scan scored and receives the rest.
AvatarMatch AvatarCatalog::rescue(const avatarView& v, double scale, avatarScores& scores) {
    std::vector<Gray> queries;
    for (double cx : {avatarBoxLeftX, avatarBoxRightX}) {
        Rect r = avatarPortraitRect(cx, scale).Intersect(v.gray->Bounds());
        if (!r.Empty()) {
            Gray sub = v.gray->SubImage(r);
            queries.push_back(match::ScaleGray(&sub, 36, 36));
        }
    }
    if (queries.empty()) return AvatarMatch{};
    std::vector<stageEntry> ranked;
    ranked.reserve(entries.size());
    for (const auto& entry : entries) {
        double score = 0;
        for (const Gray& q : queries) {
            score = std::max(score, coarseAvatarScore(&q, &entry->coarseGray, &entry->coarseMask));
        }
        ranked.push_back(stageEntry{entry.get(), score});
    }
    std::stable_sort(ranked.begin(), ranked.end(), sortStage);
    if (ranked.size() > static_cast<size_t>(avatarRescueEntries)) ranked.resize(avatarRescueEntries);
    std::vector<avatarJob> jobs;
    jobs.reserve(ranked.size() * avatarClassFactors70.size());
    {
        std::lock_guard<std::mutex> lk(mu);
        for (const auto& f : ranked) {
            scores.order.push_back(f.entry);
            if (!decode(f.entry)) continue;
            scores.note(f.entry);
            appendPortraitJobs(jobs, f.entry, scale, v.view.Bounds());
        }
    }
    addScores(v, jobs, scores);
    return scores.decide(AvatarMatch{});
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

// note registers a decoded entry's names (Go nameByID/baseByID).
void avatarScores::note(avatarEntry* entry) {
    name[entry->id] = entry->name;
    base[entry->id] = entry->baseName;
}

// decide fills out's winner, runner-up and candidate and applies the strict
// identity gate. A single identity is evaluated at several display scales;
// keeping only its best before calculating the runner-up stops its second
// scale from counting as a different candidate, which could reject an
// otherwise unambiguous portrait (best=.90, same-ID runner-up=.85).
AvatarMatch avatarScores::decide(AvatarMatch out) const {
    // Go 遍历 map（随机顺序）；这里按 order 顺序，结果只在分数恰好并列时才会不同（见文件头）。
    std::string runnerName;
    std::unordered_set<std::string> visited;
    for (avatarEntry* entry : order) {
        if (entry == nullptr || !visited.insert(entry->id).second) continue;
        auto it = best.find(entry->id);
        if (it == best.end()) continue;
        const std::string& id = it->first;
        double score = it->second;
        // Go map 读缺省零值：没有登记名字的条目（未解码）名字为空。
        const auto text = [](const std::unordered_map<std::string, std::string>& m, const std::string& key) {
            auto t = m.find(key);
            return t == m.end() ? std::string() : t->second;
        };
        if (score > out.Score) {
            out.RunnerUp = out.Score;
            runnerName = out.Name;
            out.ID = id;
            out.Name = text(name, id);
            out.BaseName = text(base, id);
            out.Score = score;
        } else if (id != out.ID && score > out.RunnerUp) {
            out.RunnerUp = score;
            runnerName = text(name, id);
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

// addScores scores jobs unlocked and in parallel on immutable prepared
// templates and the caller's pixels, then reduces them in job order with the
// strict '>', so the result equals the serial scan exactly.
void AvatarCatalog::addScores(const avatarView& v, std::vector<avatarJob>& jobs, avatarScores& scores) {
    ParallelFor(static_cast<int>(jobs.size()), [&](int i) {
        jobs[i].score = avatarJobScore(v.view, v.gray, jobs[i].prepared.get(), v.prefix, jobs[i].roi);
    });
    for (const auto& job : jobs) {
        double& b = scores.best[job.entry->id];  // Go map 读缺省 0
        if (job.score > b) b = job.score;
    }
}

// gray is ToGray(view), converted once by the caller and shared by every NCC
// query instead of being rebuilt per entry/factor; nullptr converts it here.
//
// The caller must not hold mu. The lock covers only the lazy decode and the
// template cache while the jobs are built; NCC scoring runs unlocked (addScores).
AvatarMatch AvatarCatalog::matchEntries(const RGBA& view, const Gray* grayIn, double scale,
                                        const std::vector<avatarEntry*>& list) {
    Gray owned;
    const Gray* gray = grayIn;
    if (gray == nullptr) {
        owned = match::ToGray(&view);
        gray = &owned;
    }
    const avatarView v{view, gray, viewPrefixFor(*gray)};
    avatarScores scores;
    return scoreEntries(v, scale, list, scores);
}

// scoreEntries is matchEntries that also returns the scores behind its result.
AvatarMatch AvatarCatalog::scoreEntries(const avatarView& v, double scale, const std::vector<avatarEntry*>& list,
                                        avatarScores& scores) {
    AvatarMatch out;
    const Rect bounds = v.view.Bounds();
    std::vector<avatarJob> jobs;
    std::unique_lock<std::mutex> lk(mu);
    beginScan();
    for (avatarEntry* entry : list) {
        if (entry != nullptr) scores.order.push_back(entry);
        if (!decode(entry)) continue;
        out.ids.push_back(entry->id);
        scores.note(entry);
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
        addScores(v, jobs, scores);
        std::vector<stageEntry> primary;
        primary.reserve(list.size());
        for (avatarEntry* entry : list) {
            if (entry != nullptr) {
                auto it = scores.best.find(entry->id);
                primary.push_back(stageEntry{entry, it == scores.best.end() ? 0 : it->second});
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
        // The primary factor's score is already in scores, exactly as a
        // repeat would compute it (same template, same pixels), and an equal
        // score never wins the strict '>', so the fine sweep skips it.
        jobs.clear();
        lk.lock();
        for (avatarEntry* entry : list) {
            if (entry != nullptr && fine.count(entry->id)) appendJobs(jobs, entry, scale, bounds, avatarFactorsFine);
        }
        lk.unlock();
        addScores(v, jobs, scores);
    } else {
        // The tracker's shortlist between full scans (or a tiny catalog). A
        // lone entry is the identity AvatarTracker re-verifies between full
        // scans: it is also scored at its generation's portrait factors, where
        // the full scan's second look may have found it, so such a portrait is
        // not lost on the frames in between. The six candidates of an
        // unidentified side keep the plain sweep and its cost.
        jobs.reserve(list.size() * avatarFactorsFull.size() + avatarClassFactors70.size());
        for (avatarEntry* entry : list) {
            appendJobs(jobs, entry, scale, bounds, avatarFactorsFull);
            if (list.size() == 1) appendPortraitJobs(jobs, entry, scale, bounds);
        }
        lk.unlock();
        addScores(v, jobs, scores);
    }
    return scores.decide(std::move(out));
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

// appendPortraitJobs fetches or prepares the entry's template at each display
// factor of its icon generation (avatarClassFactors), searched only where a
// HUD portrait of that size sits: centres within avatarPortraitSlack units of
// the measured left or right diamond centre (caller holds mu). Besides the
// clutter of the rest of the window, this keeps the sparse first grid of a
// large template's NCC scan from missing a sharp portrait peak: over the
// whole window 春野樱[百豪] (90230) drawn at 0.65 scored 0.83 at 0.63 but 0.34
// at 0.64, where the grid locked onto the title bar.
void AvatarCatalog::appendPortraitJobs(std::vector<avatarJob>& jobs, avatarEntry* entry, double scale,
                                       const Rect& bounds) {
    for (double factor : avatarClassFactors(entry)) {
        avatarJob job;
        job.entry = entry;
        int w = 0, h = 0;
        avatarFactorSize(entry, scale, factor, w, h);
        Rect roi = avatarPortraitBand(bounds, scale, w, h);
        if (w <= roi.Dx() && h <= roi.Dy()) {
            job.prepared = prepared(entry, w, h);
            job.roi = roi;
        }
        jobs.push_back(std::move(job));
    }
}

// avatarPortraitBand is the search area of a w x h portrait template in the
// window bounds (see appendPortraitJobs).
Rect avatarPortraitBand(const Rect& bounds, double scale, int w, int h) {
    return MakeRect(bounds.Min.X + static_cast<int>(std::round((avatarBoxRightX - avatarPortraitSlack) * scale)) - w / 2,
                    bounds.Min.Y + static_cast<int>(std::round((avatarBoxCentreY - avatarPortraitSlack) * scale)) - h / 2,
                    bounds.Min.X + static_cast<int>(std::round((avatarBoxLeftX + avatarPortraitSlack) * scale)) + w - w / 2,
                    bounds.Min.Y + static_cast<int>(std::round((avatarBoxCentreY + avatarPortraitSlack) * scale)) + h - h / 2)
        .Intersect(bounds);
}

// avatarJobScore only reads the immutable template and the caller's pixels,
// so jobs may be scored concurrently.
double avatarJobScore(const RGBA& view, const Gray* gray, const match::PreparedNCC* prepared,
                      const match::grayPrefix* prefix, const Rect& roi) {
    if (prepared == nullptr) return 0;
    match::Query q;
    q.Image = &view;
    q.Gray = gray;
    q.ROI = roi.Empty() ? view.Bounds() : roi;
    q.Prepared = prepared;
    q.Prefix = prefix;
    match::Score score;
    if (!match::NCC{}.Match(q, score)) return 0;
    return score.Value;
}

// decode fills the full-resolution gray/mask lazily for entries that were not
// built by loadAvatarCatalog (caller holds mu).
bool AvatarCatalog::decode(avatarEntry* entry) {
    if (entry == nullptr || !entry->data) return false;
    if (entry->gray.Nil()) entry->gray = entry->data->Luma;
    return true;
}

// avatarVariantPolicy reports whether this catalog display name carries a
// special slot/palette/geometry policy. Such identities must never be decided
// by the relaxed same-role margin path.
bool avatarVariantPolicy(const std::string& name) {
    static const std::string& obito = *new std::string(normalizeAvatarName(Obito));
    static const std::string& xiayin = *new std::string(normalizeAvatarName(SasukeXiayin));
    static const std::string& itachi = *new std::string(normalizeAvatarName(ItachiHyakusen));
    static const std::string& minato = *new std::string(normalizeAvatarName(MinatoKyubi));
    const std::string key = normalizeAvatarName(canonicalAvatarName(name));
    return key == obito || key == xiayin || key == itachi || key == minato;
}

void avatarFactorSize(const avatarEntry* entry, double scale, double factor, int& w, int& h) {
    w = std::max(12, static_cast<int>(std::round(static_cast<double>(entry->gray.Bounds().Dx()) * scale * factor)));
    h = std::max(12, static_cast<int>(std::round(static_cast<double>(entry->gray.Bounds().Dy()) * scale * factor)));
}

void AvatarCatalog::beginScan() {
    templateEpoch++;
    if (templateEpoch % 64 != 0 || templateEpoch <= templateIdleCalls) return;
    for (auto it = templates.begin(); it != templates.end();) {
        if (it->second.epoch + templateIdleCalls < templateEpoch) {
            templateBytes -= it->second.bytes;
            it = templates.erase(it);
            templateEvictions++;
        } else {
            ++it;
        }
    }
}

// prepared returns the cached scaled template for the entry at this size,
// preparing it on first use. All catalog template state lives under the
// catalog lock. The template depends only on (entry, w, h), so the cache is
// keyed by size, not by one global scale: a calibrated left/right pair with
// different portrait scales no longer wipes and rebuilds every call. Memory is
// bounded by templateCacheCap bytes, evicting least recently used templates;
// a job keeps its own reference, so eviction never affects a running scan.
PreparedPtr AvatarCatalog::prepared(avatarEntry* entry, int w, int h) {
    const avatarTemplateKey key{entry, w, h};
    const uint64_t now = ++templateClock;
    auto it = templates.find(key);
    if (it != templates.end()) {
        it->second.used = now;
        it->second.epoch = templateEpoch;
        return it->second.prepared;
    }
    Gray g = match::ScaleGray(&entry->gray, w, h);
    const Gray face = avatarFaceMask(*entry->data);
    Gray m = match::ScaleGray(&face, w, h);
    PreparedPtr p = match::PrepareNCC(&g, &m);
    cachedTemplate c;
    c.prepared = p;
    c.bytes = p != nullptr ? p->bytes() + 64 : 64;
    c.used = now;
    c.epoch = templateEpoch;
    templateBytes += c.bytes;
    templates.emplace(key, std::move(c));
    if (templateBytes > templateCacheCap) {
        // Evict down to 3/4 of the cap so a full working set does not evict on
        // every miss; the newest templates (this scan) are kept.
        std::vector<std::pair<uint64_t, avatarTemplateKey>> order;
        order.reserve(templates.size());
        for (const auto& kv : templates) order.emplace_back(kv.second.used, kv.first);
        std::sort(order.begin(), order.end(),
                  [](const auto& a, const auto& b) { return a.first < b.first; });
        for (const auto& o : order) {
            if (templateBytes <= templateCacheCap / 4 * 3 || o.first == now) break;
            auto victim = templates.find(o.second);
            templateBytes -= victim->second.bytes;
            templates.erase(victim);
            templateEvictions++;
        }
    }
    return p;
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
    // A memo hit scans nothing: it still counts towards cutting back this
    // thread's view prefix after a larger window (NC-02, nt/match.h).
    if (same) match::trimGrayPrefix(t_viewPrefix, roi.Dx(), roi.Dy());
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
    const size_t need = row * static_cast<size_t>(roi.Dy());
    // One side's window keeps its size: a memo far larger than it (an earlier
    // resolution or calibration) is released (match::kKeepPrefixBytes rule, NC-02).
    if (memoPix_.capacity() > match::kKeepPrefixBytes && memoPix_.capacity() / 4 > need) {
        std::vector<uint8_t>().swap(memoPix_);
    }
    memoPix_.reserve(need);
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

Rect AvatarRegionFromBox(double x, double y, double w, double h, bool left, double& outScale) {
    const double scale = (w + h) / 2 / avatarBoxSide;
    const double cx = left ? avatarBoxLeftX : avatarBoxRightX;
    const double x0 = x + w / 2 - cx * scale, y0 = y + h / 2 - avatarBoxCentreY * scale;
    outScale = scale;
    return MakeRect(static_cast<int>(std::round(x0)), static_cast<int>(std::round(y0)),
                    static_cast<int>(std::round(x0 + 70 * scale)), static_cast<int>(std::round(y0 + 100 * scale)));
}

// AvatarBoxFromRegion inverts AvatarRegionFromBox. The window origin is
// centred in its rounding range; a window 1px off the 70x100 shape (rounding
// ties) keeps its top-left corner.
void AvatarBoxFromRegion(const Rect& window, double scale, bool left, double& x, double& y, double& w, double& h) {
    const auto origin = [](int lo, int hi, double size) {
        const double d = static_cast<double>(hi - lo) - size;
        if (d > -1 && d < 1) return (static_cast<double>(lo + hi) - size) / 2;
        return static_cast<double>(lo);
    };
    const double cx = left ? avatarBoxLeftX : avatarBoxRightX;
    const double side = avatarBoxSide * scale;
    const double x0 = origin(window.Min.X, window.Max.X, 70 * scale);
    const double y0 = origin(window.Min.Y, window.Max.Y, 100 * scale);
    x = x0 + cx * scale - side / 2;
    y = y0 + avatarBoxCentreY * scale - side / 2;
    w = side;
    h = side;
}

// canonicalAvatarName maps only documented historical spellings onto a current
// canonical catalog title. It deliberately does not collapse distinct forms.
std::string canonicalAvatarName(const std::string& s) {
    static const std::string& legacy = *new std::string(normalizeAvatarName(MadaraLegacyAlias));
    if (normalizeAvatarName(s) == legacy) return Madara;
    return s;
}

bool sameAvatarVariant(const std::string& a, const std::string& b) {
    return normalizeAvatarName(canonicalAvatarName(a)) == normalizeAvatarName(canonicalAvatarName(b));
}

std::string normalizeAvatarName(const std::string& s) {
    static const std::u32string& drop = *new std::u32string(U"[]【】「」()（）·・");
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
