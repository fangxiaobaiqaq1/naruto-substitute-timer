#pragma once
#include "nt/hudtext.h"

namespace nt::ios {
// iOS-only OCR backend. Recognition/evidence policy stays in shared hudtext;
// this adapter replaces only PP-OCR inference with public, offline Apple Vision.
// Called synchronously on the recognition worker, never on ReplayKit delivery.
class VisionAccounts {
public:
    explicit VisionAccounts(const config::Config& config) : layout_(config.Layout) {}
    void analyze(const RGBA& image, engine::Result& result, TimeNs at,
                 const std::vector<std::string>& names, bool enabled);
    void reset();
    std::string status = "waiting", error;
private:
    hudtext::NameLayout layout_;
    std::array<hudtext::ValueState, 2> accounts_;
    std::array<std::shared_ptr<hudtext::GlyphEvidence>, 2> proofs_;
    std::array<uint64_t, 2> signatures_{};
    std::array<Rect, 2> regions_{};
    Rect bounds_;
    std::string profile_;
    std::vector<std::string> names_;
    TimeNs nextAt_ = 0, lastAt_ = 0, pausedAt_ = 0;
};
} // namespace nt::ios
