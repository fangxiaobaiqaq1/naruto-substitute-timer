// Port of: internal/config/calibration_limits.go (docs/区域校准.md 框的大小限制).
// Same operations in the same order as Go and Kotlin (CalibrationLimits.kt), in
// double, without FMA (-ffp-contract=off): the three accept exactly the same boxes.
#include "nt/config.h"

namespace nt::config {

void CalibrationBoxUnits(bool avatar, const NormalizedRect& r, double& u, double& v) {
    if (avatar) {
        u = r.Width * 960 / 68.5;
        v = r.Height * 540 / 68.5;
        return;
    }
    u = r.Width * 16;
    v = r.Height * 36;
}

CalibrationIssue CalibrationRectIssue(bool avatar, const NormalizedRect& r) {
    double u = 0, v = 0;
    CalibrationBoxUnits(avatar, r, u, v);
    double k = u, shortSide = v;
    if (v > u) {
        k = v;
        shortSide = u;
    }
    if (!(k <= CalibrationMaxScale)) return CalibrationIssue::TooLarge;
    if (!(k >= CalibrationMinScale)) return CalibrationIssue::TooSmall;
    if (!(shortSide >= k * CalibrationMinShape)) return CalibrationIssue::BadShape;
    return CalibrationIssue::None;
}

}  // namespace nt::config
