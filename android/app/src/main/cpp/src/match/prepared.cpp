// Owner: cpp-match-detect
// Port of: internal/match/prepared.go
// Contract: include/nt/match.h — see android/ARCHITECTURE.md.
//
// PreparedNCC owns a snapshot of one template and its binary mask. It is
// immutable and safe to share between matchers. Cache only the current geometry;
// preparing at every frame would waste work and memory after source resizes.
// No pixel is sampled approximately: all nonzero-mask pixels contribute exactly
// as in the scalar matcher.
#include <algorithm>
#include <cmath>
#include <cstring>

#include "nt/match.h"

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
    p->data_.assign(static_cast<size_t>(w) * static_cast<size_t>(h), 0);
    int64_t count = 0, sum = 0, squares = 0;
    p->runs_.reserve(static_cast<size_t>(h));
    const int maskW = mask != nullptr ? mask->Bounds().Dx() : 0;
    const int maskH = mask != nullptr ? mask->Bounds().Dy() : 0;
    for (int y = 0; y < h; ++y) {
        uint8_t* row = p->data_.data() + static_cast<size_t>(y) * static_cast<size_t>(w);
        std::memcpy(row, templ->Pix + static_cast<ptrdiff_t>(y) * templ->Stride, static_cast<size_t>(w));
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
                run.offset = y * w + start;
                run.len = std::min(x, start + maxDotRun) - start;
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
    const size_t n = static_cast<size_t>(h) * static_cast<size_t>(w + 1);
    if (g.sum.size() < n) {
        g.sum.resize(n);
        g.square.resize(n);
    }
    for (int y = 0; y < h; ++y) {
        const uint8_t* row = img.Pix + static_cast<ptrdiff_t>(y0 + y) * img.Stride + x0;
        uint64_t* sums = g.sum.data() + static_cast<size_t>(y) * static_cast<size_t>(w + 1);
        uint64_t* squares = g.square.data() + static_cast<size_t>(y) * static_cast<size_t>(w + 1);
        uint64_t s = 0, q = 0;
        sums[0] = 0;
        squares[0] = 0;
        for (int x = 0; x < w; ++x) {
            const uint64_t v = row[x];
            s += v;
            q += v * v;
            sums[x + 1] = s;
            squares[x + 1] = q;
        }
    }
}

// at scores the template at (ox, oy). pre must cover the template footprint.
// Sums are exact integers, so the float formula sees the same values (and
// performs the same operations in the same order) as the per-pixel scalar
// accumulation it replaced: scores are bit-identical.
double PreparedNCC::at(const nt::Gray& img, const grayPrefix& pre, int ox, int oy) const {
    if (count_ < 8) {
        return 0;
    }
    uint64_t sum = 0, squares = 0, product = 0;
    const ptrdiff_t stride = static_cast<ptrdiff_t>(pre.w) + 1;
    const uint8_t* tdata = data_.data();
    const uint64_t* psum = pre.sum.data();
    const uint64_t* psq = pre.square.data();
    // Match bounds-checks the whole template footprint first, so the hot loop
    // has no per-pixel GrayAt bounds/origin/mask checks.
    for (const nccRun& run : runs_) {
        const uint8_t* t = tdata + run.offset;
        const int n = run.len;
        const ptrdiff_t base = static_cast<ptrdiff_t>(oy + run.y - pre.y0) * stride + ox + run.x - pre.x0;
        const ptrdiff_t end = base + n;
        sum += psum[end] - psum[base];
        squares += psq[end] - psq[base];
        const uint8_t* row = img.Pix + static_cast<ptrdiff_t>(oy + run.y) * img.Stride + ox + run.x;
        // PrepareNCC caps runs at maxDotRun, so uint32 cannot overflow.
        // 四路累加与 Go 相同；因为不会溢出，整数结果与累加顺序无关（逐位一致）。
        uint32_t a = 0, b = 0, c = 0, d = 0;
        int i = 0;
        for (; i + 4 <= n; i += 4) {
            a += uint32_t(row[i]) * uint32_t(t[i]);
            b += uint32_t(row[i + 1]) * uint32_t(t[i + 1]);
            c += uint32_t(row[i + 2]) * uint32_t(t[i + 2]);
            d += uint32_t(row[i + 3]) * uint32_t(t[i + 3]);
        }
        for (; i < n; ++i) {
            a += uint32_t(row[i]) * uint32_t(t[i]);
        }
        product += uint64_t(a) + uint64_t(b) + uint64_t(c) + uint64_t(d);
    }
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
