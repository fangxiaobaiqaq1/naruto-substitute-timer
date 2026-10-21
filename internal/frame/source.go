package frame

import (
	"context"
	"encoding/binary"
	"fmt"
	"hash/maphash"
	"image"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"narutotimer/internal/capture"
	"narutotimer/internal/config"
	"narutotimer/internal/detect"
	"narutotimer/internal/engine"
	"narutotimer/internal/safego"
	"narutotimer/internal/win"
)

// AnalyzeImage is the same pixel-to-observation path for live capture and replay.
// capturedAt is acquisition completion, NOT an asserted Android render time.
func AnalyzeImage(img *image.RGBA, eng engine.Engine, mode detect.ContentMode, started, capturedAt time.Time, source string) (f Frame) {
	f = Frame{Img: img, CaptureStarted: started, CapturedAt: capturedAt, CaptureMethod: source}
	if img == nil {
		f.CapturedAt = time.Time{}
		f.Hold = true
		f.Err = fmt.Errorf("empty captured image")
		return f
	}
	w, h := img.Bounds().Dx(), img.Bounds().Dy()
	if w < 400 || h < 250 {
		f.Hold = true
		f.Err = fmt.Errorf("capture is too small: %dx%d", w, h)
		return f
	}
	_, timedEngine := eng.(engine.TimedEngine)
	if !timedEngine && detect.ClassifyScreen(img, mode) == detect.ScreenBlank {
		f.Hold = true
		f.Status = "空帧，等待画面"
		return f
	}
	defer recoverAnalysis(&f)
	var res engine.Result
	f.AnalysisStarted = time.Now()
	if timed, ok := eng.(engine.TimedEngine); ok {
		res = timed.AnalyzeAt(img, capturedAt)
	} else {
		res = eng.Analyze(img)
	}
	f.AnalyzedAt = time.Now()
	fillFromEngine(&f, win.Window{Title: source}, img, res)
	f.Hold = res.Uncertain
	return f
}

// recoverAnalysis is deferred around engine analysis. A panic in the engine,
// including one re-raised from its helper goroutines (safego.Group), fails this
// frame only: it keeps the capture (image, times, method), drops any partial
// result, shows the error in the status line and logs the stack.
func recoverAnalysis(f *Frame) {
	p := safego.Recovered(recover())
	if p == nil {
		return
	}
	safego.Report("analysis", p)
	*f = Frame{Img: f.Img, CaptureStarted: f.CaptureStarted, CapturedAt: f.CapturedAt, CaptureMethod: f.CaptureMethod,
		AnalysisStarted: f.AnalysisStarted, AnalyzedAt: time.Now(), Hold: true,
		Err: fmt.Errorf("识别出错，已跳过本帧：%w", p)}
}

// captureGuarded runs one capture request on the provider's worker. A panic in
// the capture client or the frame plumbing fails this request only; the
// worker keeps serving later requests.
func captureGuarded(inner Provider) (f Frame) {
	defer func() {
		if p := safego.Recovered(recover()); p != nil {
			safego.Report("capture", p)
			f = Frame{Hold: true, Err: fmt.Errorf("采集出错，已跳过本帧：%w", p)}
		}
	}()
	return inner()
}

// NewConfiguredSnapshotter keeps one screenshot connection. A single worker is
// allowed to enter native capture: timeouts never spawn an unbounded call queue.
// Close signals shutdown and releases the SDK once any in-flight native call ends.
func NewConfiguredSnapshotter(eng engine.Engine, cfg config.Config) (Provider, func()) {
	p, close, _ := NewSelectableSnapshotter(eng, cfg)
	return p, close
}

// CaptureState distinguishes a saved selection from the target currently
// applied by the native capture worker. A new revision means "requested" until
// the worker has torn down its old SDK client and begins using the new target.
type CaptureState struct {
	RequestedRevision uint64 `json:"requested_revision"`
	AppliedRevision   uint64 `json:"applied_revision"`
	// Requested/Applied remain MuMu-shaped for compatibility with existing UI
	// and support bundles. The complete provider-neutral snapshots are included
	// for new callers.
	Requested        config.MuMuCaptureConfig `json:"requested"`
	Applied          config.MuMuCaptureConfig `json:"applied"`
	RequestedCapture config.CaptureConfig     `json:"requested_capture"`
	AppliedCapture   config.CaptureConfig     `json:"applied_capture"`
	Method           string                   `json:"method,omitempty"`
	Source           string                   `json:"source,omitempty"`
	LastError        string                   `json:"last_error,omitempty"`
	UpdatedAt        time.Time                `json:"updated_at"`
}

