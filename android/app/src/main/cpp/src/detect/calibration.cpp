// Owner: cpp-match-detect
// Port of: internal/detect/calibration.go
// Contract: include/nt/detect.h — see android/ARCHITECTURE.md.
#include <cmath>

#include "nt/detect.h"

namespace nt::detect {

Point SideCalibration::Map(double x, double y) const {
    return Pt(static_cast<int>(std::round(X0 + Scale * (x - RefX))), static_cast<int>(std::round(Y0 + Scale * (y - RefY))));
}

double RowHUDScale(const std::vector<BeadPosition>& row, const ContentArea& ca, double referenceWidth,
                   double referenceHeight) {
    if (!row.empty() && CalBeads(row[0].Cal)) {
        return row[0].Cal->Scale * HUDReferenceWidth / referenceWidth;
    }
    return ca.HUDScale(referenceWidth, referenceHeight);
}

}  // namespace nt::detect
