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
