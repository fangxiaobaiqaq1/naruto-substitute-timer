// Owner: hudtext（安卓文字认边，见 nt/hudtext.h）
// Port of: internal/hudtext/engine.go（Engine：异步、节奏、作废规则、状态）、pause.go，
// 以及 internal/ocr 的单一后台工作者（Windows 是常驻的本机 OCR 子进程）。
#include <algorithm>
#include <chrono>
#include <cstdio>
#include <thread>

#include "nt/hudtext.h"
#include "nt/match.h"
#include "nt/utf8.h"

namespace nt::hudtext {

// ---------------------------------------------------------------------------
// Worker
// ---------------------------------------------------------------------------

Worker& Worker::Instance() {
    static Worker& w = *new Worker();  // never destroyed: the thread may still be running at exit
    return w;
}

void Worker::SetLoader(Loader loader) {
    std::lock_guard<std::mutex> lock(mu_);
    loader_ = std::move(loader);
}

bool Worker::HasLoader() {
    std::lock_guard<std::mutex> lock(mu_);
    return loader_ != nullptr;
}

void Worker::SetIdleRelease(DurationNs d) {
    std::lock_guard<std::mutex> lock(mu_);
    idle_ = d;
    cv_.notify_all();
}

bool Worker::TrySubmit(Task task) {
    std::lock_guard<std::mutex> lock(mu_);
    if (busy_ || task_ != nullptr) return false;
    task_ = std::move(task);
    busy_ = true;
    if (!started_) {
        started_ = true;
        std::thread([this] { run(); }).detach();
    }
    cv_.notify_all();
    return true;
}

void Worker::Release() {
    std::lock_guard<std::mutex> lock(mu_);
    releaseRequested_ = true;
    cv_.notify_all();
}

bool Worker::Idle() {
    std::lock_guard<std::mutex> lock(mu_);
    return !busy_ && task_ == nullptr;
}

bool Worker::Loaded() {
    std::lock_guard<std::mutex> lock(mu_);
    return recognizer_ != nullptr;
}

void Worker::Stats(double& loadMillis, size_t& weightBytes) {
    std::lock_guard<std::mutex> lock(mu_);
    loadMillis = loadMillis_;
    weightBytes = weightBytes_;
}

void Worker::SetLog(std::function<void(const std::string&)> log) {
    std::lock_guard<std::mutex> lock(mu_);
    log_ = std::move(log);
}

void Worker::Log(const std::string& line) {
    std::function<void(const std::string&)> log;
    {
        std::lock_guard<std::mutex> lock(mu_);
        log = log_;
    }
    if (log) log(line);
}

bool Worker::Known(char32_t r) {
    std::lock_guard<std::mutex> lock(mu_);
    if (known_.empty()) return true;  // never loaded: every character must match exactly
    return std::binary_search(known_.begin(), known_.end(), r);
}

// One thread, one task at a time. The model loads on the first task and is released after
// `idle_` without a task (or on Release()).
void Worker::run() {
    std::unique_lock<std::mutex> lock(mu_);
    for (;;) {
        if (task_ == nullptr) {
            if (recognizer_ != nullptr && !releaseRequested_) {
                cv_.wait_for(lock, std::chrono::nanoseconds(idle_), [this] { return task_ != nullptr || releaseRequested_; });
            } else if (recognizer_ == nullptr) {
                cv_.wait(lock, [this] { return task_ != nullptr || releaseRequested_; });
            }
            if (task_ == nullptr) {
                // Idle timeout or an explicit release: free the network now.
                std::unique_ptr<ocr::Recognizer> drop = std::move(recognizer_);
                releaseRequested_ = false;
                auto log = log_;
                lock.unlock();
                if (drop != nullptr && log) log("text model released");
                drop.reset();
                lock.lock();
                continue;
            }
        }
        Task task = std::move(task_);
        task_ = nullptr;
        std::string loadError;
        if (recognizer_ == nullptr) {
            Loader loader = loader_;
            lock.unlock();
            std::unique_ptr<ocr::Recognizer> loaded;
            std::vector<char32_t> known;
            const TimeNs start = NowNs();
            if (loader == nullptr) {
                loadError = "本机文字识别模型未加载";
            } else {
                std::vector<uint8_t> bytes;
                if (loader(bytes, &loadError)) {
                    loaded = ocr::Recognizer::Load(bytes.data(), bytes.size(), &loadError);
                    if (loaded != nullptr) {
                        for (const std::string& c : loaded->Characters()) {
                            const std::vector<char32_t> rs = utf8::Runes(c);
                            if (rs.size() == 1) known.push_back(rs[0]);
                        }
                        std::sort(known.begin(), known.end());
                        known.erase(std::unique(known.begin(), known.end()), known.end());
                    }
                } else if (loadError.empty()) {
                    loadError = "本机文字识别模型读取失败";
                }
            }
            const double millis = static_cast<double>(NowNs() - start) / 1e6;
            if (loaded != nullptr) {
                char buf[128];
                std::snprintf(buf, sizeof buf, "text model loaded in %.1f ms, weights %.1f MB", millis,
                              static_cast<double>(loaded->WeightBytes()) / (1 << 20));
                Log(buf);
            } else {
                Log("text model load failed: " + loadError);
            }
            lock.lock();
            if (loaded != nullptr) {
                weightBytes_ = loaded->WeightBytes();
                loadMillis_ = millis;
                recognizer_ = std::move(loaded);
                if (known_.empty()) known_ = std::move(known);
            }
        }
        const ocr::Recognizer* recognizer = recognizer_.get();
        lock.unlock();
        task(recognizer, loadError);
        task = nullptr;
        lock.lock();
        busy_ = false;
    }
}

// ---------------------------------------------------------------------------
// Engine
// ---------------------------------------------------------------------------

struct Completion {
    uint64_t generation = 0;
    Rect bounds;
    std::array<Rect, 2> regions;
    std::string profile;
    TimeNs at = 0;
    std::array<uint64_t, 2> signatures{};
    std::array<std::string, 2> values;
    std::array<std::shared_ptr<GlyphEvidence>, 2> proofs;
    std::string err;
};

struct Engine::Mailbox {
    std::mutex mu;
    std::shared_ptr<Completion> done;
};

namespace {

// Go configuredNames: an exact, sorted, de-duplicated snapshot and its key.
std::vector<std::string> configured(const std::vector<std::string>& names, std::string& key) {
    std::vector<std::string> out;
    for (const std::string& n : names) {
        const std::string t = utf8::TrimSpace(n);
        if (!t.empty()) out.push_back(t);
    }
    std::sort(out.begin(), out.end());
    out.erase(std::unique(out.begin(), out.end()), out.end());
    key.clear();
    for (size_t i = 0; i < out.size(); ++i) {
        if (i > 0) key.push_back('\0');
        key += out[i];
    }
    return out;
}

// The strip crop the OCR job owns. Android frames may be RGBX (undefined X byte); the Go
// frames OCR reads are opaque, and the row scaling clamps colour to alpha, so alpha is 255.
RGBA opaqueCrop(const RGBA* img, const Rect& r) {
    RGBA crop = match::CropRGBA(img, r);
    for (int y = crop.Bounds().Min.Y; y < crop.Bounds().Max.Y; ++y) {
        uint8_t* p = crop.Pix + crop.PixOffset(crop.Bounds().Min.X, y);
        for (int x = 0; x < crop.Bounds().Dx(); ++x) p[x * 4 + 3] = 255;
    }
    return crop;
}

}  // namespace

// One OCR job (Go Engine.read for the accounts): the three treatments of both strips, the
// two-of-three consensus and its glyph evidence. The third treatment of a side is read only
// when it can change the result: the first two give the same account segment (or none) means
// the majority is already decided, and the evidence comes from the first row (in Go's order)
// that yields it; a third row is still read when the first two give no evidence. So the
// values and proofs are exactly those of reading all six rows; a phone saves a third of the
// work in the common case.
bool ReadAccounts(const ocr::Recognizer& recognizer, const std::array<RGBA, 2>& strips, const std::array<Rect, 2>& regions,
                  ReadResult& out, const std::atomic<bool>* cancel) {
    out = ReadResult{};
    const std::vector<Row> rows = BuildRows(strips);
    std::vector<ocr::Line> lines;
    std::vector<bool> read(rows.size(), false);
    auto readRows = [&](const std::function<bool(const Row&)>& want) {
        std::vector<ocr::Recognizer::RowImage> images;
        std::vector<size_t> which;
        for (size_t k = 0; k < rows.size(); ++k) {
            if (read[k] || !want(rows[k])) continue;
            images.push_back({&rows[k].image, rows[k].rect});
            which.push_back(k);
        }
        if (images.empty()) return true;
        std::vector<ocr::Line> more;
        std::vector<double> millis;
        if (!recognizer.ReadRows(images, more, &out.err, 2, cancel, &millis)) return false;
        for (size_t k : which) read[k] = true;
        for (double m : millis) out.rowMillis += m;
        out.rows += static_cast<int>(images.size());
        lines.insert(lines.end(), std::make_move_iterator(more.begin()), std::make_move_iterator(more.end()));
        return true;
    };
    if (!readRows([](const Row& r) { return r.variant < 2; })) return false;
    std::array<bool, 2> third{};
    {
        const auto texts = Texts(rows, lines);
        for (size_t i = 0; i < 2; ++i) third[i] = AccountSegment(texts[i][0]) != AccountSegment(texts[i][1]);
    }
    if ((third[0] || third[1]) && !readRows([&](const Row& r) { return r.variant == 2 && third[static_cast<size_t>(r.side)]; }))
        return false;
    out.texts = Texts(rows, lines);
    for (size_t i = 0; i < 2; ++i) {
        out.values[i] = Consensus(out.texts[i]);
        if (out.values[i].empty()) continue;
        out.proofs[i] = Evidence(rows, lines, static_cast<int>(i), out.values[i], strips[i], regions[i]);
        if (out.proofs[i] == nullptr && !third[i]) {
            // Go would go on to the third treatment's row.
            if (!readRows([&](const Row& r) { return r.variant == 2 && r.side == static_cast<int>(i); })) return false;
            out.texts = Texts(rows, lines);
            out.proofs[i] = Evidence(rows, lines, static_cast<int>(i), out.values[i], strips[i], regions[i]);
        }
    }
    return true;
}

Engine::Engine(std::shared_ptr<engine::Engine> inner, const config::Config& cfg, std::function<std::vector<std::string>()> names,
               Dictionary known, Worker* worker)
    : inner_(std::move(inner)),
      cfg_(cfg),
      names_(std::move(names)),
      known_(std::move(known)),
      worker_(worker),
      layout_(cfg.Layout),
      mailbox_(std::make_shared<Mailbox>()) {}

Engine::~Engine() {
    if (cancel_ != nullptr) cancel_->store(true);  // a running job for this engine is no longer wanted
}

void Engine::invalidate() {
    reported_ = false;
    generation_++;
    haveKey_ = false;
    sides_ = {};
    nextAt_ = 0;
    pausedAt_ = 0;
}

void Engine::backendFailed(TimeNs at, const std::string& cause) {
    std::lock_guard<std::mutex> lock(statusMu_);
    status_ = "unavailable";
    lastError_ = "本机文字识别暂不可用，仍使用账号名图片/手动选择";
    if (!cause.empty()) {
        std::vector<char32_t> detail = utf8::Runes(cause);
        if (detail.size() > 320) {
            detail.resize(320);
            detail.push_back(U'…');
        }
        lastError_ += ": " + utf8::FromRunes(detail);
    }
    retryAt_ = at + retryDelay;
}

void Engine::SetEnabled(bool enabled) {
    if (enabled_ == enabled) return;
    enabled_ = enabled;
    invalidate();
    retryAt_ = 0;
    std::lock_guard<std::mutex> lock(statusMu_);
    lastError_.clear();
    status_ = enabled ? "waiting" : "off";
}

std::string Engine::Status() const {
    std::lock_guard<std::mutex> lock(statusMu_);
    return status_;
}

std::string Engine::LastError() const {
    std::lock_guard<std::mutex> lock(statusMu_);
    return lastError_;
}

bool Engine::poll(std::shared_ptr<Completion>& out) {
    std::lock_guard<std::mutex> lock(mailbox_->mu);
    if (mailbox_->done == nullptr) return false;
    out = std::move(mailbox_->done);
    mailbox_->done = nullptr;
    return true;
}

// Android: a readable bead HUD on both sides while the scene is unknown (the duel's round
// marker sits under our own overlay card) counts like a confirmed fight frame.
bool Engine::hudReadable(const engine::Result& res) const {
    if (res.Fighting && !res.Uncertain) return true;
    if (!res.Scene.empty() && res.Scene != "fight") return false;
    return engine::hasCompleteHUD(res);
}

// Go pauseText: a remembered fight with a short observation hold is not a new scene; no
// fields are emitted during it. Android differs (nt/hudtext.h): on the phone our own overlay
// card sits on the duel's round marker, so a fight is "uncertain" for a frame every second or
// two, and one reading takes 1–2 s there. Go drops pending readings and in-flight work at every
// hold, which on the phone means an account is never read twice in a row. Here a hold of the
// same geometry (fight scene, or no scene at all) up to textPauseTTL keeps them: a pending
// reading keeps its glyph proof, which is checked on every hold frame as before, and a reading
// that arrives during the hold is used at the first readable frame after it — again only if
// its glyphs match that frame. A longer hold, another page or other geometry still clears all.
bool Engine::pauseText(const RGBA* img, const engine::Result& res, TimeNs at) {
    if (!enabled_ || !haveKey_ || img == nullptr || !res.Uncertain || (res.Scene != "fight" && !res.Scene.empty())) return false;
    if (pausedAt_ != 0 && at - pausedAt_ > textPauseTTL) return false;
    std::array<Rect, 2> regions;
    if (!layout_.Regions(img, res.LayoutProfile, regions)) return false;
    if (key_.bounds != img->Bounds() || key_.regions != regions || key_.profile != res.LayoutProfile) return false;
    if (pausedAt_ == 0) pausedAt_ = at;
    std::array<uint64_t, 2> signatures{};
    std::array<int, 2> white{};
    validateTextPixels(img, regions, signatures, white);
    return true;
}

void Engine::validateTextPixels(const RGBA* img, const std::array<Rect, 2>& regions, std::array<uint64_t, 2>& signatures,
                                std::array<int, 2>& white) {
    for (size_t i = 0; i < 2; ++i) {
        signatures[i] = Lettering(*img, regions[i], white[i]);
        Side& s = sides_[i];
        const bool changed = signatures[i] != s.signature;
        if ((s.accountProof != nullptr && !s.accountProof->Matches(img)) || (s.accountProof == nullptr && changed)) {
            s.account = ValueState{};
            s.accountProof = nullptr;
        }
        s.signature = signatures[i];
    }
}

engine::Result Engine::AnalyzeAt(const RGBA* img, TimeNs at) {
    engine::Result res = inner_->AnalyzeAt(img, at);
    if (at == 0) at = NowNs();
    std::string namesKey;
    const std::vector<std::string> names = configured(names_ ? names_() : std::vector<std::string>{}, namesKey);
    if (namesKey != namesKey_) {
        // Evidence belongs to the configured account list that produced it.
        invalidate();
        namesKey_ = namesKey;
    }
    if (lastAt_ != 0 && at < lastAt_) {
        invalidate();
        // Replay/clock reversal starts a new time domain, not an infinite backoff.
        retryAt_ = 0;
        std::lock_guard<std::mutex> lock(statusMu_);
        lastError_.clear();
    }
    lastAt_ = at;
    // A job past its time is cancelled and counts as a backend failure (Go: 3 s context).
    if (busy_ && cancel_ != nullptr && NowNs() - busySince_ > jobTimeout && !cancel_->load()) {
        cancel_->store(true);
        backendFailed(at, "识别超时");
    }
    bool active = enabled_ && !names.empty() && img != nullptr &&
                  (res.LayoutProfile == "duel" || res.LayoutProfile == "camp") && hudReadable(res);
    std::array<Rect, 2> regions;
    if (active) active = layout_.Regions(img, res.LayoutProfile, regions);
    std::shared_ptr<Completion> done;
    if (!active) {
        const bool paused = pauseText(img, res, at);
        if (!paused && haveKey_) invalidate();
        // During a hold a reading stays in the mailbox for the first readable frame after it.
        if (!paused && poll(done)) {
            busy_ = false;
            if (!done->err.empty() && enabled_) backendFailed(at, done->err);
        }
        std::lock_guard<std::mutex> lock(statusMu_);
        if (!enabled_ || names.empty()) status_ = "off";
        else if (status_ != "unavailable" || at >= retryAt_) status_ = "waiting";
        res.TextStatus = status_;
        return res;
    }
    if (pausedAt_ != 0 && at - pausedAt_ > textPauseTTL) invalidate();
    pausedAt_ = 0;
    Key key{img->Bounds(), regions, res.LayoutProfile};
    if (!haveKey_ || key_ != key) {
        invalidate();
        key_ = key;
        haveKey_ = true;
        std::lock_guard<std::mutex> lock(statusMu_);
        status_ = "waiting";
    }
    std::array<uint64_t, 2> signatures{};
    std::array<int, 2> white{};
    validateTextPixels(img, regions, signatures, white);
    if (poll(done)) {
        busy_ = false;
        if (!done->err.empty()) {
            // Backend health outlives scene generations; only text evidence is stale.
            backendFailed(at, done->err);
        } else if (done->generation == generation_ && done->bounds == key_.bounds && done->regions == key_.regions &&
                   done->profile == key_.profile && at - done->at >= 0 && at - done->at <= resultMaxAge) {
            {
                std::lock_guard<std::mutex> lock(statusMu_);
                status_ = "pending";
                lastError_.clear();
            }
            retryAt_ = 0;
            std::string note;
            for (size_t i = 0; i < 2; ++i) {
                const bool unchanged = signatures[i] == done->signatures[i];
                ObserveBound(sides_[i].account, sides_[i].accountProof, done->values[i], done->proofs[i], img, done->at, unchanged);
                note += std::string(i == 0 ? " L" : " R") + (done->values[i].empty() ? "-" : done->proofs[i] != nullptr ? "+" : "?") +
                        (sides_[i].account.accepted.empty() ? "" : "=");
            }
            if (worker_ != nullptr) worker_->Log("text result used:" + note);
        } else if (worker_ != nullptr) {
            worker_->Log(std::string("text result dropped: ") +
                         (done->generation != generation_ ? "scene/hold changed" : at - done->at > resultMaxAge ? "too old" : "geometry changed"));
        }
    }
    // The Windows rule (Go Engine.Analyze): the one side showing 我的名字, else (ours covered) the
    // side opposite the one complete account that is not mine. See DecideSide / nt/hudtext.h.
    bool used = false;
    if (res.PlayerSide.empty()) {
        const SideDecision d = DecideSide({sides_[0].account.accepted, sides_[1].account.accepted}, names, known_);
        if (d.side >= 0) {
            if (!reported_ && worker_ != nullptr) {
                reported_ = true;
                worker_->Log(std::string("text side: ") + (d.side == 0 ? "left" : "right") + " (" + d.how + ")");
            }
            res.PlayerSide = d.side == 0 ? "left" : "right";
            if (!d.player.empty()) res.PlayerName = d.player;  // elimination: PlayerName stays empty (Go)
            if (!d.opp.empty()) res.OppName = d.opp;
            used = true;
        }
    }
    const bool needs = res.PlayerSide.empty();
    std::string status;
    {
        std::lock_guard<std::mutex> lock(statusMu_);
        // UI status from CURRENT evidence/work, not a remembered "ready".
        if (needs && at < retryAt_) status_ = "unavailable";
        else if (used) status_ = "ready";
        else if (!needs) status_ = "template";
        else if (busy_) status_ = "reading";
        else if (white[0] >= 12 || white[1] >= 12) status_ = "pending";
        else status_ = "waiting";
        status = status_;
    }
    if ((needs || used) && !busy_ && at >= nextAt_ && at >= retryAt_ && (white[0] >= 12 || white[1] >= 12) &&
        worker_ != nullptr) {
        auto job = std::make_shared<Completion>();
        job->generation = generation_;
        job->bounds = key_.bounds;
        job->regions = regions;
        job->profile = key_.profile;
        job->at = at;
        job->signatures = signatures;
        auto strips = std::make_shared<std::array<RGBA, 2>>();
        for (size_t i = 0; i < 2; ++i) (*strips)[i] = opaqueCrop(img, regions[i]);
        auto cancel = std::make_shared<std::atomic<bool>>(false);
        std::weak_ptr<Mailbox> mailbox = mailbox_;
        auto onRead = OnRead;
        auto task = [job, strips, cancel, mailbox, onRead](const ocr::Recognizer* recognizer, const std::string& loadError) {
            const TimeNs start = NowNs();
            try {
                if (recognizer == nullptr) {
                    job->err = loadError.empty() ? std::string("本机文字识别模型不可用") : loadError;
                } else {
                    ReadResult r;
                    if (!ReadAccounts(*recognizer, *strips, job->regions, r, cancel.get())) {
                        job->err = r.err.empty() ? std::string("识别失败") : r.err;
                    } else {
                        job->values = r.values;
                        job->proofs = r.proofs;
                        char buf[160];
                        std::snprintf(buf, sizeof buf, "text job: %d rows in %.0f ms (%.0f ms per row, 2 threads)", r.rows,
                                      static_cast<double>(NowNs() - start) / 1e6, r.rows > 0 ? r.rowMillis / r.rows : 0.0);
                        Worker::Instance().Log(buf);
                        if (onRead) onRead(job->at, r.texts, static_cast<double>(NowNs() - start) / 1e6);
                    }
                }
            } catch (const std::exception& e) {
                job->err = std::string("识别出错: ") + e.what();
            } catch (...) {
                job->err = "识别出错";
            }
            if (auto box = mailbox.lock()) {
                std::lock_guard<std::mutex> lock(box->mu);
                box->done = job;
            }
        };
        if (worker_->TrySubmit(std::move(task))) {
            busy_ = true;
            busySince_ = NowNs();
            cancel_ = cancel;
            nextAt_ = at + (needs ? requestInterval : refreshInterval);
            std::lock_guard<std::mutex> lock(statusMu_);
            if (status_ != "ready") status_ = "reading";
            status = status_;
        }
    }
    res.TextStatus = status;
    if (status == "unavailable") {
        std::lock_guard<std::mutex> lock(statusMu_);
        res.TextError = lastError_;
    }
    return res;
}

}  // namespace nt::hudtext
