// Owner: cpp-ninja
// Port of: internal/ninja/tracker.go
// Contract: include/nt/ninja.h — see android/ARCHITECTURE.md.
//
// Tracker keeps a name-search off the per-frame hot path. Once a name is found,
// every frame rechecks only those exact pixels (+/-1px), not the entire corner.
// A failed recheck immediately returns unknown. The old template LOCATION may
// remain a bounded search hint, never an identity vote: a returning name must
// pass the original current-pixel threshold again on that frame.
//
// Go 的 t.mu 省略：每个 Tracker 只被一个并行体使用（见 nt/ninja.h）。
#include <cstdint>
#include <limits>

#include "nt/match.h"
#include "nt/ninja.h"

namespace nt::ninja {

namespace {

// Go now.Sub(t)：t 为零时刻时 Go 得到饱和的 maxDuration（远大于任何阈值）。
DurationNs sinceNs(TimeNs now, TimeNs t) {
    if (t == 0) return std::numeric_limits<DurationNs>::max();
    return now - t;
}

}  // namespace

Readout Tracker::Read(Reader* reader, const RGBA* img, Rect roi, double scale, TimeNs now) {
    if (reader == nullptr || img == nullptr) return Readout{};
    bool changed = reader_ != reader || bounds_ != img->Bounds() || roi_ != roi || scale_ != scale ||
                   (lastAt_ != 0 && now < lastAt_);
    lastAt_ = now;
    if (changed) {
        hint_ = evidence{};
        retryAfter_ = 0;
        verifiedAt_ = 0;
    }
    reader_ = reader;
    bounds_ = img->Bounds();
    roi_ = roi;
    scale_ = scale;
    auto unavailable = [&]() -> Readout {
        if (!hint_.Name.empty() && hint_.Slots > 0 && sinceNs(now, verifiedAt_) <= Second) {
            Readout out;
            out.Slots = hint_.Slots;
            out.RowOffsetY = hint_.RowOffsetY;
            out.Unverified = true;
            out.PaletteHint = hint_.Palette_;
            return out;
        }
        return Readout{};
    };
    if (!hint_.Name.empty()) {
        Rect region = hint_.rect.Inset(-1).Intersect(roi).Intersect(img->Bounds());
        if (!region.Empty()) {
            RGBA view = img->SubImage(region);
            match::Query q;
            q.Image = &view;
            q.ROI = region;
            q.Prepared = hint_.template_.get();
            match::Score score;
            bool ok = match::NCC{}.Match(q, score);
            if (ok && score.Value >= .80) {
                // A special template stays double-gated: the stored pixels AND
                // the fixed portrait must both be current. A kept title alone
                // may never keep an energy-gauge geometry alive.
                if (hint_.portrait == nullptr ||
                    itachiPortraitEvidence(img, scale, hint_.portrait.get(), hint_.portraitSize)) {
                    hint_.rect = hint_.rect.Add(score.Peak.Sub(hint_.rect.Min));
                    hint_.Score = score.Value;
                    verifiedAt_ = now;
                    // A verified title clears any old failed-search backoff so a
                    // subsequent occlusion still gets its bounded full-ROI search.
                    retryAfter_ = 0;
                    return static_cast<const Readout&>(hint_);
                }
            }
        }
        if (sinceNs(now, verifiedAt_) > Second) hint_ = evidence{};
    }
    // Unknown characters are normal. Keep trying at a bounded rate without
    // delaying every bead sample. A stable hint is rechecked on every frame, but
    // repeated visible/occluded flicker must NOT trigger a full scan every time.
    if (now < retryAfter_) return unavailable();
    // A verified title clears any old failed-search backoff. Therefore the
    // first failed current-pixel recheck performs one bounded full-ROI search:
    // a title that moved with the current HUD may still be recognized on this
    // frame. A failed search starts the backoff; later failed rechecks cannot
    // turn rapid occlusion/effect flicker into repeated scans.
    retryAfter_ = now + 500 * Millisecond;
    evidence found = reader->read(img, roi, scale);
    if (!found.Name.empty()) {
        hint_ = found;
        verifiedAt_ = now;
        retryAfter_ = 0;
        return static_cast<const Readout&>(found);
    }
    // In particular, never return the retained hint on this failed frame.
    return unavailable();
}

// ReadWithAvatar keeps title and portrait evidence independent until a current
// frame resolves them. It deliberately does not reuse title/portrait identity
// from prior frames; Tracker still provides only the existing bounded geometry
// hint and never a name.
Readout Tracker::ReadWithAvatar(Reader* reader, AvatarTracker* avatar, const RGBA* img, Rect titleROI, Rect avatarROI,
                                double scale, TimeNs now) {
    Readout title = Read(reader, img, titleROI, scale, now);
    // Go 在 reader == nil 时 ResolveEvidence 的 nil 接收者直接返回 title（TitleName 已填）。
    if (reader == nullptr) {
        Readout out = title;
        out.TitleName = title.Name;
        return out;
    }
    return reader->ResolveEvidence(img, titleROI, avatarROI, scale, title, avatar, now);
}

}  // namespace nt::ninja
