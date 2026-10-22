#import "RecognitionBridge.h"
#import <CoreImage/CoreImage.h>
#import <ImageIO/ImageIO.h>
#import <CommonCrypto/CommonDigest.h>

// Objective-C defines Nil as a Class null pointer; the portable image API
// legitimately has Nil() methods. Keep that macro out of this C++ adapter.
#ifdef Nil
#undef Nil
#endif
#include "TimerSession.hpp"
#include "PixelConversion.hpp"
#include "nt/assets.h"
#include "nt/config.h"
#include "nt/ninja.h"
#include "nt/parallel.h"
#include "nt/utf8.h"
#include <algorithm>
#include <cmath>
#include <memory>
#include <mutex>
#include <stdexcept>
#include <time.h>

NSString *const NTRecognitionErrorDomain = @"NTRecognitionErrorDomain";

namespace {
// The shared avatar catalogue is immutable/process-global. Do not clear it or
// replace roots underneath live engines.
std::recursive_mutex& coreMutex() { static auto *m = new std::recursive_mutex; return *m; }
std::string& loadedRoot() { static auto *s = new std::string; return *s; }
bool engineAlive = false;
constexpr size_t MiB = 1024 * 1024;
NSString *ns(const std::string& s) { return [[NSString alloc] initWithBytes:s.data() length:s.size() encoding:NSUTF8StringEncoding] ?: @""; }
std::string str(NSString *s) { return s.UTF8String ?: ""; }
void fail(const std::string& s) { throw std::runtime_error(s); }
void errorOut(NSError **out, const std::string& message) {
    if (out) *out = [NSError errorWithDomain:NTRecognitionErrorDomain code:1 userInfo:@{NSLocalizedDescriptionKey:ns(message)}];
}
NSData *readAsset(NSString *root, NSString *key) {
    if ([key hasPrefix:@"/"] || [[key pathComponents] containsObject:@".."] || [key containsString:@"\\"])
        fail("invalid asset path");
    NSError *error = nil;
    NSData *data = [NSData dataWithContentsOfFile:[root stringByAppendingPathComponent:key]
                                        options:NSDataReadingMappedIfSafe error:&error];
    if (!data || data.length == 0 || data.length > 16 * MiB) fail("missing/oversized asset: " + str(key));
    return data;
}
id jsonAsset(NSString *root, NSString *key) {
    NSData *data = readAsset(root, key);
    NSError *error = nil;
    id value = [NSJSONSerialization JSONObjectWithData:data options:0 error:&error];
    if (!value) fail("invalid JSON: " + str(key));
    nt::AssetStore::Instance().AddText(str(key), std::string((const char *)data.bytes, data.length));
    return value;
}
void collectPNGs(id value, NSMutableSet<NSString *> *out) {
    if ([value isKindOfClass:NSString.class] && [[value pathExtension].lowercaseString isEqual:@"png"]) [out addObject:value];
    else if ([value isKindOfClass:NSDictionary.class]) for (id child in [value allValues]) collectPNGs(child, out);
    else if ([value isKindOfClass:NSArray.class]) for (id child in value) collectPNGs(child, out);
}
uint32_t be32(const uint8_t *p) { return (uint32_t(p[0]) << 24) | (uint32_t(p[1]) << 16) | (uint32_t(p[2]) << 8) | p[3]; }
// Same colour-chunk policy as Android AssetLoader: reference Go png decoder
// ignores ICC/gamma. SHA is computed BEFORE this lossless chunk removal.
NSData *stripColour(NSData *data) {
    const auto *p = static_cast<const uint8_t *>(data.bytes);
    static const uint8_t magic[] = {137,80,78,71,13,10,26,10};
    if (data.length < 8 || memcmp(p, magic, 8)) fail("asset is not PNG");
    NSMutableData *out = [NSMutableData dataWithBytes:p length:8];
    for (size_t at = 8; at < data.length;) {
        if (data.length - at < 12) fail("truncated PNG");
        const size_t n = be32(p + at);
        if (n > data.length - at - 12) fail("truncated PNG chunk");
        const std::string type((const char *)p + at + 4, 4);
        if (type != "iCCP" && type != "sRGB" && type != "gAMA" && type != "cHRM")
            [out appendBytes:p + at length:n + 12];
        at += n + 12;
    }
    return out;
}
// Bundled PNG decoding only; straight RGBA is required by AssetStore.
void decodePNGAsset(const std::string& path, NSData *png, int w, int h) {
    if (w < 1 || h < 1 || w > 2048 || h > 2048 || size_t(w) * h > 512 * 1024)
        fail("invalid PNG asset dimensions");
    NSDictionary *options = @{(__bridge NSString *)kCGImageSourceShouldCache:@NO};
    CGImageSourceRef source = CGImageSourceCreateWithData((__bridge CFDataRef)png, (__bridge CFDictionaryRef)options);
    if (!source) fail("invalid PNG asset");
    NSDictionary *properties = CFBridgingRelease(CGImageSourceCopyPropertiesAtIndex(source, 0, nullptr));
    if (CGImageSourceGetCount(source) != 1 || CGImageSourceGetStatus(source) != kCGImageStatusComplete ||
        [properties[(__bridge NSString *)kCGImagePropertyPixelWidth] integerValue] != w ||
        [properties[(__bridge NSString *)kCGImagePropertyPixelHeight] integerValue] != h) {
        CFRelease(source); fail("invalid PNG asset dimensions");
    }
    CGImageRef image = CGImageSourceCreateImageAtIndex(source, 0, (__bridge CFDictionaryRef)options);
    const bool complete = CGImageSourceGetStatusAtIndex(source, 0) == kCGImageStatusComplete;
    CFRelease(source);
    if (!image) fail("PNG decode failed");
    if (!complete || CGImageGetWidth(image) != size_t(w) || CGImageGetHeight(image) != size_t(h)) {
        CGImageRelease(image); fail("incomplete PNG asset");
    }
    std::vector<uint8_t> rgba(size_t(w) * h * 4);
    CGColorSpaceRef color = CGColorSpaceCreateDeviceRGB();
    CGContextRef ctx = CGBitmapContextCreate(rgba.data(), w, h, 8, size_t(w) * 4, color,
                                            kCGImageAlphaPremultipliedLast | kCGBitmapByteOrder32Big);
    CGColorSpaceRelease(color);
    if (!ctx) { CGImageRelease(image); fail("PNG bitmap allocation failed"); }
    CGContextSetBlendMode(ctx, kCGBlendModeCopy);
    CGContextDrawImage(ctx, CGRectMake(0, 0, w, h), image);
    CGContextRelease(ctx); CGImageRelease(image);
    for (size_t i = 0; i < rgba.size(); i += 4) {
        const unsigned a = rgba[i + 3];
        if (a && a < 255) for (int c = 0; c < 3; ++c)
            rgba[i + c] = uint8_t(std::min(255u, (unsigned(rgba[i + c]) * 255 + a / 2) / a));
    }
    nt::AssetStore::Instance().AddImage(path, w, h, rgba.data(), size_t(w) * 4);
}
void loadImage(NSString *root, NSString *key, size_t& resident) {
    @autoreleasepool {
        NSData *raw = readAsset(root, key);
        if ([key hasPrefix:@"avatars/"]) {
            unsigned char digest[CC_SHA256_DIGEST_LENGTH];
            CC_SHA256(raw.bytes, (CC_LONG)raw.length, digest);
            static const char hex[] = "0123456789abcdef";
            std::string sum;
            for (auto b : digest) { sum += hex[b >> 4]; sum += hex[b & 15]; }
            nt::AssetStore::Instance().AddText(str(key) + ".sha256", sum);
        }
        NSData *png = stripColour(raw);
        CGImageSourceRef source = CGImageSourceCreateWithData((__bridge CFDataRef)png, (__bridge CFDictionaryRef)@{(__bridge NSString *)kCGImageSourceShouldCache:@NO});
        if (!source) fail("ImageIO source failed: " + str(key));
        NSDictionary *properties = CFBridgingRelease(CGImageSourceCopyPropertiesAtIndex(source, 0, nullptr));
        const size_t w = [properties[(__bridge NSString *)kCGImagePropertyPixelWidth] unsignedIntegerValue];
        const size_t h = [properties[(__bridge NSString *)kCGImagePropertyPixelHeight] unsignedIntegerValue];
        if (!w || !h || w > 2048 || h > 2048 || w * h > 512 * 1024 || resident + w * h * 2 > 8 * MiB) {
            CFRelease(source); fail("recognition asset exceeds memory budget: " + str(key));
        }
        CFRelease(source);
        decodePNGAsset(str(key), png, int(w), int(h));
        resident += nt::AssetStore::Instance().Image(str(key))->Bytes();
    }
}
void loadAssets(NSString *root) {
    if (!loadedRoot().empty()) {
        if (loadedRoot() != str(root)) fail("recognition assets cannot change within one process");
        return;
    }
    nt::SetParallelEnabled(false); // No thread-pool scratch multiplication in the extension.
    auto& store = nt::AssetStore::Instance();
    store.Clear();
    size_t resident = 0;
    @autoreleasepool {
        id manifest = jsonAsset(root, @"templates/manifest.json");
        NSMutableSet<NSString *> *files = [NSMutableSet set];
        collectPNGs(manifest, files);
        if (files.count == 0) fail("empty scene template manifest");
        for (NSString *file in [[files allObjects] sortedArrayUsingSelector:@selector(compare:)])
            loadImage(root, [@"templates/" stringByAppendingString:file], resident);
        id index = jsonAsset(root, @"avatars/index.json");
        if (![index isKindOfClass:NSDictionary.class] || ![index[@"entries"] isKindOfClass:NSArray.class]) fail("invalid avatar index");
        for (id entry in index[@"entries"]) {
            NSString *identifier = [entry isKindOfClass:NSDictionary.class] ? entry[@"id"] : nil;
            if (![identifier isKindOfClass:NSString.class] || identifier.length < 5 || identifier.length > 6 ||
                [identifier rangeOfCharacterFromSet:NSCharacterSet.decimalDigitCharacterSet.invertedSet].location != NSNotFound)
                fail("invalid avatar ID");
            loadImage(root, [NSString stringWithFormat:@"avatars/%@.png", identifier], resident);
        }
        NSString *ninja = [root stringByAppendingPathComponent:@"ninja_templates"];
        NSArray *names = [[NSFileManager defaultManager] contentsOfDirectoryAtPath:ninja error:nil];
        if (!names.count) fail("missing ninja_templates directory");
        for (NSString *name in [names sortedArrayUsingSelector:@selector(compare:)]) {
            if ([name.pathExtension.lowercaseString isEqual:@"png"] && ![name hasSuffix:@".source.png"])
                loadImage(root, [@"ninja_templates/" stringByAppendingString:name], resident);
        }
        // Validate before the catalogue's process-global success cache is built.
        nt::ninja::AvatarCatalogStats stats;
        std::string error;
        if (!nt::ninja::AvatarStats(stats, &error)) fail(error);
    }
    loadedRoot() = str(root);
}

double number(id value, double fallback, double lo, double hi) {
    if (![value isKindOfClass:NSNumber.class]) return fallback;
    const double n = [value doubleValue];
    return std::isfinite(n) ? std::clamp(n, lo, hi) : fallback;
}
std::string stringSetting(id value, const std::string& fallback, size_t maxRunes = 40) {
    if (![value isKindOfClass:NSString.class]) return fallback;
    auto runes = nt::utf8::Runes(nt::utf8::TrimSpace(str(value)));
    return nt::utf8::FromRunes(runes, 0, std::min(maxRunes, runes.size()));
}
std::optional<nt::config::NormalizedRect> box(id value, bool avatar) {
    if (!value || value == NSNull.null) return std::nullopt;
    if (![value isKindOfClass:NSDictionary.class]) fail("calibration box must be an object");
    auto component = [&](NSString *key) {
        id v = value[key];
        if (![v isKindOfClass:NSNumber.class] || !std::isfinite([v doubleValue])) fail("invalid calibration coordinate");
        return [v doubleValue];
    };
    nt::config::NormalizedRect r{component(@"x"), component(@"y"), component(@"width"), component(@"height")};
    if (!(r.X >= 0 && r.Y >= 0 && r.Width > 0 && r.Height > 0 && r.X + r.Width <= 1 && r.Y + r.Height <= 1) ||
        nt::config::CalibrationRectIssue(avatar, r) != nt::config::CalibrationIssue::None)
        fail("calibration box outside shared core size/shape limits");
    return r;
}
bool sameCalibration(const std::optional<nt::config::RegionCalibration>& a,
                     const std::optional<nt::config::RegionCalibration>& b) {
    if (!a || !b) return bool(a) == bool(b);
    auto sameBox = [](const auto& x, const auto& y) {
        if (!x || !y) return bool(x) == bool(y);
        return x->X == y->X && x->Y == y->Y && x->Width == y->Width && x->Height == y->Height;
    };
    return a->Profile == b->Profile && sameBox(a->LeftBeads, b->LeftBeads) && sameBox(a->RightBeads, b->RightBeads) &&
           sameBox(a->LeftAvatar, b->LeftAvatar) && sameBox(a->RightAvatar, b->RightAvatar);
}
NSDictionary *rect(const nt::config::NormalizedRect& r) {
    return @{@"x":@(r.X), @"y":@(r.Y), @"width":@(r.Width), @"height":@(r.Height)};
}
NSDictionary *geometry(const nt::engine::Result& r) {
    NSMutableDictionary *out = [NSMutableDictionary dictionary];
    for (int i = 0; i < 2; ++i) {
        const auto& s = r.Geometry[i];
        NSMutableArray *centres = [NSMutableArray array];
        for (auto p : s.Beads) [centres addObject:@{@"x":@(p.X), @"y":@(p.Y)}];
        out[i == 0 ? @"left" : @"right"] = @{@"beads":centres, @"beadBox":rect(s.BeadBox), @"avatarBox":rect(s.AvatarBox)};
    }
    return out;
}
// Legacy sliders use the SAME shared calibration transform, not image crops.
// Explicit per-region boxes take precedence. Auto geometry stays untouched at
// identity slider values, retaining the core's HP-bar anchor detection.
void applySliders(nt::config::Config& cfg, double scale, double dx, double dy) {
    if (scale == 1 && dx == 0 && dy == 0) return;
    const std::string profile = cfg.Layout.Calibration ? cfg.Layout.Calibration->Profile :
        (cfg.Layout.PreferredProfile == "camp" ? "camp" : "duel");
    nt::engine::ConfiguredLayout layout(nt::detect::ModeStretch, cfg.Layout);
    const auto ca = nt::detect::ComputeContentArea(1920, 1080, nt::detect::ModeStretch);
    const auto positions = layout.PositionsIn(profile, ca);
    auto c = cfg.Layout.Calibration.value_or(nt::config::RegionCalibration{});
    c.Profile = profile;
    for (int i = 0; i < 2; ++i) {
        auto& target = i == 0 ? c.LeftBeads : c.RightBeads;
        if (target) continue;
        std::vector<nt::detect::BeadPosition> row;
        for (const auto& p : positions) if (p.Side == (i == 0 ? "left" : "right")) row.push_back(p);
        nt::config::NormalizedRect r;
        if (!layout.DefaultBeadBox(profile, row, nt::MakeRect(0, 0, 1920, 1080), ca, r)) fail("cannot construct calibrated row");
        r.X += dx + r.Width * (1 - scale) / 2;
        r.Y += dy + r.Height * (1 - scale) / 2;
        r.Width *= scale; r.Height *= scale;
        target = box(rect(r), false);
    }
    cfg.Layout.Calibration = c;
}
}

