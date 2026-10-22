import Foundation
import CoreFoundation

// Shared by the app and upload extension. No App Groups, pixels, audio or remote
// endpoints. This is local transport, not an authentication boundary against a
// malicious app on the same device. Session IDs prevent accidental stale replay.
enum CaptureProtocol {
  static let port: UInt16 = 17861
  static let limit = 16_384
  static let version = 1
  static func now() -> Double {
    var value = timespec()
    clock_gettime(CLOCK_MONOTONIC, &value)
    return Double(value.tv_sec) + Double(value.tv_nsec) / 1_000_000_000
  }
  static func encode(_ packet: [String: Any]) -> Data? {
    guard JSONSerialization.isValidJSONObject(packet),
          var data = try? JSONSerialization.data(withJSONObject: packet), data.count < limit else { return nil }
    data.append(10)
    return data
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
    let side = source["playerSide"] as? String ?? "auto"
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
    return [
      "playerSide": ["left", "right"].contains(side) ? side : "auto",
      "showBothSides": bool(source["showBothSides"]),
      "dual": bool(source["dual"]),
      "playerName": String((source["playerName"] as? String ?? "").prefix(40)),
      "playerNames": Array((source["playerNames"] as? [String] ?? []).prefix(20)).map { String($0.prefix(40)) },
      "ninjaQuery": String((source["ninjaQuery"] as? String ?? "").prefix(100)),
      "rememberSide": bool(source["rememberSide"], true),
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
