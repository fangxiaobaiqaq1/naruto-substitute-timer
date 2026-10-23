import Foundation
import CoreFoundation
#if canImport(CoreMedia)
import CoreMedia
#endif

// Host-only source of truth. The upload extension still needs only
// CaptureProtocol; it receives these values in the ordinary configuration.
final class CaptureControls {
  private static let key = "NarutoCapture.controls.v1"
  private let defaults: UserDefaults
  private(set) var playerSide = "left"
  private(set) var showBothSides = false
  private(set) var rememberSide = true
  private(set) var initialized = false
  private(set) var revision = 0

  init(defaults: UserDefaults = .standard) {
    self.defaults = defaults
    guard let saved = defaults.dictionary(forKey: Self.key),
          CaptureProtocol.bool(saved["initialized"]) else { return }
    playerSide = saved["playerSide"] as? String == "right" ? "right" : "left"
    showBothSides = CaptureProtocol.bool(saved["showBothSides"])
    rememberSide = CaptureProtocol.bool(saved["rememberSide"], true)
    initialized = true
    revision = Int(min(9_007_199_254_740_990, max(0, CaptureProtocol.number(saved["revision"]))).rounded(.down))
    // Remember-side affects the next launch, never the current selection.
    if !rememberSide && playerSide != "left" {
      playerSide = "left"
      revision += 1
      persist()
    }
  }

  var snapshot: [String: Any] {
    ["playerSide": playerSide, "showBothSides": showBothSides,
     "rememberSide": rememberSide, "initialized": initialized, "revision": revision]
  }

  @discardableResult
  func setControls(_ patch: [String: Any]) -> Bool {
    let side = patch["playerSide"] as? String
    func boolean(_ key: String) -> Bool? {
      guard let n = patch[key] as? NSNumber, CFGetTypeID(n) == CFBooleanGetTypeID() else { return nil }
      return n.boolValue
    }
    let validSide = side == "left" || side == "right" ? side : nil
    let both = boolean("showBothSides"), remember = boolean("rememberSide")
    guard validSide != nil || both != nil || remember != nil else { return false }
    let nextSide = validSide ?? playerSide
    let nextBoth = both ?? showBothSides
    let nextRemember = remember ?? rememberSide
    guard !initialized || nextSide != playerSide || nextBoth != showBothSides || nextRemember != rememberSide else { return false }
    playerSide = nextSide; showBothSides = nextBoth; rememberSide = nextRemember
    initialized = true
    revision += 1
    persist()
    return true
  }

  // One-time legacy migration. Later full settings from suspended/stale JS
  // must never undo a PiP selection. All other settings remain normalized.
  func settings(_ source: [String: Any]) -> [String: Any] {
    var output = CaptureProtocol.settings(source)
    if !initialized { setControls(output) }
    output["playerSide"] = playerSide
    output["showBothSides"] = showBothSides
    output["rememberSide"] = rememberSide
    return output
  }

  private func persist() { defaults.set(snapshot, forKey: Self.key) }
}

enum CaptureTransport {
  static func side(forSkipSeconds seconds: Double) -> String? {
    guard seconds.isFinite, seconds != 0 else { return nil }
    return seconds < 0 ? "left" : "right"
  }

  static func showBothSides(playing: Bool) -> Bool { !playing }

  #if canImport(CoreMedia)
  // A utility timeline, not recorded game video. Leave ample finite space on
  // BOTH sides of the actual display-layer clock to permit either skip.
  // AVKit still decides which controls are visible on a device.
  // Never seek/reset that clock: cooldowns and frame delivery keep running.
  static func timeRange(around time: CMTime) -> CMTimeRange {
    guard time.isNumeric else { return CMTimeRange(start: .zero, duration: .zero) }
    let margin = CMTime(seconds: 60, preferredTimescale: 600)
    let start = CMTimeSubtract(time, margin)
    guard start.isNumeric else { return CMTimeRange(start: .zero, duration: .zero) }
    return CMTimeRange(start: start, duration: CMTime(seconds: 120, preferredTimescale: 600))
  }
  #endif
}