@implementation NTRecognitionEngine {
    std::unique_ptr<nt::frame::Pipeline> _pipeline;
    nt::ios::TimerSession _session;
    nt::ios::TimerSettings _settings;
    nt::config::Config _config;
    CIContext *_context;
    CGColorSpaceRef _color;
    std::vector<uint8_t> _pixels;
    NSDictionary *_lastGeometry;
    std::string _error, _configurationError;
    double _lastWall, _scale, _dx, _dy, _latencyMs;
    uint64_t _frames;
    BOOL _ownsEngine;
}

- (nullable instancetype)initWithAssetsPath:(NSString *)path error:(NSError **)error {
    self = [super init];
    if (!self) return nil;
    std::lock_guard<std::recursive_mutex> lock(coreMutex());
    try {
        if (engineAlive) fail("only one recognition engine is supported per extension process");
        NSString *root = path.stringByStandardizingPath;
        loadAssets(root);
        _config = nt::config::Default();
        _config.UI.PlayerSide = "left";
        // No account names/templates are loaded; the OCR wrapper stays disabled.
        _pipeline = std::make_unique<nt::frame::Pipeline>(_config, false);
        if (!_pipeline->Ok()) fail(_pipeline->Error());
        _color = CGColorSpaceCreateDeviceRGB();
        _context = [CIContext contextWithOptions:@{kCIContextCacheIntermediates:@NO,
            kCIContextWorkingColorSpace:NSNull.null, kCIContextOutputColorSpace:NSNull.null}];
        _scale = 1;
        _lastGeometry = @{};
        engineAlive = true;
        _ownsEngine = YES;
        return self;
    } catch (const std::exception& e) { errorOut(error, e.what()); }
    catch (...) { errorOut(error, "native recognition initialization failed"); }
    return nil;
}

