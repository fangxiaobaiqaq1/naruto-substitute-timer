#import <Vision/Vision.h>
#import <CoreGraphics/CoreGraphics.h>
#import <Foundation/Foundation.h>
#include "VisionAccounts.hpp"
#include "nt/identity.h"
#include "nt/match.h"
#include "nt/utf8.h"
#include <algorithm>

namespace nt::ios {
namespace {
// Source data stays alive throughout the synchronous request. A CGImage wraps
// only a cropped title treatment, NEVER a full ReplayKit/IOSurface frame.
bool readRow(const hudtext::Row& row, std::vector<ocr::Line>& lines, std::string& error) {
    @autoreleasepool {
        const auto& img = row.image;
        CGColorSpaceRef color = CGColorSpaceCreateDeviceRGB();
        CGDataProviderRef provider = CGDataProviderCreateWithData(nullptr, img.Pix,
            size_t(img.Stride) * img.Bounds().Dy(), nullptr);
        CGImageRef cg = CGImageCreate(img.Bounds().Dx(), img.Bounds().Dy(), 8, 32, img.Stride, color,
            kCGImageAlphaLast | kCGBitmapByteOrder32Big, provider, nullptr, false, kCGRenderingIntentDefault);
        CGDataProviderRelease(provider); CGColorSpaceRelease(color);
        if (!cg) { error = "无法创建账号 OCR 裁图"; return false; }
        VNRecognizeTextRequest *request = [[VNRecognizeTextRequest alloc] init];
        request.recognitionLevel = VNRequestTextRecognitionLevelAccurate;
        request.usesLanguageCorrection = NO; // Never autocorrect an account into a configured name.
        request.preferBackgroundProcessing = YES;
        NSError *failure = nil;
        NSArray<NSString *> *languages = [request supportedRecognitionLanguagesAndReturnError:&failure];
        if (![languages containsObject:@"zh-Hans"]) {
            CGImageRelease(cg); error = "当前 iOS Vision 不支持简体中文账号识别"; return false;
        }
        request.recognitionLanguages = @[@"zh-Hans", @"en-US"];
        VNImageRequestHandler *handler = [[VNImageRequestHandler alloc] initWithCGImage:cg options:@{}];
        const BOOL ok = [handler performRequests:@[request] error:&failure];
        CGImageRelease(cg);
        if (!ok) { error = failure.localizedDescription.UTF8String ?: "Vision 账号识别失败"; return false; }
        for (VNRecognizedTextObservation *observation in request.results) {
            VNRecognizedText *candidate = [observation topCandidates:1].firstObject;
            if (!candidate || candidate.confidence < .45) continue;
            ocr::Line line;
            line.Text = candidate.string.UTF8String ?: "";
            // Preserve Vision's actual per-character boxes for shared glyph
            // evidence. Foundation ranges are UTF-16, not byte/rune indices.
            NSString *text = candidate.string;
            for (NSUInteger i = 0; i < text.length;) {
                NSRange range = [text rangeOfComposedCharacterSequenceAtIndex:i];
                VNRectangleObservation *glyph = [candidate boundingBoxForRange:range error:nil];
                const CGRect b = glyph ? glyph.boundingBox : observation.boundingBox;
                if (CGRectIsEmpty(b)) { i = NSMaxRange(range); continue; }
                ocr::Word word;
                word.Text = [[text substringWithRange:range] UTF8String] ?: "";
                if (!glyph) {
                    // Without a glyph box use the whole line ONCE; shared
                    // Evidence splits it into cells, never invent duplicate boxes.
                    word.Text = line.Text;
                }
                word.X = row.rect.Min.X + b.origin.x * row.rect.Dx();
                word.Y = row.rect.Min.Y + (1 - CGRectGetMaxY(b)) * row.rect.Dy();
                word.Width = b.size.width * row.rect.Dx();
                word.Height = b.size.height * row.rect.Dy();
                line.Words.push_back(std::move(word));
                if (!glyph) break;
                i = NSMaxRange(range);
            }
            if (!line.Words.empty()) lines.push_back(std::move(line));
        }
        return true;
    }
}
}

void VisionAccounts::reset() {
    accounts_ = {}; proofs_ = {}; signatures_ = {}; regions_ = {};
    bounds_ = {}; profile_.clear(); names_.clear();
    nextAt_ = lastAt_ = pausedAt_ = 0;
    status = "waiting"; error.clear();
}

void VisionAccounts::analyze(const RGBA& img, engine::Result& res, TimeNs at,
                             const std::vector<std::string>& names, bool enabled) {
    if (!enabled || names.empty()) { reset(); status = "off"; return; }
    std::array<Rect, 2> regions;
    const bool sameScene = res.Scene.empty() || res.Scene == "fight";
    bool haveLayout = sameScene && layout_.Regions(&img, res.LayoutProfile, regions);
    const bool readable = (res.Fighting && !res.Uncertain) || (sameScene && engine::hasCompleteHUD(res));
    if (!haveLayout || names != names_ || bounds_ != img.Bounds() || regions != regions_ ||
        profile_ != res.LayoutProfile || (lastAt_ && at < lastAt_)) {
        reset();
        if (!haveLayout) return;
        names_ = names; bounds_ = img.Bounds(); regions_ = regions; profile_ = res.LayoutProfile;
    }
    if (at <= lastAt_) return;
    lastAt_ = at;
    std::array<int, 2> white{};
    for (size_t i = 0; i < 2; ++i) {
        const uint64_t sig = hudtext::Lettering(img, regions[i], white[i]);
        if ((proofs_[i] && !proofs_[i]->Matches(&img)) || (!proofs_[i] && sig != signatures_[i])) {
            accounts_[i] = {}; proofs_[i].reset();
        }
        signatures_[i] = sig;
    }
    if (!readable) {
        if (!pausedAt_) pausedAt_ = at;
        if (at - pausedAt_ > hudtext::textPauseTTL) reset();
        return;
    }
    pausedAt_ = 0;
    if (at >= nextAt_ && (white[0] >= 12 || white[1] >= 12)) {
        nextAt_ = at + hudtext::requestInterval;
        std::array<RGBA, 2> strips;
        for (size_t i = 0; i < 2; ++i) strips[i] = match::CropRGBA(&img, regions[i]);
        const auto rows = hudtext::BuildRows(strips);
        std::vector<ocr::Line> lines;
        bool ok = true;
        for (const auto& row : rows) if (row.variant < 2 && !readRow(row, lines, error)) { ok = false; break; }
        if (ok) {
            auto texts = hudtext::Texts(rows, lines);
            for (size_t i = 0; i < 2; ++i) {
                const auto initial = hudtext::Consensus(texts[i]);
                auto proof = hudtext::Evidence(rows, lines, int(i), initial, strips[i], regions[i]);
                if (hudtext::AccountSegment(texts[i][0]) != hudtext::AccountSegment(texts[i][1]) || (!initial.empty() && !proof)) {
                    for (const auto& row : rows) if (row.side == int(i) && row.variant == 2)
                        if (!readRow(row, lines, error)) ok = false;
                }
            }
            texts = hudtext::Texts(rows, lines);
            // This worker is synchronous, so glyphs are from the same captured
            // image. Late work still cannot become evidence after a long stall.
            if (ok && NowNs() - at <= hudtext::resultMaxAge) {
                for (size_t i = 0; i < 2; ++i) {
                    const auto value = hudtext::Consensus(texts[i]);
                    auto proof = hudtext::Evidence(rows, lines, int(i), value, strips[i], regions[i]);
                    hudtext::ObserveBound(accounts_[i], proofs_[i], value, proof, &img, at, true);
                }
                error.clear(); status = "pending";
            } else if (ok) { ok = false; error = "账号 OCR 超时，丢弃过期结果"; }
        }
        if (!ok) { status = "unavailable"; nextAt_ = at + hudtext::retryDelay; }
    }
    if (res.PlayerSide.empty()) {
        // Vision does not expose its dictionary. Strict equality is safer than
        // pretending any unknown glyph is a wildcard; partial-name rules remain.
        const auto d = hudtext::DecideSide({accounts_[0].accepted, accounts_[1].accepted}, names,
                                           [](char32_t) { return true; });
        if (d.side >= 0) {
            res.PlayerSide = d.side == 0 ? "left" : "right";
            res.PlayerName = d.player; res.OppName = d.opp; status = "ready";
        }
    }
    res.TextStatus = status; res.TextError = error;
}
} // namespace nt::ios
