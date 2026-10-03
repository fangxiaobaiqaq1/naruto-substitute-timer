// Owner: cpp-identity
// Port of: internal/identity/identity.go
// Contract: include/nt/identity.h — see android/ARCHITECTURE.md.
//
// 与 Go 的差异（只限平台）：
//  * 账号名图鉴来自 AssetStore（Kotlin 把 filesDir/identity/** 注册进来），不读文件系统。
//  * saveOppCrop 不写 PNG：裁剪放进单槽，由 JNI nativeTakeOppCrop → Kotlin 编码保存并重新注册。
//    Go 里是 `go saveOppCrop(...)`，这里同步执行（只是一次裁剪拷贝）。
//  * Go 的 Guess（兼容旧测试的包装）没有移植：测试暂不写，也没有调用方。
#include "nt/identity.h"

#include <cmath>
#include <mutex>
#include <set>
#include <utility>

#include "nt/match.h"
#include "nt/utf8.h"

namespace nt::identity {

namespace {

std::mutex mineMu;
std::shared_ptr<const std::set<std::string>> mineSet = std::make_shared<const std::set<std::string>>();
uint64_t mineRevision = 0;

// The set is immutable after publication. Readers can keep a snapshot while
// settings replace it; explicit Read/Guess books do not depend on global state.
std::pair<std::shared_ptr<const std::set<std::string>>, uint64_t> mineSnapshot() {
    std::lock_guard<std::mutex> lock(mineMu);
    return {mineSet, mineRevision};
}

bool hasImageSuffix(const std::string& name) {
    // Go: strings.ToLower 后比较后缀。后缀全是 ASCII，按 ASCII 小写比较等价。
    auto endsWithFold = [&](const std::string& suf) {
        if (name.size() < suf.size()) return false;
        size_t off = name.size() - suf.size();
        for (size_t i = 0; i < suf.size(); ++i) {
            char c = name[off + i];
            if (c >= 'A' && c <= 'Z') c = static_cast<char>(c - 'A' + 'a');
            if (c != suf[i]) return false;
        }
        return true;
    };
    return endsWithFold(".png") || endsWithFold(".jpg") || endsWithFold(".jpeg");
}

// filepath.Base（Android 只有 '/' 分隔符）。
std::string baseName(std::string path) {
    if (path.empty()) return ".";
    while (path.size() > 1 && path.back() == '/') path.pop_back();
    if (path == "/") return "/";
    size_t i = path.rfind('/');
    if (i != std::string::npos) path = path.substr(i + 1);
    return path.empty() ? "/" : path;
}

// filepath.Ext：最后一个 '.' 起的后缀（不跨越分隔符）。
std::string extName(const std::string& path) {
    for (size_t i = path.size(); i > 0; --i) {
        char c = path[i - 1];
        if (c == '/') break;
        if (c == '.') return path.substr(i - 1);
    }
    return "";
}

std::vector<Named> mineOf(const std::vector<Named>& book) {
    std::vector<Named> out;
    for (const auto& n : book) {
        if (n.Mine) out.push_back(n);
    }
    return out;
}

std::vector<Named> othersOf(const std::vector<Named>& book) {
    std::vector<Named> out;
    for (const auto& n : book) {
        if (!n.Mine && !n.Name.empty()) out.push_back(n);
    }
    return out;
}

std::vector<Named> bookForScene(const std::vector<Named>& book, const std::string& scene) {
    std::vector<Named> out;
    for (const auto& n : book) {
        if (n.Scene == scene || (scene == "vs" && n.Scene.empty())) out.push_back(n);
    }
    return out;
}

struct hit {
    std::string Name;
    double Score = 0;
};

Rect mapRect(const detect::ContentArea& ca, const config::NormalizedRect& r) {
    Point p0 = ca.Map(r.X * detect::LogicWidth, r.Y * detect::LogicHeight);
    Point p1 = ca.Map((r.X + r.Width) * detect::LogicWidth, (r.Y + r.Height) * detect::LogicHeight);
    int x0 = p0.X, y0 = p0.Y, x1 = p1.X, y1 = p1.Y;
    if (x1 <= x0) x1 = x0 + 1;
    if (y1 <= y0) y1 = y0 + 1;
    return MakeRect(x0, y0, x1, y1);
}

// match.ToGray(n.Image)：优先用 LoadBook 预算好的缓存（同一函数的结果，逐位相同）。
Gray templateGray(const Named& n) {
    if (n.GrayCache) return *n.GrayCache;
    return match::ToGrayAsset(*n.Image);
}

const Gray* maskPtr(const Gray& m) { return m.Nil() ? nullptr : &m; }

hit bestHit(const RGBA* img, const detect::ContentArea& ca, const config::NormalizedRect& roi,
            const std::vector<Named>& names, int maxWidth) {
    hit best;
    if (names.empty()) return best;
    RGBA search = match::CropRGBA(img, mapRect(ca, roi));
    if (search.Nil() || search.Bounds().Empty()) return best;
    Gray gray = match::ToGray(&search);
    double scale = 1.0;
    if (maxWidth > 0 && ca.W > maxWidth) {
        scale = static_cast<double>(maxWidth) / static_cast<double>(ca.W);
        int w = static_cast<int>(std::round(static_cast<double>(gray.Bounds().Dx()) * scale));
        int h = static_cast<int>(std::round(static_cast<double>(gray.Bounds().Dy()) * scale));
        gray = match::ScaleGray(&gray, w, h);
        search = RGBA::New(gray.Bounds());  // Query.Gray supplies pixels, Image supplies bounds.
    }
    const Rect sb = search.Bounds();
    match::NCC ncc;
    for (const auto& n : names) {
        if (!n.Image) continue;
        int tw = static_cast<int>(std::round(static_cast<double>(n.Image->Width) * static_cast<double>(ca.W) /
                                             static_cast<double>(detect::LogicWidth) * scale));
        int th = static_cast<int>(std::round(static_cast<double>(n.Image->Height) * static_cast<double>(ca.H) /
                                             static_cast<double>(detect::LogicHeight) * scale));
        if (tw < 8 || th < 8 || tw > gray.Bounds().Dx() || th > gray.Bounds().Dy()) continue;
        Gray src = templateGray(n);
        Gray templ = match::ScaleGray(&src, tw, th);
        Gray mask;
        if (maxWidth > 0) mask = accountGlyphMask(templ);

        match::Query q;
        q.Image = &search;
        q.Gray = &gray;
        q.ROI = sb;
        q.Template = &templ;
        q.Mask = maskPtr(mask);
        match::Score score;
        bool ok = ncc.Match(q, score);
        // Preserve the original full-patch path where downsampling leaves too
        // little glyph/outline detail for the mask. Neither threshold is lowered.
        if (!mask.Nil() && (!ok || score.Value < fightMinScore)) {
            match::Query fq;
            fq.Image = &search;
            fq.Gray = &gray;
            fq.ROI = sb;
            fq.Template = &templ;
            match::Score full;
            bool fullOk = ncc.Match(fq, full);
            if (fullOk && (!ok || full.Value > score.Value)) {
                score = full;
                ok = true;
            }
        }
        // Low-resolution font/ROI rounding can put the glyph patch one pixel
        // away from its nominal width/height. Refine only an already plausible
        // location, not nine new full-strip scans and not a lower threshold.
        if (!mask.Nil() && ok && score.Value >= .65 && score.Value < fightMinScore) {
            const Point peak = score.Peak;
            for (int dw = -1; dw <= 1; ++dw) {
                for (int dh = -1; dh <= 1; ++dh) {
                    if (dw == 0 && dh == 0) continue;
                    Gray adjusted = match::ScaleGray(&src, tw + dw, th + dh);
                    Rect r = Rect(peak, peak.Add(adjusted.Bounds().Size())).Inset(-2).Intersect(sb);
                    Gray adjustedMask = accountGlyphMask(adjusted);
                    match::Query rq;
                    rq.Image = &search;
                    rq.Gray = &gray;
                    rq.ROI = r;
                    rq.Template = &adjusted;
                    rq.Mask = maskPtr(adjustedMask);
                    match::Score refined;
                    if (ncc.Match(rq, refined) && refined.Value > score.Value) {
                        score = refined;
                    }
                }
            }
        }
        if (ok && score.Value > best.Score) {
            best = hit{n.Name, score.Value};
        }
    }
    return best;
}

Readout readNames(const RGBA* img, const std::vector<Named>& book, detect::ContentMode mode, const sideROIs& rois,
                  double threshold, int maxWidth) {
    Readout out;
    if (img == nullptr || book.empty()) return out;
    auto [ca, ok] = detect::ResolveContentArea(img, mode, detect::LogicWidth, detect::LogicHeight, 0.015);
    if (!ok) return out;
    std::vector<Named> mine = mineOf(book);
    // A configured mine name is the strongest evidence. When that strip is
    // obscured, a confident, non-mine name on exactly one side is still useful:
    // it identifies the opponent, so our side is the other side. This prevents
    // a masked player name from making the timer choose a random side.
    if (mine.empty()) return out;
    std::vector<Named> others = othersOf(book);
    hit leftMine = bestHit(img, ca, rois.Left, mine, maxWidth);
    hit rightMine = bestHit(img, ca, rois.Right, mine, maxWidth);
    hit leftOpp = bestHit(img, ca, rois.Left, others, maxWidth);
    hit rightOpp = bestHit(img, ca, rois.Right, others, maxWidth);
    if (leftMine.Score >= threshold && leftMine.Score >= rightMine.Score + minGap) {
        out.Side = "left";
        out.Mine = leftMine.Name;
    } else if (rightMine.Score >= threshold && rightMine.Score >= leftMine.Score + minGap) {
        out.Side = "right";
        out.Mine = rightMine.Name;
    } else if (leftOpp.Score >= threshold && leftOpp.Score >= rightOpp.Score + minGap) {
        // The left name is definitely not in the configured mine list.
        out.Side = "right";
        out.Opp = leftOpp.Name;
    } else if (rightOpp.Score >= threshold && rightOpp.Score >= leftOpp.Score + minGap) {
        out.Side = "left";
        out.Opp = rightOpp.Name;
    }
    if (out.Side.empty()) return out;
    if (out.Opp.empty()) {
        const hit& oppHit = out.Side == "right" ? leftOpp : rightOpp;
        if (oppHit.Score >= threshold) out.Opp = oppHit.Name;
    }
    return out;
}

std::mutex saveMu;
TimeNs lastSave = 0;
bool havePending = false;
RGBA pendingCrop;

}  // namespace

// SetMineNames 设置里改我方名字后立刻用于认边，不用重启。
void SetMineNames(const std::vector<std::string>& names) {
    auto next = std::make_shared<std::set<std::string>>();
    for (const auto& raw : names) {
        std::string n = NormalizeName(raw);
        if (!n.empty()) next->insert(n);
    }
    std::lock_guard<std::mutex> lock(mineMu);
    mineSet = std::move(next);
    ++mineRevision;
}

// MineNames returns an owned snapshot for optional text recognizers. Changes in
// the settings window never leave a background job bound to a stale account.
std::vector<std::string> MineNames() {
    auto snap = mineSnapshot().first;
    return std::vector<std::string>(snap->begin(), snap->end());
}

const sideROIs DefaultROIs = {
    config::NormalizedRect{0.06, 0.88, 0.22, 0.08},
    config::NormalizedRect{0.72, 0.88, 0.22, 0.08},
};

const sideROIs FightROIs = {
    config::NormalizedRect{0.10, 0.02, 0.34, 0.055},
    config::NormalizedRect{0.56, 0.02, 0.34, 0.055},
};

// LoadBook 读账号名图鉴。
// 我方：config.ui.playerNames 里填写的名字，对应 identity/<名字>.png。
// 对面：同目录里其余 PNG，以及 seen/ 里曾经裁过的图。
std::vector<Named> LoadBook(const config::Config& cfg) {
    std::set<std::string> mine;
    for (const auto& raw : cfg.UI.PlayerNames) {
        std::string n = NormalizeName(raw);
        if (!n.empty()) mine.insert(n);
    }
    std::vector<Named> out;
    std::set<std::string> seen;
    AssetStore& store = AssetStore::Instance();
    auto addFile = [&](const std::string& path, std::string label, const std::string& scene) {
        if (seen.count(path)) return;
        std::shared_ptr<const AssetImage> img = store.Image(path);
        if (!img) return;
        seen.insert(path);
        if (label.empty()) label = labelFromFile(path);
        if (label.empty() || label == "seen") return;
        Named n;
        n.Name = label;
        n.Image = img;
        n.Mine = mine.count(label) > 0;
        n.Scene = scene;
        n.GrayCache = std::make_shared<const Gray>(match::ToGrayAsset(*img));
        out.push_back(std::move(n));
    };

    // os.ReadDir 的等价物：只取 dir 的直接子文件（余部不含 '/'），按文件名字典序。
    auto scanDir = [&](const std::string& dir, const std::string& scene) {
        for (const std::string& key : store.List(dir)) {
            std::string name = key.substr(dir.size());
            if (name.empty() || name.find('/') != std::string::npos) continue;  // 子目录
            if (!hasImageSuffix(name)) continue;
            addFile(key, labelFromFile(name), scene);
        }
    };
    scanDir(AssetDir, "vs");
    scanDir(SeenDir, "vs");
    scanDir(FightDir, "fight");
    return out;
}

std::string labelFromFile(const std::string& name) {
    std::string base = baseName(name);
    std::string ext = extName(base);
    if (!ext.empty() && base.size() >= ext.size() && base.compare(base.size() - ext.size(), ext.size(), ext) == 0) {
        base.resize(base.size() - ext.size());
    }
    size_t i = base.find('_');
    if (i != std::string::npos && i > 0) base.resize(i);
    return NormalizeName(base);
}

// Guesser recognizes accounts only in confirmed VS or battle scenes. Account
// changes reload the book, including templates added after startup.
GuessFunc Guesser(const config::Config& cfg0) {
    SetMineNames(cfg0.UI.PlayerNames);
    struct state {
        config::Config cfg;
        std::vector<Named> book;
        uint64_t revision = 0;
        TimeNs lastRead = 0;
        std::string lastScene;
        Rect lastBounds;
        detect::ContentMode mode = detect::ModeAuto;
    };
    auto st = std::make_shared<state>();
    st->cfg = cfg0;
    if (!detect::ParseMode(st->cfg.Layout.ContentMode, st->mode, nullptr)) {
        st->mode = detect::ModeAuto;
    }
    return [st](const RGBA* img, const std::string& scene) -> Readout {
        if (img == nullptr || (scene != "fight" && scene != "vs")) {
            st->lastScene.clear();
            return Readout{};
        }
        auto [names, current] = mineSnapshot();
        if (st->revision != current) {
            st->cfg.UI.PlayerNames.assign(names->begin(), names->end());
            st->book = LoadBook(st->cfg);
            st->revision = current;
            st->lastRead = 0;
        }
        if (names->empty() || st->book.empty()) return Readout{};
        TimeNs now = NowNs();
        // Go: now.Sub(time.Time{}) 是极大值，零时刻永远不算"刚读过"。
        if (scene == st->lastScene && img->Bounds() == st->lastBounds && st->lastRead != 0 &&
            now - st->lastRead < recognitionInterval) {
            // No new evidence. Do not manufacture repeated confirmations from a cache.
            return Readout{};
        }
        st->lastRead = now;
        st->lastScene = scene;
        st->lastBounds = img->Bounds();
        if (scene == "fight") return ReadFight(img, st->book, st->mode);
        Readout r = Read(img, st->book, st->mode);
        if (!r.Side.empty() && r.Opp.empty()) {
            saveOppCrop(img, opposite(r.Side), st->mode);
        }
        return r;
    };
}

// Read 比较左右底栏和名册。
Readout Read(const RGBA* img, const std::vector<Named>& book, detect::ContentMode mode) {
    return readNames(img, bookForScene(book, "vs"), mode, DefaultROIs, minScore, 0);
}

// ReadFight recognizes account-name-only templates from the battle HUD.
Readout ReadFight(const RGBA* img, const std::vector<Named>& book, detect::ContentMode mode) {
    // Bound matching cost at high capture resolutions. Only the two name strips
    // are converted/scaled, never a whole 1080p/4K frame for each template.
    return readNames(img, bookForScene(book, "fight"), mode, FightROIs, fightMinScore, 960);
}

std::string opposite(const std::string& side) {
    if (side == "left") return "right";
    return "left";
}

void saveOppCrop(const RGBA* img, const std::string& side, detect::ContentMode mode) {
    std::lock_guard<std::mutex> lock(saveMu);
    // Go: time.Since(lastSave) < 8s；零时刻 lastSave 永远放行（单调时钟开机不足 8 秒时也一样）。
    if (lastSave != 0 && NowNs() - lastSave < 8 * Second) return;
    if (img == nullptr) return;
    auto [ca, ok] = detect::ResolveContentArea(img, mode, detect::LogicWidth, detect::LogicHeight, 0.015);
    if (!ok) return;
    config::NormalizedRect roi = DefaultROIs.Left;
    if (side == "right") roi = DefaultROIs.Right;
    RGBA crop = match::CropRGBA(img, mapRect(ca, roi));
    if (crop.Nil() || crop.Bounds().Dx() < 8) return;
    // Go 的采集帧一律不透明（leidian ForceOpaque / mumu |= 0xff000000），png.Encode 因此写成 RGB，
    // 回读走不透明亮度路径。RGBX_8888 / 截图帧的 alpha 字节未定义，这里补成 255 以保持同一不变量
    // （crop 是独立拷贝，不会写到采集帧；RGBA_8888 帧本来就是 255，结果不变）。
    {
        const Rect cb = crop.Bounds();
        for (int y = cb.Min.Y; y < cb.Max.Y; ++y) {
            uint8_t* row = crop.Pix + crop.PixOffset(cb.Min.X, y);
            for (int x = 0; x < cb.Dx(); ++x) row[4 * x + 3] = 255;
        }
    }
    // Go 在这里编码 PNG 写 SeenDir/opp-<时间>.png；Android 交给 Kotlin（nativeTakeOppCrop），只保留最新一张。
    pendingCrop = std::move(crop);
    havePending = true;
    lastSave = NowNs();
}

bool TakePendingOppCrop(RGBA& out) {
    std::lock_guard<std::mutex> lock(saveMu);
    if (!havePending) return false;
    out = std::move(pendingCrop);
    pendingCrop = RGBA{};
    havePending = false;
    return true;
}

// NormalizeName 去掉空白和路径字符，方便当文件名、当配置项。
std::string NormalizeName(const std::string& s) {
    std::string trimmed = utf8::TrimSpace(s);
    std::string b;
    b.reserve(trimmed.size());
    for (size_t i = 0, w = 0; i < trimmed.size(); i += w) {
        char32_t r = utf8::DecodeRune(trimmed, i, w);
        if (utf8::IsSpace(r) || r == '/' || r == '\\' || r == ':') continue;
        utf8::AppendRune(b, r);
    }
    return b;
}

}  // namespace nt::identity