- (void)dealloc {
    std::lock_guard<std::recursive_mutex> lock(coreMutex());
    _pipeline.reset();
    if (_ownsEngine) engineAlive = false;
    if (_color) CGColorSpaceRelease(_color);
}

- (void)configure:(NSDictionary<NSString *,id> *)values {
    std::lock_guard<std::recursive_mutex> lock(coreMutex());
    try {
        if (![values isKindOfClass:NSDictionary.class]) fail("settings must be a dictionary");
        auto s = _settings;
        auto cfg = _config;
        s.playerSide = stringSetting(values[@"playerSide"], s.playerSide);
        if (s.playerSide != "left" && s.playerSide != "right") fail("playerSide must be left or right");
        cfg.UI.PlayerSide = s.playerSide;
        if (values[@"showBothSides"]) s.showBothSides = [values[@"showBothSides"] isEqual:@YES];
        if (values[@"dual"]) s.dual = [values[@"dual"] isEqual:@YES];
        if (values[@"rememberSide"]) s.rememberSide = [values[@"rememberSide"] isEqual:@YES];
        s.ninjaQuery = stringSetting(values[@"ninjaQuery"], s.ninjaQuery, 80);
        const double fps = number(values[@"backgroundFPS"], 1000.0 / s.pollIntervalMs, 1, 10);
        s.pollIntervalMs = int(std::lround(1000 / fps));
        auto preset = stringSetting(values[@"layoutPreset"], cfg.Layout.PreferredProfile);
        if (preset != "auto" && preset != "camp" && preset != "duel") fail("invalid layoutPreset");
        cfg.Layout.PreferredProfile = preset;
        id cal = values[@"calibration"];
        if (cal == NSNull.null) cfg.Layout.Calibration.reset();
        else if (cal) {
            if (![cal isKindOfClass:NSDictionary.class]) fail("calibration must be an object or null");
            nt::config::RegionCalibration c;
            c.Profile = stringSetting(cal[@"profile"], "");
            if (c.Profile != "camp" && c.Profile != "duel") fail("calibration profile must be camp or duel");
            c.LeftBeads = box(cal[@"leftBeads"], false); c.RightBeads = box(cal[@"rightBeads"], false);
            c.LeftAvatar = box(cal[@"leftAvatar"], true); c.RightAvatar = box(cal[@"rightAvatar"], true);
            cfg.Layout.Calibration = c;
        }
        double scale = number(values[@"roiScale"], _scale, .7, 1.4);
        double dx = number(values[@"roiOffsetX"], _dx, -.15, .15);
        double dy = number(values[@"roiOffsetY"], _dy, -.15, .15);
        const bool calibrationChanged = !sameCalibration(cfg.Layout.Calibration, _config.Layout.Calibration);
        bool rebuild = calibrationChanged || preset != _config.Layout.PreferredProfile || scale != _scale || dx != _dx || dy != _dy;
        if (rebuild) {
            auto effective = cfg;
            applySliders(effective, scale, dx, dy);
            std::string error;
            if (!_pipeline->Rebuild(effective, &error)) fail(error);
            _session.pauseObservations();
            _lastGeometry = @{};
        }
        _config = cfg; _settings = s; _scale = scale; _dx = dx; _dy = dy;
        _session.configure(s);
        _configurationError.clear();
    } catch (const std::exception& e) { _configurationError = e.what(); }
    catch (...) { _configurationError = "invalid recognition settings"; }
}

