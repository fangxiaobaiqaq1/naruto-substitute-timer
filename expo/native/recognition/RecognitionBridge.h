#import <Foundation/Foundation.h>
#import <CoreVideo/CoreVideo.h>

NS_ASSUME_NONNULL_BEGIN

FOUNDATION_EXPORT NSString *const NTRecognitionErrorDomain;

// Compile this header into the extension's Swift bridging header. No UIKit,
// ReplayKit, React or Expo dependency. Call synchronously on ONE serial worker;
// never on ReplayKit's delivery queue (frame recognition may take time).
// The implementation also serializes instances because core assets are global.
@interface NTRecognitionEngine : NSObject

- (instancetype)init NS_UNAVAILABLE;
+ (instancetype)new NS_UNAVAILABLE;

// Root contains templates/, avatars/, ninja_templates/. PNGs must be the
// original files: avatar SHA-256 checks their encoded bytes, not decoded pixels.
// Assets are immutable and shared for the process; a second different root fails.
- (nullable instancetype)initWithAssetsPath:(NSString *)path
                                    error:(NSError * _Nullable * _Nullable)error
    NS_SWIFT_NAME(init(assetsPath:));

// Partial settings update. Numeric settings are clamped to supported ranges.
// backgroundFPS defaults to 8 (1...10); caller schedules/throttles acquisitions.
// roiScale .7...1.4, roiOffsetX/Y -.15... .15 modify calibrated coordinates.
// playerSide left/right only (default left); showBothSides; dual (opponent only).
// The user chooses their own side; there is no account/automatic side recognition.
// ninjaQuery is the full opponent variant override.
// rememberSide is host persistence policy: a live
// manual selection always works; the host must not restore it when disabled.
// calibration: null (automatic), or {profile: camp|duel, leftBeads?,rightBeads?,
// leftAvatar?,rightAvatar?}; boxes are {x,y,width,height} fractions of the full
// oriented frame and must satisfy shared calibration size/shape limits.
// layoutPreset: auto|camp|duel. Explicit boxes win over the legacy ROI sliders.
// Invalid calibration is rejected atomically; recognitionError reports why.
- (void)configure:(NSDictionary<NSString *, id> *)settings
    NS_SWIFT_NAME(configure(_:));

// orientation is the CGImagePropertyOrientation raw value (1...8), not a UIKit
// orientation. NV12 full/video range and BGRA are supported. Rotation happens
// BEFORE the landscape check, then the whole frame is uniformly downsampled to
// <=1280 pixels wide. frameWidth/frameHeight (also width/height) describe that
// processed image. The app selects screenshot calibration independently; no
// captured frame pixels are sent through this dictionary/API.
//
// Keep only one in-flight sample on a serial worker, dropping arrivals while busy.
// capturedUptime is CLOCK_MONOTONIC seconds at acquisition, NOT worker start
// (CaptureProtocol.now()); do not substitute ProcessInfo.systemUptime.
// capturedAtMs is its paired epoch-ms value, used to pin the JS deadline and
// reject replayed/out-of-order deliveries. Both must be finite and increasing.
// Queued samples older than 2 seconds are discarded instead of becoming votes.
// Do not repeatedly analyze the same sample; pixel duplicates add no votes.
//
// All four deadline values are doubles: 0 means no confirmed event. A confirmed
// absolute deadline stays fixed until another event/reset; it may be in the past.
// Error/portrait/pause evidence never stops an already-confirmed clock.
// Results also include leftNinja/rightNinja, leftSubstitutions/rightSubstitutions,
// scene, latencyMs, frameWidth/frameHeight, and geometry.
// geometry.sides.{left,right}.{beadBox,avatarBox} are normalized; beads are pixels.
- (NSDictionary<NSString *, id> *)analyzePixelBuffer:(CVPixelBufferRef)pixelBuffer
                                       orientation:(NSInteger)orientation
                                      capturedAtMs:(double)capturedAtMs
                                    capturedUptime:(double)capturedUptime
    NS_SWIFT_NAME(analyze(_:orientation:capturedAtMs:capturedUptime:));

// Clears visual continuity, NOT confirmed clocks. Call for ReplayKit pause,
// capture failure/dropout, or source changes not visible in image dimensions.
- (void)pauseObservations NS_SWIFT_NAME(pauseObservations());

// Clears match, clocks and visual history; keeps settings and decoded assets.
- (void)reset NS_SWIFT_NAME(reset());

@end

NS_ASSUME_NONNULL_END
