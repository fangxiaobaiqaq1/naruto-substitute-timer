#include "TimerSession.hpp"
#include "nt/ninja.h"
#include <cassert>
#include <cmath>
#include <iostream>
#include <limits>
using namespace nt;
using namespace nt::ios;

namespace {
constexpr double epoch = 1700000000000.;
frame::Frame fight(int tick, int l = 4, int r = 4, int slots = 4) {
    frame::Frame f;
    f.CapturedAt = Second + tick * 100 * Millisecond;
    f.Width = 1280; f.Height = 720; f.CaptureMethod = "ios-replaykit-1";
    f.Res.Fighting = true; f.Res.Scene = "fight"; f.Res.LayoutProfile = "duel";
    f.Res.LeftSlots = f.Res.RightSlots = slots;
    for (char side : {'L', 'R'}) for (int i = 1; i <= slots; ++i) {
        engine::BeadInfo b;
        b.Label = std::string(1, side) + std::to_string(i);
        b.Lit = i <= (side == 'L' ? l : r);
        f.Res.Beads.push_back(b);
    }
    return f;
}
TimerSnapshot feed(TimerSession& s, frame::Frame f, double wall = 0) {
    return s.onFrame(f, wall ? wall : epoch + double(f.CapturedAt) / Millisecond);
}
void clocks() {
    TimerSession s;
    feed(s, fight(0));
    feed(s, fight(1, 3));
    auto a = feed(s, fight(2, 3));
    assert(a.left.eventCount == 1 && a.right.eventCount == 0);
    assert(a.left.eventAtNs == 1100 * Millisecond);
    assert(a.left.deadlineMs == epoch + 16100);
    // A wall clock adjustment during confirmation cannot move the pinned deadline.
    feed(s, fight(3, 2), epoch + 1300);
    a = feed(s, fight(4, 2), epoch + 99999);
    assert(a.left.eventCount == 2 && a.left.deadlineMs == epoch + 16300);
    const auto deadline = a.left.deadlineMs;
    s.pauseObservations();
    assert(s.snapshot().left.deadlineMs == deadline && !s.snapshot().match);
    feed(s, fight(5, 1)); feed(s, fight(6, 1));
    assert(s.snapshot().left.eventCount == 2); // pause breaks baseline
    feed(s, fight(7, 0)); feed(s, fight(8, 0));
    assert(s.snapshot().left.eventCount == 3); // single drop to zero is real
    s.reset();
    assert(s.snapshot().left.eventCount == 0 && s.snapshot().left.deadlineMs == 0);
    assert(s.snapshot().left.eventSerial == 3);
}
void ambiguity() {
    TimerSession s;
    feed(s, fight(0)); feed(s, fight(1, 2)); feed(s, fight(2, 2));
    assert(s.snapshot().left.eventCount == 0); // multi-drop not several substitutes
    feed(s, fight(3, 1));
    auto unknown = fight(4, 1); unknown.Res.Beads[0].Unknown = true;
    feed(s, unknown); feed(s, fight(5, 1));
    assert(s.snapshot().left.eventCount == 0);
    feed(s, fight(6, 1)); assert(s.snapshot().left.eventCount == 1);
    auto f = fight(7, 0); feed(s, f); feed(s, f); // replayed timestamp no vote
    assert(s.snapshot().left.eventCount == 1);
    f = fight(8, 0); f.Duplicate = true; feed(s, f);
    assert(s.snapshot().left.eventCount == 1);
    feed(s, fight(9, 0)); assert(s.snapshot().left.eventCount == 2);
    feed(s, fight(10, 1)); feed(s, fight(11, 1));
    feed(s, fight(30, 0)); feed(s, fight(31, 0));
    assert(s.snapshot().left.eventCount == 2); // blackout cannot authorize itself
}
void recoverAndReuse() {
    SideClock c;
    auto observe = [&](int n, int ms) { c.observe(n, ms * Millisecond, epoch + ms, 2, false); };
    observe(4, 1000); observe(3, 1100); observe(3, 1200);
    observe(4, 16050); // +1 only once, near the real 16100 cooldown end
    observe(3, 16150); observe(3, 16250);
    assert(c.snapshot().eventCount == 2 && c.snapshot().deadlineMs == epoch + 31150);
    // Same +1 well away from a cooldown deadline is just a glint.
    observe(4, 17000); observe(3, 17100); observe(3, 17200);
    assert(c.snapshot().eventCount == 2);
}
void topologyAndScenes() {
    TimerSession s;
    feed(s, fight(0)); feed(s, fight(1, 3)); feed(s, fight(2, 3));
    auto f = fight(3, 2); f.Width = 1920; f.Height = 1080;
    feed(s, f); f.CapturedAt += 100 * Millisecond; feed(s, f);
    assert(s.snapshot().left.eventCount == 1);
    feed(s, fight(5, 5, 6, 6)); feed(s, fight(6, 5, 6, 6));
    assert(s.snapshot().left.eventCount == 1);
    feed(s, fight(7, 4, 6, 6)); feed(s, fight(8, 4, 6, 6));
    assert(s.snapshot().left.eventCount == 2 && s.snapshot().rightCount == 6);
    f = fight(9); f.Hold = true; f.Res.Fighting = false; f.Res.Scene = "vs"; feed(s, f);
    feed(s, fight(30, 3, 6, 6)); feed(s, fight(31, 3, 6, 6));
    assert(s.snapshot().left.eventCount == 2); // VS return is calibration only
    feed(s, fight(32, 2, 6, 6)); feed(s, fight(33, 2, 6, 6));
    assert(s.snapshot().left.eventCount == 3);
    f = fight(34, 2, 6, 6); f.Res.RoundOpening = true; f.Hold = true; feed(s, f);
    assert(s.snapshot().left.deadlineMs == 0 && s.snapshot().left.eventCount == 3);
    feed(s, fight(38, 1, 6, 6)); feed(s, fight(39, 1, 6, 6));
    assert(s.snapshot().left.eventCount == 3);
    for (int i = 40; i < 49; ++i) { f = fight(i); f.Res.Fighting = false; f.Res.Scene = "result"; feed(s, f); }
    assert(s.fighting());
    f = fight(49); f.Res.Fighting = false; f.Res.Scene = "unknown"; feed(s, f);
    for (int i = 50; i < 59; ++i) { f = fight(i); f.Res.Fighting = false; f.Res.Scene = "result"; feed(s, f); }
    assert(s.fighting());
    f.CapturedAt += 100 * Millisecond; feed(s, f);
    assert(!s.fighting() && s.snapshot().left.eventCount == 0);
}
void manualSideAndRules() {
    TimerSettings settings;
    assert(settings.playerSide == "left");
    settings.dual = true;
    TimerSession manual(settings);
    feed(manual, fight(0)); feed(manual, fight(1, 3, 3)); feed(manual, fight(2, 3, 3));
    const auto original = manual.snapshot();
    assert(original.left.alternateDeadlineMs == 0 && original.right.alternateDeadlineMs == epoch + 11100);
    settings.playerSide = "right"; manual.configure(settings);
    auto switched = manual.snapshot();
    assert(switched.left.alternateDeadlineMs == epoch + 11100 && switched.right.alternateDeadlineMs == 0);
    assert(switched.left.deadlineMs == original.left.deadlineMs && switched.right.deadlineMs == original.right.deadlineMs);
    assert(switched.left.eventCount == 1 && switched.right.eventCount == 1);
    // Even a shared result carrying a side hint cannot change manual selection.
    auto f = fight(3, 3, 3); f.Res.PlayerSide = "left"; feed(manual, f);
    assert(manual.snapshot().left.alternateDeadlineMs > 0 && manual.snapshot().right.alternateDeadlineMs == 0);
    settings.dual = false; settings.ninjaQuery = ninja::HanzoEdo; manual.configure(settings);
    assert(manual.snapshot().left.alternateDeadlineMs > 0 && manual.snapshot().right.alternateDeadlineMs == 0);
    settings.playerSide = "left"; manual.configure(settings);
    assert(manual.snapshot().left.alternateDeadlineMs == 0 && manual.snapshot().right.alternateDeadlineMs > 0);
    settings.ninjaQuery = "普通忍者"; manual.configure(settings);
    assert(manual.snapshot().left.alternateDeadlineMs == 0 && manual.snapshot().right.alternateDeadlineMs == 0);
    // Direct C++ callers get the safe left default for unsupported side modes.
    settings.playerSide = "auto"; settings.dual = true; manual.configure(settings);
    assert(manual.snapshot().left.alternateDeadlineMs == 0 && manual.snapshot().right.alternateDeadlineMs > 0);
    manual.pauseObservations();
    assert(manual.snapshot().right.deadlineMs == original.right.deadlineMs);
    for (const auto& name : {ninja::FifthMizukage, ninja::HanzoEdo}) {
        TimerSession t;
        feed(t, fight(0));
        f = fight(1, 3); f.Res.LeftNinja = name; feed(t, f);
        f.CapturedAt += 100 * Millisecond; feed(t, f);
        assert(t.snapshot().left.alternateDeadlineMs == epoch + 11100);
    }
    assert(!ninja::DualCooldown("半藏") && !ninja::DualCooldown("照美冥"));
    // A PiP control change between the two confirming frames is not a new
    // capture source. Do not lose the first candidate or move its deadline.
    TimerSession duringDrop;
    feed(duringDrop, fight(0)); feed(duringDrop, fight(1, 3, 3));
    TimerSettings controls;
    controls.playerSide = "right"; controls.showBothSides = true;
    duringDrop.configure(controls);
    const auto afterControl = feed(duringDrop, fight(2, 3, 3));
    assert(afterControl.left.eventCount == 1 && afterControl.right.eventCount == 1);
    assert(afterControl.left.deadlineMs == epoch + 16100 && afterControl.right.deadlineMs == epoch + 16100);
}
}
int main() {
    clocks(); ambiguity(); recoverAndReuse(); topologyAndScenes(); manualSideAndRules();
    std::cout << "TimerSession: clocks/recovery/duplicates/blackout/topology/scenes/manual side/special rules PASS\n";
}
