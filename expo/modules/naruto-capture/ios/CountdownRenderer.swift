import AVFoundation
import UIKit

/// One side of the always-both-sides strip (APK `MiniTimerView` two-column mode).
struct CountdownColumn {
  let title: String
  let clock: String
  let color: UIColor
  let alternate: (text: String, color: UIColor)?
}

final class CountdownRenderer {
  // APK TimerColors: cyan = cooling down, amber = last 3 s, grey-blue = idle.
  static let idle = UIColor(red: 0x9F / 255, green: 0xB9 / 255, blue: 0xC4 / 255, alpha: 1)
  static let live = UIColor(red: 0x5C / 255, green: 0xE1 / 255, blue: 1, alpha: 1)
  static let warn = UIColor(red: 1, green: 0xB5 / 255, blue: 0x47 / 255, alpha: 1)
  static let tag = UIColor(red: 0xE3 / 255, green: 0xF6 / 255, blue: 0xFB / 255, alpha: 1)
  private static let background = UIColor(red: 0x06 / 255, green: 0x18 / 255, blue: 0x20 / 255, alpha: 1)
  private static let border = UIColor(red: 0x24 / 255, green: 0x48 / 255, blue: 0x5A / 255, alpha: 1)

  /// APK SideClock.displayTenths: 14.87 -> 148 (floor, never rounds up to 15.0).
  static func tenths(_ seconds: Double) -> Int {
    seconds > 0 && seconds.isFinite ? Int((seconds * 10 + 1e-9).rounded(.down)) : 0
  }

  let layer = AVSampleBufferDisplayLayer()
  private var pool: CVPixelBufferPool?
  private var format: CMVideoFormatDescription?
  // iOS gives apps no PiP size API; the system sizes the window by the video's
  // aspect ratio. A thin 4:1 strip keeps the floating window small.
  private let width = 720
  private let height = 180

  var playbackTime: CMTime {
    guard let timebase = layer.controlTimebase else { return CMClockGetTime(CMClockGetHostTimeClock()) }
    return CMTimebaseGetTime(timebase)
  }

  init() {
    layer.videoGravity = .resizeAspect
    layer.backgroundColor = UIColor.black.cgColor
    let attributes: [String: Any] = [
      kCVPixelBufferPixelFormatTypeKey as String: kCVPixelFormatType_32BGRA,
      kCVPixelBufferWidthKey as String: width,
      kCVPixelBufferHeightKey as String: height,
      kCVPixelBufferIOSurfacePropertiesKey as String: [:],
      kCVPixelBufferCGImageCompatibilityKey as String: true,
      kCVPixelBufferCGBitmapContextCompatibilityKey as String: true
    ]
    CVPixelBufferPoolCreate(kCFAllocatorDefault, nil, attributes as CFDictionary, &pool)
    var timebase: CMTimebase?
    if CMTimebaseCreateWithSourceClock(allocator: kCFAllocatorDefault,
                                      sourceClock: CMClockGetHostTimeClock(), timebaseOut: &timebase) == noErr,
       let timebase {
      CMTimebaseSetTime(timebase, time: CMClockGetTime(CMClockGetHostTimeClock()))
      CMTimebaseSetRate(timebase, rate: 1)
      layer.controlTimebase = timebase
    }
  }

  @discardableResult
  func render(columns: [CountdownColumn], labelColor: UIColor) -> Bool {
    if layer.status == .failed || layer.requiresFlushToResumeDecoding { layer.flush() }
    guard layer.isReadyForMoreMediaData, let pool else { return false }
    var output: CVPixelBuffer?
    let auxiliary = [kCVPixelBufferPoolAllocationThresholdKey as String: 4] as CFDictionary
    guard CVPixelBufferPoolCreatePixelBufferWithAuxAttributes(kCFAllocatorDefault, pool, auxiliary, &output) == kCVReturnSuccess,
          let pixels = output,
          CVPixelBufferLockBaseAddress(pixels, []) == kCVReturnSuccess else { return false }
    defer { CVPixelBufferUnlockBaseAddress(pixels, []) }
    guard let base = CVPixelBufferGetBaseAddress(pixels),
          let context = CGContext(data: base, width: width, height: height, bitsPerComponent: 8,
                                  bytesPerRow: CVPixelBufferGetBytesPerRow(pixels),
                                  space: CGColorSpaceCreateDeviceRGB(),
                                  bitmapInfo: CGBitmapInfo.byteOrder32Little.rawValue | CGImageAlphaInfo.premultipliedFirst.rawValue) else { return false }
    context.setFillColor(Self.background.cgColor)
    context.fill(CGRect(x: 0, y: 0, width: CGFloat(width), height: CGFloat(height)))
    context.translateBy(x: 0, y: CGFloat(height))
    context.scaleBy(x: 1, y: -1)
    UIGraphicsPushContext(context)
    let paragraph = NSMutableParagraphStyle()
    paragraph.alignment = .center
    paragraph.lineBreakMode = .byTruncatingTail
    func draw(_ text: String, _ rect: CGRect, _ font: UIFont, _ color: UIColor) {
      (text as NSString).draw(in: rect, withAttributes: [.font: font, .foregroundColor: color, .paragraphStyle: paragraph])
    }
    let half = CGFloat(width / 2)
    context.setFillColor(Self.border.cgColor)
    context.fill(CGRect(x: half - 1, y: 24, width: 2, height: CGFloat(height) - 48))
    for (index, item) in columns.prefix(2).enumerated() {
      let x = CGFloat(index) * half
      draw(item.title, CGRect(x: x + 8, y: 64, width: 54, height: 52), .systemFont(ofSize: 40, weight: .semibold), labelColor)
      let clockX = x + 62, clockWidth = half - 70
      if let alternate = item.alternate {
        draw(item.clock, CGRect(x: clockX, y: 4, width: clockWidth, height: 118),
             .monospacedDigitSystemFont(ofSize: 96, weight: .bold), item.color)
        draw(alternate.text, CGRect(x: clockX, y: 122, width: clockWidth, height: 52),
             .monospacedDigitSystemFont(ofSize: 38, weight: .semibold), alternate.color)
      } else {
        draw(item.clock, CGRect(x: clockX, y: 18, width: clockWidth, height: 150),
             .monospacedDigitSystemFont(ofSize: 120, weight: .bold), item.color)
      }
    }
    UIGraphicsPopContext()
    if format == nil {
      CMVideoFormatDescriptionCreateForImageBuffer(allocator: kCFAllocatorDefault, imageBuffer: pixels,
                                                  formatDescriptionOut: &format)
    }
    guard let format else { return false }
    var timing = CMSampleTimingInfo(duration: CMTime(value: 1, timescale: 8),
                                    presentationTimeStamp: playbackTime,
                                    decodeTimeStamp: .invalid)
    var sample: CMSampleBuffer?
    guard CMSampleBufferCreateReadyWithImageBuffer(allocator: kCFAllocatorDefault, imageBuffer: pixels,
              formatDescription: format, sampleTiming: &timing, sampleBufferOut: &sample) == noErr,
          let sample else { return false }
    // Real host-clock timestamps plus a running control timebase, not a static
    // poster or a looped movie. Backpressure drops frames rather than queuing.
    layer.enqueue(sample)
    return true
  }
}
