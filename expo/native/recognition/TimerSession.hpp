#pragma once

#include <cstdint>
#include <string>

#include "nt/frame.h"

// Native-free port of tracker/{Session,SideClock}. NinjaRules is already ported
// verbatim in the shared core's src/ninja/rules.cpp; use it, not a second table.
// All event ordering/durations use CLOCK_MONOTONIC nanoseconds. Wall time is
// sampled only to pin each confirmed event's JS deadline, never to run a clock.
namespace nt::ios {

struct TimerSettings {
    int pollIntervalMs = 125; // iOS backgroundFPS defaults to 8.
    int minimumConfirmFrames = 2;
    int enterFightFrames = 1;
    int leaveFightFrames = 10;
    int beadsPerSide = 4;
    std::string playerSide = "left"; // Manual selection only.
    bool showBothSides = false;
    bool rememberSide = false; // Persistence policy; current manual choices still work.
    bool dual = false; // Explicit manual estimate, never identity evidence.
    std::string ninjaQuery; // Explicit opponent override, never visual identity.
};

struct ClockSnapshot {
    TimeNs eventAtNs = 0;
    TimeNs deadlineNs = 0;
    TimeNs alternateDeadlineNs = 0;
    double deadlineMs = 0;
    double alternateDeadlineMs = 0;
    uint64_t eventCount = 0;
    uint64_t eventSerial = 0;
};

struct TimerSnapshot {
    bool match = false;
    int leftCount = -1;
    int rightCount = -1;
    ClockSnapshot left;
    ClockSnapshot right;
    std::string leftNinja;
    std::string rightNinja;
    std::string scene;
    std::string message = "等待横屏游戏画面";
    int width = 0;
    int height = 0;
};

class SideClock {
public:
    void observe(int ready, TimeNs at, double captureWallMs, int confirmNeed, bool dual);
    void invalidateObservation(TimeNs at);
    bool holdObservation(int ready, TimeNs at);
    bool resumeInheritedObservation(TimeNs at);
    void resyncObservation();
    void syncReady(int ready);
    void reset(bool roundOnly = false);
    void setObservationGap(DurationNs gap) { maxGap_ = gap; }
    ClockSnapshot snapshot(bool manualDual = false) const;

private:
    void clearPending();
    void expireRecoveries(TimeNs at);
    TimeNs expectedRecovery(TimeNs at) const;
    void useRecovery(TimeNs end);
    std::vector<TimeNs> recoveryEnds_;
    TimeNs pendingRecovery_ = 0;
    int lastReady_ = 0;
    bool hasPrev_ = false;
    int pendingReady_ = 0;
    TimeNs pendingAt_ = 0;
    double pendingWallMs_ = 0;
    int pendingHits_ = 0;
    TimeNs observedAt_ = 0;
    TimeNs validAt_ = 0;
    DurationNs maxGap_ = 0;
    bool inheritedReturn_ = false;
    bool eventDual_ = false;
    ClockSnapshot event_;
    // The API exposes only the latest event (as does KT latestRemaining).
    // recoveryEnds_ is a time-bounded evidence ledger, NEVER a display fallback.
};

class TimerSession {
public:
    explicit TimerSession(const TimerSettings& settings = {});
    void configure(const TimerSettings& settings);
    TimerSnapshot onFrame(const frame::Frame& frame, double captureWallMs);
    TimerSnapshot snapshot() const;
    // No fresh pixels: discard candidates AND baseline, keep confirmed clocks.
    void pauseObservations();
    void reset();
    bool fighting() const { return fighting_; }

private:
    bool updateSource(const frame::Frame& frame);
    void updateTopology(const engine::Result& result);
    void observeRoundOpening(const frame::Frame& frame);
    void clearRoundOpening();
    void applyScene(const frame::Frame& frame);
    void observeBeads(const frame::Frame& frame, double captureWallMs);
    void invalidate(TimeNs at, bool resync);
    DurationNs observationGap() const;
    static int readyCount(const engine::Result& result, bool left, int expected);

    TimerSettings settings_;
    SideClock leftClock_, rightClock_;
    TimerSnapshot visible_;
    bool fighting_ = false;
    int fightHits_ = 0, leaveHits_ = 0;
    TimeNs lastCaptured_ = 0;
    bool holdFreeze_ = false, inheritOnReturn_ = false;
    bool syncLeft_ = false, syncRight_ = false;
    bool roundOpeningActive_ = false;
    TimeNs roundOpeningSeenAt_ = 0;
    bool roundBaselineLeft_ = false, roundBaselineRight_ = false;
    int sourceWidth_ = 0, sourceHeight_ = 0;
    std::string sourceMethod_, layoutProfile_;
    int leftSlots_ = 0, rightSlots_ = 0;
    TimeNs lastBeadAt_ = 0;
    DurationNs cadenceEma_ = 0;
};

} // namespace nt::ios