// NewSelectableSnapshotterStateful exposes the capture-worker state needed by
// settings and support diagnostics. The existing constructor remains available
// for callers that only need a selection callback.
func NewSelectableSnapshotterStateful(eng engine.Engine, cfg config.Config) (Provider, func(), func(config.MuMuCaptureConfig) uint64, func() CaptureState) {
	return newSelectableSnapshotterLegacy(eng, cfg)
}

// NewSelectableSnapshotterConfigStateful exposes provider-neutral target changes.
func NewSelectableSnapshotterConfigStateful(eng engine.Engine, cfg config.Config) (Provider, func(), func(config.CaptureConfig) uint64, func() CaptureState) {
	return newSelectableSnapshotter(eng, cfg)
}

func NewSelectableSnapshotter(eng engine.Engine, cfg config.Config) (Provider, func(), func(config.MuMuCaptureConfig) uint64) {
	provider, close, selectTarget, _ := NewSelectableSnapshotterStateful(eng, cfg)
	return provider, close, selectTarget
}

func newSelectableSnapshotterLegacy(eng engine.Engine, cfg config.Config) (Provider, func(), func(config.MuMuCaptureConfig) uint64, func() CaptureState) {
	provider, close, selectConfig, state := newSelectableSnapshotter(eng, cfg)
	selectTarget := func(next config.MuMuCaptureConfig) uint64 {
		current := state().RequestedCapture
		current.MuMu = next
		return selectConfig(current)
	}
	return provider, close, selectTarget, state
}

