// Owner: cpp-match-detect
// Port of: internal/match/prepared.go
// Contract: include/nt/match.h — see android/ARCHITECTURE.md.
//
// PreparedNCC owns a snapshot of one template and its binary mask. It is
// immutable and safe to share between matchers. Cache only the current geometry;
// preparing at every frame would waste work and memory after source resizes.
// No pixel is sampled approximately: all nonzero-mask pixels contribute exactly
// as in the scalar matcher.
//
// 性能（docs/性能优化-android-native.md）：模板只存掩膜内各 run 的像素（紧凑）；点积按 CPU
// 选 SIMD 内核（src/match/ncc_dot.h），全部是精确整数，与标量逐位相同；浮点公式不变。
#include <algorithm>
#include <atomic>
#include <cmath>
#include <cstring>

#include "nt/match.h"
#include "nt/parallel.h"
#include "ncc_dot.h"

#if defined(__aarch64__)
#include <sys/auxv.h>
#ifndef HWCAP_ASIMDDP
#define HWCAP_ASIMDDP (1 << 20)
#endif
#endif

namespace nt::match {

// PrepareNCC precomputes template statistics and contiguous mask runs. Mask
// origins/strides need not match the template; a smaller mask omits its missing
// pixels and all nonzero bytes are equally included (not weighted).
std::shared_ptr<const PreparedNCC> PrepareNCC(const nt::Gray* templ, const nt::Gray* mask) {
    if (templ == nullptr || templ->Bounds().Empty()) {
        return nullptr;
    }
    const Rect b = templ->Bounds();
    const int w = b.Dx(), h = b.Dy();
    auto p = std::make_shared<PreparedNCC>();
    p->rect_ = b;
    int64_t count = 0, sum = 0, squares = 0;
    const int maskW = mask != nullptr ? mask->Bounds().Dx() : 0;
    const int maskH = mask != nullptr ? mask->Bounds().Dy() : 0;
    // First pass: the run layout, so data_/runs_ are allocated exactly once.
    size_t runCount = 0, pixels = 0;
    for (int y = 0; y < h; ++y) {
        if (mask != nullptr && y >= maskH) break;
        const int end = mask != nullptr ? std::min(w, maskW) : w;
        const uint8_t* mrow = mask != nullptr ? mask->Pix + static_cast<ptrdiff_t>(y) * mask->Stride : nullptr;
        for (int x = 0; x < end;) {
            if (mrow != nullptr && mrow[x] == 0) {
                x++;
                continue;
            }
            const int start = x;
            while (x < end && (mrow == nullptr || mrow[x] != 0)) x++;
            pixels += static_cast<size_t>(x - start);
            runCount += static_cast<size_t>((x - start + maxDotRun - 1) / maxDotRun);
        }
    }
    p->data_.resize(pixels);
    p->runs_.reserve(runCount);
    int offset = 0;
    for (int y = 0; y < h; ++y) {
        const uint8_t* row = templ->Pix + static_cast<ptrdiff_t>(y) * templ->Stride;
        if (mask != nullptr && y >= maskH) {
            continue;
        }
        int end = w;
        if (mask != nullptr) {
            end = std::min(end, maskW);
        }
        const uint8_t* mrow = mask != nullptr ? mask->Pix + static_cast<ptrdiff_t>(y) * mask->Stride : nullptr;
        for (int x = 0; x < end;) {
            if (mrow != nullptr && mrow[x] == 0) {
                x++;
                continue;
            }
            int start = x;
            while (x < end && (mrow == nullptr || mrow[x] != 0)) {
                int64_t v = row[x];
                count++;
                sum += v;
                squares += v * v;
                x++;
            }
            // Split very long runs so each run's dot product fits in uint32.
            for (; start < x; start += maxDotRun) {
                PreparedNCC::nccRun run;
                run.x = start;
                run.y = y;
                run.offset = offset;
                run.len = std::min(x, start + maxDotRun) - start;
                std::memcpy(p->data_.data() + offset, row + start, static_cast<size_t>(run.len));
                offset += run.len;
                p->runs_.push_back(run);
            }
        }
    }
    p->count_ = static_cast<double>(count);
    p->sum_ = static_cast<double>(sum);
    if (count > 0) {
        p->variance_ = static_cast<double>(squares) - p->sum_ * p->sum_ / p->count_;
    }
    return p;
}

// newGrayPrefix covers columns [x0,x0+w) and rows [y0,y0+h) of img.
// Offsets are relative to img.Bounds().Min (the same offsets at uses).
// Go 用 sync.Pool 复用；这里调用方传入可复用的 g（容量只增不减）。
void newGrayPrefix(grayPrefix& g, const nt::Gray& img, int x0, int y0, int w, int h) {
    g.x0 = x0;
    g.y0 = y0;
    g.w = w;
    g.h = h;
    const size_t n = static_cast<size_t>(h) * static_cast<size_t>(w + 1);
    if (g.sum.size() < n) {
        g.sum.resize(n);
        g.square.resize(n);
    }
    uint32_t* sumData = g.sum.data();
    uint32_t* squareData = g.square.data();
    auto rows = [&](int ya, int yb) {
        for (int y = ya; y < yb; ++y) {
            const uint8_t* row = img.Pix + static_cast<ptrdiff_t>(y0 + y) * img.Stride + x0;
            uint32_t* sums = sumData + static_cast<size_t>(y) * static_cast<size_t>(w + 1);
            uint32_t* squares = squareData + static_cast<size_t>(y) * static_cast<size_t>(w + 1);
            uint32_t s = 0, q = 0;
            sums[0] = 0;
            squares[0] = 0;
            for (int x = 0; x < w; ++x) {
                const uint32_t v = row[x];
                s += v;
                q += v * v;
                sums[x + 1] = s;
                squares[x + 1] = q;
            }
        }
    };
    // Rows are independent: a large search area (a wide screen region) is
    // summed in parallel bands; small ones (avatar/name windows) stay inline.
    if (static_cast<int64_t>(w) * h >= (int64_t{1} << 16) && h >= 8) {
        constexpr int bands = 4;
        ParallelFor(bands, [&](int b) { rows(h * b / bands, h * (b + 1) / bands); });
    } else {
        rows(0, h);
    }
}

namespace {

struct nccSums {
    uint64_t sum = 0, squares = 0, product = 0;
};

using sumsFn = void (*)(const PreparedNCC&, const nt::Gray&, const grayPrefix&, int, int, nccSums&);

// runSums accumulates the exact integer sums of one placement. Every run's
// segment sums are uint32-exact (prefix rows <= maxDotRun wide, runs <= maxDotRun
// long), so the uint64 totals equal the per-pixel scalar accumulation.
template <uint32_t (*Dot)(const uint8_t*, const uint8_t*, int)>
inline void runSums(const PreparedNCC& p, const nt::Gray& img, const grayPrefix& pre, int ox, int oy, nccSums& s) {
    uint64_t sum = 0, squares = 0, product = 0;
    const ptrdiff_t stride = static_cast<ptrdiff_t>(pre.w) + 1;
    const uint8_t* tdata = p.data_.data();
    const uint32_t* psum = pre.sum.data();
    const uint32_t* psq = pre.square.data();
    const ptrdiff_t preX = static_cast<ptrdiff_t>(ox) - pre.x0;
    const ptrdiff_t preY = static_cast<ptrdiff_t>(oy) - pre.y0;
    const uint8_t* origin = img.Pix + static_cast<ptrdiff_t>(oy) * img.Stride + ox;
    // Match bounds-checks the whole template footprint first, so the hot loop
    // has no per-pixel GrayAt bounds/origin/mask checks.
    for (const PreparedNCC::nccRun& run : p.runs_) {
        const ptrdiff_t base = (preY + run.y) * stride + preX + run.x;
        sum += psum[base + run.len] - psum[base];
        squares += psq[base + run.len] - psq[base];
        product += Dot(origin + static_cast<ptrdiff_t>(run.y) * img.Stride + run.x, tdata + run.offset, run.len);
    }
    s.sum = sum;
    s.squares = squares;
    s.product = product;
}

// flatten：把 runSums 与点积内核整体内联进带 target 属性的函数（GCC/Clang 均可）。
__attribute__((flatten)) void sumsScalar(const PreparedNCC& p, const nt::Gray& img, const grayPrefix& pre, int ox,
                                         int oy, nccSums& s) {
    runSums<dot::Scalar>(p, img, pre, ox, oy, s);
}

#if defined(NT_DOT_X86)
__attribute__((flatten)) void sumsSSE2(const PreparedNCC& p, const nt::Gray& img, const grayPrefix& pre, int ox, int oy,
                                       nccSums& s) {
    runSums<dot::SSE2>(p, img, pre, ox, oy, s);
}
NT_TARGET_AVX2 __attribute__((flatten)) void sumsAVX2(const PreparedNCC& p, const nt::Gray& img,
                                                      const grayPrefix& pre, int ox, int oy, nccSums& s) {
    runSums<dot::AVX2>(p, img, pre, ox, oy, s);
}
bool hasAVX2() {
    __builtin_cpu_init();
    return __builtin_cpu_supports("avx2");
}
#endif

#if defined(NT_DOT_NEON)
__attribute__((flatten)) void sumsNEON(const PreparedNCC& p, const nt::Gray& img, const grayPrefix& pre, int ox, int oy,
                                       nccSums& s) {
    runSums<dot::NEON>(p, img, pre, ox, oy, s);
}
NT_TARGET_DOTPROD __attribute__((flatten)) void sumsDotProd(const PreparedNCC& p, const nt::Gray& img,
                                                            const grayPrefix& pre, int ox, int oy, nccSums& s) {
    runSums<dot::DotProd>(p, img, pre, ox, oy, s);
}
bool hasDotProd() { return (getauxval(AT_HWCAP) & HWCAP_ASIMDDP) != 0; }
#endif

struct kernel {
    const char* name;
    sumsFn fn;
};

// availableKernels lists this CPU's kernels, slowest first.
std::vector<kernel> availableKernels() {
    std::vector<kernel> out{{"scalar", sumsScalar}};
#if defined(NT_DOT_X86)
    out.push_back({"sse2", sumsSSE2});
    if (hasAVX2()) out.push_back({"avx2", sumsAVX2});
#endif
#if defined(NT_DOT_NEON)
    out.push_back({"neon", sumsNEON});
    if (hasDotProd()) out.push_back({"dotprod", sumsDotProd});
#endif
    return out;
}

struct activeKernel {
    std::atomic<sumsFn> fn;
    std::atomic<const char*> name;
    activeKernel() {
        const kernel best = availableKernels().back();
        fn.store(best.fn, std::memory_order_relaxed);
        name.store(best.name, std::memory_order_relaxed);
    }
};

activeKernel& active() {
    static activeKernel k;
    return k;
}

}  // namespace

const char* NCCKernel() { return active().name.load(std::memory_order_relaxed); }

std::vector<std::string> NCCKernels() {
    std::vector<std::string> out;
    for (const kernel& k : availableKernels()) out.emplace_back(k.name);
    return out;
}

bool SetNCCKernel(const std::string& name) {
    for (const kernel& k : availableKernels()) {
        if (name == k.name) {
            active().fn.store(k.fn, std::memory_order_relaxed);
            active().name.store(k.name, std::memory_order_relaxed);
            return true;
        }
    }
    return false;
}

// at scores the template at (ox, oy). pre must cover the template footprint.
// Sums are exact integers, so the float formula sees the same values (and
// performs the same operations in the same order) as the per-pixel scalar
// accumulation it replaced: scores are bit-identical.
double PreparedNCC::at(const nt::Gray& img, const grayPrefix& pre, int ox, int oy) const {
    if (count_ < 8) {
        return 0;
    }
    nccSums s;
    active().fn.load(std::memory_order_relaxed)(*this, img, pre, ox, oy, s);
    return score(s.sum, s.squares, s.product);
}

// atDirect is the same score without prefix sums: plain per-pixel scalar sums.
double PreparedNCC::atDirect(const nt::Gray& img, int ox, int oy) const {
    if (count_ < 8) {
        return 0;
    }
    uint64_t sum = 0, squares = 0, product = 0;
    for (const nccRun& run : runs_) {
        const uint8_t* row = img.Pix + static_cast<ptrdiff_t>(oy + run.y) * img.Stride + ox + run.x;
        const uint8_t* t = data_.data() + run.offset;
        for (int i = 0; i < run.len; ++i) {
            const uint64_t v = row[i];
            sum += v;
            squares += v * v;
            product += v * t[i];
        }
    }
    return score(sum, squares, product);
}

double PreparedNCC::score(uint64_t sum, uint64_t squares, uint64_t product) const {
    const double sumI = static_cast<double>(sum);
    const double variance = static_cast<double>(squares) - sumI * sumI / count_;
    if (variance <= 1e-6 || variance_ <= 1e-6) {
        if (variance > 1e-6 || variance_ > 1e-6) {
            return 0;
        }
        if (std::fabs(sumI / count_ - sum_ / count_) <= 6) {
            return 1;
        }
        return 0;
    }
    const double score = (static_cast<double>(product) - sumI * sum_ / count_) / std::sqrt(variance * variance_);
    // Go: min(1, max(0, score))。NaN 不会出现（variance、variance_ 均 > 1e-6）。
    return std::min(1.0, std::max(0.0, score));
}

}  // namespace nt::match
