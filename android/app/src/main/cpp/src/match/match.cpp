// Owner: cpp-match-detect
// Port of: internal/match/match.go
// Contract: include/nt/match.h — see android/ARCHITECTURE.md.
//
// Package match 提供与游戏无关的模板匹配器。
// 第一实现是灰度 NCC；以后可换哈希 / 其它 Matcher，场景门闩不用改。
#include "nt/match.h"
#include "nt/parallel.h"

#include <algorithm>
#include <cstring>
#include <memory>
#include <mutex>
#include <vector>

namespace nt::match {

namespace {
// Go 的 sync.Pool 复用 grayPrefix；Android 上 Match 只在调用线程上同步使用前缀和，
// 用 thread_local 缓冲等价复用（ParallelFor 的每个工作线程各自一份）。
thread_local grayPrefix tlsPrefix;
// Prefix sums above kKeepPrefixBytes (wide screen regions: ~2 MiB at 1080p,
// ~5 MiB at 2880x1800) do not stay resident on every pool thread: they come
// from a process-wide pool that keeps ONE such buffer for the next large scan.
constexpr size_t kKeepPrefixBytes = static_cast<size_t>(512) << 10;

class largePrefixPool {
public:
    std::unique_ptr<grayPrefix> take() {
        std::lock_guard<std::mutex> lk(mu_);
        if (spare_) return std::move(spare_);
        return std::make_unique<grayPrefix>();
    }
    void give(std::unique_ptr<grayPrefix> p) {
        std::lock_guard<std::mutex> lk(mu_);
        if (!spare_ || spare_->sum.capacity() < p->sum.capacity()) spare_ = std::move(p);
    }

private:
    std::mutex mu_;
    std::unique_ptr<grayPrefix> spare_;
};

largePrefixPool& largePrefixes() {
    static largePrefixPool* pool = new largePrefixPool();  // process lifetime, like the thread pool
    return *pool;
}
// kParallelScanWork (positions x template pixels) splits one scan over the
// pool: ~1 ms of single-thread NCC work. Smaller scans (every avatar job,
// name strips, hint windows) stay on their caller, which is already parallel.
constexpr int64_t kParallelScanWork = int64_t{3} << 20;
}  // namespace

// Match 在 ROI 内滑动模板，返回最高 NCC。
bool NCC::Match(const Query& q, Score& out) const {
    out = Score{};
    if (q.Image == nullptr || (q.Template == nullptr && q.Prepared == nullptr)) {
        return false;  // match: image and template are required
    }
    const PreparedNCC* prepared = q.Prepared;
    std::shared_ptr<const PreparedNCC> owned;
    if (prepared == nullptr) {
        owned = PrepareNCC(q.Template, q.Mask);
        prepared = owned.get();
    }
    if (prepared == nullptr) {
        return false;  // match: empty template
    }
    const Rect tb = prepared->rect();
    const int tw = tb.Dx(), th = tb.Dy();
    if (tw < 1 || th < 1) {
        return false;  // match: empty template
    }
    const Rect ib = q.Image->Bounds();
    if (q.Gray != nullptr && q.Gray->Bounds().Size() != ib.Size()) {
        return false;  // match: gray image size does not match image size
    }
    const Rect roi = q.ROI.Intersect(ib);
    if (roi.Empty()) {
        out.Template = tb.String();
        return true;
    }
    if (roi.Dx() < tw || roi.Dy() < th) {
        out.Peak = roi.Min;
        return true;
    }

    // 小模板 1px 错位就会打穿；大厅底栏那种大图才粗扫。
    const int step = coarseStep(tw, th);

    nt::Gray ownedGray;
    const nt::Gray* gray = q.Gray;
    if (gray == nullptr) {
        ownedGray = ToGray(q.Image);
        gray = &ownedGray;
    }
    // ToGray(RGBA) 从零开始，而调用方也可传带原点和 stride 的 Gray 子图。
    // nccAt 使用相对于 Gray.Bounds().Min 的偏移，输出仍是截图坐标。
    const Point origin = ib.Min;
    // The prefix sums cover the ROI (every template footprint scanned lies
    // inside it). A caller-shared prefix of the same gray is reused as is; else
    // they are local to this call, hence single-thread. Exact integers either
    // way, so the scores do not depend on which covering prefix is used.
    const int px0 = roi.Min.X - origin.X, py0 = roi.Min.Y - origin.Y;
    const grayPrefix* pre = nullptr;
    std::unique_ptr<grayPrefix> large;
    if (q.Prefix != nullptr && gray == q.Gray && q.Prefix->covers(px0, py0, px0 + roi.Dx(), py0 + roi.Dy())) {
        pre = q.Prefix;
    } else if (roi.Dx() <= maxDotRun) {
        const size_t bytes = static_cast<size_t>(roi.Dx() + 1) * static_cast<size_t>(roi.Dy()) * 2 * sizeof(uint32_t);
        if (bytes > kKeepPrefixBytes) {
            large = largePrefixes().take();
            newGrayPrefix(*large, *gray, px0, py0, roi.Dx(), roi.Dy());
            pre = large.get();
        } else {
            newGrayPrefix(tlsPrefix, *gray, px0, py0, roi.Dx(), roi.Dy());
            pre = &tlsPrefix;
        }
    }
    Score best;
    best.Peak = roi.Min;
    // grids already scanned by this call. A refinement window overlaps the
    // coarser grids (the peak itself, and coarse points at +/-step); scoring
    // such a position again yields the same value, which can never pass the
    // strict '>' against a best that only grew since, so it is skipped.
    struct grid {
        int x0, y0, x1, y1, st;
        bool has(int x, int y) const {
            return x >= x0 && x <= x1 && y >= y0 && y <= y1 && (x - x0) % st == 0 && (y - y0) % st == 0;
        }
    };
    grid done[2];
    int doneCount = 0;
    auto skipped = [&](int x, int y) {
        return (doneCount > 0 && done[0].has(x, y)) || (doneCount > 1 && done[1].has(x, y));
    };
    // Rows wider than maxDotRun cannot use uint32 prefix sums (never in practice).
    auto value = [&](int x, int y) {
        return pre != nullptr ? prepared->at(*gray, *pre, x - origin.X, y - origin.Y)
                              : prepared->atDirect(*gray, x - origin.X, y - origin.Y);
    };
    const int64_t templatePixels = static_cast<int64_t>(prepared->data_.size());
    auto scan = [&](int x0, int y0, int x1, int y1, int st) {
        if (st < 1) st = 1;
        if (x0 < roi.Min.X) x0 = roi.Min.X;
        if (y0 < roi.Min.Y) y0 = roi.Min.Y;
        if (x1 > roi.Max.X - tw) x1 = roi.Max.X - tw;
        if (y1 > roi.Max.Y - th) y1 = roi.Max.Y - th;
        if (x0 > x1 || y0 > y1) return;
        const int nx = (x1 - x0) / st + 1, ny = (y1 - y0) / st + 1;
        if (ny >= 4 && static_cast<int64_t>(nx) * ny * templatePixels >= kParallelScanWork) {
            // A large scan (a big control over a wide screen region): score the
            // position rows on the pool into a buffer, then reduce here in the
            // exact sequential order with the same strict '>' (first maximum wins).
            thread_local std::vector<double> scanValues;
            scanValues.resize(static_cast<size_t>(nx) * static_cast<size_t>(ny));
            double* values = scanValues.data();
            const int bands = std::min(ny, 8);
            ParallelFor(bands, [&](int band) {
                for (int j = ny * band / bands; j < ny * (band + 1) / bands; ++j) {
                    const int y = y0 + j * st;
                    for (int i = 0; i < nx; ++i) {
                        const int x = x0 + i * st;
                        if (!skipped(x, y)) values[static_cast<size_t>(j) * nx + i] = value(x, y);
                    }
                }
            });
            for (int j = 0; j < ny; ++j) {
                const int y = y0 + j * st;
                for (int i = 0; i < nx; ++i) {
                    const int x = x0 + i * st;
                    if (skipped(x, y)) continue;
                    const double v = values[static_cast<size_t>(j) * nx + i];
                    if (v > best.Value) {
                        best.Value = v;
                        best.Peak = Pt(x, y);
                    }
                }
            }
            if (scanValues.capacity() > (static_cast<size_t>(32) << 10)) scanValues = std::vector<double>();
        } else {
            for (int y = y0; y <= y1; y += st) {
                for (int x = x0; x <= x1; x += st) {
                    if (skipped(x, y)) continue;
                    double v = value(x, y);
                    if (v > best.Value) {
                        best.Value = v;
                        best.Peak = Pt(x, y);
                    }
                }
            }
        }
        if (doneCount < 2) done[doneCount++] = grid{x0, y0, x1, y1, st};
    };
    scan(roi.Min.X, roi.Min.Y, roi.Max.X - tw, roi.Max.Y - th, step);
    if (step > 1) {
        // Refine the coarse peak. For large templates the old exhaustive
        // (2*step+1)^2 window was the dominant cost of the whole scene gate; a
        // medium grid narrows the window first, then 1-px search finishes.
        const int mid = refineStep(step);
        if (mid > 1) {
            // 注意：Go 的 scan 闭包按值接收参数，第二次 scan 的窗口中心是第一次细化后的峰值。
            scan(best.Peak.X - step, best.Peak.Y - step, best.Peak.X + step, best.Peak.Y + step, mid);
            scan(best.Peak.X - mid, best.Peak.Y - mid, best.Peak.X + mid, best.Peak.Y + mid, 1);
        } else {
            scan(best.Peak.X - step, best.Peak.Y - step, best.Peak.X + step, best.Peak.Y + step, 1);
        }
    }
    if (large) largePrefixes().give(std::move(large));
    out = std::move(best);
    return true;
}

// coarseStep is the sparse grid for large templates; small templates cannot
// tolerate a single pixel of misalignment and are scanned exhaustively.
int coarseStep(int tw, int th) {
    int minSide = std::min(tw, th);
    if (minSide < 48) {
        return 1;
    }
    return std::max(2, minSide / 8);
}

// refineStep is the medium grid used between the coarse scan and the final
// 1-px window. It stays within a quarter of the coarse step so the true peak,
// whose correlation lobe is a few pixels wide for HUD text, is not skipped.
int refineStep(int step) { return step / 4; }

// lumaDiv1000 is x / 1000 for x <= 255500 (= 1000*255 + 500) using only 32-bit
// lanes, so the conversion loop vectorizes: floor(floor(x/8)/125) == floor(x/1000)
// and (y * 33555) >> 22 == y / 125 for every y <= 31937 (exhaustively checked
// in tests/perf_kernels_test.cpp). Bit-identical to the division.
inline uint8_t lumaDiv1000(uint32_t x) { return static_cast<uint8_t>(((x >> 3) * 33555u) >> 22); }

// ToGray 把采集帧转成灰度。
// Capture frames are RGBA. The direct path preserves the same integer luma
// calculation and also handles subimages with a padded stride.
nt::Gray ToGray(const nt::RGBA* src) {
    if (src == nullptr) {
        return nt::Gray::New(MakeRect(0, 0, 0, 0));
    }
    const Rect b = src->Bounds();
    nt::Gray out = nt::Gray::NewUninit(MakeRect(0, 0, b.Dx(), b.Dy()));
    // RGBA subimages keep their parent Pix and Stride. 视图的 Pix 已指向 Rect.Min，
    // 因此子图按其自身当前像素转换，而不是父图左上角。
    const int w = b.Dx();
    for (int y = 0; y < b.Dy(); ++y) {
        const uint8_t* srcRow = src->Pix + static_cast<ptrdiff_t>(y) * src->Stride;
        uint8_t* dstRow = out.Pix + static_cast<ptrdiff_t>(y) * out.Stride;
        for (int x = 0; x < w; ++x) {
            const int i = x * 4;
            dstRow[x] = lumaDiv1000(299u * uint32_t(srcRow[i]) + 587u * uint32_t(srcRow[i + 1]) +
                                    114u * uint32_t(srcRow[i + 2]) + 500u);
        }
    }
    return out;
}

// ToGray(*image.Gray)：Go 用 draw.Draw(draw.Src) 复制，保留原点。
nt::Gray ToGray(const nt::Gray* src) {
    if (src == nullptr) {
        return nt::Gray::New(MakeRect(0, 0, 0, 0));
    }
    nt::Gray out = nt::Gray::NewUninit(src->Bounds());
    const int w = src->Bounds().Dx();
    for (int y = 0; y < src->Bounds().Dy(); ++y) {
        std::memcpy(out.Pix + static_cast<ptrdiff_t>(y) * out.Stride, src->Pix + static_cast<ptrdiff_t>(y) * src->Stride,
                    static_cast<size_t>(w));
    }
    return out;
}

// ToGrayAsset：Go 对 png.Decode 结果调用 ToGray。
//  * RGB PNG → *image.RGBA 快速路径（不透明，等价于下面 a=255）；
//  * 灰度 PNG → *image.Gray 复制（BitmapFactory 给出 r=g=b=Y，亮度公式恒等于 Y）；
//  * 带 alpha 的 PNG → *image.NRGBA 通用路径：At().RGBA() 预乘 16 位 → >>8 → 整数亮度。
// GoLumaFromNRGBA 对三种情况都与 Go 逐位一致；AssetStore::AddImage 注册时已算好（AssetImage::Luma），
// 这里直接返回共享视图（只读，不复制）。
nt::Gray ToGrayAsset(const AssetImage& src) { return src.Luma; }

// ScaleGray 双线性缩放到指定尺寸。
// 每列的源坐标/权重与每行的源行/权重只算一次（与逐像素写法是同一组 double 运算），
// 内层循环直接读行指针；像素值的加权和保持原来的运算顺序，结果逐位相同。
nt::Gray ScaleGray(const nt::Gray* src, int w, int h) {
    if (src == nullptr) {
        return nt::Gray::New(MakeRect(0, 0, 0, 0));
    }
    if (w < 1) w = 1;
    if (h < 1) h = 1;
    const Rect sb = src->Bounds();
    const int sw = sb.Dx(), sh = sb.Dy();
    if (sw < 1 || sh < 1) {
        return nt::Gray::New(MakeRect(0, 0, w, h));
    }
    nt::Gray dst = nt::Gray::NewUninit(MakeRect(0, 0, w, h));
    if (sw == w && sh == h) {
        for (int y = 0; y < h; ++y) {
            std::memcpy(dst.Pix + static_cast<ptrdiff_t>(y) * dst.Stride, src->Pix + static_cast<ptrdiff_t>(y) * src->Stride,
                        static_cast<size_t>(w));
        }
        return dst;
    }
    struct column {
        int x0, x1;
        double wx, omx;  // omx == 1 - wx
    };
    thread_local std::vector<column> columns;
    columns.resize(static_cast<size_t>(w));
    for (int x = 0; x < w; ++x) {
        double fx = (static_cast<double>(x) + 0.5) * static_cast<double>(sw) / static_cast<double>(w) - 0.5;
        if (fx < 0) fx = 0;
        int x0 = static_cast<int>(fx);
        int x1 = x0 + 1;
        if (x0 >= sw) x0 = sw - 1;
        if (x1 >= sw) x1 = sw - 1;
        const double wx = fx - static_cast<double>(x0);
        columns[static_cast<size_t>(x)] = column{x0, x1, wx, 1 - wx};
    }
    const column* cols = columns.data();
    for (int y = 0; y < h; ++y) {
        double fy = (static_cast<double>(y) + 0.5) * static_cast<double>(sh) / static_cast<double>(h) - 0.5;
        if (fy < 0) fy = 0;
        int y0 = static_cast<int>(fy);
        int y1 = y0 + 1;
        if (y0 >= sh) y0 = sh - 1;
        if (y1 >= sh) y1 = sh - 1;
        const double wy = fy - static_cast<double>(y0);
        const double omy = 1 - wy;
        const uint8_t* r0 = src->Pix + static_cast<ptrdiff_t>(y0) * src->Stride;
        const uint8_t* r1 = src->Pix + static_cast<ptrdiff_t>(y1) * src->Stride;
        uint8_t* out = dst.Pix + static_cast<ptrdiff_t>(y) * dst.Stride;
        for (int x = 0; x < w; ++x) {
            const column& c = cols[x];
            const double v00 = static_cast<double>(r0[c.x0]);
            const double v10 = static_cast<double>(r0[c.x1]);
            const double v01 = static_cast<double>(r1[c.x0]);
            const double v11 = static_cast<double>(r1[c.x1]);
            // Same operations, same order: (1-wx)*(1-wy)*v00 + wx*(1-wy)*v10 + (1-wx)*wy*v01 + wx*wy*v11.
            const double v = c.omx * omy * v00 + c.wx * omy * v10 + c.omx * wy * v01 + c.wx * wy * v11;
            out[x] = static_cast<uint8_t>(v + 0.5);
        }
    }
    return dst;
}

// CropRGBA 裁一块并复制成独立 RGBA。
nt::RGBA CropRGBA(const nt::RGBA* src, const Rect& r0) {
    if (src == nullptr) {
        return nt::RGBA{};
    }
    const Rect r = r0.Intersect(src->Bounds());
    if (r.Empty()) {
        return nt::RGBA::New(MakeRect(0, 0, 0, 0));
    }
    nt::RGBA out = nt::RGBA::NewUninit(MakeRect(0, 0, r.Dx(), r.Dy()));
    const size_t row = static_cast<size_t>(r.Dx()) * 4;
    for (int y = 0; y < r.Dy(); ++y) {
        std::memcpy(out.Pix + static_cast<ptrdiff_t>(y) * out.Stride, src->Pix + src->PixOffset(r.Min.X, r.Min.Y + y), row);
    }
    return out;
}

}  // namespace nt::match