func newSelectableSnapshotter(eng engine.Engine, cfg config.Config) (Provider, func(), func(config.CaptureConfig) uint64, func() CaptureState) {
	var selectionMu sync.Mutex
	selected := cfg.Capture
	var revision uint64
	// activeRevision and appliedConfig describe one moment: the worker (their
	// only writer) changes both under stateMu and state() reads both under it.
	var activeRevision uint64
	var appliedConfig = cfg.Capture
	var stateMu sync.RWMutex
	var lastCaptureError string
	var stateSource string
	setSelection := func(next config.CaptureConfig) uint64 {
		selectionMu.Lock()
		selected = next
		revision++
		value := revision
		selectionMu.Unlock()
		stateMu.Lock()
		lastCaptureError = ""
		stateMu.Unlock()
		return value
	}

	mode, _ := detect.ParseMode(cfg.Layout.ContentMode)
	var analyzed dedupedAnalysis
	var rowHashes []uint64
	var printWindowFrames captureRing
	var client capture.Client
	var retryAfter time.Time
	var retryError error
	var attemptMu sync.Mutex
	var attempt captureAttempt
	setAttempt := func(started time.Time, method string) {
		attemptMu.Lock()
		attempt = captureAttempt{started: started, method: method}
		attemptMu.Unlock()
	}
	currentAttempt := func() captureAttempt {
		attemptMu.Lock()
		defer attemptMu.Unlock()
		return attempt
	}
	preferredMethods := func(c config.CaptureConfig) []string {
		if isLeidianMethod(c.Provider) {
			return []string{c.Provider}
		}
		if c.Provider == capture.MethodMuMuSDK {
			return []string{capture.MethodMuMuSDK}
		}
		if len(c.PreferredMethods) > 0 {
			return c.PreferredMethods
		}
		return []string{capture.MethodMuMuSDK}
	}
	inner := func() Frame {
		selectionMu.Lock()
		next, rev := selected, revision
		selectionMu.Unlock()
		if rev != activeRevision {
			if client != nil {
				client.Close()
				client = nil
			}
			cfg.Capture = next
			stateMu.Lock()
			appliedConfig = next
			activeRevision = rev
			stateSource = ""
			lastCaptureError = ""
			stateMu.Unlock()
			retryAfter = time.Time{}
		}
		var f Frame
		for _, method := range preferredMethods(cfg.Capture) {
			started := time.Now()
			setAttempt(started, method)
			if method == capture.MethodMuMuSDK || isLeidianMethod(method) {
				if client == nil {
					if time.Now().Before(retryAfter) {
						f = Frame{Hold: true, Err: fmt.Errorf("%s 重连等待中：%w", method, retryError), CaptureStarted: started, CaptureMethod: method}
						continue
					}
					var err error
					client, err = capture.OpenConfigured(context.Background(), method, cfg.Capture)
					if err != nil {
						retryError = err
						stateMu.Lock()
						lastCaptureError = err.Error()
						stateMu.Unlock()
						retryAfter = time.Now().Add(5 * time.Second)
						f = Frame{Hold: true, Err: err, CaptureStarted: started, CaptureMethod: method}
						continue
					}
					stateMu.Lock()
					stateSource = client.Source()
					lastCaptureError = ""
					stateMu.Unlock()
				}
				started = time.Now()
				setAttempt(started, method)
				// A row-hashing client fingerprints each row while it is in
				// cache; the fold equals frameFingerprint of the image.
				var img *image.RGBA
				var err error
				hashedRows := false
				if rows, ok := client.(capture.RowHashClient); ok {
					img, rowHashes, err = rows.CaptureRowHashes(fingerprintRow, rowHashes)
					hashedRows = err == nil && img != nil && len(rowHashes) == img.Rect.Dy()
				} else {
					img, err = client.Capture()
				}
				captured := time.Now()
				if err != nil {
					client.Close()
					client = nil
					retryError = err
					stateMu.Lock()
					stateSource = ""
					lastCaptureError = err.Error()
					stateMu.Unlock()
					retryAfter = time.Now().Add(time.Second)
					f = Frame{Hold: true, Err: err, CaptureStarted: started, CaptureMethod: method}
					continue
				}
				source := client.Source()
				if hashedRows {
					f = analyzed.analyzeFingerprinted(img, frameFingerprintRows(img.Rect, source, rowHashes), eng, mode, started, captured, source)
				} else {
					f = analyzed.analyze(img, eng, mode, started, captured, source)
				}
			} else if method == capture.MethodPrintWindow {
				f = snapshotFingerprinted(eng, mode, &printWindowFrames)
			} else {
				f = Frame{Hold: true, Err: fmt.Errorf("unsupported capture method %q", method), CaptureStarted: started, CaptureMethod: method}
			}
			if f.Err == nil && f.Img != nil {
				break
			}
		}
		return f
	}
	cleanup := func() {
		if client != nil {
			client.Close()
			client = nil
		}
	}
	timeout := time.Duration(cfg.Capture.TimeoutMS) * time.Millisecond
	if timeout <= 0 {
		timeout = 1200 * time.Millisecond
	}
	provider, close := boundedProvider(inner, cleanup, timeout, currentAttempt)
	guarded := func() Frame {
		selectionMu.Lock()
		before := revision
		selectionMu.Unlock()
		f := provider()
		f.SourceRevision = before
		selectionMu.Lock()
		changed := before != revision
		selectionMu.Unlock()
		if changed {
			return Frame{Hold: true, Err: fmt.Errorf("正在切换模拟器，请稍候")}
		}
		return f
	}
	state := func() CaptureState {
		selectionMu.Lock()
		requestedRevision := revision
		requested := selected
		selectionMu.Unlock()
		stateMu.RLock()
		appliedRevision := activeRevision
		applied := appliedConfig
		lastError := lastCaptureError
		source := stateSource
		stateMu.RUnlock()
		selectionMu.Lock()
		requestedForState := requested
		selectionMu.Unlock()
		methods := preferredMethods(requestedForState)
		method := requestedForState.Provider
		if method == "" && len(methods) > 0 {
			method = methods[0]
		}
		return CaptureState{
			RequestedRevision: requestedRevision,
			AppliedRevision:   appliedRevision,
			Requested:         requested.MuMu,
			Applied:           applied.MuMu,
			RequestedCapture:  requested,
			AppliedCapture:    applied,
			Method:            method,
			Source:            source,
			LastError:         lastError,
			UpdatedAt:         time.Now(),
		}
	}
	return guarded, close, setSelection, state
}

type captureAttempt struct {
	started time.Time
	method  string
}