- (NSDictionary<NSString *,id> *)state {
    const auto s = _session.snapshot();
    const auto& error = _configurationError.empty() ? _error : _configurationError;
    return @{@"match":@(s.match), @"leftCount":@(s.leftCount), @"rightCount":@(s.rightCount),
        @"leftDeadlineMs":@(s.left.deadlineMs), @"rightDeadlineMs":@(s.right.deadlineMs),
        @"leftAlternateDeadlineMs":@(s.left.alternateDeadlineMs), @"rightAlternateDeadlineMs":@(s.right.alternateDeadlineMs),
        @"leftNinja":ns(s.leftNinja), @"rightNinja":ns(s.rightNinja), @"scene":ns(s.scene),
        @"message":ns(error.empty() ? s.message : error), @"recognitionError":ns(error),
        @"width":@(s.width), @"height":@(s.height), @"frameWidth":@(s.width), @"frameHeight":@(s.height),
        @"frames":@(_frames), @"lastFrameAtMs":@(_lastWall), @"latencyMs":@(_latencyMs),
        @"leftSubstitutions":@(s.left.eventCount), @"rightSubstitutions":@(s.right.eventCount),
        @"geometry":_lastGeometry ?: @{}};
}

- (NSDictionary<NSString *,id> *)analyzePixelBuffer:(CVPixelBufferRef)pixelBuffer orientation:(NSInteger)orientation capturedAtMs:(double)capturedAtMs capturedUptime:(double)capturedUptime {
    const nt::TimeNs started = nt::NowNs();
    std::lock_guard<std::recursive_mutex> lock(coreMutex());
    @autoreleasepool {
        try {
            // CaptureProtocol.now() uses CLOCK_MONOTONIC, not systemUptime.
            // Mixing those clocks can reject every frame after device sleep.
            struct timespec acquiredClock{};
            if (clock_gettime(CLOCK_MONOTONIC, &acquiredClock) != 0) fail("capture clock unavailable");
            const double nowUptime = double(acquiredClock.tv_sec) + double(acquiredClock.tv_nsec) / 1e9;
            const double age = nowUptime - capturedUptime;
            if (!pixelBuffer || !std::isfinite(capturedAtMs) || capturedAtMs <= 0 || capturedAtMs <= _lastWall ||
                !std::isfinite(capturedUptime) || capturedUptime <= 0 || age < 0 || age > 2) {
                _session.pauseObservations();
                _pipeline->ResetDelivery();
                _error = "采集时间无效或重复，等待新画面";
                return [self state];
            }
            // Map acquisition uptime into the core's steady clock epoch. A
            // worker scheduling delay must not move the first candidate event.
            const nt::TimeNs at = nt::NowNs() - static_cast<nt::TimeNs>(age * nt::Second);
            _lastWall = capturedAtMs;
            ++_frames;
            const OSType format = CVPixelBufferGetPixelFormatType(pixelBuffer);
            if (format != kCVPixelFormatType_32BGRA && format != kCVPixelFormatType_420YpCbCr8BiPlanarFullRange &&
                format != kCVPixelFormatType_420YpCbCr8BiPlanarVideoRange) fail("unsupported ReplayKit pixel format");
            if (orientation < 1 || orientation > 8) fail("invalid image orientation");
            CIImage *image = [[CIImage imageWithCVPixelBuffer:pixelBuffer] imageByApplyingOrientation:(int)orientation];
            CGRect bounds = image.extent;
            if (CGRectIsInfinite(bounds) || CGRectIsEmpty(bounds) || bounds.size.width <= bounds.size.height) {
                _session.pauseObservations(); _pipeline->ResetDelivery();
                _error = "请使用横屏游戏画面；已确认计时继续";
                return [self state];
            }
            const double scale = std::min(1.0, 1280.0 / bounds.size.width);
            image = [image imageByApplyingTransform:CGAffineTransformMakeTranslation(-bounds.origin.x, -bounds.origin.y)];
            image = [image imageByApplyingTransform:CGAffineTransformMakeScale(scale, scale)];
            const int w = int(std::floor(bounds.size.width * scale)), h = int(std::floor(bounds.size.height * scale));
            if (w < 400 || h < 250) fail("capture too small for calibrated recognition");
            _pixels.resize(size_t(w) * h * 4);
            nt::ios::RenderTopDown(_context, image, _pixels.data(), w, h, _color);
            nt::RGBA view(_pixels.data(), w * 4, nt::MakeRect(0, 0, w, h));
            auto f = _pipeline->Analyze(&view, at, at, "ios-replaykit-" + std::to_string(orientation));
            _session.onFrame(f, capturedAtMs);
            _lastGeometry = @{@"profile":ns(f.Res.LayoutProfile), @"width":@(w), @"height":@(h), @"sides":geometry(f.Res)};
            _error = f.HasErr ? f.Err : "";
            _latencyMs = double(nt::NowNs() - started) / nt::Millisecond;
        } catch (const std::exception& e) {
            _session.pauseObservations(); _pipeline->ResetDelivery(); _error = e.what();
        } catch (...) {
            _session.pauseObservations(); _pipeline->ResetDelivery(); _error = "native recognition failed";
        }
        return [self state];
    }
}

- (void)pauseObservations {
    std::lock_guard<std::recursive_mutex> lock(coreMutex());
    _session.pauseObservations();
    _pipeline->ResetDelivery();
    [_context clearCaches];
    std::vector<uint8_t>().swap(_pixels);
}

- (void)reset {
    std::lock_guard<std::recursive_mutex> lock(coreMutex());
    _session.reset();
    // Rebuilding also invalidates ninja/scene history; resetting
    // just delivery would retain the previous match's gated-engine state.
    auto effective = _config;
    try {
        applySliders(effective, _scale, _dx, _dy);
        if (!_pipeline->Rebuild(effective, &_error)) _session.pauseObservations();
    } catch (const std::exception& e) { _error = e.what(); }
    _lastWall = 0; _frames = 0; _latencyMs = 0; _lastGeometry = @{};
}
@end
