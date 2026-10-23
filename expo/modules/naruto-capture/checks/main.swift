import Foundation
#if canImport(CoreMedia)
import CoreMedia
#endif

// Compile on macOS with (no AVKit/UIKit required):
// xcrun swiftc ../ios/CaptureProtocol.swift ../ios/CaptureControls.swift main.swift -o /tmp/naruto-wire-check
// /tmp/naruto-wire-check
func near(_ a: Double, _ b: Double) { precondition(abs(a - b) < 0.00001, "\(a) != \(b)") }

near(CaptureProtocol.captureInterval(fps: 8, inMatch: true), 0.125)
near(CaptureProtocol.captureInterval(fps: 8, inMatch: false), 0.4)
near(CaptureProtocol.captureInterval(fps: 1, inMatch: false), 1)
near(CaptureProtocol.captureInterval(fps: .nan, inMatch: true), 0.125)

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

// A maximum-size packet is 16,383 JSON bytes plus LF. The next receive may
// contain its final bytes AND another packet, without either being oversized.
let overhead = CaptureProtocol.encode(["value": ""])!.count
let maximumValue = String(repeating: "x", count: CaptureProtocol.limit - overhead)
let maximumPacket = CaptureProtocol.encode(["value": maximumValue])!
let smallPacket = CaptureProtocol.encode(["sequence": 2])!
precondition(maximumPacket.count == CaptureProtocol.limit)
var input = Data()
for offset in stride(from: 0, to: 16_000, by: CaptureProtocol.receiveLimit) {
  let chunk = maximumPacket.subdata(in: offset..<min(offset + CaptureProtocol.receiveLimit, 16_000))
  let lines = try CaptureProtocol.receiveLines(chunk, buffering: &input)
  precondition(lines.isEmpty)
}
precondition(input.count == 16_000)
let boundaryChunk = Data(maximumPacket.dropFirst(16_000)) + smallPacket
precondition(input.count + boundaryChunk.count > CaptureProtocol.limit)
let boundaryLines = try CaptureProtocol.receiveLines(boundaryChunk, buffering: &input)
precondition(boundaryLines == [Data(maximumPacket.dropLast()), Data(smallPacket.dropLast())])
precondition(input.isEmpty)
let decoded = try JSONSerialization.jsonObject(with: boundaryLines[0]) as! [String: String]
precondition(decoded["value"] == maximumValue)

// Different receive boundaries must produce exactly the same packets, even
// with a nonempty tail after several complete lines and Data's sliced indices.
let wire = maximumPacket + smallPacket + maximumPacket + Data("{\"tail\":".utf8)
for size in [1, 383, 384, 4095, CaptureProtocol.receiveLimit] {
  input = Data()
  var lines: [Data] = []
  for offset in stride(from: 0, to: wire.count, by: size) {
    lines += try CaptureProtocol.receiveLines(wire.subdata(in: offset..<min(offset + size, wire.count)), buffering: &input)
    precondition(input.count < CaptureProtocol.limit)
  }
  precondition(lines == [Data(maximumPacket.dropLast()), Data(smallPacket.dropLast()), Data(maximumPacket.dropLast())])
  precondition(input == Data("{\"tail\":".utf8))
  let tailLines = try CaptureProtocol.receiveLines(Data("true}\n".utf8), buffering: &input)
  precondition(tailLines == [Data("{\"tail\":true}".utf8)] && input.isEmpty)
}

