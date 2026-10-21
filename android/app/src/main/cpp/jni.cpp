// jni.cpp — Kotlin <-> C++ 边界（architect 所有）。
//
// 职责：
//  * 资源注册（AssetStore），引擎构建（frame::Pipeline），我方账号名（identity::SetMineNames）。
//  * 一帧分析：AHardwareBuffer（零拷贝 CPU 映射）或 direct ByteBuffer → nt::RGBA 视图 →
//    Pipeline::Analyze → 编码进调用方预分配的 direct ByteBuffer（格式见 ARCHITECTURE.md §JNI）。
//  * 捕获所有 C++ 异常：JNI 边界之外绝不抛出。
//
// 线程：两把锁，固定顺序 g_analysisMu → g_cfgMu（反过来从不）。
//  * g_analysisMu：引擎的使用与生命周期（nativeAnalyze* 整帧、nativeInit / nativeFinishAssets /
//    nativeSetCalibration 的重建 / nativeResetDelivery / nativeRelease），识别核心因此只会在一个线程上运行
//    （Go 里的 sync.Mutex 在 C++ 中可省略）。
//  * g_cfgMu：配置与报告（g_playerNames / g_calibration / g_lastError / g_lastGeometry），只短暂持有。
//    nativeSetPlayerNames / nativeLastError / nativeLastGeometry 只用它，UI 线程不会等一整帧（audit NC-08）。
// 进程级状态一律 *new、永不析构：exit() 跑静态析构时分析线程和线程池可能还在一帧里（audit NC-06）。
// 方法通过 RegisterNatives 注册，库本身 -fvisibility=hidden，只导出 JNI_OnLoad。

#include <android/hardware_buffer.h>
#include <android/hardware_buffer_jni.h>
#include <android/log.h>
#include <jni.h>

#include <array>
#include <climits>
#include <cmath>
#include <cstdint>
#include <cstring>
#include <exception>
#include <memory>
#include <mutex>
#include <optional>
#include <string>
#include <vector>

#include "nt/assets.h"
#include "nt/config.h"
#include "nt/frame.h"
#include "nt/identity.h"
#include "nt/image.h"
#include "nt/result.h"
#include "nt/time.h"

#define NT_LOG_TAG "narutocore"
#define NT_LOGI(...) __android_log_print(ANDROID_LOG_INFO, NT_LOG_TAG, __VA_ARGS__)
#define NT_LOGE(...) __android_log_print(ANDROID_LOG_ERROR, NT_LOG_TAG, __VA_ARGS__)

namespace {

// ---------------------------------------------------------------------------
// 结果编码（v1，小端）。改动必须同步 NativeCore.kt 与 ARCHITECTURE.md。
// ---------------------------------------------------------------------------
constexpr int32_t kMagic = 0x4E545231;  // 'NTR1'
constexpr int kHeaderBytes = 72;
constexpr int kBeadBytes = 24;
constexpr int kStringSlots = 13;

enum ResultFlag : int32_t {
    kFighting = 1 << 0,
    kUncertain = 1 << 1,
    kRoundOpening = 1 << 2,
    kHold = 1 << 3,
    kHasErr = 1 << 4,
    kDuplicate = 1 << 5,
    kReused = 1 << 6,
};

enum BeadFlag : int32_t {
    kBeadLit = 1 << 0,
    kBeadGold = 1 << 1,
    kBeadUnknown = 1 << 2,
};

// 返回码：>0 写入字节数；0 = native 未就绪/内部错误（看 nativeLastError）；<0 = 缓冲太小，需要 -r 字节。
constexpr jint kNotReady = 0;

struct Writer {
    uint8_t* base;
    size_t cap;
    size_t pos = 0;
    bool overflow = false;

