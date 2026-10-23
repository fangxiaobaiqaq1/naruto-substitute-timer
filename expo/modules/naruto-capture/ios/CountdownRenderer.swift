import AVFoundation
import UIKit

final class CountdownRenderer {
  let layer = AVSampleBufferDisplayLayer()
  private var pool: CVPixelBufferPool?
  private var format: CMVideoFormatDescription?
  private let width = 640
  private let height = 320

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
  func render(lines: [String], status: String, controls: String, scale: Double, stale: Bool, warning: Bool = false) -> Bool {
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
    context.setFillColor(UIColor(red: 0.04, green: 0.06, blue: 0.10, alpha: 1).cgColor)
    context.fill(CGRect(x: 0, y: 0, width: CGFloat(width), height: CGFloat(height)))
    context.translateBy(x: 0, y: CGFloat(height))
    context.scaleBy(x: 1, y: -1)
    UIGraphicsPushContext(context)
    let paragraph = NSMutableParagraphStyle()
    paragraph.alignment = .center
    paragraph.lineBreakMode = .byTruncatingTail
    let fontSize = min(76, max(35, 70 * scale))
    for (index, line) in lines.prefix(2).enumerated() {
      let y = lines.count == 1 ? 62 : 22 + index * 79
      (line as NSString).draw(in: CGRect(x: 12, y: CGFloat(y), width: CGFloat(width - 24), height: 88), withAttributes: [
        .font: UIFont.monospacedDigitSystemFont(ofSize: CGFloat(fontSize), weight: .bold),
        .foregroundColor: stale ? UIColor.systemOrange : (warning ? UIColor.systemRed : UIColor.white),
        .paragraphStyle: paragraph
      ])
    }
    (status as NSString).draw(in: CGRect(x: 16, y: 197, width: CGFloat(width - 32), height: 35), withAttributes: [
      .font: UIFont.systemFont(ofSize: 22, weight: .medium),
      .foregroundColor: stale ? UIColor.systemOrange : (warning ? UIColor.systemRed : UIColor.systemTeal),
      .paragraphStyle: paragraph
    ])
    // Always visible, including stale/no-match frames. This is the local
    // applied control state, not recognition evidence from an older packet.
    (controls as NSString).draw(in: CGRect(x: 12, y: 239, width: CGFloat(width - 24), height: 34), withAttributes: [
      .font: UIFont.systemFont(ofSize: 24, weight: .semibold),
      .foregroundColor: UIColor.systemGreen,
      .paragraphStyle: paragraph
    ])
    ("左键=我方左 | 中键=单/双 | 右键=我方右" as NSString).draw(
      in: CGRect(x: 8, y: 282, width: CGFloat(width - 16), height: 30), withAttributes: [
        .font: UIFont.systemFont(ofSize: 22, weight: .medium),
        .foregroundColor: UIColor.white,
        .paragraphStyle: paragraph
      ])
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
