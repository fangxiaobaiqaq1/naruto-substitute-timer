// nt/factory.h — internal/engine/factory（owner: cpp-gated-frame；实现 src/engine/factory.cpp）
// 只保留 KindRGB；OpenCV / 模型 / OCR(hudtext) 包装已移除。
#pragma once

#include <memory>
#include <string>

#include "nt/config.h"
#include "nt/detect.h"
#include "nt/engine.h"

namespace nt::factory {

// Kind 是引擎类型标识。
using Kind = std::string;
inline const Kind KindRGB = "rgb";      // 纯 RGB 区间判色
inline const Kind KindOpenCV = "opencv";  // 不支持
inline const Kind KindModel = "model";    // 不支持

struct Config {
    Kind Kind_ = KindRGB;
    detect::ContentMode Mode = detect::ModeAuto;
    config::Config App;
};

// FromApp 用应用配置构造引擎工厂配置（ContentMode 解析失败回退 auto）。
Config FromApp(const config::Config& appCfg);
Config DefaultConfig();

// New 按配置构造引擎：Gated(scene::NewGate(cfg.App), rgb::Engine(ConfiguredLayout), identity::Guesser)。
// 失败返回 nullptr 并写 err。
std::shared_ptr<engine::Engine> New(const Config& cfg, std::string* err = nullptr);

}  // namespace nt::factory
