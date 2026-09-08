#pragma once

#ifdef __cplusplus
extern "C" {
#endif

/* ranges: 每组 6 个 double，顺序 hMin hMax sMin sMax vMin vMax，取值 [0,1] */
int cv_analyze_beads(
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
);

#ifdef __cplusplus
}
#endif
