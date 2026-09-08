#include "cvbind.h"

#include <opencv2/core.hpp>
#include <opencv2/imgproc.hpp>

#include <algorithm>
#include <cmath>
#include <cstdint>
#include <vector>

namespace {

struct HSVRange {
    int h0, h1, s0, s1, v0, v1;
};

inline int clampi(int v, int lo, int hi) {
    return std::max(lo, std::min(v, hi));
}

inline bool in_diamond(int cx, int cy, int w, int h, int x, int y) {
    int a = std::max(1, w / 2);
    int b = std::max(1, h / 2);
    int dx = std::abs(x - cx);
    int dy = std::abs(y - cy);
    return dx * b + dy * a <= a * b;
}

inline bool in_hsv(const cv::Vec3b &p, const HSVRange &r) {
    int h = p[0], s = p[1], v = p[2];
    return h >= r.h0 && h <= r.h1 && s >= r.s0 && s <= r.s1 && v >= r.v0 && v <= r.v1;
}

std::vector<HSVRange> pack_ranges(const double *ranges, int offset, int n) {
    std::vector<HSVRange> out;
    out.reserve(static_cast<size_t>(n));
    for (int i = 0; i < n; i++) {
        const double *r = ranges + (offset + i) * 6;
        HSVRange hr;
        hr.h0 = clampi(static_cast<int>(std::lround(r[0] * 180.0)), 0, 180);
        hr.h1 = clampi(static_cast<int>(std::lround(r[1] * 180.0)), 0, 180);
        hr.s0 = clampi(static_cast<int>(std::lround(r[2] * 255.0)), 0, 255);
        hr.s1 = clampi(static_cast<int>(std::lround(r[3] * 255.0)), 0, 255);
        hr.v0 = clampi(static_cast<int>(std::lround(r[4] * 255.0)), 0, 255);
        hr.v1 = clampi(static_cast<int>(std::lround(r[5] * 255.0)), 0, 255);
        out.push_back(hr);
    }
    return out;
}

bool any_range(const cv::Vec3b &p, const std::vector<HSVRange> &rs) {
    for (const auto &r : rs) {
        if (in_hsv(p, r)) {
            return true;
        }
    }
    return false;
}

void count_at(
    const cv::Mat &hsv,
    int cx,
    int cy,
    int sample_w,
    int sample_h,
    const std::vector<HSVRange> &dark,
    const std::vector<HSVRange> &light,
    const std::vector<HSVRange> &gold,
    int *ol,
    int *od,
    int *og,
    int *oo
) {
    int light_n = 0, dark_n = 0, gold_n = 0, other_n = 0;
    const int x0 = std::max(0, cx - sample_w / 2);
    const int y0 = std::max(0, cy - sample_h / 2);
    const int x1 = std::min(hsv.cols - 1, cx + sample_w / 2);
    const int y1 = std::min(hsv.rows - 1, cy + sample_h / 2);
    for (int y = y0; y <= y1; y++) {
        const cv::Vec3b *row = hsv.ptr<cv::Vec3b>(y);
        for (int x = x0; x <= x1; x++) {
            if (!in_diamond(cx, cy, sample_w, sample_h, x, y)) {
                continue;
            }
            const cv::Vec3b &p = row[x];
            if (any_range(p, gold)) {
                gold_n++;
            } else if (any_range(p, light)) {
                light_n++;
            } else if (any_range(p, dark)) {
                dark_n++;
            } else {
                other_n++;
            }
        }
    }
    *ol = light_n;
    *od = dark_n;
    *og = gold_n;
    *oo = other_n;
}

} // namespace

extern "C" int cv_analyze_beads(
    const unsigned char *rgba,
    int width,
    int height,
    int stride,
    const int *xs,
    const int *ys,
    int nbeads,
    int sample_w,
    int sample_h,
    int search_radius,
    const double *ranges,
    int n_dark,
    int n_light,
    int n_gold,
    int *out_light,
    int *out_dark,
    int *out_gold,
    int *out_other,
    int *out_cx,
    int *out_cy
) {
    if (rgba == nullptr || width <= 0 || height <= 0 || stride < width * 4 || nbeads <= 0) {
        return -1;
    }
    if (xs == nullptr || ys == nullptr || ranges == nullptr) {
        return -1;
    }
    if (sample_w < 5) {
        sample_w = 5;
    }
    if (sample_h < 7) {
        sample_h = 7;
    }
    if (search_radius < 0) {
        search_radius = 0;
    }

    try {
        cv::Mat src(height, width, CV_8UC4, const_cast<unsigned char *>(rgba), static_cast<size_t>(stride));
        cv::Mat bgr, hsv;
        cv::cvtColor(src, bgr, cv::COLOR_RGBA2BGR);
        cv::cvtColor(bgr, hsv, cv::COLOR_BGR2HSV);

        auto dark = pack_ranges(ranges, 0, n_dark);
        auto light = pack_ranges(ranges, n_dark, n_light);
        auto gold = pack_ranges(ranges, n_dark + n_light, n_gold);

        for (int i = 0; i < nbeads; i++) {
            int best_score = -1;
            int bl = 0, bd = 0, bg = 0, bo = 0;
            int bx = xs[i], by = ys[i];
            const int step = search_radius >= 4 ? 2 : 1;
            for (int oy = -search_radius; oy <= search_radius; oy += step) {
                for (int ox = -search_radius; ox <= search_radius; ox += step) {
                    int cx = clampi(xs[i] + ox, 0, width - 1);
                    int cy = clampi(ys[i] + oy, 0, height - 1);
                    int l = 0, d = 0, g = 0, o = 0;
                    count_at(hsv, cx, cy, sample_w, sample_h, dark, light, gold, &l, &d, &g, &o);
                    // 只用亮/暗给搜索打分，金色不参与，避免吸到血条。
                    int score = l + d;
                    if (score > best_score) {
                        best_score = score;
                        bl = l;
                        bd = d;
                        bg = g;
                        bo = o;
                        bx = cx;
                        by = cy;
                    }
                }
            }
            out_light[i] = bl;
            out_dark[i] = bd;
            out_gold[i] = bg;
            out_other[i] = bo;
            out_cx[i] = bx;
            out_cy[i] = by;
        }
        return 0;
    } catch (...) {
        return -2;
    }
}