func expectOversized(_ wire: Data) {
  var input = Data()
  do {
    for offset in stride(from: 0, to: wire.count, by: CaptureProtocol.receiveLimit) {
      _ = try CaptureProtocol.receiveLines(wire.subdata(in: offset..<min(offset + CaptureProtocol.receiveLimit, wire.count)), buffering: &input)
    }
    preconditionFailure("oversized line accepted")
  } catch CaptureProtocol.FramingError.oversized {
    precondition(input.count <= CaptureProtocol.limit - 1 + CaptureProtocol.receiveLimit)
  } catch { preconditionFailure("unexpected framing error: \(error)") }
}
let oversizedJSON = try JSONSerialization.data(withJSONObject: ["value": maximumValue + "x"])
precondition(oversizedJSON.count == CaptureProtocol.limit)
precondition(CaptureProtocol.encode(["value": maximumValue + "x"]) == nil)
expectOversized(oversizedJSON + Data([10])) // Complete over-limit line.
expectOversized(oversizedJSON) // Unterminated tail cannot grow to limit either.
expectOversized(smallPacket + oversizedJSON + Data([10]))
input = Data()
do {
  _ = try CaptureProtocol.receiveLines(Data(repeating: 0, count: CaptureProtocol.receiveLimit + 1), buffering: &input)
  preconditionFailure("oversized receive accepted")
} catch CaptureProtocol.FramingError.oversized {
  precondition(input.isEmpty, "reject before appending an oversized receive")
}
let settings = CaptureProtocol.settings([
  "backgroundFPS": 100, "fontScale": -3, "roiScale": true,
  "playerNames": Array(repeating: String(repeating: "a", count: 99), count: 99),
  "calibration": ["profile": "camp", "leftBeads": ["x": 0.5, "y": 0.1, "width": 1.0 / 16, "height": 1.0 / 36],
                  "rightBeads": ["x": 1, "y": 0, "width": 0.1, "height": 0.1]]
])
near(settings["backgroundFPS"] as! Double, 10)
near(settings["fontScale"] as! Double, 0.55)
near(settings["roiScale"] as! Double, 1)
precondition(settings["playerNames"] == nil && settings["playerName"] == nil)
precondition(settings["playerSide"] as? String == "left")
precondition(CaptureProtocol.settings(["playerSide": "right"])["playerSide"] as? String == "right")
precondition(CaptureProtocol.settings(["playerSide": "auto"])["playerSide"] as? String == "left")
let calibration = settings["calibration"] as! [String: Any]
precondition(calibration["leftBeads"] != nil && calibration["rightBeads"] == nil)
print("PASS native deadline pinning, clock jumps, expiry, sequence bounds, fragmented NDJSON boundaries, JSON bounds and settings validation")

let suiteName = "NarutoCaptureChecks.\(UUID().uuidString)"
let defaults = UserDefaults(suiteName: suiteName)!
defer { defaults.removePersistentDomain(forName: suiteName) }
let controls = CaptureControls(defaults: defaults)
precondition(!controls.initialized && controls.revision == 0)
precondition(controls.playerSide == "left" && !controls.showBothSides && controls.rememberSide)
precondition(!controls.setControls(["playerSide": "auto", "showBothSides": 1, "rememberSide": "false", "revision": 99]))
precondition(!controls.initialized && controls.revision == 0)
let migrated = controls.settings(["playerSide": "right", "showBothSides": true, "rememberSide": true, "backgroundFPS": 100])
precondition(controls.initialized && controls.playerSide == "right" && controls.showBothSides && controls.revision == 1)
near(migrated["backgroundFPS"] as! Double, 10)
precondition(CaptureControls(defaults: defaults).playerSide == "right")
let ignored = controls.settings(["playerSide": "left", "showBothSides": false, "rememberSide": false, "fontScale": -1])
precondition(ignored["playerSide"] as? String == "right" && ignored["showBothSides"] as? Bool == true)
precondition(ignored["rememberSide"] as? Bool == true && controls.revision == 1)
near(ignored["fontScale"] as! Double, 0.55)
precondition(!controls.setControls(["playerSide": "right", "showBothSides": true, "rememberSide": true]))
precondition(controls.revision == 1, "identical actions must not create a JS echo revision")
for seconds in [0.0, -0.0, .nan, .infinity, -.infinity] {
  precondition(CaptureTransport.side(forSkipSeconds: seconds) == nil)
}
precondition(CaptureTransport.side(forSkipSeconds: -15) == "left")
precondition(CaptureTransport.side(forSkipSeconds: 15) == "right")
precondition(CaptureTransport.side(forSkipSeconds: -.leastNonzeroMagnitude) == "left")
precondition(CaptureTransport.side(forSkipSeconds: .leastNonzeroMagnitude) == "right")
for seconds in [-15.0, -15.0, 15.0, 15.0] {
  let before = controls.revision
  let side = CaptureTransport.side(forSkipSeconds: seconds)!
  let changed = controls.playerSide != side
  controls.setControls(["playerSide": side])
  precondition(controls.playerSide == side && controls.revision == before + (changed ? 1 : 0))
}
for playing in [true, true, false, false] {
  let before = controls.revision
  let both = CaptureTransport.showBothSides(playing: playing)
  let changed = controls.showBothSides != both
  controls.setControls(["showBothSides": both])
  precondition(controls.showBothSides == !playing && controls.revision == before + (changed ? 1 : 0))
}
precondition(controls.setControls(["rememberSide": false]))
precondition(controls.playerSide == "right", "rememberSide=false must not change this launch's selection")
let revision = controls.revision
let restarted = CaptureControls(defaults: defaults)
precondition(restarted.initialized && restarted.playerSide == "left" && restarted.showBothSides && !restarted.rememberSide)
precondition(restarted.revision == revision + 1)
precondition(CaptureControls(defaults: defaults).revision == restarted.revision, "launch reset is persisted once")
let preserved = restarted.settings(["playerSide": "right", "showBothSides": false, "rememberSide": true])
precondition(preserved["playerSide"] as? String == "left" && preserved["showBothSides"] as? Bool == true)
precondition(preserved["rememberSide"] as? Bool == false)
// Explicit controls before the first full settings push also block migration.
defaults.removePersistentDomain(forName: suiteName)
let explicit = CaptureControls(defaults: defaults)
precondition(explicit.setControls(["playerSide": "right"]))
_ = explicit.settings(["playerSide": "left", "showBothSides": true])
precondition(explicit.playerSide == "right" && !explicit.showBothSides && explicit.revision == 1)
// Even an empty first full settings push initializes the canonical defaults.
defaults.removePersistentDomain(forName: suiteName)
let firstDefault = CaptureControls(defaults: defaults)
_ = firstDefault.settings([:])
precondition(firstDefault.initialized && firstDefault.revision == 1)
_ = firstDefault.settings(["playerSide": "right"])
precondition(firstDefault.playerSide == "left")
print("PASS native controls: validation, migration, stale full-settings immunity, persistence, remember-side restart, idempotent skip/center mapping")

