// Host tests of the native-core audit fixes (NC-01 calibration limits, NC-02 buffer release,
// NC-04/NC-05 thread pool, NC-09 opaque hashing). Run tests/native_fixes_test.sh, also under
// ASan/UBSan (EXTRA_CXXFLAGS/EXTRA_LDFLAGS). The JNI-level fixes are in jni_host_test.cpp.
//   native_fixes_test               every in-process check
//   native_fixes_test pool-threads  NC-05 with pthread_create failing for the FAIL_NTH-th thread
#include <dlfcn.h>
#include <malloc.h>
#include <pthread.h>

#include <atomic>
#include <cerrno>
#include <chrono>
#include <cmath>
#include <cstdint>
#include <cstdio>
#include <cstdlib>
#include <cstring>
#include <memory>
#include <new>
#include <string>
#include <thread>
#include <vector>

#include "nt/config.h"
#include "nt/engine.h"
#include "nt/frame.h"
#include "nt/hash.h"
#include "nt/match.h"
#include "nt/parallel.h"

// ---- allocation hooks: live bytes of large blocks (NC-02), injected bad_alloc (NC-04) ----
static std::atomic<long long> g_bigLive{0};
static std::atomic<size_t> g_failAtLeast{0};  // 0 = off; else the next allocation >= this throws
static std::atomic<int> g_failures{0};
constexpr size_t kBig = 64 << 10;

static void* tracked_new(size_t n) {
    const size_t fail = g_failAtLeast.load(std::memory_order_relaxed);
    if (fail != 0 && n >= fail && g_failAtLeast.exchange(0) != 0) {
        g_failures++;
        throw std::bad_alloc();
    }
    void* p = std::malloc(n ? n : 1);
    if (p == nullptr) throw std::bad_alloc();
    if (n >= kBig) g_bigLive += static_cast<long long>(malloc_usable_size(p));
    return p;
}
static void tracked_delete(void* p) noexcept {
    if (p == nullptr) return;
    const size_t n = malloc_usable_size(p);
    if (n >= kBig) g_bigLive -= static_cast<long long>(n);
    std::free(p);
}
void* operator new(size_t n) { return tracked_new(n); }
void operator delete(void* p) noexcept { tracked_delete(p); }
void operator delete(void* p, size_t) noexcept { tracked_delete(p); }
// Every other (non-aligned) form too: a mix with the runtime's own is an alloc-dealloc mismatch.
void* operator new[](size_t n) { return tracked_new(n); }
void* operator new(size_t n, const std::nothrow_t&) noexcept {
    try {
        return tracked_new(n);
    } catch (...) {
        return nullptr;
    }
}
void* operator new[](size_t n, const std::nothrow_t& t) noexcept { return operator new(n, t); }
void operator delete[](void* p) noexcept { tracked_delete(p); }
void operator delete[](void* p, size_t) noexcept { tracked_delete(p); }
void operator delete(void* p, const std::nothrow_t&) noexcept { tracked_delete(p); }
void operator delete[](void* p, const std::nothrow_t&) noexcept { tracked_delete(p); }

// ---- NC-05: the FAIL_NTH-th pthread_create fails with EAGAIN (pool-threads mode only) ----
static std::atomic<int> g_threadCalls{0};
static int g_failNth = 0;
extern "C" int pthread_create(pthread_t* t, const pthread_attr_t* a, void* (*f)(void*), void* arg) {
    using fn = int (*)(pthread_t*, const pthread_attr_t*, void* (*)(void*), void*);
    static fn real = reinterpret_cast<fn>(dlsym(RTLD_NEXT, "pthread_create"));
    if (g_failNth > 0 && ++g_threadCalls == g_failNth) return EAGAIN;
    return real(t, a, f, arg);
}

