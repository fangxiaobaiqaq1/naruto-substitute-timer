// The production jni.cpp on the host against a fake JNIEnv / JavaVM / AHardwareBuffer
// (tests/jnihost/android/*.h), with the repo's recognition assets registered as AssetLoader does.
// Run tests/jni_host_test.sh (also under ASan/UBSan). Native-core audit fixes covered:
//   NC-03 HardwareBuffer descriptors are checked, the buffer is unlocked on every path
//   NC-07 a result too large for the caller's buffer is copied, not analysed again
//   NC-08 names / last error / geometry never wait for a running frame
//   NC-09 RGBX X bytes do not make an unchanged frame new
//   NC-01 calibration boxes outside the size limits stay automatic through nativeSetCalibration
//   NC-02 (mode `memory`) huge frames do not pin their buffers once frames are normal again
//   NC-06 (mode `exitrace`) exit() while a frame is analysed is clean
//     jni_host_test <repo> [memory|exitrace]
#include <jni.h>
#include <malloc.h>

#include <atomic>
#include <chrono>
#include <map>
#include <mutex>
#include <thread>
#include <type_traits>

#include "android/hardware_buffer.h"
#include "android/hardware_buffer_jni.h"

// Asset registration and PNG-free helpers of the host tool (its allocator is not replaced).
#define main unused_hostcheck_main
#include "../../../../../tools/hostcheck.cpp"
#undef main

#include "nt/identity.h"
#include "nt/match.h"

