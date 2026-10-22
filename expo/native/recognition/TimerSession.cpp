#include "TimerSession.hpp"

#include <algorithm>
#include <cmath>

#include "nt/ninja.h"
#include "nt/identity.h"

namespace nt::ios {
namespace {
constexpr DurationNs OpeningMarkerGap = 250 * Millisecond;
bool holdScene(const std::string& s) { return s == "vs" || s == "queue" || s == "pick" || s == "ban"; }
bool endScene(const std::string& s) { return s == "lobby" || s == "result"; }
int slots(int n, int fallback) { return n == 4 || n == 6 ? n : fallback; }
}

void SideClock::clearPending() {
    pendingReady_ = 0;
    pendingAt_ = 0;
    pendingWallMs_ = 0;
    pendingHits_ = 0;
    pendingRecovery_ = 0;
}

void SideClock::expireRecoveries(TimeNs at) {
    recoveryEnds_.erase(std::remove_if(recoveryEnds_.begin(), recoveryEnds_.end(),
        [at](TimeNs end) { return at - end > 250 * Millisecond; }), recoveryEnds_.end());
}

TimeNs SideClock::expectedRecovery(TimeNs at) const {
    for (TimeNs end : recoveryEnds_) if (std::abs(end - at) <= 250 * Millisecond) return end;
    return 0;
}

void SideClock::useRecovery(TimeNs end) {
    auto it = std::find(recoveryEnds_.begin(), recoveryEnds_.end(), end);
    if (it != recoveryEnds_.end()) recoveryEnds_.erase(it);
}

void SideClock::observe(int ready, TimeNs at, double wallMs, int need, bool dual) {
    // Unlike the standalone KT convenience API, missing acquisition time is NOT
    // replaced with now(): a heartbeat must never manufacture an observation.
    if (at <= 0 || !std::isfinite(wallMs) || wallMs <= 0) {
        invalidateObservation(at);
        resyncObservation();
        return;
    }
    if (observedAt_ != 0 && at <= observedAt_) return;
    observedAt_ = at;
    if (maxGap_ > 0 && validAt_ != 0 && at - validAt_ > maxGap_) {
        hasPrev_ = false;
        clearPending();
    }
    validAt_ = at;
    expireRecoveries(at);
    if (!hasPrev_) {
        inheritedReturn_ = false;
        lastReady_ = ready;
        hasPrev_ = true;
        clearPending();
        return;
    }
    if (ready == lastReady_) {
        if (pendingHits_ != 1 || pendingReady_ != ready + 1 || pendingRecovery_ == 0) {
            inheritedReturn_ = false;
            clearPending();
            return;
        }
        // Android recovery-and-immediate-reuse: the one-frame +1 coincided
        // with a real cooldown end; this frame starts a NEW drop candidate.
        useRecovery(pendingRecovery_);
        lastReady_ = ready + 1;
    }
    // Recovery and inherited return need two observations too; settings cannot
    // weaken the public iOS contract to a one-frame trigger.
    need = std::max(2, need);
    if (pendingHits_ == 0 || pendingReady_ != ready) {
        pendingReady_ = ready;
        pendingAt_ = at;
        pendingWallMs_ = wallMs;
        pendingHits_ = 1;
        pendingRecovery_ = ready == lastReady_ + 1 && !inheritedReturn_ ? expectedRecovery(at) : 0;
    } else {
        ++pendingHits_;
    }
    if (pendingHits_ < need) return;
    if (ready > lastReady_) useRecovery(pendingRecovery_);
    const int drop = lastReady_ - ready;
    if (drop == 1 && !inheritedReturn_) {
        event_.eventAtNs = pendingAt_;
        event_.deadlineNs = pendingAt_ + ninja::DefaultCooldown;
        event_.alternateDeadlineNs = pendingAt_ + ninja::AlternateCooldown;
        // Pin both deadlines ONCE to the first candidate's wall/monotonic pair.
        // Later confirmation latency, NTP jumps and resizes cannot move them.
        event_.deadlineMs = pendingWallMs_ + double(event_.deadlineNs - pendingAt_) / Millisecond;
        event_.alternateDeadlineMs = pendingWallMs_ + double(event_.alternateDeadlineNs - pendingAt_) / Millisecond;
        ++event_.eventCount;
        ++event_.eventSerial;
        eventDual_ = dual;
        recoveryEnds_.push_back(event_.deadlineNs);
    }
    lastReady_ = ready;
    inheritedReturn_ = false;
    clearPending();
}

void SideClock::invalidateObservation(TimeNs at) {
    if (at > observedAt_) observedAt_ = at;
    clearPending();
}

bool SideClock::holdObservation(int ready, TimeNs at) {
    if (at <= 0 || !hasPrev_ || validAt_ == 0 || at <= observedAt_) return false;
    const int expected = pendingHits_ > 0 ? pendingReady_ : lastReady_;
    if (ready != expected || (maxGap_ > 0 && at - validAt_ > maxGap_)) return false;
    observedAt_ = at;
    validAt_ = at;
    return true;
}

bool SideClock::resumeInheritedObservation(TimeNs at) {
    if (at <= 0 || (observedAt_ != 0 && at <= observedAt_)) return false;
    validAt_ = 0;
    clearPending();
    inheritedReturn_ = hasPrev_;
    return true;
}

void SideClock::resyncObservation() {
    inheritedReturn_ = false;
    hasPrev_ = false;
    validAt_ = 0;
    clearPending();
}

void SideClock::syncReady(int ready) {
    inheritedReturn_ = false;
    lastReady_ = ready;
    hasPrev_ = true;
    clearPending();
}

void SideClock::reset(bool roundOnly) {
    const auto serial = event_.eventSerial;
    const auto count = event_.eventCount;
    event_ = {};
    recoveryEnds_.clear();
    event_.eventSerial = serial; // monotonic diagnostics across resets
    if (roundOnly) event_.eventCount = count;
    lastReady_ = 0;
    hasPrev_ = false;
    observedAt_ = validAt_ = 0;
    inheritedReturn_ = eventDual_ = false;
    clearPending();
}

ClockSnapshot SideClock::snapshot(bool manualDual) const {
    auto out = event_;
    if (!eventDual_ && !manualDual) {
        out.alternateDeadlineNs = 0;
        out.alternateDeadlineMs = 0;
    }
    return out;
}

TimerSession::TimerSession(const TimerSettings& s) { configure(s); }

void TimerSession::configure(const TimerSettings& s) {
    const bool cadenceChanged = settings_.pollIntervalMs != s.pollIntervalMs;
    if (settings_.playerNames != s.playerNames) {
        autoSide_.clear();
        visible_.detectedPlayerSide.clear();
        visible_.playerName.clear();
        visible_.opponentName.clear();
    }
    settings_ = s;
    settings_.pollIntervalMs = std::clamp(s.pollIntervalMs, 100, 1000);
    settings_.minimumConfirmFrames = std::max(2, s.minimumConfirmFrames);
    settings_.enterFightFrames = std::max(1, s.enterFightFrames);
    settings_.leaveFightFrames = std::max(10, s.leaveFightFrames);
    settings_.beadsPerSide = slots(s.beadsPerSide, 4);
    if (s.playerSide != "left" && s.playerSide != "right") settings_.playerSide = "auto";
    if (cadenceChanged) pauseObservations();
}

void TimerSession::invalidate(TimeNs at, bool resync) {
    leftClock_.invalidateObservation(at);
    rightClock_.invalidateObservation(at);
    if (resync) {
        leftClock_.resyncObservation();
        rightClock_.resyncObservation();
        syncLeft_ = syncRight_ = false;
    }
}

void TimerSession::pauseObservations() {
    invalidate(0, true);
    lastBeadAt_ = cadenceEma_ = 0;
    fightHits_ = leaveHits_ = 0;
    holdFreeze_ = inheritOnReturn_ = false;
    visible_.match = false;
    visible_.leftCount = visible_.rightCount = -1;
    visible_.detectedPlayerSide.clear();
    autoSide_.clear();
    clearRoundOpening();
    visible_.message = "采集已暂停，已确认计时继续";
}

void TimerSession::reset() {
    leftClock_.reset();
    rightClock_.reset();
    visible_ = {};
    autoSide_.clear();
    fighting_ = false;
    fightHits_ = leaveHits_ = 0;
    lastCaptured_ = 0;
    holdFreeze_ = inheritOnReturn_ = false;
    syncLeft_ = syncRight_ = false;
    clearRoundOpening();
    sourceWidth_ = sourceHeight_ = 0;
    sourceMethod_.clear();
    layoutProfile_.clear();
    leftSlots_ = rightSlots_ = 0;
    lastBeadAt_ = cadenceEma_ = 0;
}

bool TimerSession::updateSource(const frame::Frame& f) {
    const bool changed = f.Width > 0 && f.Height > 0 && sourceWidth_ != 0 &&
        (sourceWidth_ != f.Width || sourceHeight_ != f.Height ||
         (!sourceMethod_.empty() && !f.CaptureMethod.empty() && sourceMethod_ != f.CaptureMethod));
    // Failed acquisitions have no geometry; don't replace a known source by 0x0.
    if (f.Width > 0 && f.Height > 0) {
        sourceWidth_ = f.Width;
        sourceHeight_ = f.Height;
        if (!f.CaptureMethod.empty()) sourceMethod_ = f.CaptureMethod;
    }
    const auto& layout = f.Res.LayoutProfile;
    if (f.Res.Fighting && !f.HasErr && !layout.empty() && !layoutProfile_.empty() && layout != layoutProfile_) {
        leftClock_.reset();
        rightClock_.reset();
        fighting_ = false;
        fightHits_ = leaveHits_ = 0;
        holdFreeze_ = inheritOnReturn_ = false;
        syncLeft_ = syncRight_ = false;
        lastBeadAt_ = cadenceEma_ = 0;
        clearRoundOpening();
    }
    if (f.Res.Fighting && !f.HasErr && !layout.empty()) layoutProfile_ = layout;
    return changed;
}

void TimerSession::updateTopology(const engine::Result& r) {
    const int left = slots(r.LeftSlots, settings_.beadsPerSide);
    const int right = slots(r.RightSlots, settings_.beadsPerSide);
    if (leftSlots_ != 0 && leftSlots_ != left) leftClock_.resyncObservation();
    if (rightSlots_ != 0 && rightSlots_ != right) rightClock_.resyncObservation();
    leftSlots_ = left;
    rightSlots_ = right;
}

void TimerSession::clearRoundOpening() {
    roundOpeningActive_ = false;
    roundOpeningSeenAt_ = 0;
    roundBaselineLeft_ = roundBaselineRight_ = false;
}

void TimerSession::observeRoundOpening(const frame::Frame& f) {
    if (f.CapturedAt <= 0) return;
    if (f.Res.Fighting && f.Res.RoundOpening) {
        if (!roundOpeningActive_) {
            leftClock_.reset(true);
            rightClock_.reset(true);
            syncLeft_ = syncRight_ = false;
            inheritOnReturn_ = holdFreeze_ = false;
        }
        roundOpeningActive_ = true;
        roundOpeningSeenAt_ = f.CapturedAt;
        roundBaselineLeft_ = roundBaselineRight_ = true;
    } else if (!f.Hold && !f.HasErr && f.Res.Fighting && roundOpeningActive_ && f.CapturedAt - roundOpeningSeenAt_ > OpeningMarkerGap) {
        roundOpeningActive_ = false;
        // First trustworthy post-opening rows are calibration, never a drop.
        roundBaselineLeft_ = roundBaselineRight_ = true;
    }
}

void TimerSession::applyScene(const frame::Frame& f) {
    const auto& r = f.Res;
    if (holdScene(r.Scene)) {
        leaveHits_ = 0;
        inheritOnReturn_ = r.Scene == "vs" && fighting_;
        holdFreeze_ = true;
        return;
    }
    if (r.Fighting) {
        if (holdFreeze_) {
            syncLeft_ = syncRight_ = true;
            holdFreeze_ = false;
        }
        leaveHits_ = 0;
        fightHits_ = std::min(fightHits_ + 1, settings_.enterFightFrames);
        if (!fighting_ && fightHits_ >= settings_.enterFightFrames) fighting_ = true;
    } else if (endScene(r.Scene)) {
        fightHits_ = 0;
        if (fighting_ && ++leaveHits_ >= settings_.leaveFightFrames) {
            fighting_ = false;
            leftClock_.reset();
            rightClock_.reset();
            clearRoundOpening();
            holdFreeze_ = inheritOnReturn_ = false;
            syncLeft_ = syncRight_ = false;
        }
    } else {
        // Ten CONSECUTIVE explicit end frames; unknown scenes aren't evidence.
        fightHits_ = leaveHits_ = 0;
    }
}

int TimerSession::readyCount(const engine::Result& r, bool left, int expected) {
    int ready = 0, count = 0;
    unsigned seen = 0;
    const char side = left ? 'L' : 'R';
    for (const auto& b : r.Beads) {
        if (b.Label.empty()) continue;
        char first = b.Label[0];
        if (first >= 'a' && first <= 'z') first -= 'a' - 'A';
        if (first != side) continue;
        // Full, unique slots required. Unknown is neither ready nor dark.
        if (b.Unknown || b.Label.size() != 2 || b.Label[1] < '1' || b.Label[1] > '0' + expected) return -1;
        const unsigned bit = 1u << (b.Label[1] - '1');
        if (seen & bit) return -1;
        seen |= bit;
        ++count;
        if (b.Lit || b.Gold) ++ready;
    }
    return count == expected ? ready : -1;
}

DurationNs TimerSession::observationGap() const {
    return std::clamp(std::max(DurationNs(settings_.pollIntervalMs) * 3 * Millisecond, cadenceEma_ * 3),
                      150 * Millisecond, 8 * Second);
}

void TimerSession::observeBeads(const frame::Frame& f, double wallMs) {
    const TimeNs at = f.CapturedAt;
    const int lc = readyCount(f.Res, true, leftSlots_);
    const int rc = readyCount(f.Res, false, rightSlots_);
    visible_.leftCount = lc;
    visible_.rightCount = rc;

    auto observe = [&](SideClock& clock, int ready, bool& sync, bool& roundBaseline, const std::string& ninjaName) {
        if (ready < 0) {
            clock.invalidateObservation(at);
            return;
        }
        if (f.Duplicate) {
            // Newly acquired identical pixels can maintain continuity, not vote.
            if (!clock.holdObservation(ready, at)) {
                clock.invalidateObservation(at);
            }
            return;
        }
        if (roundBaseline) {
            clock.syncReady(ready);
            sync = false;
            roundBaseline = roundOpeningActive_;
        } else {
            if (sync) {
                if (inheritOnReturn_) sync = !clock.resumeInheritedObservation(at);
                else { clock.syncReady(ready); sync = false; }
            }
            clock.observe(ready, at, wallMs, settings_.minimumConfirmFrames, ninja::DualCooldown(ninjaName));
        }
    };
    observe(leftClock_, lc, syncLeft_, roundBaselineLeft_, f.Res.LeftNinja);
    observe(rightClock_, rc, syncRight_, roundBaselineRight_, f.Res.RightNinja);
    if (!syncLeft_ && !syncRight_) inheritOnReturn_ = false;
}

TimerSnapshot TimerSession::onFrame(const frame::Frame& f, double wallMs) {
    // Equal timestamps are replay/heartbeat, older timestamps an ordering break.
    if (f.CapturedAt > 0 && lastCaptured_ > 0 && f.CapturedAt <= lastCaptured_) {
        if (f.CapturedAt < lastCaptured_) pauseObservations();
        return snapshot();
    }
    if (f.CapturedAt > 0) lastCaptured_ = f.CapturedAt;
    visible_.match = false;
    visible_.leftCount = visible_.rightCount = -1;
    visible_.width = f.Width;
    visible_.height = f.Height;
    visible_.scene = f.Res.Scene;
    if (f.Res.Fighting && !f.HasErr) {
        visible_.leftNinja = f.Res.LeftNinja;
        visible_.rightNinja = f.Res.RightNinja;
        updateTopology(f.Res);
    }
    // Android ObservationCadence: every acquisition, including holds, is a
    // sample; use the PREVIOUS EMA so an outage cannot authorize itself.
    const auto gap = observationGap();
    leftClock_.setObservationGap(gap);
    rightClock_.setObservationGap(gap);
    if (f.CapturedAt <= 0) lastBeadAt_ = 0;
    else {
        if (lastBeadAt_ > 0) {
            const auto interval = f.CapturedAt - lastBeadAt_;
            cadenceEma_ = cadenceEma_ <= 0 ? interval : cadenceEma_ * 3 / 4 + interval / 4;
        }
        lastBeadAt_ = f.CapturedAt;
    }
    const bool sourceChanged = updateSource(f);
    updateIdentity(f);
    if (sourceChanged) {
        leftClock_.resyncObservation();
        rightClock_.resyncObservation();
        syncLeft_ = syncRight_ = false;
        lastBeadAt_ = cadenceEma_ = 0;
    }
    if (f.CapturedAt > 0 && !f.HasErr) observeRoundOpening(f);
    if (f.Hold || f.HasErr || f.Res.Uncertain || f.CapturedAt <= 0 || !std::isfinite(wallMs) || wallMs <= 0) {
        fightHits_ = leaveHits_ = 0;
        const bool inherited = f.Res.Scene == "vs" && fighting_ && !f.HasErr;
        invalidate(f.CapturedAt, f.HasErr || f.CapturedAt <= 0 || !std::isfinite(wallMs) || wallMs <= 0);
        if (holdScene(f.Res.Scene)) {
            inheritOnReturn_ = inherited;
            holdFreeze_ = true;
        }
        visible_.message = f.HasErr ? "识别暂不可用，已确认计时继续" : "画面暂不可辨，等待重新识别";
        return snapshot();
    }
    applyScene(f);
    if (!fighting_ || !f.Res.Fighting) {
        invalidate(f.CapturedAt, !(inheritOnReturn_ && holdFreeze_));
        visible_.message = endScene(f.Res.Scene) ? "检测到对局结束画面" : "等待横屏对局画面";
        return snapshot();
    }
    if (roundOpeningActive_) roundBaselineLeft_ = roundBaselineRight_ = true;
    visible_.match = true;
    observeBeads(f, wallMs);
    if (visible_.leftCount < 0 || visible_.rightCount < 0) visible_.message = "对局中，替身豆暂不可辨";
    else if (settings_.playerSide == "auto" && autoSide_.empty()) visible_.message = "对局中，等待账号认边或手动选边";
    else visible_.message = "对局中，等待确认替身";
    return snapshot();
}

bool TimerSession::wantsTextRecognition() const {
    return settings_.playerSide == "auto" && autoSide_.empty() && !settings_.playerNames.empty();
}

void TimerSession::updateIdentity(const frame::Frame& f) {
    const auto& r = f.Res;
    if (f.HasErr) return;
    if (!r.Scene.empty() && r.Scene != "fight" && r.Scene != "vs") {
        autoSide_.clear();
        visible_.playerName.clear();
        visible_.opponentName.clear();
    } else if (r.PlayerSide == "left" || r.PlayerSide == "right") {
        // Empty PlayerName is the core's verified OTHER-account inference,
        // not permission to invent a side when no account is configured.
        const auto name = identity::NormalizeName(r.PlayerName);
        const bool mine = std::any_of(settings_.playerNames.begin(), settings_.playerNames.end(),
            [&](const std::string& s) { return !identity::NormalizeName(s).empty() &&
                (name.empty() || identity::NormalizeName(s) == name); });
        if (mine) {
            autoSide_ = r.PlayerSide;
            visible_.playerName = r.PlayerName;
            visible_.opponentName = r.OppName;
        }
    }
    visible_.detectedPlayerSide = autoSide_;
}

TimerSnapshot TimerSession::snapshot() const {
    auto out = visible_;
    // A manual opponent estimate is never evidence of WHICH side is mine.
    const std::string side = settings_.playerSide == "auto" ? autoSide_ : settings_.playerSide;
    const bool leftDual = settings_.dual && side == "right";
    const bool rightDual = settings_.dual && side == "left";
    const auto leftName = side == "right" && !settings_.ninjaQuery.empty() ? settings_.ninjaQuery : out.leftNinja;
    const auto rightName = side == "left" && !settings_.ninjaQuery.empty() ? settings_.ninjaQuery : out.rightNinja;
    out.left = leftClock_.snapshot(leftDual || ninja::DualCooldown(leftName));
    out.right = rightClock_.snapshot(rightDual || ninja::DualCooldown(rightName));
    return out;
}

} // namespace nt::ios