namespace {

int checks = 0;
void check(bool ok, const std::string& message) {
    ++checks;
    if (!ok) {
        std::fprintf(stderr, "FAIL: %s\n", message.c_str());
        std::exit(1);
    }
}

// ---------------------------------------------------------------------------
// NC-01: calibration box size limits, identical to Go internal/config (and Kotlin)
// ---------------------------------------------------------------------------

// splitmix64 + FNV-1a digest of the decisions: internal/config/calibration_limits_test.go
// (calibrationDecisionDigest) and android/tools/limitscheck draw the same boxes.
struct SplitMix {
    uint64_t s;
    uint64_t next() {
        s += 0x9E3779B97F4A7C15ull;
        uint64_t z = s;
        z = (z ^ (z >> 30)) * 0xBF58476D1CE4E5B9ull;
        z = (z ^ (z >> 27)) * 0x94D049BB133111EBull;
        return z ^ (z >> 31);
    }
    double unit() { return static_cast<double>(next() >> 11) * 0x1p-53; }
};

int decision(bool avatar, const nt::config::NormalizedRect& r) {
    if (!(r.X >= 0 && r.Y >= 0 && r.Width > 0 && r.Height > 0 && r.X + r.Width <= 1 && r.Y + r.Height <= 1)) return 1;
    switch (nt::config::CalibrationRectIssue(avatar, r)) {
        case nt::config::CalibrationIssue::None: return 0;
        case nt::config::CalibrationIssue::TooLarge: return 2;
        case nt::config::CalibrationIssue::TooSmall: return 3;
        case nt::config::CalibrationIssue::BadShape: return 4;
    }
    return -1;
}

uint64_t digest(int n, bool float32Inputs, int counts[5]) {
    SplitMix g{20261021};
    uint64_t d = 14695981039346656037ull;
    for (int i = 0; i < n; ++i) {
        const bool avatar = i % 2 == 1;
        const double wn = avatar ? 68.5 / 960 : 0.0625, hn = avatar ? 68.5 / 540 : 1.0 / 36;
        const double k = 0.2 + 2.2 * g.unit();
        double w = wn * k * (0.5 + g.unit());
        double h = hn * k * (0.5 + g.unit());
        switch (g.next() % 8) {
            case 0: w = wn * 2; break;
            case 1: h = hn * 0.5; break;
            case 2: w = wn * 2, h = hn * 0.5; break;
        }
        double x = (1 - w) * g.unit();
        double y = (1 - h) * g.unit();
        switch (g.next() % 16) {
            case 0: x = 1 - w; break;
            case 1: y = 1 - h + 1e-9; break;
        }
        nt::config::NormalizedRect r{x, y, w, h};
        if (float32Inputs) {
            r = {static_cast<double>(static_cast<float>(x)), static_cast<double>(static_cast<float>(y)),
                 static_cast<double>(static_cast<float>(w)), static_cast<double>(static_cast<float>(h))};
        }
        const int c = decision(avatar, r);
        counts[c]++;
        d = (d ^ static_cast<uint64_t>(c)) * 1099511628211ull;
    }
    return d;
}

void calibrationLimits() {
    // Pinned in Go (calibrationVectorsDigest / calibrationVectorsDigestF).
    int counts[5] = {};
    check(digest(200000, false, counts) == 0xe9e1f0fa7bdcdb2dull, "calibration decisions differ from Go (float64)");
    int countsF[5] = {};
    check(digest(200000, true, countsF) == 0x5c07b936a6c3ad61ull, "calibration decisions differ from Go (float32)");
    std::printf("NC-01 calibration limits: 2x200000 decisions match Go (usable %d, outside %d, large %d, small %d, shape %d)\n",
                counts[0], counts[1], counts[2], counts[3], counts[4]);

    // Frame-size independent: the automatic box is k = 1 on every frame shape.
    const double sizes[][2] = {{1280, 720}, {2400, 1080}, {3840, 1080}, {1024, 768}, {2388, 1668}, {1600, 1600}, {2316, 904}};
    for (const auto& sz : sizes) {
        const double W = sz[0], H = sz[1];
        const double s = std::min(W / 1600, H / 900), a = std::min(W / 960, H / 540);
        const nt::config::NormalizedRect boxes[2] = {{0.1, 0.1, 100 * s / W, 25 * s / H}, {0.01, 0.01, 68.5 * a / W, 68.5 * a / H}};
        for (int avatar = 0; avatar < 2; ++avatar) {
            double u = 0, v = 0;
            nt::config::CalibrationBoxUnits(avatar != 0, boxes[avatar], u, v);
            check(std::fabs(std::max(u, v) - 1) < 1e-12, "automatic box is not k = 1");
            for (double f : {0.51, 1.0, 1.99, 2.01, 0.49}) {
                nt::config::NormalizedRect r = boxes[avatar];
                r.Width *= f;
                r.Height *= f;
                const bool usable = nt::config::CalibrationRectIssue(avatar != 0, r) == nt::config::CalibrationIssue::None;
                check(usable == (f > 0.5 && f < 2), "scaled automatic box decided wrongly");
            }
        }
    }

    // Oversized boxes stay automatic in the engine: same positions as without calibration.
    nt::config::LayoutConfig cfg = nt::config::Default().Layout;
    nt::engine::ConfiguredLayout plain(nt::detect::ModeAuto, cfg);
    nt::config::RegionCalibration c;
    c.Profile = "duel";
    c.LeftBeads = nt::config::NormalizedRect{0, 0, 1, 1};
    c.RightBeads = nt::config::NormalizedRect{0.8, 0.1, 0.004, 0.003};
    c.LeftAvatar = nt::config::NormalizedRect{0, 0, 0.25, 0.45};
    c.RightAvatar = nt::config::NormalizedRect{0.9, 0, 0.01, 0.12};
    cfg.Calibration = c;
    nt::engine::ConfiguredLayout rejected(nt::detect::ModeAuto, cfg);
    for (int w : {1280, 1920, 2400}) {
        nt::RGBA img = nt::RGBA::New(nt::MakeRect(0, 0, w, 1080));
        for (const char* profile : {"camp", "duel"}) {
            auto [ca, ok] = plain.ContentArea(&img);
            (void)ok;
            auto want = plain.PositionsInImage(profile, &img, ca);
            auto got = rejected.PositionsInImage(profile, &img, ca);
            check(want.size() == got.size() && !want.empty(), "position count");
            for (size_t i = 0; i < want.size(); ++i) {
                check(got[i].X == want[i].X && got[i].Y == want[i].Y && got[i].Cal == nullptr,
                      "an oversized calibration box changed the automatic layout");
            }
        }
    }
    // A usable box still calibrates.
    c.LeftBeads = nt::config::NormalizedRect{0.081, 0.104, 0.052, 0.022};
    cfg.Calibration = c;
    nt::engine::ConfiguredLayout used(nt::detect::ModeAuto, cfg);
    nt::RGBA img = nt::RGBA::New(nt::MakeRect(0, 0, 2400, 1080));
    auto [ca, ok] = used.ContentArea(&img);
    (void)ok;
    auto pos = used.PositionsInImage("duel", &img, ca);
    check(!pos.empty() && pos[0].Cal != nullptr && pos[0].Cal->Beads(), "a usable bead box is not used");
}

// ---------------------------------------------------------------------------
// NC-09: the 4th byte (RGBX_8888's undefined X) does not feed the fingerprints
// ---------------------------------------------------------------------------

struct CountingEngine : nt::engine::Engine {
    int calls = 0;
    nt::engine::Result AnalyzeAt(const nt::RGBA* img, nt::TimeNs) override {
        if (img != nullptr) ++calls;
        nt::engine::Result r;
        r.Name = "count";
        return r;
    }
};

void opaqueHashing() {
    SplitMix g{9};
    std::vector<uint8_t> row, opaque;
    for (size_t pixels = 0; pixels <= 300; ++pixels) {
        for (int rep = 0; rep < 8; ++rep) {
            row.resize(pixels * 4);
            for (auto& b : row) b = static_cast<uint8_t>(g.next());
            opaque = row;
            for (size_t i = 3; i < opaque.size(); i += 4) opaque[i] = 0xFF;
            const uint64_t seed = g.next();
            check(nt::HashOpaquePixels(seed, row.data(), pixels) == nt::HashBytes(seed, opaque.data(), opaque.size()),
                  "HashOpaquePixels is not HashBytes of the opaque row (" + std::to_string(pixels) + " px)");
        }
    }
    // Images: alpha/X bytes never count, colour does; padding never did.
    const int w = 401, h = 260, stride = w * 4 + 12;
    std::vector<uint8_t> a(static_cast<size_t>(stride) * h), b;
    for (auto& v : a) v = static_cast<uint8_t>(g.next());
    b = a;
    for (int y = 0; y < h; ++y) {
        for (int x = 0; x < w; ++x) b[static_cast<size_t>(y) * stride + 4 * x + 3] ^= static_cast<uint8_t>(1 + g.next() % 255);
        for (int k = w * 4; k < stride; ++k) b[static_cast<size_t>(y) * stride + k] ^= 0x5A;
    }
    nt::RGBA ia(a.data(), stride, nt::MakeRect(0, 0, w, h)), ib(b.data(), stride, nt::MakeRect(0, 0, w, h));
    check(nt::frame::frameFingerprint(&ia, "m") == nt::frame::frameFingerprint(&ib, "m"), "X bytes change the frame fingerprint");
    check(nt::engine::pixelHash(&ia) == nt::engine::pixelHash(&ib), "X bytes change pixelHash");
    std::vector<uint8_t> c = a;
    c[static_cast<size_t>(130) * stride + 4 * 200 + 1] ^= 1;  // one green bit
    nt::RGBA ic(c.data(), stride, nt::MakeRect(0, 0, w, h));
    check(nt::frame::frameFingerprint(&ia, "m") != nt::frame::frameFingerprint(&ic, "m"), "a colour change kept the fingerprint");
    check(nt::engine::pixelHash(&ia) != nt::engine::pixelHash(&ic), "a colour change kept pixelHash");
    // The dedupe layer reuses the analysis of a frame that differs only in X bytes.
    CountingEngine eng;
    nt::frame::DedupedAnalysis dedupe;
    nt::frame::Frame f1 = dedupe.analyze(&ia, &eng, nt::detect::ModeAuto, 1, 1, "m");
    nt::frame::Frame f2 = dedupe.analyze(&ib, &eng, nt::detect::ModeAuto, 2, 2, "m");
    nt::frame::Frame f3 = dedupe.analyze(&ic, &eng, nt::detect::ModeAuto, 3, 3, "m");
    check(!f1.Reused && f2.Reused && !f3.Reused && eng.calls == 2, "dedupe: X-only change not reused / colour change reused");
    std::printf("NC-09 opaque hashing: %d row lengths, fingerprint/pixelHash/dedupe ignore the X byte\n", 301);
}

// ---------------------------------------------------------------------------
// NC-02: prefix and memo buffers do not stay at their peak
// ---------------------------------------------------------------------------

nt::Gray noiseGray(int w, int h, uint64_t seed) {
    nt::Gray g = nt::Gray::NewUninit(nt::MakeRect(0, 0, w, h));
    SplitMix r{seed};
    for (int y = 0; y < h; ++y)
        for (int x = 0; x < w; ++x) g.Pix[static_cast<size_t>(y) * g.Stride + x] = static_cast<uint8_t>(r.next());
    return g;
}

void bufferRelease() {
    using nt::match::grayPrefix;
    const nt::Gray big = noiseGray(1200, 900, 1), small = noiseGray(300, 200, 2), tiny = noiseGray(120, 80, 3);
    const size_t bigN = static_cast<size_t>(900) * 1201, smallN = static_cast<size_t>(200) * 301;
    grayPrefix g;
    nt::match::newGrayPrefix(g, big, 0, 0, 1200, 900);
    check(g.sum.capacity() >= bigN, "prefix did not grow");
    for (int i = 1; i < nt::match::kOversizedBuilds; ++i) {
        nt::match::newGrayPrefix(g, small, 0, 0, 300, 200);
        check(g.sum.capacity() >= bigN, "prefix shrank before kOversizedBuilds builds");
    }
    nt::match::newGrayPrefix(g, small, 0, 0, 300, 200);
    check(g.sum.capacity() < bigN / 4 && g.square.capacity() < bigN / 4, "oversized prefix kept after kOversizedBuilds builds");
    check(g.sum[smallN - 1] != 0 || small.Pix[0] == 0, "prefix content after shrinking");
    // Sizes that keep alternating are not churned.
    grayPrefix alt;
    for (int i = 0; i < 4 * nt::match::kOversizedBuilds; ++i) {
        nt::match::newGrayPrefix(alt, i % 3 == 0 ? big : small, 0, 0, i % 3 == 0 ? 1200 : 300, i % 3 == 0 ? 900 : 200);
        check(alt.sum.capacity() >= bigN, "alternating sizes shrank the prefix");
    }
    // Buffers within kKeepPrefixBytes are never cut back.
    grayPrefix keep;
    nt::match::newGrayPrefix(keep, small, 0, 0, 300, 200);
    const size_t kept = keep.sum.capacity();
    for (int i = 0; i < 3 * nt::match::kOversizedBuilds; ++i) nt::match::newGrayPrefix(keep, tiny, 0, 0, 120, 80);
    check(keep.sum.capacity() == kept, "a small prefix was released");

    // End to end through NCC (thread_local and the shared large-prefix pool): after a
    // huge scan, normal scans bring the large live blocks back down.
    nt::Gray tmpl = noiseGray(64, 64, 4);  // coarse grid: a scan costs ~64 ops per ROI pixel
    auto prepared = nt::match::PrepareNCC(&tmpl, nullptr);
    auto scan = [&](int w, int h) {
        nt::Gray img = noiseGray(w, h, static_cast<uint64_t>(w) * 7 + h);
        nt::RGBA bounds(nullptr, 0, img.Bounds());
        nt::match::Query q;
        q.Image = &bounds;
        q.Gray = &img;
        q.ROI = img.Bounds();
        q.Prepared = prepared.get();
        nt::match::Score s;
        check(nt::match::NCC{}.Match(q, s), "NCC match");
    };
    scan(700, 500);  // ~2.8 MiB prefix: from the pool
    for (int i = 0; i < 4; ++i) scan(700, 500);
    const long long steady = g_bigLive.load();
    scan(1800, 1300);  // ~18 MiB
    for (int i = 0; i < 2 * nt::match::kOversizedBuilds; ++i) scan(700, 500);
    const long long after = g_bigLive.load();
    std::printf("NC-02 buffer release: prefix cut back after %d oversized builds; large live blocks %.1f MiB -> %.1f MiB after a 1800x1300 scan\n",
                nt::match::kOversizedBuilds, steady / 1048576.0, after / 1048576.0);
    check(after <= steady + (static_cast<long long>(4) << 20), "a huge scan's prefix stayed resident");
}

// ---------------------------------------------------------------------------
// NC-04: bad_alloc while queueing never lets a helper run fn after ParallelFor returned
// ---------------------------------------------------------------------------

struct Call {
    std::atomic<int> ran{0};
    std::atomic<bool> returned{false};
    std::atomic<int> late{0};
};

void poolSubmitFailure() {
    nt::ParallelFor(4, [](int) {});  // the pool exists before injecting failures
    std::vector<std::unique_ptr<Call>> calls;  // kept alive: a late helper must not touch freed memory
    int escaped = 0;
    // Whether a queue node is allocated in a given call depends on timing: keep calling until
    // enough failures were injected (few per thousand calls under TSan).
    for (int k = 0; k < 200000 && (k < 3000 || g_failures.load() < 50); ++k) {
        calls.push_back(std::make_unique<Call>());
        Call* c = calls.back().get();
        // libstdc++ deque<shared_ptr<Job>> nodes are 512 bytes: the next node allocation in
        // Pool::submit (or anything at least as large) throws.
        g_failAtLeast = 512;
        try {
            nt::ParallelFor(64, [c](int) {
                if (c->returned.load()) c->late++;
                c->ran++;
            });
        } catch (const std::bad_alloc&) {
            ++escaped;
        }
        g_failAtLeast = 0;
        c->returned = true;
        check(c->ran.load() == 64, "an index did not run");
    }
    std::this_thread::sleep_for(std::chrono::milliseconds(50));
    nt::ParallelFor(8, [](int) {});
    int late = 0;
    for (auto& c : calls) late += c->late.load();
    std::printf("NC-04 pool submit: %d injected bad_allocs, %d escaped ParallelFor, %d late helper runs\n",
                g_failures.load(), escaped, late);
    check(g_failures.load() > 0, "no allocation failure was injected");
    check(escaped == 0 && late == 0, "bad_alloc in submit escaped or a helper ran after return");
}

// NC-05 (separate process): a failed worker thread leaves a smaller pool, not std::terminate.
int poolThreads() {
    const char* nth = std::getenv("FAIL_NTH");
    g_failNth = nth != nullptr ? std::atoi(nth) : 0;
    for (int round = 0; round < 3; ++round) {
        std::atomic<long> sum{0};
        nt::ParallelFor(64, [&](int i) { sum += i; });
        check(sum.load() == 64 * 63 / 2, "ParallelFor result with a failed worker");
        std::vector<int> nested(16, 0);
        nt::ParallelFor(4, [&](int i) { nt::ParallelFor(4, [&](int j) { nested[static_cast<size_t>(4 * i + j)] = 1; }); });
        for (int v : nested) check(v == 1, "nested ParallelFor with a failed worker");
    }
    std::printf("NC-05 pool threads: thread %d failed to start (%d created), ParallelFor ok\n", g_failNth, g_threadCalls.load());
    return 0;
}

}  // namespace

int main(int argc, char** argv) {
    if (argc > 1 && std::string(argv[1]) == "pool-threads") return poolThreads();
    calibrationLimits();
    opaqueHashing();
    bufferRelease();
    poolSubmitFailure();
    std::printf("native_fixes_test: %d checks passed\n", checks);
    return 0;
}