#if canImport(CoreMedia)
// Exercise the exact CMTime -> seconds conversion used by the AVKit delegate.
// A tiny nonzero skip still selects a side; it must never round into a no-op.
let skipCases: [(CMTime, String?)] = [
  (CMTime(value: -1, timescale: Int32.max), "left"),
  (CMTime(value: 1, timescale: Int32.max), "right"),
  (CMTime(seconds: -15, preferredTimescale: 600), "left"),
  (CMTime(seconds: 15, preferredTimescale: 600), "right"),
  (.zero, nil), (.invalid, nil), (.indefinite, nil),
  (.positiveInfinity, nil), (.negativeInfinity, nil),
  (CMTime(seconds: .nan, preferredTimescale: 600), nil),
  (CMTime(seconds: .infinity, preferredTimescale: 600), nil),
  (CMTime(seconds: -.infinity, preferredTimescale: 600), nil)
]
for (interval, expected) in skipCases {
  precondition(CaptureTransport.side(forSkipSeconds: CMTimeGetSeconds(interval)) == expected)
}
print("PASS CoreMedia skip mapping: tiny signed intervals, zero, NaN, invalid, indefinite and infinities")
for seconds in [0.0, 0.25, 1000.0, 10_000_000.0] {
  let time = CMTime(seconds: seconds, preferredTimescale: 600)
  let range = CaptureTransport.timeRange(around: time)
  precondition(range.isValid && range.start.isNumeric && range.duration.isNumeric)
  near(CMTimeGetSeconds(range.start), seconds - 60)
  near(CMTimeGetSeconds(range.duration), 120)
  precondition(CMTimeRangeContainsTime(range, time: CMTimeSubtract(time, CMTime(seconds: 15, preferredTimescale: 600))))
  precondition(CMTimeRangeContainsTime(range, time: CMTimeAdd(time, CMTime(seconds: 15, preferredTimescale: 600))))
}
for time in [CMTime.invalid, .indefinite, .positiveInfinity, .negativeInfinity] {
  precondition(CaptureTransport.timeRange(around: time).isEmpty)
}
print("PASS finite rolling CoreMedia transport window around the running display clock")
#endif