func boundedProvider(inner Provider, cleanup func(), timeout time.Duration, attempts ...func() captureAttempt) (Provider, func()) {
	requests := make(chan chan Frame, 1)
	done := make(chan struct{})
	var once sync.Once
	var busy atomic.Bool
	// A caller owns delivery state until it returns, even if the native worker
	// has already completed. Rejected/late results never change this state.
	var delivering atomic.Bool
	var sequence uint64
	var previous uint64
	havePrevious := false
	go func() {
		defer func() {
			if cleanup != nil {
				cleanup()
			}
		}()
		for {
			// Give shutdown priority over requests left in the bounded queue.
			select {
			case <-done:
				return
			default:
			}
			select {
			case <-done:
				return
			case result := <-requests:
				select {
				case <-done:
					return
				default:
				}
				f := captureGuarded(inner)
				busy.Store(false)
				// Each request owns one buffer, so a timed-out caller cannot block
				// cleanup or cause its late frame to satisfy the next request.
				result <- f
			}
		}
	}()
	provider := func() Frame {
		requestedAt := time.Now()
		failed := func(message string) Frame {
			f := Frame{Hold: true, Err: fmt.Errorf("%s", message), CaptureStarted: requestedAt}
			if len(attempts) > 0 {
				attempt := attempts[0]()
				// Do not attribute a queued/rejected request to an older capture.
				if !attempt.started.Before(requestedAt) {
					f.CaptureStarted, f.CaptureMethod = attempt.started, attempt.method
				}
			}
			return f
		}
		select {
		case <-done:
			return failed("采集已关闭")
		default:
		}
		if !delivering.CompareAndSwap(false, true) {
			return failed("另一调用正在接收画面，暂停接收新观测")
		}
		defer delivering.Store(false)
		if !busy.CompareAndSwap(false, true) {
			return failed("上次采集仍在等待，暂停接收新观测")
		}
		deadline := time.Now().Add(timeout)
		result := make(chan Frame, 1)
		select {
		case <-done:
			busy.Store(false)
			return failed("采集已关闭")
		case requests <- result:
		}
		timer := time.NewTimer(time.Until(deadline))
		defer timer.Stop()
		select {
		case f := <-result:
			select {
			case <-done:
				return failed("采集已关闭")
			default:
			}
			if time.Now().After(deadline) {
				return failed("采集超时，已丢弃迟到画面")
			}
			if f.Img != nil && f.Err == nil {
				sequence++
				f.Sequence = sequence
				hash := f.fingerprint
				if !f.fingerprinted {
					hash = frameFingerprint(f.Img, f.CaptureMethod)
				}
				f.Duplicate = havePrevious && hash == previous
				previous, havePrevious = hash, true
			}
			return f
		case <-done:
			return failed("采集已关闭")
		case <-timer.C:
			return failed("采集超时，等待原调用结束")
		}
	}
	return provider, func() { once.Do(func() { close(done) }) }
}

// fingerprintSeed is process-local: fingerprints are only ever compared within
// one run, never persisted or sent anywhere.
var fingerprintSeed = maphash.MakeSeed()

// fingerprintHook is a test-only counter hook; nil in production.
var fingerprintHook func()

// frameFingerprint hashes visible pixels, geometry and capture source with the
// runtime's AES-based hash. Padding bytes are not observations; a different
// image geometry or capture coordinate system is a new observation. A 64-bit
// process-local hash is ample for equality and duplicate detection.
//
// Rows go through maphash.Bytes rather than a streaming maphash.Hash: Write
// re-chunks input into 128-byte blocks and measured ~10% slower at 1080p.
func frameFingerprint(img *image.RGBA, method string) uint64 {
	if img == nil {
		return 0
	}
	if fingerprintHook != nil {
		fingerprintHook()
	}
	r := img.Rect
	sum := fingerprintHeader(r, method)
	rowBytes := r.Dx() * 4
	if rowBytes <= 0 || r.Dy() <= 0 {
		return sum
	}
	// Hash row by row so a padded stride and a packed buffer with the same
	// visible pixels produce the same fingerprint.
	for y := 0; y < r.Dy(); y++ {
		start := y * img.Stride
		sum = mixHash(sum, fingerprintRow(img.Pix[start:start+rowBytes]))
	}
	return sum
}

func fingerprintHeader(r image.Rectangle, method string) uint64 {
	var header [32]byte
	for i, value := range []int{r.Min.X, r.Min.Y, r.Max.X, r.Max.Y} {
		binary.LittleEndian.PutUint64(header[i*8:], uint64(value))
	}
	sum := maphash.Bytes(fingerprintSeed, header[:])
	return mixHash(sum, maphash.String(fingerprintSeed, method))
}

// fingerprintRow hashes one row of visible pixels (capture.RowHashClient).
func fingerprintRow(row []byte) uint64 { return maphash.Bytes(fingerprintSeed, row) }

// frameFingerprintRows is frameFingerprint(img, method) for an image of
// bounds r whose visible rows hash (fingerprintRow) to rows, top to bottom.
func frameFingerprintRows(r image.Rectangle, method string, rows []uint64) uint64 {
	if fingerprintHook != nil {
		fingerprintHook()
	}
	sum := fingerprintHeader(r, method)
	if r.Dx() <= 0 || r.Dy() <= 0 {
		return sum
	}
	for _, row := range rows {
		sum = mixHash(sum, row)
	}
	return sum
}

