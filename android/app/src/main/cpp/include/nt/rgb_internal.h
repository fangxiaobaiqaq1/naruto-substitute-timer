// nt/rgb_internal.h — internal/engine/rgb 的未导出函数（owner: cpp-rgb，仅 rgb 模块内部使用）。
// 文件对应：rgb.cpp(rgb.go) special.cpp blue_body.cpp blue_glint.cpp body_contrast.cpp dark_glint.cpp
//           gold_body.cpp gold_glint.cpp purple_glint.cpp purple_halo.cpp purple_lobes.cpp
//           warm_glint.cpp xiayin_palette.cpp
// cpp-rgb 可自由修改本文件。
#pragma once

#include <array>
#include <string>
#include <vector>

#include "nt/config.h"
#include "nt/detect.h"
#include "nt/engine.h"
#include "nt/ninja.h"
#include "nt/result.h"

namespace nt::rgb {

using detect::BeadPosition;
using detect::BeadState;
using engine::BeadInfo;

// rgb.go
std::vector<BeadInfo> sampleCalibrated(const RGBA* img, const std::vector<BeadPosition>& positions,
                                       const detect::ContentArea& area, const config::VisionConfig& cfg);
BeadState sampleUnverifiedSpecial(const RGBA* img, const BeadPosition& p, int w, int h, int guard,
                                  ninja::Palette palette, double& conf);
std::vector<BeadInfo> sampleCalibratedSpecial(const RGBA* img, const std::vector<BeadPosition>& positions,
                                              const detect::ContentArea& area, const config::VisionConfig& cfg,
                                              const std::array<ninja::Readout, 2>& identified);
void sampleCalibratedRow(const RGBA* img, const std::vector<BeadPosition>& row, const detect::ContentArea& area,
                         const config::VisionConfig& cfg, ninja::Palette palette, bool xiayin,
                         const ninja::Readout& identified, Point delta, std::vector<BeadInfo>& infos,
                         std::vector<BeadState>& states);
bool allUnknownStates(const std::vector<BeadState>& states);
double rowInfosScore(const std::vector<BeadInfo>& infos);
// 返回 chosen，name 写入 outName（"camp"/"duel"）。
const std::vector<BeadInfo>& pickLayout(const std::vector<BeadInfo>& camp, const std::vector<BeadInfo>& duel,
                                        std::string& outName);
std::vector<BeadInfo> sampleLayout(const RGBA* img, const std::vector<BeadPosition>& positions);
int legalSides(const std::vector<BeadInfo>& beads);
int knownCount(const std::vector<BeadInfo>& beads);
// label 生成豆子编号（L1..L6 / R1..R6）。
std::string label(engine::Side side, int idx);
// locateBead 返回状态并写回定位到的 (nx, ny)。
BeadState locateBead(const RGBA* img, int cx, int cy, int idx, int& nx, int& ny);
int classifiedAt(const RGBA* img, int cx, int cy);
BeadState voteBead(const RGBA* img, int cx, int cy);

// special.go
void applyNames(engine::Result& res, const std::array<ninja::Readout, 2>& readouts);
BeadState specialPixel(ninja::Palette palette, int r, int g, int b);
bool specialWash(const RGBA* img, const BeadPosition& p, ninja::Palette palette, int guard);
bool redLowerBody(const RGBA* img, const BeadPosition& p, int w, int h);
bool isolatedRedHalo(const RGBA* img, const BeadPosition& p, int guard);

// blue_body.go / blue_glint.go
bool blueBodyVisible(const RGBA* img, const BeadPosition& p, int w, int h);
bool blueGlintBody(const RGBA* img, const BeadPosition& p, int w, int h, int gap);

// body_contrast.go
int beadHalfPitch(const std::vector<BeadPosition>& positions, const BeadPosition& p, int fallback);
// bodyContrast pools a few adjacent, already color-verified interior rows.
struct bodyContrast {
    struct row { int left = 0, right = 0; };
    std::vector<row> rows;
    int next = 0, count = 0, left = 0, right = 0;

    explicit bodyContrast(int n = 0) : rows(static_cast<size_t>(n)) {}
    void reset() { next = count = left = right = 0; }
    bool add(int l, int r, int minimum) {
        int n = static_cast<int>(rows.size());
        if (count == n) {
            left -= rows[next].left;
            right -= rows[next].right;
        } else {
            count++;
        }
        rows[next].left = l;
        rows[next].right = r;
        left += l;
        right += r;
        next = (next + 1) % n;
        return count == n && left >= minimum * count && right >= minimum * count;
    }
};
inline bodyContrast newBodyContrast(int rows) { return bodyContrast(rows); }

// dark_glint.go
bool darkGlintBody(const RGBA* img, const BeadPosition& p, int w, int h, ninja::Palette palette);
// gold_body.go / gold_glint.go
bool goldBodyVisible(const RGBA* img, const BeadPosition& p, int w, int h);
bool goldGlintBody(const RGBA* img, const BeadPosition& p, int w, int h);
// purple_glint.go / purple_halo.go / purple_lobes.go
bool purpleGlintBody(const RGBA* img, const BeadPosition& p, int w, int h, int gap);
bool isolatedPurpleHalo(const RGBA* img, const BeadPosition& p, int w, int h, int guard, int gap);
bool purplePairedBody(const RGBA* img, const BeadPosition& p, int w, int h, int gap);
// warm_glint.go
bool warmGlintBody(const RGBA* img, const BeadPosition& p, int w, int h);
// xiayin_palette.go
ninja::Palette xiayinPalette(const RGBA* img, const std::vector<BeadPosition>& positions, const std::string& side,
                             int w, int h);
BeadState sampleUnverifiedXiayinRed(const RGBA* img, const BeadPosition& p, int w, int h, int guard, double& conf);

}  // namespace nt::rgb