// ---- live bytes of large heap blocks (memory mode) ----
static std::atomic<long long> g_bigLive{0};
static void* tracked_new(size_t n) {
    void* p = std::malloc(n ? n : 1);
    if (p == nullptr) throw std::bad_alloc();
    if (n >= (64u << 10)) g_bigLive += static_cast<long long>(malloc_usable_size(p));
    return p;
}
static void tracked_delete(void* p) noexcept {
    if (p == nullptr) return;
    const size_t n = malloc_usable_size(p);
    if (n >= (64u << 10)) g_bigLive -= static_cast<long long>(n);
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

extern "C" JNIEXPORT jint JNI_OnLoad(JavaVM* vm, void*);

namespace {

int checks = 0;
void check(bool ok, const std::string& message) {
    ++checks;
    if (!ok) {
        std::fprintf(stderr, "FAIL: %s\n", message.c_str());
        std::exit(1);
    }
}

[[noreturn]] void misuse(const char* what) {
    std::fprintf(stderr, "JNI MISUSE: %s\n", what);
    std::abort();
}

// ---------------------------------------------------------------------------
// Fake JNI objects. Never destroyed while the process lives (exitrace).
// ---------------------------------------------------------------------------
enum Kind { kString = 1, kDirect, kFloats, kObjects, kInts, kHB, kClass };
struct Obj {
    Kind kind;
    explicit Obj(Kind k) : kind(k) {}
    virtual ~Obj() = default;
};
struct Str : Obj {
    std::u16string s;
    Str() : Obj(kString) {}
};
struct Direct : Obj {
    void* addr = nullptr;
    jlong cap = 0;
    Direct() : Obj(kDirect) {}
};
struct Floats : Obj {
    std::vector<jfloat> v;
    Floats() : Obj(kFloats) {}
};
struct Ints : Obj {
    std::vector<jint> v;
    Ints() : Obj(kInts) {}
};
struct Objects : Obj {
    std::vector<jobject> v;
    Objects() : Obj(kObjects) {}
};
struct HB : Obj {
    AHardwareBuffer_Desc desc{};
    std::vector<uint8_t> mem;  // exactly stride·height·4 bytes: any over-read is an ASan error
    int lockRc = 0;
    bool nullAddr = false;
    std::atomic<int> refs{1}, locks{0};
    HB() : Obj(kHB) {}
};

// Like JVM objects they outlive every native call; the registry keeps them reachable (no
// LeakSanitizer reports) and is never destroyed (exitrace).
template <class T, class... A>
T* make(A&&... a) {
    static auto* mu = new std::mutex;
    static auto* all = new std::vector<Obj*>;
    T* o = new T(std::forward<A>(a)...);
    std::lock_guard<std::mutex> lock(*mu);
    all->push_back(o);
    return o;
}

template <class T>
T* as(const void* o, Kind k) {
    auto* p = static_cast<Obj*>(const_cast<void*>(o));
    if (p == nullptr || p->kind != k) misuse("wrong object kind");
    return static_cast<T*>(p);
}

std::map<std::string, std::pair<std::string, void*>>& natives() {
    static auto* m = new std::map<std::string, std::pair<std::string, void*>>;
    return *m;
}

using Table = std::remove_const_t<std::remove_pointer_t<decltype(std::declval<JNIEnv&>().functions)>>;
using VMTable = std::remove_const_t<std::remove_pointer_t<decltype(std::declval<JavaVM&>().functions)>>;

JNIEnv* env() {
    static JNIEnv* e = [] {
        auto* t = new Table{};
        t->GetStringLength = [](JNIEnv*, jstring s) -> jsize { return static_cast<jsize>(as<Str>(s, kString)->s.size()); };
        t->GetStringRegion = [](JNIEnv*, jstring s, jsize start, jsize len, jchar* buf) {
            const auto& v = as<Str>(s, kString)->s;
            if (start < 0 || len < 0 || static_cast<size_t>(start) + static_cast<size_t>(len) > v.size()) misuse("GetStringRegion bounds");
            for (jsize i = 0; i < len; ++i) buf[i] = v[static_cast<size_t>(start + i)];
        };
        t->NewString = [](JNIEnv*, const jchar* u, jsize len) -> jstring {
            auto* s = make<Str>();
            s->s.assign(reinterpret_cast<const char16_t*>(u), static_cast<size_t>(len));
            return reinterpret_cast<jstring>(static_cast<Obj*>(s));
        };
        t->GetDirectBufferAddress = [](JNIEnv*, jobject b) -> void* { return as<Direct>(b, kDirect)->addr; };
        t->GetDirectBufferCapacity = [](JNIEnv*, jobject b) -> jlong { return as<Direct>(b, kDirect)->cap; };
        t->GetArrayLength = [](JNIEnv*, jarray a) -> jsize {
            auto* o = static_cast<Obj*>(static_cast<void*>(a));
            if (o == nullptr) misuse("GetArrayLength(null)");
            if (o->kind == kFloats) return static_cast<jsize>(static_cast<Floats*>(o)->v.size());
            if (o->kind == kObjects) return static_cast<jsize>(static_cast<Objects*>(o)->v.size());
            if (o->kind == kInts) return static_cast<jsize>(static_cast<Ints*>(o)->v.size());
            misuse("GetArrayLength kind");
        };
        t->GetObjectArrayElement = [](JNIEnv*, jobjectArray a, jsize i) -> jobject {
            auto* o = as<Objects>(a, kObjects);
            if (i < 0 || static_cast<size_t>(i) >= o->v.size()) misuse("GetObjectArrayElement bounds");
            return o->v[static_cast<size_t>(i)];
        };
        t->DeleteLocalRef = [](JNIEnv*, jobject) {};
        t->GetFloatArrayRegion = [](JNIEnv*, jfloatArray a, jsize start, jsize len, jfloat* buf) {
            auto* o = as<Floats>(a, kFloats);
            if (start < 0 || len < 0 || static_cast<size_t>(start) + static_cast<size_t>(len) > o->v.size()) misuse("GetFloatArrayRegion bounds");
            std::memcpy(buf, o->v.data() + start, sizeof(jfloat) * static_cast<size_t>(len));
        };
        t->NewIntArray = [](JNIEnv*, jsize len) -> jintArray {
            auto* o = make<Ints>();
            o->v.resize(static_cast<size_t>(len));
            return reinterpret_cast<jintArray>(static_cast<Obj*>(o));
        };
        t->SetIntArrayRegion = [](JNIEnv*, jintArray a, jsize start, jsize len, const jint* buf) {
            auto* o = as<Ints>(a, kInts);
            if (start < 0 || len < 0 || static_cast<size_t>(start) + static_cast<size_t>(len) > o->v.size()) misuse("SetIntArrayRegion bounds");
            std::memcpy(o->v.data() + start, buf, sizeof(jint) * static_cast<size_t>(len));
        };
        t->NewFloatArray = [](JNIEnv*, jsize len) -> jfloatArray {
            auto* o = make<Floats>();
            o->v.resize(static_cast<size_t>(len));
            return reinterpret_cast<jfloatArray>(static_cast<Obj*>(o));
        };
        t->SetFloatArrayRegion = [](JNIEnv*, jfloatArray a, jsize start, jsize len, const jfloat* buf) {
            auto* o = as<Floats>(a, kFloats);
            if (start < 0 || len < 0 || static_cast<size_t>(start) + static_cast<size_t>(len) > o->v.size()) misuse("SetFloatArrayRegion bounds");
            std::memcpy(o->v.data() + start, buf, sizeof(jfloat) * static_cast<size_t>(len));
        };
        t->FindClass = [](JNIEnv*, const char* name) -> jclass {
            if (std::string(name) != "com/narutotimer/NativeCore") misuse("FindClass");
            return reinterpret_cast<jclass>(static_cast<Obj*>(make<Obj>(kClass)));
        };
        t->RegisterNatives = [](JNIEnv*, jclass, const JNINativeMethod* m, jint n) -> jint {
            for (jint i = 0; i < n; ++i) natives()[m[i].name] = {m[i].signature, m[i].fnPtr};
            return JNI_OK;
        };
        auto* e = new JNIEnv{};
        e->functions = t;
        return e;
    }();
    return e;
}

JavaVM* vm() {
    static JavaVM* v = [] {
        auto* t = new VMTable{};
        t->GetEnv = [](JavaVM*, void** out, jint) -> jint {
            *out = env();
            return JNI_OK;
        };
        auto* v = new JavaVM{};
        v->functions = t;
        return v;
    }();
    return v;
}

template <class F>
F native(const char* name, const char* signature) {
    auto it = natives().find(name);
    if (it == natives().end()) misuse(name);
    if (it->second.first != signature) {
        std::fprintf(stderr, "%s: signature %s, want %s\n", name, it->second.first.c_str(), signature);
        misuse("signature");
    }
    return reinterpret_cast<F>(it->second.second);
}

jobject direct(void* addr, jlong cap) {
    auto* d = make<Direct>();
    d->addr = addr;
    d->cap = cap;
    return reinterpret_cast<jobject>(static_cast<Obj*>(d));
}
jstring jstr(const std::string& ascii) {
    auto* s = make<Str>();
    for (char c : ascii) s->s.push_back(static_cast<char16_t>(static_cast<unsigned char>(c)));
    return reinterpret_cast<jstring>(static_cast<Obj*>(s));
}
std::string str(jstring s) {
    if (s == nullptr) return "<null>";
    std::string out;
    for (char16_t c : as<Str>(s, kString)->s) out.push_back(c < 0x80 ? static_cast<char>(c) : '?');
    return out;
}

// ---------------------------------------------------------------------------
// AHardwareBuffer over HB
// ---------------------------------------------------------------------------
}  // namespace

extern "C" {
AHardwareBuffer* AHardwareBuffer_fromHardwareBuffer(JNIEnv*, jobject o) {
    return reinterpret_cast<AHardwareBuffer*>(as<HB>(o, kHB));
}
void AHardwareBuffer_acquire(AHardwareBuffer* b) { reinterpret_cast<HB*>(b)->refs++; }
void AHardwareBuffer_release(AHardwareBuffer* b) {
    if (--reinterpret_cast<HB*>(b)->refs < 1) misuse("AHardwareBuffer over-released");
}
void AHardwareBuffer_describe(const AHardwareBuffer* b, AHardwareBuffer_Desc* d) { *d = reinterpret_cast<const HB*>(b)->desc; }
int AHardwareBuffer_lock(AHardwareBuffer* b, uint64_t, int32_t, const ARect*, void** out) {
    auto* h = reinterpret_cast<HB*>(b);
    if (h->lockRc != 0) return h->lockRc;
    h->locks++;
    *out = h->nullAddr ? nullptr : h->mem.data();
    return 0;
}
int AHardwareBuffer_unlock(AHardwareBuffer* b, int32_t*) {
    if (--reinterpret_cast<HB*>(b)->locks < 0) misuse("AHardwareBuffer unlock without lock");
    return 0;
}
}

namespace {

// ---------------------------------------------------------------------------
// The NativeCore surface
// ---------------------------------------------------------------------------
struct Core {
    jboolean (*init)(JNIEnv*, jclass);
    jboolean (*finish)(JNIEnv*, jclass);
    jstring (*lastError)(JNIEnv*, jclass);
    void (*setNames)(JNIEnv*, jclass, jobjectArray);
    jboolean (*setCalibration)(JNIEnv*, jclass, jstring, jfloatArray);
    jint (*analyzeHB)(JNIEnv*, jclass, jobject, jlong, jint, jobject);
    jint (*analyzeBuffer)(JNIEnv*, jclass, jobject, jint, jint, jint, jlong, jint, jobject);
    jint (*copyLast)(JNIEnv*, jclass, jobject);
    jfloatArray (*lastGeometry)(JNIEnv*, jclass);
    void (*release)(JNIEnv*, jclass);
};

Core& core() {
    static Core* c = [] {
        if (JNI_OnLoad(vm(), nullptr) != JNI_VERSION_1_6) misuse("JNI_OnLoad");
        auto* c = new Core;
        c->init = native<decltype(c->init)>("nativeInit", "()Z");
        c->finish = native<decltype(c->finish)>("nativeFinishAssets", "()Z");
        c->lastError = native<decltype(c->lastError)>("nativeLastError", "()Ljava/lang/String;");
        c->setNames = native<decltype(c->setNames)>("nativeSetPlayerNames", "([Ljava/lang/String;)V");
        c->setCalibration = native<decltype(c->setCalibration)>("nativeSetCalibration", "(Ljava/lang/String;[F)Z");
        c->analyzeHB = native<decltype(c->analyzeHB)>("nativeAnalyzeHardwareBuffer",
                                                      "(Landroid/hardware/HardwareBuffer;JILjava/nio/ByteBuffer;)I");
        c->analyzeBuffer = native<decltype(c->analyzeBuffer)>("nativeAnalyzeBuffer",
                                                              "(Ljava/nio/ByteBuffer;IIIJILjava/nio/ByteBuffer;)I");
        c->copyLast = native<decltype(c->copyLast)>("nativeCopyLastResult", "(Ljava/nio/ByteBuffer;)I");
        c->lastGeometry = native<decltype(c->lastGeometry)>("nativeLastGeometry", "()[F");
        c->release = native<decltype(c->release)>("nativeRelease", "()V");
        return c;
    }();
    return *c;
}

std::string lastError() { return str(core().lastError(env(), nullptr)); }

void load(const std::string& repo) {
    check(core().init(env(), nullptr) == JNI_TRUE, "nativeInit");
    registerAssets(repo);  // as AssetLoader: after nativeInit (it clears the store)
    check(g_images > 0 && g_texts > 0, "repo assets registered");
    check(core().finish(env(), nullptr) == JNI_TRUE, "nativeFinishAssets: " + lastError());
}

// A frame of RGBA pixels (exact-size allocation), deterministic noise with a few flat bands.
struct Pixels {
    int w = 0, h = 0, stride = 0;
    std::vector<uint8_t> px;
};
Pixels noise(int w, int h, uint64_t seed, int stride = 0) {
    Pixels p;
    p.w = w;
    p.h = h;
    p.stride = stride > 0 ? stride : 4 * w;
    p.px.resize(static_cast<size_t>(p.stride) * (h - 1) + 4 * static_cast<size_t>(w));
    uint64_t s = seed * 0x9E3779B97F4A7C15ull + 1;
    for (auto& b : p.px) {
        s ^= s << 13, s ^= s >> 7, s ^= s << 17;
        b = static_cast<uint8_t>(s);
    }
    return p;
}

struct Out {
    std::vector<uint8_t> buf;
    jobject obj = nullptr;
    explicit Out(size_t n) : buf(n) { obj = direct(buf.data(), static_cast<jlong>(n)); }
    int32_t i32(size_t at) const {
        int32_t v;
        std::memcpy(&v, buf.data() + at, 4);
        return v;
    }
    int64_t i64(size_t at) const {
        int64_t v;
        std::memcpy(&v, buf.data() + at, 8);
        return v;
    }
    int64_t sequence() const { return i64(32); }
    bool duplicate() const { return (i32(8) & (1 << 5)) != 0; }
    bool reused() const { return (i32(8) & (1 << 6)) != 0; }
};

jint analyze(const Pixels& p, jlong at, Out& out) {
    jobject in = direct(const_cast<uint8_t*>(p.px.data()), static_cast<jlong>(p.px.size()));
    return core().analyzeBuffer(env(), nullptr, in, p.w, p.h, p.stride, at, 2, out.obj);
}

HB* hardwareBuffer(const Pixels& p, uint32_t format, uint32_t strideOverride = 0) {
    auto* hb = make<HB>();
    hb->desc.width = static_cast<uint32_t>(p.w);
    hb->desc.height = static_cast<uint32_t>(p.h);
    hb->desc.layers = 1;
    hb->desc.format = format;
    hb->desc.stride = strideOverride != 0 ? strideOverride : static_cast<uint32_t>(p.stride / 4);
    hb->mem = p.px;
    return hb;
}
jobject hbObject(HB* hb) { return reinterpret_cast<jobject>(static_cast<Obj*>(hb)); }

// ---------------------------------------------------------------------------
// NC-03
// ---------------------------------------------------------------------------
void hardwareBufferChecks() {
    Out out(16 << 10);
    const Pixels ok = noise(640, 360, 3, 4 * 700);  // padded stride: 700 px
    {
        HB* hb = hardwareBuffer(ok, AHARDWAREBUFFER_FORMAT_R8G8B8A8_UNORM);
        const jint n = core().analyzeHB(env(), nullptr, hbObject(hb), 1000, 0, out.obj);
        check(n > 0, "a valid HardwareBuffer is analysed: " + lastError());
        check(hb->refs == 1 && hb->locks == 0, "valid HB released and unlocked");
    }
    struct Bad {
        const char* name;
        uint32_t width, height, layers, stride;
    } bads[] = {
        {"stride < width", 640, 360, 1, 600},  // the allocation is 600·360·4: reading 640 per row overruns it
        {"zero width", 0, 360, 1, 700},
        {"zero height", 640, 0, 1, 700},
        {"two layers", 640, 360, 2, 700},
        {"stride past INT_MAX/4", 640, 360, 1, 0x40000000u},
        {"height past INT_MAX", 640, 0x80000000u, 1, 700},
    };
    for (const Bad& b : bads) {
        Pixels small = noise(static_cast<int>(std::max<uint32_t>(1, std::min<uint32_t>(b.stride, 700))), 360, 4);
        HB* hb = hardwareBuffer(small, AHARDWAREBUFFER_FORMAT_R8G8B8A8_UNORM);
        hb->desc.width = b.width;
        hb->desc.height = b.height;
        hb->desc.layers = b.layers;
        hb->desc.stride = b.stride;
        const jint n = core().analyzeHB(env(), nullptr, hbObject(hb), 2000, 0, out.obj);
        check(n == 0 && lastError().find("bad HardwareBuffer geometry") != std::string::npos,
              std::string("bad descriptor accepted: ") + b.name + " (" + lastError() + ")");
        check(hb->refs == 1 && hb->locks == 0, std::string("bad descriptor leaked a ref or lock: ") + b.name);
    }
    {  // locked without an address: unlocked again
        HB* hb = hardwareBuffer(ok, AHARDWAREBUFFER_FORMAT_R8G8B8X8_UNORM);
        hb->nullAddr = true;
        check(core().analyzeHB(env(), nullptr, hbObject(hb), 3000, 0, out.obj) == 0, "null address analysed");
        check(hb->refs == 1 && hb->locks == 0, "null address: still locked (refs " + std::to_string(hb->refs) + ", locks " +
                                                  std::to_string(hb->locks) + ")");
    }
    {  // lock failure
        HB* hb = hardwareBuffer(ok, AHARDWAREBUFFER_FORMAT_R8G8B8A8_UNORM);
        hb->lockRc = -22;
        check(core().analyzeHB(env(), nullptr, hbObject(hb), 4000, 0, out.obj) == 0 &&
                  lastError().find("lock failed: -22") != std::string::npos, "lock failure");
        check(hb->refs == 1 && hb->locks == 0, "lock failure leaked");
    }
    std::printf("NC-03 HardwareBuffer: %zu bad descriptors refused, every path unlocks and releases\n", std::size(bads));
}

// ---------------------------------------------------------------------------
// NC-07 + NC-09
// ---------------------------------------------------------------------------
void resultCopyAndOpaque() {
    const Pixels a = noise(800, 450, 11), b = noise(800, 450, 12);
    Out big(16 << 10), tiny(100);
    check(analyze(a, 10'000'000, big) > 0, "first frame: " + lastError());
    const int64_t seq0 = big.sequence();
    // Too small: -need, and the very same analysis is copied out afterwards.
    const jint n = analyze(b, 20'000'000, tiny);
    check(n < 0, "a 100-byte buffer is not too small");
    Out tooSmall(static_cast<size_t>(-n) - 1);
    check(core().copyLast(env(), nullptr, tooSmall.obj) == n, "copy into a still too small buffer is not -need");
    Out right(static_cast<size_t>(-n));
    check(core().copyLast(env(), nullptr, right.obj) == -n, "nativeCopyLastResult: " + lastError());
    check(right.i32(0) == 0x4E545231 && right.i32(4) == -n, "copied result header");
    check(right.sequence() == seq0 + 1 && !right.duplicate() && !right.reused(),
          "the copied result is a second analysis (seq " + std::to_string(right.sequence()) + ", dup " +
              std::to_string(right.duplicate()) + ", reused " + std::to_string(right.reused()) + ")");
    check(core().copyLast(env(), nullptr, right.obj) == 0, "a result is copied out twice");
    // Pending results are per thread.
    check(analyze(a, 30'000'000, tiny) < 0, "too small again");
    jint other = 1;
    std::thread([&] { other = core().copyLast(env(), nullptr, right.obj); }).join();
    check(other == 0, "another thread took this thread's pending result");
    check(core().copyLast(env(), nullptr, big.obj) > 0, "pending result lost");
    check(analyze(b, 40'000'000, big) > 0 && big.sequence() == seq0 + 3, "sequence after copies");

    // NC-09: the same RGBX pixels with other X bytes are the same frame.
    Pixels x1 = noise(800, 450, 21), x2 = x1, x3 = x1;
    for (size_t i = 3; i < x1.px.size(); i += 4) x1.px[i] = 0xFF, x2.px[i] = static_cast<uint8_t>(i * 7), x3.px[i] = 0;
    x3.px[4 * (200 * 800 + 300)] ^= 0x10;  // one red change
    Out o(16 << 10);
    const Pixels* frames[] = {&x1, &x2, &x3};
    for (int i = 0; i < 3; ++i) {
        HB* hb = hardwareBuffer(*frames[i], AHARDWAREBUFFER_FORMAT_R8G8B8X8_UNORM);
        check(core().analyzeHB(env(), nullptr, hbObject(hb), 50'000'000 + 1000 * i, 0, o.obj) > 0, "RGBX frame");
        if (i == 1) check(o.duplicate() && o.reused(), "RGBX frame with other X bytes is not a duplicate");
        if (i == 2) check(!o.duplicate() && !o.reused(), "a colour change is a duplicate");
    }
    std::printf("NC-07 result copy: -need then nativeCopyLastResult = the same analysis (one sequence number); NC-09 X bytes ignored\n");
}

// ---------------------------------------------------------------------------
// NC-08: a frame in flight does not block names / errors / geometry
// ---------------------------------------------------------------------------
void nonBlockingSettings() {
    for (int attempt = 0; attempt < 3; ++attempt) {
        const int scale = attempt + 1;
        const Pixels slow = noise(1920 * scale, 1080 * scale, 31 + attempt);
        std::atomic<bool> started{false}, finished{false};
        std::thread analysis([&] {
            Out out(16 << 10);
            started = true;
            analyze(slow, 60'000'000 + attempt, out);
            finished = true;
        });
        while (!started) std::this_thread::yield();
        std::this_thread::sleep_for(std::chrono::milliseconds(30));
        double worst = 0;
        bool inFlight = true;
        for (int i = 0; i < 5; ++i) {
            const auto t0 = std::chrono::steady_clock::now();
            auto* names = make<Objects>();
            names->v = {reinterpret_cast<jobject>(jstr("player" + std::to_string(i)))};
            core().setNames(env(), nullptr, reinterpret_cast<jobjectArray>(static_cast<Obj*>(names)));
            (void)core().lastError(env(), nullptr);
            (void)core().lastGeometry(env(), nullptr);
            const double ms = std::chrono::duration<double, std::milli>(std::chrono::steady_clock::now() - t0).count();
            worst = std::max(worst, ms);
            inFlight = inFlight && !finished.load();
        }
        analysis.join();
        const std::vector<std::string> mine = nt::identity::MineNames();
        check(mine.size() == 1 && mine[0] == "player4", "names set during a frame were not applied");
        if (!inFlight) continue;  // the frame ended first: try a bigger one
        check(worst < 100, "setPlayerNames/lastError/lastGeometry waited " + std::to_string(worst) + " ms for a running frame");
        std::printf("NC-08 settings during a %dx%d frame: worst %.2f ms\n", slow.w, slow.h, worst);
        return;
    }
    check(false, "no frame was slow enough to overlap the settings calls");
}

// ---------------------------------------------------------------------------
// NC-01: oversized calibration boxes through JNI stay automatic
// ---------------------------------------------------------------------------
std::vector<jfloat> geometry() {
    jfloatArray g = core().lastGeometry(env(), nullptr);
    return g != nullptr ? as<Floats>(g, kFloats)->v : std::vector<jfloat>{};
}

void calibrationThroughJni() {
    // Geometry with no calibration and with boxes the limits reject must be the same. A game frame
    // (NT_JNI_TEST_FRAME, a PNG; the repo has none) has bead geometry to compare; noise may have none.
    Pixels f = noise(2400, 1080, 41);
    const char* framePath = std::getenv("NT_JNI_TEST_FRAME");
    if (framePath != nullptr && *framePath != 0) {
        nt::RGBA img;
        check(loadFrame(framePath, img), std::string("NT_JNI_TEST_FRAME: ") + framePath);
        f.w = img.Bounds().Dx();
        f.h = img.Bounds().Dy();
        f.stride = 4 * f.w;
        f.px.assign(img.Pix, img.Pix + static_cast<size_t>(f.stride) * f.h);
    }
    Out out(16 << 10);
    auto calibrate = [&](std::vector<jfloat> rects) {
        auto* a = make<Floats>();
        a->v = std::move(rects);
        return core().setCalibration(env(), nullptr, jstr("duel"), reinterpret_cast<jfloatArray>(static_cast<Obj*>(a)));
    };
    const jfloat nan = std::nanf("");
    check(calibrate(std::vector<jfloat>(16, nan)) == JNI_TRUE, "automatic calibration");
    check(analyze(f, 70'000'000, out) > 0, "frame (automatic)");
    const std::vector<jfloat> automatic = geometry();
    check(calibrate({0, 0, 1, 1, 0.8f, 0.1f, 0.004f, 0.003f, 0, 0, 0.25f, 0.45f, 0.9f, 0, 0.01f, 0.12f}) == JNI_TRUE,
          "oversized calibration: " + lastError());
    check(analyze(f, 80'000'000, out) > 0, "frame (oversized boxes)");
    const std::vector<jfloat> rejected = geometry();
    check(!automatic.empty() && automatic.size() == rejected.size(), "geometry present");
    const bool beads = automatic.size() > 5 && automatic[5] > 0;
    check(beads || framePath == nullptr, "NT_JNI_TEST_FRAME has no bead geometry");
    for (size_t i = 3; i < automatic.size(); ++i) {  // [3], [4]: sequence
        if (i == 3 || i == 4) continue;
        check(automatic[i] == rejected[i], "geometry differs at " + std::to_string(i) + " with boxes outside the limits");
    }
    check(calibrate(std::vector<jfloat>(16, nan)) == JNI_TRUE, "back to automatic");
    std::printf("NC-01 through JNI: boxes outside the limits leave the frame's geometry automatic (%zu values, %s)\n",
                automatic.size(), beads ? "with bead geometry" : "noise frame without bead geometry");
}

// ---------------------------------------------------------------------------
// NC-02 (memory) and NC-06 (exitrace) modes
// ---------------------------------------------------------------------------
int memory(const std::string& repo) {
    load(repo);
    Out out(16 << 10);
    jlong at = 1'000'000'000;
    auto frames = [&](int w, int h, int n, uint64_t seed) {
        for (int i = 0; i < n; ++i) {
            const Pixels p = noise(w, h, seed + static_cast<uint64_t>(i));
            check(analyze(p, at += 100'000'000, out) > 0, "memory frame: " + lastError());
        }
    };
    frames(1280, 720, 30, 100);
    const long long baseline = g_bigLive.load();
    frames(7680, 4320, 1, 200);
    const long long peak = g_bigLive.load();
    frames(1280, 720, 40, 300);
    const long long after = g_bigLive.load();
    const size_t spare = nt::match::LargePrefixSpareBytes();
    std::printf("NC-02 memory: large live blocks %.1f MiB at 1280x720, %.1f MiB after a 7680x4320 frame, %.1f MiB after 40 more "
                "1280x720 (large-prefix spare %.1f MiB)\n",
                baseline / 1048576.0, peak / 1048576.0, after / 1048576.0, spare / 1048576.0);
    // What may stay: the avatar template cache (its own 8 MiB cap, idle eviction after 2048
    // scans) and per-thread buffers within kKeepPrefixBytes. Before the fix the 7680x4320
    // prefixes (pool spare, avatar view prefixes, memo) stayed as well: ~24 MiB more.
    check(spare <= (static_cast<size_t>(4) << 20), "the 7680x4320 frame's large-prefix spare stays resident");
    check(after <= baseline + (static_cast<long long>(11) << 20), "buffers of the 7680x4320 frame stay resident");
    return 0;
}

int exitRace(const std::string& repo) {
    load(repo);
    auto* slow = new Pixels(noise(3840, 2160, 51));
    auto* out = new Out(16 << 10);
    auto* started = new std::atomic<bool>{false};
    new std::thread([=] {
        *started = true;
        for (int i = 0; i < 1000; ++i) analyze(*slow, 90'000'000 + i, *out);
    });
    while (!*started) std::this_thread::yield();
    std::this_thread::sleep_for(std::chrono::milliseconds(300));
    std::printf("NC-06 exitrace: exit() during a 3840x2160 frame\n");
    std::fflush(stdout);
    std::exit(0);
}

}  // namespace

int main(int argc, char** argv) {
    if (argc < 2) {
        std::fprintf(stderr, "usage: jni_host_test <repo> [memory|exitrace]\n");
        return 2;
    }
    std::setvbuf(stdout, nullptr, _IOLBF, 0);
    const std::string repo = argv[1];
    const std::string mode = argc > 2 ? argv[2] : "";
    if (mode == "memory") return memory(repo);
    if (mode == "exitrace") return exitRace(repo);
    load(repo);
    hardwareBufferChecks();
    resultCopyAndOpaque();
    nonBlockingSettings();
    calibrationThroughJni();
    core().release(env(), nullptr);
    std::printf("jni_host_test: %d checks passed\n", checks);
    return 0;
}
