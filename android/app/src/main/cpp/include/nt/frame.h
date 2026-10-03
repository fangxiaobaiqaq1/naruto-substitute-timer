// nt/frame.h — internal/frame 中与平台无关的部分（owner: cpp-gated-frame；实现 src/frame/frame.cpp）
//
// 保留：AnalyzeImage（像素 → 观测的唯一路径）、frameFingerprint、dedupedAnalysis、
//       boundedProvider 中的 Sequence / Duplicate 判定（"交付层"）。
// 移除：Windows 窗口查找/截屏、超时 worker（Android 端由 Kotlin FrameLoop 的
//       单分析线程 + latest-frame-wins 取代）。
//
// Frame 的 Beads 在 native 侧保持 engine::BeadInfo 原样；frame.Bead 的 Lit/Dark 换算
// （fillFromEngine）在 Kotlin AnalysisResult.frameBeads 里做，二者逐字段等价。
#pragma once

#include <cstdint>
#include <memory>
#include <string>

#include "nt/config.h"
#include "nt/detect.h"
#include "nt/engine.h"
#include "nt/image.h"
#include "nt/result.h"
#include "nt/time.h"

namespace nt::frame {

// 采集方式（进入指纹：不同坐标系/来源 = 新观测）。值与 Kotlin NativeCore.METHOD_* 一致。
inline const std::string MethodProjection = "android-media-projection";
inline const std::string MethodScreenshot = "android-accessibility-screenshot";
inline const std::string MethodBuffer = "android-buffer";
// JNI 的 int method → 字符串（未知值归为 MethodBuffer）。
const std::string& MethodName(int method);

struct Frame {
    engine::Result Res;  // engine.Result（Gated 输出）
    // Go f.Status：fillFromEngine 的 "已连接 <CaptureMethod> · WxH · <scr> · <Name>"，
    // 或 AnalyzeImage 的提示（例如 "空帧，等待画面"）。仅诊断显示。
    std::string Status;
    bool Hold = false;
    bool HasErr = false;
    std::string Err;
    int Width = 0, Height = 0;
    TimeNs CaptureStarted = 0;
    TimeNs CapturedAt = 0;      // Successful image acquisition only; zero on failed capture.
    TimeNs AnalysisStarted = 0; // zero if analysis was skipped (deduped)
    TimeNs AnalyzedAt = 0;
    std::string CaptureMethod;
    uint64_t Sequence = 0;
    bool Duplicate = false;  // Identical pixels; never count as an independent confirmation.
    bool Reused = false;     // dedupedAnalysis 命中：本帧未重新跑识别链（诊断）

    uint64_t fingerprint = 0;
    bool fingerprinted = false;
};

// frameFingerprint hashes visible pixels, geometry and capture source.
// Padding bytes are not observations; rows are hashed separately.
uint64_t frameFingerprint(const RGBA* img, const std::string& method);

// AnalyzeImage is the same pixel-to-observation path for live capture and replay.
// capturedAt is acquisition completion, NOT an asserted Android render time.
// （eng 是 TimedEngine，因此与 Go 一样跳过 ClassifyScreen 空帧预检。）
Frame AnalyzeImage(const RGBA* img, engine::Engine* eng, detect::ContentMode mode, TimeNs started,
                   TimeNs capturedAt, const std::string& source);

// dedupedAnalysis skips the recognition chain for a capture whose pixels are
// identical to the previously analyzed capture.
class DedupedAnalysis {
public:
    Frame analyze(const RGBA* img, engine::Engine* eng, detect::ContentMode mode, TimeNs started, TimeNs capturedAt,
                  const std::string& source);

private:
    bool valid_ = false;
    uint64_t fingerprint_ = 0;
    Frame last_;
};

// Pipeline：JNI 唯一调用的入口。持有 Gated 引擎、去重器与交付层序号/重复判定。
// 只在单一分析线程上使用（JNI 全局锁保证）。
class Pipeline {
public:
    // 用 factory::New(factory::FromApp(cfg)) 构造引擎。失败时 Ok()==false。
    explicit Pipeline(const config::Config& cfg);

    bool Ok() const { return engine_ != nullptr; }
    const std::string& Error() const { return error_; }

    // 一帧：dedupedAnalysis → boundedProvider 的交付判定（Sequence++ / Duplicate）。
    Frame Analyze(const RGBA* img, TimeNs started, TimeNs capturedAt, const std::string& method);

    // 交付层状态复位（采集源切换 / 服务重启）。
    void ResetDelivery();

private:
    config::Config cfg_;
    detect::ContentMode mode_ = detect::ModeAuto;
    std::shared_ptr<engine::Engine> engine_;
    std::string error_;
    DedupedAnalysis dedupe_;
    uint64_t sequence_ = 0;
    uint64_t previous_ = 0;
    bool havePrevious_ = false;
};

}  // namespace nt::frame
