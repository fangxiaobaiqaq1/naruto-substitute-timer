// Host-only randomized equivalence tests for the performance work
// (docs/性能优化-android-native.md). Every optimized path must return results
// bit-identical to a straightforward reference:
//   * every u8 dot-product kernel (scalar / SSE2 / AVX2 when the CPU has it,
//     and the AArch64 NEON / DotProd kernels through tests/neon_emu.h) vs scalar,
//     over all short lengths, random lengths/alignments and the uint32 extreme;
//   * match::NCC::Match (compact runs, uint32 prefix sums, shared prefixes, every
//     available kernel) vs a per-pixel reference of the original algorithm.
// Run tests/perf_kernels_test.sh.
#include <algorithm>
#include <cmath>
#include <cstdint>
#include <cstdio>
#include <cstdlib>
#include <cstring>
#include <functional>
#include <random>
#include <string>
#include <vector>

#define NT_DOT_NEON_EMULATED 1
#include "neon_emu.h"
#include "../src/match/ncc_dot.h"

#include "nt/assets.h"
#include "nt/image.h"
#include "nt/match.h"
#include "nt/ninja_internal.h"

namespace {

int checks = 0;
void check(bool ok, const std::string& message) {
    ++checks;
    if (!ok) {
        std::fprintf(stderr, "FAIL: %s\n", message.c_str());
        std::exit(1);
    }
}

bool sameDouble(double a, double b) { return std::memcmp(&a, &b, sizeof a) == 0; }

using DotFn = uint32_t (*)(const uint8_t*, const uint8_t*, int);

struct namedDot {
    const char* name;
    DotFn fn;
};

std::vector<namedDot> dotKernels() {
    std::vector<namedDot> out{{"scalar", nt::match::dot::Scalar}};
#if defined(NT_DOT_X86)
    out.push_back({"sse2", nt::match::dot::SSE2});
    __builtin_cpu_init();
    if (__builtin_cpu_supports("avx2")) out.push_back({"avx2", nt::match::dot::AVX2});
    else std::printf("note: CPU has no AVX2; the avx2 kernel is not executed here\n");
#endif
    out.push_back({"neon(emulated)", nt::match::dot::NEON});
    out.push_back({"dotprod(emulated)", nt::match::dot::DotProd});
    return out;
}

void testDotKernels(std::mt19937& rng) {
    const auto kernels = dotKernels();
    const int maxLen = nt::match::maxDotRun;
    std::vector<uint8_t> a(maxLen + 128), b(maxLen + 128);
    auto fill = [&](int mode) {
        for (size_t i = 0; i < a.size(); ++i) {
            if (mode == 0) {
                a[i] = static_cast<uint8_t>(rng());
                b[i] = static_cast<uint8_t>(rng());
            } else if (mode == 1) {
                a[i] = 255;
                b[i] = 255;
            } else {
                a[i] = static_cast<uint8_t>(rng() & 1 ? 255 : rng());
                b[i] = static_cast<uint8_t>(rng() & 1 ? 0 : 255);
            }
        }
    };
    auto run = [&](int offA, int offB, int n) {
        const uint32_t want = nt::match::dot::Scalar(a.data() + offA, b.data() + offB, n);
        for (const auto& k : kernels) {
            const uint32_t got = k.fn(a.data() + offA, b.data() + offB, n);
            if (got != want) {
                check(false, std::string(k.name) + " dot n=" + std::to_string(n) + " offA=" + std::to_string(offA) +
                                 " offB=" + std::to_string(offB) + " got " + std::to_string(got) + " want " +
                                 std::to_string(want));
            }
            ++checks;
        }
    };
    for (int mode = 0; mode < 3; ++mode) {
        fill(mode);
        for (int n = 0; n <= 300; ++n) {
            for (int off = 0; off < 17; ++off) run(off, (off * 7 + n) % 64, n);
        }
        for (int i = 0; i < 2000; ++i) {
            const int n = static_cast<int>(rng() % 5000);
            run(static_cast<int>(rng() % 64), static_cast<int>(rng() % 64), n);
        }
        for (int n : {maxLen - 17, maxLen - 1, maxLen}) run(static_cast<int>(rng() % 64), static_cast<int>(rng() % 64), n);
    }
    // The uint32 extreme: 255*255*maxDotRun = 4294966275 exactly.
    fill(1);
    check(nt::match::dot::Scalar(a.data(), b.data(), maxLen) == 4294966275u, "scalar extreme");
    for (const auto& k : kernels) {
        check(k.fn(a.data() + 3, b.data() + 5, maxLen) == 4294966275u, std::string(k.name) + " extreme sum");
    }
}

// ---- reference NCC: the original per-pixel algorithm (uint64 sums, scalar) ----

struct refTemplate {
    nt::Gray templ, mask;
    bool hasMask = false;
    double count = 0, sum = 0, variance = 0;
};

refTemplate makeRef(const nt::Gray& templ, const nt::Gray* mask) {
    refTemplate r;
    r.templ = templ;
    r.hasMask = mask != nullptr;
    if (mask) r.mask = *mask;
    int64_t count = 0, sum = 0, squares = 0;
    const int w = templ.Bounds().Dx(), h = templ.Bounds().Dy();
    for (int y = 0; y < h; ++y) {
        if (mask && y >= mask->Bounds().Dy()) continue;
        const int end = mask ? std::min(w, mask->Bounds().Dx()) : w;
        for (int x = 0; x < end; ++x) {
            if (mask && mask->Pix[y * mask->Stride + x] == 0) continue;
            const int64_t v = templ.Pix[y * templ.Stride + x];
            count++;
            sum += v;
            squares += v * v;
        }
    }
    r.count = static_cast<double>(count);
    r.sum = static_cast<double>(sum);
    if (count > 0) r.variance = static_cast<double>(squares) - r.sum * r.sum / r.count;
    return r;
}

double refAt(const refTemplate& t, const nt::Gray& img, int ox, int oy) {
    if (t.count < 8) return 0;
    uint64_t sum = 0, squares = 0, product = 0;
    const int w = t.templ.Bounds().Dx(), h = t.templ.Bounds().Dy();
    for (int y = 0; y < h; ++y) {
        if (t.hasMask && y >= t.mask.Bounds().Dy()) continue;
        const int end = t.hasMask ? std::min(w, t.mask.Bounds().Dx()) : w;
        for (int x = 0; x < end; ++x) {
            if (t.hasMask && t.mask.Pix[y * t.mask.Stride + x] == 0) continue;
            const uint64_t v = img.Pix[static_cast<ptrdiff_t>(oy + y) * img.Stride + ox + x];
            sum += v;
            squares += v * v;
            product += v * t.templ.Pix[y * t.templ.Stride + x];
        }
    }
    const double sumI = static_cast<double>(sum);
    const double variance = static_cast<double>(squares) - sumI * sumI / t.count;
    if (variance <= 1e-6 || t.variance <= 1e-6) {
        if (variance > 1e-6 || t.variance > 1e-6) return 0;
        if (std::fabs(sumI / t.count - t.sum / t.count) <= 6) return 1;
        return 0;
    }
    const double score = (static_cast<double>(product) - sumI * t.sum / t.count) / std::sqrt(variance * t.variance);
    return std::min(1.0, std::max(0.0, score));
}

// refMatch mirrors the original NCC::Match scan (coarse grid, medium grid, 1px).
bool refMatch(const nt::RGBA& image, const nt::Gray* grayIn, const nt::Rect& roiIn, const refTemplate& t,
              nt::match::Score& out) {
    out = nt::match::Score{};
    const int tw = t.templ.Bounds().Dx(), th = t.templ.Bounds().Dy();
    if (tw < 1 || th < 1) return false;
    const nt::Rect ib = image.Bounds();
    if (grayIn && grayIn->Bounds().Size() != ib.Size()) return false;
    const nt::Rect roi = roiIn.Intersect(ib);
    if (roi.Empty()) {
        out.Template = t.templ.Bounds().String();
        return true;
    }
    if (roi.Dx() < tw || roi.Dy() < th) {
        out.Peak = roi.Min;
        return true;
    }
    const int step = nt::match::coarseStep(tw, th);
    nt::Gray owned;
    const nt::Gray* gray = grayIn;
    if (!gray) {
        owned = nt::match::ToGray(&image);
        gray = &owned;
    }
    const nt::Point origin = ib.Min;
    nt::match::Score best;
    best.Peak = roi.Min;
    auto scan = [&](int x0, int y0, int x1, int y1, int st) {
        if (st < 1) st = 1;
        x0 = std::max(x0, roi.Min.X);
        y0 = std::max(y0, roi.Min.Y);
        x1 = std::min(x1, roi.Max.X - tw);
        y1 = std::min(y1, roi.Max.Y - th);
        for (int y = y0; y <= y1; y += st)
            for (int x = x0; x <= x1; x += st) {
                const double v = refAt(t, *gray, x - origin.X, y - origin.Y);
                if (v > best.Value) {
                    best.Value = v;
                    best.Peak = nt::Pt(x, y);
                }
            }
    };
    scan(roi.Min.X, roi.Min.Y, roi.Max.X - tw, roi.Max.Y - th, step);
    if (step > 1) {
        const int mid = nt::match::refineStep(step);
        if (mid > 1) {
            scan(best.Peak.X - step, best.Peak.Y - step, best.Peak.X + step, best.Peak.Y + step, mid);
            scan(best.Peak.X - mid, best.Peak.Y - mid, best.Peak.X + mid, best.Peak.Y + mid, 1);
        } else {
            scan(best.Peak.X - step, best.Peak.Y - step, best.Peak.X + step, best.Peak.Y + step, 1);
        }
    }
    out = best;
    return true;
}

// Smooth-ish random content so NCC peaks are meaningful; mode 1 = flat.
void fillGray(nt::Gray& g, std::mt19937& rng, int mode) {
    const nt::Rect b = g.Bounds();
    const int base = static_cast<int>(rng() % 256);
    for (int y = 0; y < b.Dy(); ++y)
        for (int x = 0; x < b.Dx(); ++x) {
            int v = base;
            if (mode == 0) v = static_cast<int>(rng() % 256);
            if (mode == 2) v = (base + 3 * x + 5 * y + static_cast<int>(rng() % 40)) & 255;
            g.Pix[y * g.Stride + x] = static_cast<uint8_t>(v);
        }
}

nt::RGBA rgbaFromGray(const nt::Gray& g, nt::Point origin) {
    const nt::Rect b = g.Bounds();
    nt::RGBA img = nt::RGBA::New(nt::Rect(origin, origin.Add(b.Size())));
    for (int y = 0; y < b.Dy(); ++y)
        for (int x = 0; x < b.Dx(); ++x) {
            uint8_t* p = img.Pix + y * img.Stride + 4 * x;
            p[0] = p[1] = p[2] = g.Pix[y * g.Stride + x];
            p[3] = 255;
        }
    return img;
}

void testMatch(std::mt19937& rng) {
    const auto kernels = nt::match::NCCKernels();
    const std::string original = nt::match::NCCKernel();
    for (int iter = 0; iter < 600; ++iter) {
        int iw = 1 + static_cast<int>(rng() % 160), ih = 1 + static_cast<int>(rng() % 120);
        if (iter % 60 == 0) {
            // Large search areas take the parallel prefix / parallel scan paths.
            iw = 300 + static_cast<int>(rng() % 120);
            ih = 230 + static_cast<int>(rng() % 60);
        }
        const nt::Point origin(static_cast<int>(rng() % 50) - 25, static_cast<int>(rng() % 50) - 25);
        nt::Gray frame = nt::Gray::New(nt::MakeRect(0, 0, iw, ih));
        fillGray(frame, rng, iter % 7 == 0 ? 1 : (iter % 3 == 0 ? 2 : 0));
        const nt::RGBA image = rgbaFromGray(frame, origin);  // ToGray(RGBA r=g=b=v) == v
        const int tw = 1 + static_cast<int>(rng() % std::min(iw + 4, 70));
        const int th = 1 + static_cast<int>(rng() % std::min(ih + 4, 60));
        nt::Gray templ = nt::Gray::New(nt::MakeRect(0, 0, tw, th));
        if (rng() % 3 == 0 && tw <= iw && th <= ih) {
            // Cut the template from the frame (exact peak) and perturb it a little.
            const int x0 = static_cast<int>(rng() % (iw - tw + 1)), y0 = static_cast<int>(rng() % (ih - th + 1));
            for (int y = 0; y < th; ++y)
                for (int x = 0; x < tw; ++x)
                    templ.Pix[y * tw + x] =
                        static_cast<uint8_t>(std::min<uint32_t>(255u, frame.Pix[(y0 + y) * iw + x0 + x] + (rng() % 9)));
        } else {
            fillGray(templ, rng, iter % 11 == 0 ? 1 : 0);
        }
        nt::Gray mask;
        const bool masked = rng() % 2 == 0;
        if (masked) {
            // Masks may be smaller/larger than the template and need not share its origin.
            const int mw = std::max(1, tw + static_cast<int>(rng() % 9) - 4);
            const int mh = std::max(1, th + static_cast<int>(rng() % 9) - 4);
            mask = nt::Gray::New(nt::MakeRect(3, -2, 3 + mw, -2 + mh));
            const int kind = static_cast<int>(rng() % 3);
            for (int y = 0; y < mh; ++y)
                for (int x = 0; x < mw; ++x) {
                    bool on = rng() % 4 != 0;
                    if (kind == 1) on = std::abs(x - mw / 2) + std::abs(y - mh / 2) <= std::min(mw, mh) / 2;
                    if (kind == 2) on = (x / 5 + y) % 3 != 0;
                    mask.Pix[y * mask.Stride + x] = on ? static_cast<uint8_t>(1 + rng() % 255) : 0;
                }
        }
        const refTemplate ref = makeRef(templ, masked ? &mask : nullptr);
        auto prepared = nt::match::PrepareNCC(&templ, masked ? &mask : nullptr);
        nt::Rect roi = nt::MakeRect(origin.X + static_cast<int>(rng() % (iw + 20)) - 10,
                                    origin.Y + static_cast<int>(rng() % (ih + 20)) - 10,
                                    origin.X + static_cast<int>(rng() % (iw + 20)) - 10,
                                    origin.Y + static_cast<int>(rng() % (ih + 20)) - 10);
        if (rng() % 4 == 0) roi = image.Bounds();
        // A gray with a different origin than the image (allowed by Query).
        nt::Gray gray = nt::match::ToGray(&image);
        nt::match::Score want;
        const bool wantOK = refMatch(image, &gray, roi, ref, want);
        nt::match::grayPrefix shared;
        nt::match::newGrayPrefix(shared, gray, 0, 0, iw, ih);
        if (prepared && tw <= iw && th <= ih) {
            for (int k = 0; k < 8; ++k) {
                const int ox = static_cast<int>(rng() % (iw - tw + 1)), oy = static_cast<int>(rng() % (ih - th + 1));
                check(sameDouble(prepared->atDirect(gray, ox, oy), refAt(ref, gray, ox, oy)), "atDirect");
                check(sameDouble(prepared->at(gray, shared, ox, oy), refAt(ref, gray, ox, oy)), "at");
            }
        }
        for (const std::string& k : kernels) {
            check(nt::match::SetNCCKernel(k), "set kernel " + k);
            for (int variant = 0; variant < 4; ++variant) {
                nt::match::Query q;
                q.Image = &image;
                q.ROI = roi;
                if (variant != 1) q.Gray = &gray;
                if (variant == 2) q.Prefix = &shared;
                if (variant == 3) {
                    q.Template = &templ;
                    q.Mask = masked ? &mask : nullptr;
                } else {
                    q.Prepared = prepared.get();
                }
                nt::match::Score got;
                const bool ok = nt::match::NCC{}.Match(q, got);
                const std::string where = "match iter=" + std::to_string(iter) + " kernel=" + k +
                                          " variant=" + std::to_string(variant);
                check(ok == wantOK, where + " ok");
                check(sameDouble(got.Value, want.Value), where + " value " + std::to_string(got.Value) + " want " +
                                                             std::to_string(want.Value));
                check(got.Peak == want.Peak, where + " peak");
                check(got.Template == want.Template, where + " template");
            }
        }
    }
    check(nt::match::SetNCCKernel(original), "restore kernel");
}

// refScaleGray is the original per-pixel ScaleGray (GrayAt/SetGray, weights recomputed per pixel).
nt::Gray refScaleGray(const nt::Gray* src, int w, int h) {
    if (w < 1) w = 1;
    if (h < 1) h = 1;
    const nt::Rect sb = src->Bounds();
    const int sw = sb.Dx(), sh = sb.Dy();
    nt::Gray dst = nt::Gray::New(nt::MakeRect(0, 0, w, h));
    if (sw < 1 || sh < 1) return dst;
    for (int y = 0; y < h; ++y) {
        double fy = (static_cast<double>(y) + 0.5) * static_cast<double>(sh) / static_cast<double>(h) - 0.5;
        if (fy < 0) fy = 0;
        int y0 = static_cast<int>(fy);
        int y1 = y0 + 1;
        if (y0 >= sh) y0 = sh - 1;
        if (y1 >= sh) y1 = sh - 1;
        const double wy = fy - static_cast<double>(y0);
        for (int x = 0; x < w; ++x) {
            double fx = (static_cast<double>(x) + 0.5) * static_cast<double>(sw) / static_cast<double>(w) - 0.5;
            if (fx < 0) fx = 0;
            int x0 = static_cast<int>(fx);
            int x1 = x0 + 1;
            if (x0 >= sw) x0 = sw - 1;
            if (x1 >= sw) x1 = sw - 1;
            const double wx = fx - static_cast<double>(x0);
            const double v00 = static_cast<double>(src->GrayAt(sb.Min.X + x0, sb.Min.Y + y0));
            const double v10 = static_cast<double>(src->GrayAt(sb.Min.X + x1, sb.Min.Y + y0));
            const double v01 = static_cast<double>(src->GrayAt(sb.Min.X + x0, sb.Min.Y + y1));
            const double v11 = static_cast<double>(src->GrayAt(sb.Min.X + x1, sb.Min.Y + y1));
            const double v = (1 - wx) * (1 - wy) * v00 + wx * (1 - wy) * v10 + (1 - wx) * wy * v01 + wx * wy * v11;
            dst.SetGray(x, y, static_cast<uint8_t>(v + 0.5));
        }
    }
    return dst;
}

bool sameGray(const nt::Gray& a, const nt::Gray& b) {
    if (a.Bounds() != b.Bounds()) return false;
    for (int y = a.Bounds().Min.Y; y < a.Bounds().Max.Y; ++y)
        for (int x = a.Bounds().Min.X; x < a.Bounds().Max.X; ++x)
            if (a.GrayAt(x, y) != b.GrayAt(x, y)) return false;
    return true;
}

void testScaleAndGray(std::mt19937& rng) {
    // ToGray's division-free luma must equal (299r + 587g + 114b + 500) / 1000
    // for every reachable numerator.
    for (uint32_t x = 500; x <= 255500; ++x) {
        if ((((x >> 3) * 33555u) >> 22) != x / 1000u) check(false, "lumaDiv1000 " + std::to_string(x));
    }
    for (int r = 0; r < 256; r += 5)
        for (int g = 0; g < 256; g += 3)
            for (int b = 0; b < 256; ++b) {
                nt::RGBA px = nt::RGBA::New(nt::MakeRect(0, 0, 1, 1));
                px.Pix[0] = static_cast<uint8_t>(r);
                px.Pix[1] = static_cast<uint8_t>(g);
                px.Pix[2] = static_cast<uint8_t>(b);
                if (nt::match::ToGray(&px).Pix[0] != nt::GoLumaRGBA(r, g, b)) check(false, "ToGray pixel");
            }
    for (int iter = 0; iter < 3000; ++iter) {
        const int pw = 1 + static_cast<int>(rng() % 90), ph = 1 + static_cast<int>(rng() % 90);
        nt::Gray parent = nt::Gray::New(nt::MakeRect(-7, 4, -7 + pw, 4 + ph));
        fillGray(parent, rng, iter % 5 == 0 ? 2 : 0);
        // A subimage keeps the parent stride and a non-zero origin.
        const int x0 = -7 + static_cast<int>(rng() % pw), y0 = 4 + static_cast<int>(rng() % ph);
        const nt::Gray src = parent.SubImage(nt::MakeRect(x0, y0, x0 + 1 + static_cast<int>(rng() % pw),
                                                          y0 + 1 + static_cast<int>(rng() % ph)));
        int w = static_cast<int>(rng() % 200) - 2, h = static_cast<int>(rng() % 200) - 2;
        if (iter % 9 == 0) {
            w = src.Bounds().Dx();
            h = src.Bounds().Dy();
        }
        check(sameGray(nt::match::ScaleGray(&src, w, h), refScaleGray(&src, w, h)),
              "ScaleGray " + std::to_string(src.Bounds().Dx()) + "x" + std::to_string(src.Bounds().Dy()) + " -> " +
                  std::to_string(w) + "x" + std::to_string(h));
    }
    for (int iter = 0; iter < 300; ++iter) {
        const int pw = 1 + static_cast<int>(rng() % 70), ph = 1 + static_cast<int>(rng() % 50);
        nt::RGBA parent = nt::RGBA::New(nt::MakeRect(3, -5, 3 + pw, -5 + ph));
        for (int i = 0; i < 4 * pw * ph; ++i) parent.Pix[i] = static_cast<uint8_t>(rng());
        const int x0 = 3 + static_cast<int>(rng() % pw), y0 = -5 + static_cast<int>(rng() % ph);
        const nt::RGBA sub = parent.SubImage(nt::MakeRect(x0, y0, x0 + 1 + static_cast<int>(rng() % pw),
                                                          y0 + 1 + static_cast<int>(rng() % ph)));
        const nt::Gray g = nt::match::ToGray(&sub);
        bool ok = g.Bounds() == nt::MakeRect(0, 0, sub.Bounds().Dx(), sub.Bounds().Dy());
        for (int y = 0; ok && y < sub.Bounds().Dy(); ++y)
            for (int x = 0; ok && x < sub.Bounds().Dx(); ++x) {
                const nt::Color c = sub.RGBAAt(sub.Bounds().Min.X + x, sub.Bounds().Min.Y + y);
                ok = g.GrayAt(x, y) == nt::GoLumaRGBA(c.R, c.G, c.B);
            }
        check(ok, "ToGray");
    }
}

// AssetStore keeps only the Go-path luma and the alpha >= 128 bits: both must
// equal what the RGBA-based code computed (ToGrayAsset / avatarGrayMask+FaceMask).
void testAssetConversion(std::mt19937& rng) {
    for (int iter = 0; iter < 400; ++iter) {
        const int w = 1 + static_cast<int>(rng() % 140), h = 1 + static_cast<int>(rng() % 90);
        const size_t stride = static_cast<size_t>(4 * w + static_cast<int>(rng() % 9));
        std::vector<uint8_t> rgba(stride * static_cast<size_t>(h));
        const int alphaMode = iter % 4;  // 0 opaque, 1 binary, 2 random, 3 around 128
        for (int y = 0; y < h; ++y)
            for (int x = 0; x < w; ++x) {
                uint8_t* p = rgba.data() + stride * static_cast<size_t>(y) + 4 * static_cast<size_t>(x);
                p[0] = static_cast<uint8_t>(rng());
                p[1] = static_cast<uint8_t>(rng());
                p[2] = static_cast<uint8_t>(rng());
                uint8_t a = 255;
                if (alphaMode == 1) a = rng() % 2 ? 255 : 0;
                if (alphaMode == 2) a = static_cast<uint8_t>(rng());
                if (alphaMode == 3) a = static_cast<uint8_t>(126 + rng() % 4);
                p[3] = a;
            }
        const std::string key = "test/asset" + std::to_string(iter) + ".png";
        nt::AssetStore::Instance().AddImage(key, w, h, rgba.data(), stride);
        auto img = nt::AssetStore::Instance().Image(key);
        check(img && img->Width == w && img->Height == h, "asset size");
        const nt::Gray luma = nt::match::ToGrayAsset(*img);
        check(luma.Bounds() == nt::MakeRect(0, 0, w, h), "asset luma bounds");
        nt::Gray alpha = nt::Gray::New(nt::MakeRect(0, 0, w, h));
        bool allOpaque = true;
        for (int y = 0; y < h; ++y)
            for (int x = 0; x < w; ++x) {
                const uint8_t* p = rgba.data() + stride * static_cast<size_t>(y) + 4 * static_cast<size_t>(x);
                check(luma.GrayAt(x, y) == nt::GoLumaFromNRGBA(p[0], p[1], p[2], p[3]), "asset luma");
                const bool opaque = nt::GoAlpha16(p[3]) >= 0x8000;
                allOpaque = allOpaque && opaque;
                check(img->Opaque(x, y) == opaque, "asset opaque bit");
                if (opaque) alpha.SetGray(x, y, 255);
            }
        check(img->OpaqueBits.empty() == allOpaque, "opaque bits only when needed");
        check(sameGray(nt::ninja::avatarFaceMask(*img), nt::ninja::avatarFaceMask(alpha.Bounds(), alpha)),
              "avatar face mask from bits");
        nt::Gray g, m;
        nt::ninja::avatarGrayMask(*img, g, m);
        check(sameGray(g, luma) && sameGray(m, alpha), "avatarGrayMask");
    }
}

}  // namespace

int main() {
    std::mt19937 rng(20261021);
    std::printf("NCC kernels on this CPU:");
    for (const auto& k : nt::match::NCCKernels()) std::printf(" %s", k.c_str());
    std::printf(" (active: %s)\n", nt::match::NCCKernel());
    testDotKernels(rng);
    testMatch(rng);
    testScaleAndGray(rng);
    testAssetConversion(rng);
    std::printf("perf_kernels_test: %d checks passed\n", checks);
    return 0;
}
