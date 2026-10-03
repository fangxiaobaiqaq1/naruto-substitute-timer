// Owner: cpp-gated-frame
// Port of: internal/frame/frame.go + internal/frame/source.go (AnalyzeImage, frameFingerprint, dedupedAnalysis, boundedProvider 的 Sequence/Duplicate)
// Contract: include/nt/frame.h — see android/ARCHITECTURE.md.
//
// 组装层：把采集到的画面与检测引擎（engine::Engine）粘合成 UI 可直接消费的 Frame。
// 判色/识别全部委托给注入的 Engine，frame 不直接调 detect 判色 —— 引擎可热插拔。
// Windows 窗口查找/截屏、capture 客户端切换、超时 worker 不移植：Android 端由 Kotlin
// FrameLoop 的单分析线程 + latest-frame-wins 取代；这里只保留交付层的 Sequence/Duplicate。
#include "nt/frame.h"

#include <cstdio>

#include "nt/factory.h"
#include "nt/hash.h"
#include "nt/utf8.h"

namespace nt::frame {

namespace {

// fingerprintSeed is process-local: fingerprints are only ever compared within
// one run, never persisted or sent anywhere.
uint64_t fingerprintSeed() {
    static const uint64_t seed = ProcessHashSeed() ^ 0x66696e6765727072ull;  // "fingerpr"
    return seed;
}

std::string formatGateScore(double v) {
    char buf[64];
    std::snprintf(buf, sizeof buf, "%.2f", v);
    return buf;
}

// fillFromEngine：Go 版把 engine.Result 字段逐个拷到 Frame；这里 Frame 直接持有 Res，
// 只需生成 Status。frame.Bead 的 Lit/Dark 换算在 Kotlin AnalysisResult.frameBeads。
// title 对应 Go 的 win.Window{Title: source}.Title。
void fillFromEngine(Frame& f, const std::string& title, const RGBA* img, const engine::Result& res) {
    const int wd = img->Bounds().Dx(), ht = img->Bounds().Dy();
    std::string scr = "其他界面（非对局）";
    if (res.Uncertain) {
        scr = "看不清";
    } else if (res.Fighting) {
        scr = "决斗场对局中";
    }
    if (!res.Scene.empty()) {
        if (res.GateScore > 0) {
            scr = scr + " · " + res.Scene + " " + formatGateScore(res.GateScore);
        } else {
            scr = scr + " · " + res.Scene;
        }
    }
    if (!res.PlayerName.empty() || !res.OppName.empty()) {
        std::string who = utf8::TrimSpace(res.PlayerName + " vs " + res.OppName);
        if (res.OppName.empty()) {
            who = res.PlayerName;
        }
        if (!who.empty()) {
            scr = scr + " · " + who;
        }
    }
    f.Status = "已连接 " + title + " · " + std::to_string(wd) + "x" + std::to_string(ht) + " · " + scr + " · " +
               res.Name;
    f.Res = res;
}

void setErr(Frame& f, std::string msg) {
    f.HasErr = true;
    f.Err = std::move(msg);
}

}  // namespace

const std::string& MethodName(int method) {
    switch (method) {
        case 0:
            return MethodProjection;
        case 1:
            return MethodScreenshot;
        default:
            return MethodBuffer;
    }
}

// frameFingerprint hashes visible pixels, geometry and capture source. Padding
// bytes are not observations; a different image geometry or capture coordinate
// system is a new observation. A 64-bit process-local hash is ample for
// equality and duplicate detection.
uint64_t frameFingerprint(const RGBA* img, const std::string& method) {
    if (img == nullptr) {
        return 0;
    }
    const uint64_t seed = fingerprintSeed();
    const Rect r = img->Bounds();
    uint8_t header[32];
    const int values[4] = {r.Min.X, r.Min.Y, r.Max.X, r.Max.Y};
    for (int i = 0; i < 4; ++i) {
        // binary.LittleEndian.PutUint64(header[i*8:], uint64(value))：负数按补码符号扩展。
        uint64_t v = static_cast<uint64_t>(static_cast<int64_t>(values[i]));
        for (int k = 0; k < 8; ++k) {
            header[i * 8 + k] = static_cast<uint8_t>(v >> (8 * k));
        }
    }
    uint64_t sum = HashBytes(seed, header, sizeof header);
    sum = MixHash(sum, HashString(seed, method));
    const int rowBytes = r.Dx() * 4;
    if (rowBytes <= 0 || r.Dy() <= 0 || img->Pix == nullptr) {
        return sum;
    }
    // Hash row by row so a padded stride and a packed buffer with the same
    // visible pixels produce the same fingerprint.
    for (int y = 0; y < r.Dy(); ++y) {
        const uint8_t* row = img->Pix + static_cast<ptrdiff_t>(y) * img->Stride;
        sum = MixHash(sum, HashBytes(seed, row, static_cast<size_t>(rowBytes)));
    }
    return sum;
}

// AnalyzeImage is the same pixel-to-observation path for live capture and replay.
// capturedAt is acquisition completion, NOT an asserted Android render time.
Frame AnalyzeImage(const RGBA* img, engine::Engine* eng, detect::ContentMode mode, TimeNs started,
                   TimeNs capturedAt, const std::string& source) {
    (void)mode;  // 仅非 TimedEngine 的空帧预检使用（见下）。
    Frame f;
    f.CaptureStarted = started;
    f.CapturedAt = capturedAt;
    f.CaptureMethod = source;
    if (img == nullptr) {
        f.CapturedAt = 0;
        f.Hold = true;
        setErr(f, "empty captured image");
        return f;
    }
    const int w = img->Bounds().Dx(), h = img->Bounds().Dy();
    f.Width = w;
    f.Height = h;
    if (w < 400 || h < 250) {
        f.Hold = true;
        setErr(f, "capture is too small: " + std::to_string(w) + "x" + std::to_string(h));
        return f;
    }
    // Go: 只有非 TimedEngine 才做 detect.ClassifyScreen 空帧预检。C++ 的 Engine 全部带
    // AnalyzeAt（工厂产出的 Gated 在 Go 里就是 TimedEngine），因此与 Go 一样跳过预检。
    if (eng == nullptr) {
        f.Hold = true;
        setErr(f, "engine unavailable");
        return f;
    }
    f.AnalysisStarted = NowNs();
    engine::Result res = eng->AnalyzeAt(img, capturedAt);
    f.AnalyzedAt = NowNs();
    fillFromEngine(f, source, img, res);
    f.Hold = res.Uncertain;
    return f;
}

// dedupedAnalysis skips the recognition chain for a capture whose pixels are
// identical to the previously analyzed capture. The emulator does not present a
// new frame for every poll; re-analyzing the same pixels would only burn CPU and
// could never produce different beads. The reused result keeps this capture's
// own image and acquisition times; the delivery layer still decides Duplicate
// against the frame the UI actually received last.
Frame DedupedAnalysis::analyze(const RGBA* img, engine::Engine* eng, detect::ContentMode mode, TimeNs started,
                               TimeNs capturedAt, const std::string& source) {
    const uint64_t fingerprint = frameFingerprint(img, source);
    if (valid_ && img != nullptr && fingerprint == fingerprint_ && !last_.HasErr) {
        Frame f = last_;
        f.Width = img->Bounds().Dx();
        f.Height = img->Bounds().Dy();
        f.CaptureStarted = started;
        f.CapturedAt = capturedAt;
        f.AnalysisStarted = 0;
        f.AnalyzedAt = 0;
        f.fingerprint = fingerprint;
        f.fingerprinted = true;
        f.Reused = true;
        return f;
    }
    Frame f = AnalyzeImage(img, eng, mode, started, capturedAt, source);
    f.fingerprint = fingerprint;
    f.fingerprinted = true;
    if (img != nullptr && !f.HasErr) {
        valid_ = true;
        fingerprint_ = fingerprint;
        last_ = f;
    } else {
        valid_ = false;
    }
    return f;
}

Pipeline::Pipeline(const config::Config& cfg) : cfg_(cfg) {
    // Go newSelectableSnapshotter: mode, _ := detect.ParseMode(cfg.Layout.ContentMode)
    detect::ContentMode mode = detect::ModeAuto;
    if (!detect::ParseMode(cfg_.Layout.ContentMode, mode)) {
        mode = detect::ModeAuto;
    }
    mode_ = mode;
    std::string err;
    engine_ = factory::New(factory::FromApp(cfg_), &err);
    if (engine_ == nullptr) {
        error_ = err.empty() ? std::string("engine build failed") : err;
    }
}

Frame Pipeline::Analyze(const RGBA* img, TimeNs started, TimeNs capturedAt, const std::string& method) {
    if (engine_ == nullptr) {
        Frame f;
        f.Hold = true;
        f.CaptureStarted = started;
        f.CaptureMethod = method;
        setErr(f, error_.empty() ? std::string("engine unavailable") : error_);
        return f;
    }
    Frame f = dedupe_.analyze(img, engine_.get(), mode_, started, capturedAt, method);
    // boundedProvider 的交付判定：只有成功帧才进入序号与重复判定；失败帧不改变交付状态。
    if (img != nullptr && !f.HasErr) {
        sequence_++;
        f.Sequence = sequence_;
        uint64_t hash = f.fingerprint;
        if (!f.fingerprinted) {
            hash = frameFingerprint(img, f.CaptureMethod);
        }
        f.Duplicate = havePrevious_ && hash == previous_;
        previous_ = hash;
        havePrevious_ = true;
    }
    return f;
}

// 交付层状态复位（采集源切换 / 服务重启）。Go 的 boundedProvider 在整个进程生命周期只
// 建一次，序号单调递增，所以这里不清零 sequence_；只丢掉「上一交付帧」与去重缓存，
// 让新来源的第一帧必定被重新识别、且不被判为 Duplicate。
void Pipeline::ResetDelivery() {
    havePrevious_ = false;
    previous_ = 0;
    dedupe_ = DedupedAnalysis{};
}

}  // namespace nt::frame
