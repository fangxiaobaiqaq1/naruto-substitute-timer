import Foundation

// Compile on macOS with:
// xcrun swiftc ../ios/CaptureProtocol.swift main.swift -o /tmp/naruto-wire-check
// /tmp/naruto-wire-check
func near(_ a: Double, _ b: Double) { precondition(abs(a - b) < 0.00001, "\(a) != \(b)") }

var clock = CaptureDeadline()
clock.update(1_040_000, sentAtMs: 1_000_000, sentAtMonotonic: 500)
near(clock.remaining(at: 502), 38)
// Repeated snapshots / wall-clock corrections never slide the original event.
clock.update(1_040_000, sentAtMs: 9_000_000, sentAtMonotonic: 505)
near(clock.remaining(at: 505), 35)
near(clock.remaining(at: 900), 0)
clock.update(.nan, sentAtMs: 1, sentAtMonotonic: 1)
near(clock.epochMs, 1_040_000)
clock.update(0, sentAtMs: 1_000_000, sentAtMonotonic: 510)
near(clock.remaining(at: 510), 0)
clock.update(1_080_000, sentAtMs: 1_050_000, sentAtMonotonic: 550)
near(clock.remaining(at: 551), 29)
precondition(CaptureProtocol.sequence(true) == nil)
precondition(CaptureProtocol.sequence(-1) == nil)
precondition(CaptureProtocol.sequence(2.5) == nil)
precondition(CaptureProtocol.sequence(9_007_199_254_740_992.0) == nil)
precondition(CaptureProtocol.sequence(123) == 123)
precondition(CaptureProtocol.encode(["value": Double.nan]) == nil)
precondition(CaptureProtocol.encode(["value": String(repeating: "x", count: 16_384)]) == nil)
precondition(CaptureProtocol.encode(["version": 1])?.last == 10)
let settings = CaptureProtocol.settings([
  "backgroundFPS": 100, "fontScale": -3, "roiScale": true,
  "playerNames": Array(repeating: String(repeating: "a", count: 99), count: 99),
  "calibration": ["profile": "camp", "leftBeads": ["x": 0.5, "y": 0.1, "width": 1.0 / 16, "height": 1.0 / 36],
                  "rightBeads": ["x": 1, "y": 0, "width": 0.1, "height": 0.1]]
])
near(settings["backgroundFPS"] as! Double, 10)
near(settings["fontScale"] as! Double, 0.55)
near(settings["roiScale"] as! Double, 1)
let names = settings["playerNames"] as! [String]
precondition(names.count == 20 && names[0].count == 40)
let calibration = settings["calibration"] as! [String: Any]
precondition(calibration["leftBeads"] != nil && calibration["rightBeads"] == nil)
print("PASS native deadline pinning, clock jumps, expiry, sequence bounds, JSON bounds and settings validation")