// Fingerprint is the duplicate-frame fingerprint the live provider uses, for
// diagnostic tools that must count duplicates with identical semantics. It is
// process-local and only comparable within one run.
func Fingerprint(img *image.RGBA, method string) uint64 { return frameFingerprint(img, method) }

func mixHash(acc, value uint64) uint64 {
	acc ^= value + 0x9e3779b97f4a7c15 + (acc << 6) + (acc >> 2)
	return acc
}

// withFingerprint records the capture fingerprint once at the source so the
// delivery layer never hashes the same pixels again.
func withFingerprint(f Frame) Frame {
	if f.Img != nil && !f.fingerprinted {
		f.fingerprint, f.fingerprinted = frameFingerprint(f.Img, f.CaptureMethod), true
	}
	return f
}

// calibrationRevision is the engine's region-calibration revision (0 = never
// replaced). Fingerprints include it: after a live replacement identical
// pixels are a new observation, analysed again and not a duplicate.
func calibrationRevision(eng engine.Engine) uint64 {
	if c, ok := eng.(engine.Calibrator); ok {
		return c.CalibrationRevision()
	}
	return 0
}

func withRevision(sum, revision uint64) uint64 {
	if revision != 0 {
		sum = mixHash(sum, revision)
	}
	return sum
}

func engineFingerprint(img *image.RGBA, method string, eng engine.Engine) uint64 {
	return withRevision(frameFingerprint(img, method), calibrationRevision(eng))
}

// snapshotFingerprinted reads the revision before analysing, so a replacement
// during the analysis is never attributed to this frame.
func snapshotFingerprinted(eng engine.Engine, mode detect.ContentMode, ring *captureRing) Frame {
	revision := calibrationRevision(eng)
	f := snapshot(eng, mode, ring)
	if f.Img != nil && !f.fingerprinted {
		f.fingerprint, f.fingerprinted = withRevision(frameFingerprint(f.Img, f.CaptureMethod), revision), true
	}
	return f
}

// dedupedAnalysis skips the recognition chain for a capture whose pixels are
// identical to the previously analyzed capture. The emulator does not present a
// new frame for every poll; re-analyzing the same pixels would only burn CPU and
// could never produce different beads. The reused result keeps this capture's
// own image and acquisition times; the delivery layer still decides Duplicate
// against the frame the UI actually received last.
type dedupedAnalysis struct {
	valid       bool
	fingerprint uint64
	last        Frame
}

func (d *dedupedAnalysis) analyze(img *image.RGBA, eng engine.Engine, mode detect.ContentMode, started, capturedAt time.Time, source string) Frame {
	return d.analyzeWith(img, engineFingerprint(img, source, eng), eng, mode, started, capturedAt, source)
}

// analyzeFingerprinted is analyze for a capture whose frameFingerprint (base,
// without the calibration revision) the client already computed.
func (d *dedupedAnalysis) analyzeFingerprinted(img *image.RGBA, base uint64, eng engine.Engine, mode detect.ContentMode, started, capturedAt time.Time, source string) Frame {
	return d.analyzeWith(img, withRevision(base, calibrationRevision(eng)), eng, mode, started, capturedAt, source)
}

func (d *dedupedAnalysis) analyzeWith(img *image.RGBA, fingerprint uint64, eng engine.Engine, mode detect.ContentMode, started, capturedAt time.Time, source string) Frame {
	if d.valid && img != nil && fingerprint == d.fingerprint && d.last.Err == nil {
		f := d.last
		f.Img = img
		f.CaptureStarted, f.CapturedAt = started, capturedAt
		f.AnalysisStarted, f.AnalyzedAt = time.Time{}, time.Time{}
		f.Beads = append([]Bead(nil), d.last.Beads...)
		for i := range f.Geometry {
			f.Geometry[i].Beads = slices.Clone(f.Geometry[i].Beads)
		}
		f.fingerprint, f.fingerprinted = fingerprint, true
		return f
	}
	f := AnalyzeImage(img, eng, mode, started, capturedAt, source)
	f.fingerprint, f.fingerprinted = fingerprint, true
	if img != nil && f.Err == nil {
		d.valid, d.fingerprint, d.last = true, fingerprint, f
	} else {
		d.valid = false
	}
	return f
}

// isLeidianMethod reports whether method is served by the 雷电 client.
func isLeidianMethod(method string) bool {
	switch method {
	case capture.MethodLeidianADB, capture.MethodLeidianADBRaw, capture.MethodLeidianWindow:
		return true
	}
	return false
}