    void raw(const void* p, size_t n) {
        if (pos + n > cap) {
            overflow = true;
            pos += n;
            return;
        }
        std::memcpy(base + pos, p, n);
        pos += n;
    }
    void i32(int32_t v) { raw(&v, 4); }  // Android ABI 均为小端
    void i64(int64_t v) { raw(&v, 8); }
    void f64(double v) { raw(&v, 8); }
    void str(const std::string& s) {
        i32(static_cast<int32_t>(s.size()));
        raw(s.data(), s.size());
    }
};

// "L1".."R6" → (side << 8) | number；其它 → -1（Kotlin 解成 ""）。
int32_t labelCode(const std::string& label) {
    if (label.size() < 2) return -1;
    int32_t side;
    if (label[0] == 'L') side = 0;
    else if (label[0] == 'R') side = 1;
    else return -1;
    int32_t n = 0;
    for (size_t i = 1; i < label.size(); ++i) {
        char c = label[i];
        if (c < '0' || c > '9') return -1;
        n = n * 10 + (c - '0');
        if (n > 255) return -1;
    }
    return (side << 8) | n;
}

size_t encodedSize(const nt::frame::Frame& f) {
    const nt::engine::Result& r = f.Res;
    size_t n = kHeaderBytes + static_cast<size_t>(r.Beads.size()) * kBeadBytes + 4 * kStringSlots;
    for (const std::string* s : {&r.Name, &r.LayoutProfile, &r.Scene, &r.LeftNinja, &r.RightNinja,
                                 &r.LeftNinjaCandidate, &r.RightNinjaCandidate, &r.PlayerSide, &r.PlayerName,
                                 &r.OppName, &f.Status, &f.Err, &f.CaptureMethod})
        n += s->size();
    return n;
}

jint encodeFrame(const nt::frame::Frame& f, uint8_t* out, size_t cap) {
    size_t need = encodedSize(f);
    if (need > cap || need > 0x7fffffff) return -static_cast<jint>(need);
    const nt::engine::Result& r = f.Res;
    Writer w{out, cap};
    int32_t flags = 0;
    if (r.Fighting) flags |= kFighting;
    if (r.Uncertain) flags |= kUncertain;
    if (r.RoundOpening) flags |= kRoundOpening;
    if (f.Hold) flags |= kHold;
    if (f.HasErr) flags |= kHasErr;
    if (f.Duplicate) flags |= kDuplicate;
    if (f.Reused) flags |= kReused;
    w.i32(kMagic);
    w.i32(static_cast<int32_t>(need));
    w.i32(flags);
    w.i32(r.LeftSlots);
    w.i32(r.RightSlots);
    w.i32(f.Width);
    w.i32(f.Height);
    w.i32(static_cast<int32_t>(r.Beads.size()));
    w.i64(static_cast<int64_t>(f.Sequence));
    w.i64(f.CapturedAt);
    w.i64(f.AnalysisStarted);
    w.i64(f.AnalyzedAt);
    w.f64(r.GateScore);
    for (const nt::engine::BeadInfo& b : r.Beads) {
        int32_t bf = 0;
        if (b.Lit) bf |= kBeadLit;
        if (b.Gold) bf |= kBeadGold;
        if (b.Unknown) bf |= kBeadUnknown;
        w.i32(b.X);
        w.i32(b.Y);
        w.i32(labelCode(b.Label));
        w.i32(bf);
        w.f64(b.Conf);
    }
    w.str(r.Name);
    w.str(r.LayoutProfile);
    w.str(r.Scene);
    w.str(r.LeftNinja);
    w.str(r.RightNinja);
    w.str(r.LeftNinjaCandidate);
    w.str(r.RightNinjaCandidate);
    w.str(r.PlayerSide);
    w.str(r.PlayerName);
    w.str(r.OppName);
    w.str(f.Status);
    w.str(f.Err);
    w.str(f.CaptureMethod);
    if (w.overflow || w.pos != need) return -static_cast<jint>(need);
    return static_cast<jint>(need);
}

// ---------------------------------------------------------------------------
// 全局状态（never destroyed, see the header comment）
// ---------------------------------------------------------------------------
std::mutex& g_analysisMu = *new std::mutex;  // g_pipeline
std::mutex& g_cfgMu = *new std::mutex;       // g_playerNames / g_calibration / g_lastError / g_lastGeometry
std::unique_ptr<nt::frame::Pipeline>& g_pipeline = *new std::unique_ptr<nt::frame::Pipeline>;
std::vector<std::string>& g_playerNames = *new std::vector<std::string>;
// 区域校准（docs/区域校准.md）：nullopt = 全部自动。与 g_playerNames 一样跨 nativeInit 保留。
std::optional<nt::config::RegionCalibration>& g_calibration = *new std::optional<nt::config::RegionCalibration>;
std::string& g_lastError = *new std::string;

// 最近一次分析的帧的几何（docs/区域校准.md 每帧几何），整帧一起替换；换引擎时清空。
struct FrameGeometry {
    bool valid = false;
    int width = 0, height = 0;
    std::string profile;
    uint64_t sequence = 0;
    std::array<nt::engine::SideGeometry, 2> sides;
};
FrameGeometry& g_lastGeometry = *new FrameGeometry;

// The encoded result of this thread's last analysis when it did not fit the
// caller's buffer (the call returned -need): nativeCopyLastResult hands out
// exactly that result instead of analysing the same pixels again, which
// would consume a second sequence number and come back Duplicate+Reused
// (audit NC-07). Per thread like the Kotlin decoder; cleared by every analysis.
thread_local std::vector<uint8_t> t_pendingResult;

// Callers must not hold g_cfgMu.
void setError(const std::string& e) {
    {
        std::lock_guard<std::mutex> lock(g_cfgMu);
        g_lastError = e;
    }
    NT_LOGE("%s", e.c_str());
}

void clearGeometry() {
    std::lock_guard<std::mutex> lock(g_cfgMu);
    g_lastGeometry = FrameGeometry{};
}

std::string jstr(JNIEnv* env, jstring s) {
    if (s == nullptr) return {};
    // GetStringUTFChars 产出 modified UTF-8（补充平面字符会编码成代理对）；
    // 资源路径与账号名用 GetStringRegion + 手工转 UTF-8 才能与 Go 字符串逐字节一致。
    jsize len = env->GetStringLength(s);
    std::vector<jchar> buf(static_cast<size_t>(len));
    if (len > 0) env->GetStringRegion(s, 0, len, buf.data());
    std::string out;
    out.reserve(static_cast<size_t>(len) * 3);
    for (jsize i = 0; i < len; ++i) {
        uint32_t c = buf[static_cast<size_t>(i)];
        if (c >= 0xD800 && c <= 0xDBFF && i + 1 < len) {
            uint32_t d = buf[static_cast<size_t>(i + 1)];
            if (d >= 0xDC00 && d <= 0xDFFF) {
                c = 0x10000 + ((c - 0xD800) << 10) + (d - 0xDC00);
                ++i;
            }
        }
        if (c >= 0xD800 && c <= 0xDFFF) c = 0xFFFD;  // 孤立代理：Go 同样替换为 U+FFFD
        if (c < 0x80) {
            out.push_back(static_cast<char>(c));
        } else if (c < 0x800) {
            out.push_back(static_cast<char>(0xC0 | (c >> 6)));
            out.push_back(static_cast<char>(0x80 | (c & 0x3F)));
        } else if (c < 0x10000) {
            out.push_back(static_cast<char>(0xE0 | (c >> 12)));
            out.push_back(static_cast<char>(0x80 | ((c >> 6) & 0x3F)));
            out.push_back(static_cast<char>(0x80 | (c & 0x3F)));
        } else {
            out.push_back(static_cast<char>(0xF0 | (c >> 18)));
            out.push_back(static_cast<char>(0x80 | ((c >> 12) & 0x3F)));
            out.push_back(static_cast<char>(0x80 | ((c >> 6) & 0x3F)));
            out.push_back(static_cast<char>(0x80 | (c & 0x3F)));
        }
    }
    return out;
}

jstring newJString(JNIEnv* env, const std::string& s) {
    // UTF-8 → UTF-16，再 NewString（避免 NewStringUTF 的 modified UTF-8 问题）。
    std::vector<jchar> u;
    u.reserve(s.size());
    for (size_t i = 0; i < s.size();) {
        uint32_t c = static_cast<uint8_t>(s[i]);
        size_t n = 1;
        if (c >= 0xF0 && i + 3 < s.size()) {
            c = ((c & 0x07) << 18) | ((static_cast<uint8_t>(s[i + 1]) & 0x3F) << 12) |
                ((static_cast<uint8_t>(s[i + 2]) & 0x3F) << 6) | (static_cast<uint8_t>(s[i + 3]) & 0x3F);
            n = 4;
        } else if (c >= 0xE0 && i + 2 < s.size()) {
            c = ((c & 0x0F) << 12) | ((static_cast<uint8_t>(s[i + 1]) & 0x3F) << 6) |
                (static_cast<uint8_t>(s[i + 2]) & 0x3F);
            n = 3;
        } else if (c >= 0xC0 && i + 1 < s.size()) {
            c = ((c & 0x1F) << 6) | (static_cast<uint8_t>(s[i + 1]) & 0x3F);
            n = 2;
        } else if (c >= 0x80) {
            c = 0xFFFD;
        }
        if (c >= 0x10000) {
            c -= 0x10000;
            u.push_back(static_cast<jchar>(0xD800 + (c >> 10)));
            u.push_back(static_cast<jchar>(0xDC00 + (c & 0x3FF)));
        } else {
            u.push_back(static_cast<jchar>(c));
        }
        i += n;
    }
    return env->NewString(u.data(), static_cast<jsize>(u.size()));
}

// 方法 int → 字符串（与 NativeCore.METHOD_* 一致）。
const std::string& methodName(jint method) { return nt::frame::MethodName(static_cast<int>(method)); }

// 用已构建的 pipeline 分析一张视图并编码。调用方持有 g_analysisMu（不持有 g_cfgMu）。
// 结果放不下时返回 -need，并把编码好的结果留在 t_pendingResult（nativeCopyLastResult）。
jint analyzeLocked(const nt::RGBA& img, jlong capturedAtNanos, jint method, uint8_t* out, size_t cap) {
    t_pendingResult.clear();
    if (!g_pipeline || !g_pipeline->Ok()) {
        setError("native pipeline not ready (call nativeFinishAssets first)");
        return kNotReady;
    }
    nt::TimeNs at = capturedAtNanos > 0 ? static_cast<nt::TimeNs>(capturedAtNanos) : nt::NowNs();
    nt::frame::Frame f = g_pipeline->Analyze(&img, at, at, methodName(method));
    if (!f.HasErr) {
        FrameGeometry g;
        g.valid = true;
        g.width = f.Width;
        g.height = f.Height;
        g.profile = f.Res.LayoutProfile;
        g.sequence = f.Sequence;
        g.sides = f.Res.Geometry;
        std::lock_guard<std::mutex> lock(g_cfgMu);
        g_lastGeometry = std::move(g);
    }
    const size_t need = encodedSize(f);
    if (need <= cap) return encodeFrame(f, out, cap);
    if (need > 0x7fffffff) return -static_cast<jint>(0x7fffffff);
    t_pendingResult.resize(need);
    if (encodeFrame(f, t_pendingResult.data(), need) != static_cast<jint>(need)) t_pendingResult.clear();
    return -static_cast<jint>(need);
}

bool directBuffer(JNIEnv* env, jobject buf, uint8_t*& ptr, size_t& cap) {
    if (buf == nullptr) return false;
    ptr = static_cast<uint8_t*>(env->GetDirectBufferAddress(buf));
    jlong c = env->GetDirectBufferCapacity(buf);
    if (ptr == nullptr || c <= 0) return false;
    cap = static_cast<size_t>(c);
    return true;
}

// ---------------------------------------------------------------------------
// JNI 方法（com.narutotimer.NativeCore，@JvmStatic external）
// ---------------------------------------------------------------------------

jboolean nativeInit(JNIEnv*, jclass) {
    try {
        std::lock_guard<std::mutex> lock(g_analysisMu);
        g_pipeline.reset();
        {
            std::lock_guard<std::mutex> cfg(g_cfgMu);
            g_lastGeometry = FrameGeometry{};
            g_lastError.clear();
        }
        nt::AssetStore::Instance().Clear();
        return JNI_TRUE;
    } catch (const std::exception& e) {
        setError(std::string("nativeInit: ") + e.what());
    } catch (...) {
        setError("nativeInit: unknown exception");
    }
    return JNI_FALSE;
}

jboolean nativeAddImage(JNIEnv* env, jclass, jstring jpath, jint w, jint h, jobject rgba, jint rowStride) {
    try {
        uint8_t* ptr = nullptr;
        size_t cap = 0;
        if (w < 0 || h < 0 || !directBuffer(env, rgba, ptr, cap)) return JNI_FALSE;
        size_t stride = rowStride > 0 ? static_cast<size_t>(rowStride) : static_cast<size_t>(w) * 4;
        if (stride < static_cast<size_t>(w) * 4) return JNI_FALSE;
        if (h > 0 && stride * static_cast<size_t>(h - 1) + static_cast<size_t>(w) * 4 > cap) return JNI_FALSE;
        nt::AssetStore::Instance().AddImage(jstr(env, jpath), w, h, ptr, stride);
        return JNI_TRUE;
    } catch (const std::exception& e) {
        setError(std::string("nativeAddImage: ") + e.what());
    } catch (...) {
        setError("nativeAddImage: unknown exception");
    }
    return JNI_FALSE;
}

jboolean nativeAddText(JNIEnv* env, jclass, jstring jpath, jstring jtext) {
    try {
        nt::AssetStore::Instance().AddText(jstr(env, jpath), jstr(env, jtext));
        return JNI_TRUE;
    } catch (const std::exception& e) {
        setError(std::string("nativeAddText: ") + e.what());
    } catch (...) {
        setError("nativeAddText: unknown exception");
    }
    return JNI_FALSE;
}

// 引擎配置：当前账号名与区域校准（快照）。调用方不持有 g_cfgMu。
nt::config::Config currentConfig() {
    nt::config::Config cfg = nt::config::Default();
    std::lock_guard<std::mutex> lock(g_cfgMu);
    cfg.UI.PlayerNames = g_playerNames;
    cfg.Layout.Calibration = g_calibration;
    return cfg;
}

// Building an engine applies cfg's account names (identity::Guesser calls
// SetMineNames); names set while it was being built win. Caller holds
// g_analysisMu, not g_cfgMu.
void keepNewestNames(const nt::config::Config& built) {
    std::lock_guard<std::mutex> lock(g_cfgMu);
    if (g_playerNames != built.UI.PlayerNames) nt::identity::SetMineNames(g_playerNames);
}

// Logs calibration boxes the engine does not use (docs/区域校准.md 框的大小限制).
void logUnusedCalibration(const nt::config::RegionCalibration& c) {
    const std::optional<nt::config::NormalizedRect>* rects[4] = {&c.LeftBeads, &c.RightBeads, &c.LeftAvatar,
                                                                 &c.RightAvatar};
    static const char* const names[4] = {"leftBeads", "rightBeads", "leftAvatar", "rightAvatar"};
    for (int i = 0; i < 4; ++i) {
        const auto& r = *rects[i];
        if (!r) continue;
        if (!(r->X >= 0 && r->Y >= 0 && r->Width > 0 && r->Height > 0 && r->X + r->Width <= 1 && r->Y + r->Height <= 1)) {
            NT_LOGI("calibration %s outside the frame: automatic", names[i]);
            continue;
        }
        switch (nt::config::CalibrationRectIssue(i >= 2, *r)) {
            case nt::config::CalibrationIssue::None: break;
            case nt::config::CalibrationIssue::TooLarge: NT_LOGI("calibration %s too large: automatic", names[i]); break;
            case nt::config::CalibrationIssue::TooSmall: NT_LOGI("calibration %s too small: automatic", names[i]); break;
            case nt::config::CalibrationIssue::BadShape: NT_LOGI("calibration %s bad shape: automatic", names[i]); break;
        }
    }
}

jboolean nativeFinishAssets(JNIEnv*, jclass) {
    try {
        std::lock_guard<std::mutex> lock(g_analysisMu);
        const nt::config::Config cfg = currentConfig();
        nt::identity::SetMineNames(cfg.UI.PlayerNames);
        auto p = std::make_unique<nt::frame::Pipeline>(cfg);
        keepNewestNames(cfg);
        if (!p->Ok()) {
            setError("engine build failed: " + p->Error());
            g_pipeline.reset();
            return JNI_FALSE;
        }
        g_pipeline = std::move(p);
        {
            std::lock_guard<std::mutex> c(g_cfgMu);
            g_lastGeometry = FrameGeometry{};
            g_lastError.clear();
        }
        NT_LOGI("pipeline ready");
        return JNI_TRUE;
    } catch (const std::exception& e) {
        setError(std::string("nativeFinishAssets: ") + e.what());
    } catch (...) {
        setError("nativeFinishAssets: unknown exception");
    }
    return JNI_FALSE;
}

jstring nativeLastError(JNIEnv* env, jclass) {
    try {
        std::string e;
        {
            std::lock_guard<std::mutex> lock(g_cfgMu);
            e = g_lastError;
        }
        return newJString(env, e);
    } catch (...) {
        return nullptr;
    }
}

void nativeSetPlayerNames(JNIEnv* env, jclass, jobjectArray names) {
    try {
        std::vector<std::string> out;
        if (names != nullptr) {
            jsize n = env->GetArrayLength(names);
            for (jsize i = 0; i < n; ++i) {
                auto s = static_cast<jstring>(env->GetObjectArrayElement(names, i));
                out.push_back(jstr(env, s));
                env->DeleteLocalRef(s);
            }
        }
        // Only g_cfgMu (never held across a frame): the UI thread does not wait for an
        // analysis (audit NC-08). identity.SetMineNames has its own lock; the Guesser reloads
        // the book when it sees the new revision on the next frame (与 Go 设置页即时生效一致)。
        std::lock_guard<std::mutex> lock(g_cfgMu);
        g_playerNames = out;
        nt::identity::SetMineNames(out);
    } catch (const std::exception& e) {
        setError(std::string("nativeSetPlayerNames: ") + e.what());
    } catch (...) {
        setError("nativeSetPlayerNames: unknown exception");
    }
}

// 区域校准：profile "camp"/"duel"，rects = 16 个比例（左豆、右豆、左头像、右头像各 x,y,w,h），
// 任一分量为 NaN 的框 = 自动；超出画面或大小限制（docs/区域校准.md 框的大小限制）的框引擎不用，
// 同样按自动。引擎已建好时立即重建并清空去重缓存/交付状态（Pipeline::Rebuild，同一画面下一帧
// 就按新框重新识别；要等正在分析的一帧结束，Kotlin 在 nt-calibration 线程上调用）；否则留给
// nativeFinishAssets。重建失败保留旧引擎。
jboolean nativeSetCalibration(JNIEnv* env, jclass, jstring jprofile, jfloatArray jrects) {
    try {
        if (jrects == nullptr || env->GetArrayLength(jrects) != 16) {
            setError("nativeSetCalibration: rects must hold 16 floats");
            return JNI_FALSE;
        }
        jfloat v[16];
        env->GetFloatArrayRegion(jrects, 0, 16, v);
        nt::config::RegionCalibration c;
        c.Profile = jstr(env, jprofile);
        std::optional<nt::config::NormalizedRect>* rects[4] = {&c.LeftBeads, &c.RightBeads, &c.LeftAvatar,
                                                               &c.RightAvatar};
        bool any = false;
        for (int i = 0; i < 4; ++i) {
            const jfloat* r = v + 4 * i;
            if (std::isnan(r[0]) || std::isnan(r[1]) || std::isnan(r[2]) || std::isnan(r[3])) continue;
            *rects[i] = nt::config::NormalizedRect{r[0], r[1], r[2], r[3]};
            any = true;
        }
        if (any) logUnusedCalibration(c);
        std::lock_guard<std::mutex> lock(g_analysisMu);
        {
            std::lock_guard<std::mutex> cfg(g_cfgMu);
            g_calibration = any ? std::optional<nt::config::RegionCalibration>(c) : std::nullopt;
        }
        if (!g_pipeline || !g_pipeline->Ok()) return JNI_TRUE;
        const nt::config::Config cfg = currentConfig();
        std::string err;
        const bool ok = g_pipeline->Rebuild(cfg, &err);
        keepNewestNames(cfg);
        if (!ok) {
            setError("calibration rebuild failed: " + err);
            return JNI_FALSE;
        }
        clearGeometry();  // 旧校准下的几何
        NT_LOGI("pipeline rebuilt for region calibration");
        return JNI_TRUE;
    } catch (const std::exception& e) {
        setError(std::string("nativeSetCalibration: ") + e.what());
    } catch (...) {
        setError("nativeSetCalibration: unknown exception");
    }
    return JNI_FALSE;
}

jint nativeAnalyzeHardwareBuffer(JNIEnv* env, jclass, jobject hardwareBuffer, jlong capturedAtNanos, jint method,
                                 jobject outBuf) {
    try {
        uint8_t* out = nullptr;
        size_t cap = 0;
        if (!directBuffer(env, outBuf, out, cap) || hardwareBuffer == nullptr) {
            setError("nativeAnalyzeHardwareBuffer: bad arguments");
            return kNotReady;
        }
        AHardwareBuffer* hb = AHardwareBuffer_fromHardwareBuffer(env, hardwareBuffer);
        if (hb == nullptr) {
            setError("AHardwareBuffer_fromHardwareBuffer failed");
            return kNotReady;
        }
        AHardwareBuffer_acquire(hb);
        AHardwareBuffer_Desc desc{};
        AHardwareBuffer_describe(hb, &desc);
        if (desc.format != AHARDWAREBUFFER_FORMAT_R8G8B8A8_UNORM && desc.format != AHARDWAREBUFFER_FORMAT_R8G8B8X8_UNORM) {
            AHardwareBuffer_release(hb);
            setError("unsupported HardwareBuffer format " + std::to_string(desc.format));
            return kNotReady;
        }
        // The CPU mapping is stride·height pixels: a descriptor that does not describe one
        // plain image of width ≤ stride would make the core read past it (audit NC-03). Kotlin
        // then falls back to the bitmap path, as for any other HardwareBuffer failure.
        if (desc.width == 0 || desc.height == 0 || desc.layers != 1 || desc.stride < desc.width ||
            desc.stride > static_cast<uint32_t>(INT_MAX / 4) || desc.height > static_cast<uint32_t>(INT_MAX)) {
            AHardwareBuffer_release(hb);
            setError("bad HardwareBuffer geometry " + std::to_string(desc.width) + "x" + std::to_string(desc.height) +
                     " stride " + std::to_string(desc.stride) + " layers " + std::to_string(desc.layers));
            return kNotReady;
        }
        void* addr = nullptr;
        int rc = AHardwareBuffer_lock(hb, AHARDWAREBUFFER_USAGE_CPU_READ_OFTEN, -1, nullptr, &addr);
        if (rc != 0 || addr == nullptr) {
            if (rc == 0) AHardwareBuffer_unlock(hb, nullptr);  // locked, only without an address
            AHardwareBuffer_release(hb);
            setError(rc != 0 ? "AHardwareBuffer_lock failed: " + std::to_string(rc)
                             : std::string("AHardwareBuffer_lock returned no address"));
            return kNotReady;
        }
        jint result = kNotReady;
        try {
            // desc.stride 以像素计。像素只读使用（见 nt/image.h 约定）。
            nt::RGBA view(static_cast<uint8_t*>(addr), static_cast<int>(desc.stride * 4),
                          nt::MakeRect(0, 0, static_cast<int>(desc.width), static_cast<int>(desc.height)));
            std::lock_guard<std::mutex> lock(g_analysisMu);
            result = analyzeLocked(view, capturedAtNanos, method, out, cap);
        } catch (...) {
            AHardwareBuffer_unlock(hb, nullptr);
            AHardwareBuffer_release(hb);
            throw;
        }
        AHardwareBuffer_unlock(hb, nullptr);
        AHardwareBuffer_release(hb);
        return result;
    } catch (const std::exception& e) {
        setError(std::string("nativeAnalyzeHardwareBuffer: ") + e.what());
    } catch (...) {
        setError("nativeAnalyzeHardwareBuffer: unknown exception");
    }
    return kNotReady;
}

jint nativeAnalyzeBuffer(JNIEnv* env, jclass, jobject pixels, jint w, jint h, jint rowStride, jlong capturedAtNanos,
                         jint method, jobject outBuf) {
    try {
        uint8_t* in = nullptr;
        size_t inCap = 0;
        uint8_t* out = nullptr;
        size_t cap = 0;
        if (w <= 0 || h <= 0 || !directBuffer(env, pixels, in, inCap) || !directBuffer(env, outBuf, out, cap)) {
            setError("nativeAnalyzeBuffer: bad arguments");
            return kNotReady;
        }
        size_t stride = rowStride > 0 ? static_cast<size_t>(rowStride) : static_cast<size_t>(w) * 4;
        if (stride < static_cast<size_t>(w) * 4 || stride * static_cast<size_t>(h - 1) + static_cast<size_t>(w) * 4 > inCap) {
            setError("nativeAnalyzeBuffer: buffer smaller than h*rowStride");
            return kNotReady;
        }
        if (stride > static_cast<size_t>(INT_MAX)) {
            setError("nativeAnalyzeBuffer: rowStride too large");
            return kNotReady;
        }
        nt::RGBA view(in, static_cast<int>(stride), nt::MakeRect(0, 0, w, h));
        std::lock_guard<std::mutex> lock(g_analysisMu);
        return analyzeLocked(view, capturedAtNanos, method, out, cap);
    } catch (const std::exception& e) {
        setError(std::string("nativeAnalyzeBuffer: ") + e.what());
    } catch (...) {
        setError("nativeAnalyzeBuffer: unknown exception");
    }
    return kNotReady;
}

// 结果放不下时（nativeAnalyze* 返回 -need）取同一次分析的结果，不重新分析：返回写入字节数；
// out 仍放不下 = -need（结果留着）；这个线程没有待取结果 = 0。
jint nativeCopyLastResult(JNIEnv* env, jclass, jobject outBuf) {
    try {
        uint8_t* out = nullptr;
        size_t cap = 0;
        if (!directBuffer(env, outBuf, out, cap)) {
            setError("nativeCopyLastResult: bad arguments");
            return kNotReady;
        }
        if (t_pendingResult.empty()) {
            setError("nativeCopyLastResult: no pending result on this thread");
            return kNotReady;
        }
        const size_t need = t_pendingResult.size();
        if (need > cap) return -static_cast<jint>(need);
        std::memcpy(out, t_pendingResult.data(), need);
        t_pendingResult.clear();
        return static_cast<jint>(need);
    } catch (const std::exception& e) {
        setError(std::string("nativeCopyLastResult: ") + e.what());
    } catch (...) {
        setError("nativeCopyLastResult: unknown exception");
    }
    return kNotReady;
}

// 取走待保存的对面账号裁剪：返回 [w, h, argb...]（ARGB_8888 int，可直接 Bitmap.createBitmap），无则 null。
// identity 自己的锁（saveMu）保护待取裁剪，不必等正在分析的一帧。
jintArray nativeTakeOppCrop(JNIEnv* env, jclass) {
    try {
        nt::RGBA crop;
        if (!nt::identity::TakePendingOppCrop(crop)) return nullptr;
        const nt::Rect& b = crop.Bounds();
        int w = b.Dx(), h = b.Dy();
        if (w <= 0 || h <= 0) return nullptr;
        std::vector<jint> data(2 + static_cast<size_t>(w) * h);
        data[0] = w;
        data[1] = h;
        for (int y = 0; y < h; ++y) {
            const uint8_t* row = crop.RowPtr(b.Min.Y + y);
            for (int x = 0; x < w; ++x) {
                const uint8_t* p = row + 4 * x;
                uint32_t argb = (static_cast<uint32_t>(p[3]) << 24) | (static_cast<uint32_t>(p[0]) << 16) |
                                (static_cast<uint32_t>(p[1]) << 8) | p[2];
                data[2 + static_cast<size_t>(y) * w + x] = static_cast<jint>(argb);
            }
        }
        jintArray arr = env->NewIntArray(static_cast<jsize>(data.size()));
        if (arr != nullptr) env->SetIntArrayRegion(arr, 0, static_cast<jsize>(data.size()), data.data());
        return arr;
    } catch (const std::exception& e) {
        setError(std::string("nativeTakeOppCrop: ") + e.what());
    } catch (...) {
        setError("nativeTakeOppCrop: unknown exception");
    }
    return nullptr;
}

void nativeResetDelivery(JNIEnv*, jclass) {
    try {
        std::lock_guard<std::mutex> lock(g_analysisMu);
        if (g_pipeline) g_pipeline->ResetDelivery();
    } catch (...) {
    }
}

// Waits for a running frame, then frees the engine. Safe at any time (also while
// other threads call in); the process-global state itself is never destroyed.
void nativeRelease(JNIEnv*, jclass) {
    try {
        std::lock_guard<std::mutex> lock(g_analysisMu);
        g_pipeline.reset();
        clearGeometry();
    } catch (...) {
    }
}

// 最近一次分析的帧的几何（成功分析或去重复用的帧；没有则 null）。扁平布局（float，改动必须同步
// NativeCore.kt 与 ARCHITECTURE.md §4）：
//   [0] width [1] height（像素）[2] profile（0 无，1 camp，2 duel）
//   [3] Sequence & 0xFFFFFF [4] Sequence >> 24（= 该帧 AnalysisResult.sequence）
//   之后左、右各一段：n，n 组豆心 x,y；nameRoi x,y,w,h；avatarRoi x,y,w,h（像素，不裁剪，空 = 0）；
//   beadBox x,y,w,h；avatarBox x,y,w,h（整帧比例，w = 0 = 没有）。
jfloatArray nativeLastGeometry(JNIEnv* env, jclass) {
    try {
        FrameGeometry g;
        {
            std::lock_guard<std::mutex> lock(g_cfgMu);
            if (!g_lastGeometry.valid) return nullptr;
            g = g_lastGeometry;
        }
        std::vector<jfloat> v = {static_cast<jfloat>(g.width), static_cast<jfloat>(g.height),
                                 static_cast<jfloat>(g.profile == "camp" ? 1 : g.profile == "duel" ? 2 : 0),
                                 static_cast<jfloat>(g.sequence & 0xFFFFFF), static_cast<jfloat>(g.sequence >> 24)};
        for (const nt::engine::SideGeometry& s : g.sides) {
            v.push_back(static_cast<jfloat>(s.Beads.size()));
            for (const nt::Point& p : s.Beads) {
                v.push_back(static_cast<jfloat>(p.X));
                v.push_back(static_cast<jfloat>(p.Y));
            }
            for (const nt::Rect& r : {s.NameROI, s.AvatarROI}) {
                for (int c : {r.Min.X, r.Min.Y, r.Dx(), r.Dy()}) v.push_back(static_cast<jfloat>(c));
            }
            for (const nt::config::NormalizedRect& r : {s.BeadBox, s.AvatarBox}) {
                for (double c : {r.X, r.Y, r.Width, r.Height}) v.push_back(static_cast<jfloat>(c));
            }
        }
        jfloatArray arr = env->NewFloatArray(static_cast<jsize>(v.size()));
        if (arr != nullptr) env->SetFloatArrayRegion(arr, 0, static_cast<jsize>(v.size()), v.data());
        return arr;
    } catch (const std::exception& e) {
        setError(std::string("nativeLastGeometry: ") + e.what());
    } catch (...) {
        setError("nativeLastGeometry: unknown exception");
    }
    return nullptr;
}

jint nativeResultHeaderBytes(JNIEnv*, jclass) { return kHeaderBytes; }

const JNINativeMethod kMethods[] = {
    {const_cast<char*>("nativeInit"), const_cast<char*>("()Z"), reinterpret_cast<void*>(nativeInit)},
    {const_cast<char*>("nativeAddImage"), const_cast<char*>("(Ljava/lang/String;IILjava/nio/ByteBuffer;I)Z"),
     reinterpret_cast<void*>(nativeAddImage)},
    {const_cast<char*>("nativeAddText"), const_cast<char*>("(Ljava/lang/String;Ljava/lang/String;)Z"),
     reinterpret_cast<void*>(nativeAddText)},
    {const_cast<char*>("nativeFinishAssets"), const_cast<char*>("()Z"), reinterpret_cast<void*>(nativeFinishAssets)},
    {const_cast<char*>("nativeLastError"), const_cast<char*>("()Ljava/lang/String;"),
     reinterpret_cast<void*>(nativeLastError)},
    {const_cast<char*>("nativeSetPlayerNames"), const_cast<char*>("([Ljava/lang/String;)V"),
     reinterpret_cast<void*>(nativeSetPlayerNames)},
    {const_cast<char*>("nativeSetCalibration"), const_cast<char*>("(Ljava/lang/String;[F)Z"),
     reinterpret_cast<void*>(nativeSetCalibration)},
    {const_cast<char*>("nativeAnalyzeHardwareBuffer"),
     const_cast<char*>("(Landroid/hardware/HardwareBuffer;JILjava/nio/ByteBuffer;)I"),
     reinterpret_cast<void*>(nativeAnalyzeHardwareBuffer)},
    {const_cast<char*>("nativeAnalyzeBuffer"), const_cast<char*>("(Ljava/nio/ByteBuffer;IIIJILjava/nio/ByteBuffer;)I"),
     reinterpret_cast<void*>(nativeAnalyzeBuffer)},
    {const_cast<char*>("nativeCopyLastResult"), const_cast<char*>("(Ljava/nio/ByteBuffer;)I"),
     reinterpret_cast<void*>(nativeCopyLastResult)},
    {const_cast<char*>("nativeTakeOppCrop"), const_cast<char*>("()[I"), reinterpret_cast<void*>(nativeTakeOppCrop)},
    {const_cast<char*>("nativeResetDelivery"), const_cast<char*>("()V"), reinterpret_cast<void*>(nativeResetDelivery)},
    {const_cast<char*>("nativeRelease"), const_cast<char*>("()V"), reinterpret_cast<void*>(nativeRelease)},
    {const_cast<char*>("nativeLastGeometry"), const_cast<char*>("()[F"), reinterpret_cast<void*>(nativeLastGeometry)},
    {const_cast<char*>("nativeResultHeaderBytes"), const_cast<char*>("()I"),
     reinterpret_cast<void*>(nativeResultHeaderBytes)},
};

}  // namespace

extern "C" JNIEXPORT jint JNI_OnLoad(JavaVM* vm, void*) {
    JNIEnv* env = nullptr;
    if (vm->GetEnv(reinterpret_cast<void**>(&env), JNI_VERSION_1_6) != JNI_OK) return JNI_ERR;
    jclass cls = env->FindClass("com/narutotimer/NativeCore");
    if (cls == nullptr) return JNI_ERR;
    if (env->RegisterNatives(cls, kMethods, sizeof(kMethods) / sizeof(kMethods[0])) != JNI_OK) return JNI_ERR;
    env->DeleteLocalRef(cls);
    return JNI_VERSION_1_6;
}
