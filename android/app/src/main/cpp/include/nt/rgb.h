// nt/rgb.h — internal/engine/rgb（owner: cpp-rgb；实现 src/rgb/*.cpp，一个 Go 文件一个 .cpp）
//
// Package rgb 实现 RGBEngine：基于纯 RGB 区间判色的检测引擎。
// 配置布局使用固定菱形核心，旧校准工具保留兼容采样接口。
#pragma once

#include <array>
#include <memory>
#include <string>

#include "nt/config.h"
#include "nt/engine.h"
#include "nt/ninja.h"
#include "nt/result.h"

namespace nt::rgb {

// Engine 保存布局偏好（单分析线程使用，无需加锁）。
// Go 的 parallelSides（camp‖duel、left‖right）用 nt::ParallelInvoke 复刻：每个并行体只写
// 自己的 readout 槽与自己 profile/side 的 tracker；共享的 names_（Reader）自带锁。
class Engine final : public engine::Engine, public engine::LayoutPreferrer {
public:
    // New：vision 取 config::Default().Vision。
    explicit Engine(std::shared_ptr<engine::LayoutProvider> layout);
    // NewConfigured
    Engine(std::shared_ptr<engine::LayoutProvider> layout, const config::VisionConfig& vision);

    // Prefer receives a template's explicit layout binding, or clears it at an end scene.
    void Prefer(const std::string& kind) override;
    // AnalyzeAt uses acquisition time for retry scheduling.
    engine::Result AnalyzeAt(const RGBA* img, TimeNs at) override;

    // special.go
    std::vector<detect::BeadPosition> specialPositionsForAt(const std::string& profile, const RGBA* img,
                                                            const std::vector<detect::BeadPosition>& positions,
                                                            const detect::ContentArea& area, TimeNs at,
                                                            std::array<ninja::Readout, 2>& identified);
    std::vector<engine::BeadInfo> stickLayout(const std::vector<engine::BeadInfo>& camp,
                                              const std::vector<engine::BeadInfo>& duel,
                                              const std::vector<engine::BeadInfo>& chosen, const std::string& name,
                                              std::string& outName);

private:
    std::shared_ptr<engine::LayoutProvider> layout_;
    std::string prefer_;
    std::string locked_;
    config::VisionConfig vision_;
    std::shared_ptr<ninja::Reader> names_;
    // [profileIndex camp=0/duel=1][side left=0/right=1]
    ninja::Tracker nameTracker_[2][2];
    ninja::AvatarTracker avatarTracker_[2][2];
};

}  // namespace nt::rgb
