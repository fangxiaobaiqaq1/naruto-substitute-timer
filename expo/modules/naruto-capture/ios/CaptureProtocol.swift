import Foundation
import CoreFoundation

// Shared by the app and upload extension. No App Groups, captured frames, audio
// or remote endpoints. This is local transport, not an authentication boundary against a
// malicious app on the same device. Session IDs prevent accidental stale replay.
enum CaptureProtocol {
  static let port: UInt16 = 17861
  static let limit = 16_384 // Per JSON line, including LF.
  static let receiveLimit = 4096
  static let version = 1
  static func now() -> Double {
    var value = timespec()
    clock_gettime(CLOCK_MONOTONIC, &value)
    return Double(value.tv_sec) + Double(value.tv_nsec) / 1_000_000_000
  }
  static func captureInterval(fps: Double, inMatch: Bool) -> Double {
    let interval = 1 / min(10, max(1, fps.isFinite ? fps : 8))
    return inMatch ? interval : max(0.4, interval)
  }
  static func encode(_ packet: [String: Any]) -> Data? {
    guard JSONSerialization.isValidJSONObject(packet),
          var data = try? JSONSerialization.data(withJSONObject: packet), data.count < limit else { return nil }
    data.append(10)
    return data
  }
  enum FramingError: Error { case oversized }

  // One bounded receive may complete a near-limit line AND start another.
  // Limit each line, not the combined stream; retain only an unfinished tail.
  static func receiveLines(_ data: Data, buffering input: inout Data) throws -> [Data] {
    guard data.count <= receiveLimit, input.count < limit else { throw FramingError.oversized }
    input.append(data) // At most limit - 1 + receiveLimit bytes.
    var lines: [Data] = []
    while let newline = input.firstIndex(of: 10) {
      let line = input.prefix(upTo: newline)
      guard line.count < limit else { throw FramingError.oversized }
      lines.append(line)
      input.removeSubrange(...newline)
    }
    guard input.count < limit else { throw FramingError.oversized }
    return lines
  }

  static func number(_ value: Any?, _ fallback: Double = 0) -> Double {
    guard let n = value as? NSNumber, CFGetTypeID(n) != CFBooleanGetTypeID(), n.doubleValue.isFinite else { return fallback }
    return n.doubleValue
  }
  static func bool(_ value: Any?, _ fallback: Bool = false) -> Bool {
    guard let n = value as? NSNumber, CFGetTypeID(n) == CFBooleanGetTypeID() else { return fallback }
    return n.boolValue
  }
  static func sequence(_ value: Any?) -> Int? {
    let n = number(value, -1)
    guard n >= 1, n <= 9_007_199_254_740_991, n.rounded() == n else { return nil }
    return Int(n)
  }
  static func settings(_ source: [String: Any]) -> [String: Any] {
    func bounded(_ key: String, _ low: Double, _ high: Double, _ fallback: Double) -> Double {
      min(high, max(low, number(source[key], fallback)))
    }
    let supplied = source["calibration"] as? [String: Any] ?? [:]
    var calibration: [String: Any] = ["profile": supplied["profile"] as? String == "camp" ? "camp" : "duel"]
    for key in ["leftBeads", "rightBeads", "leftAvatar", "rightAvatar"] {
      guard let rect = supplied[key] as? [String: Any] else { continue }
      let x = number(rect["x"], -1), y = number(rect["y"], -1)
      let w = number(rect["width"], -1), h = number(rect["height"], -1)
      guard x >= -0.000001, y >= -0.000001, w > 0, h > 0,
            x + w <= 1.000001, y + h <= 1.000001 else { continue }
      let rx = max(0, x), ry = max(0, y)
      let rw = min(w, 1 - rx), rh = min(h, 1 - ry)
      let avatar = key.hasSuffix("Avatar")
      let u = avatar ? rw * 960 / 68.5 : rw * 16
      let v = avatar ? rh * 540 / 68.5 : rh * 36
      let k = max(u, v)
      guard k >= 0.5, k <= 2, min(u, v) >= k * 0.25 else { continue }
      calibration[key] = ["x": rx, "y": ry, "width": rw, "height": rh]
    }
    // iOS has no side selection: both sides are always shown, and special
    // 15/10 s ninjas come from per-side recognition, not an opponent override.
    return [
      "calibration": calibration,
      "fontScale": bounded("fontScale", 0.55, 1.2, 0.75),
      "roiScale": bounded("roiScale", 0.7, 1.4, 1),
      "roiOffsetX": bounded("roiOffsetX", -0.15, 0.15, 0),
      "roiOffsetY": bounded("roiOffsetY", -0.15, 0.15, 0),
      "backgroundFPS": bounded("backgroundFPS", 1, 10, 8).rounded()
    ]
  }
}

// Convert each *new* confirmed absolute epoch deadline once, using the sender's
// paired clock. Never subtract Date() on every frame: manual/NTP clock changes
// must not restart, shorten or extend a confirmed cooldown.
struct CaptureDeadline {
  private(set) var epochMs = 0.0
  private(set) var monotonic = 0.0
  mutating func update(_ epoch: Double, sentAtMs: Double, sentAtMonotonic: Double) {
    guard epoch.isFinite, epoch >= 0, epoch != epochMs else { return }
    epochMs = epoch
    monotonic = epoch == 0 ? 0 : sentAtMonotonic + (epoch - sentAtMs) / 1000
  }
  func remaining(at now: Double) -> Double { max(0, monotonic - now) }
}
