package frame

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"image"
	"sync"
	"sync/atomic"
	"time"

	"narutotimer/internal/capture/mumu"
	"narutotimer/internal/config"
	"narutotimer/internal/detect"
	"narutotimer/internal/engine"
	"narutotimer/internal/win"
)

// AnalyzeImage is the same pixel-to-observation path for live capture and replay.
// capturedAt is acquisition completion, NOT an asserted Android render time.
func AnalyzeImage(img *image.RGBA, eng engine.Engine, mode detect.ContentMode, started, capturedAt time.Time, source string) Frame {
	f := Frame{Img: img, CaptureStarted: started, CapturedAt: capturedAt, CaptureMethod: source}
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
	RequestedRevision uint64                   `json:"requested_revision"`
	AppliedRevision   uint64                   `json:"applied_revision"`
	Requested         config.MuMuCaptureConfig `json:"requested"`
	Applied           config.MuMuCaptureConfig `json:"applied"`
	Source            string                   `json:"source,omitempty"`
	LastError         string                   `json:"last_error,omitempty"`
	UpdatedAt         time.Time                `json:"updated_at"`
}

// NewSelectableSnapshotterStateful exposes the capture-worker state needed by
// settings and support diagnostics. The existing constructor remains available
// for callers that only need a selection callback.
func NewSelectableSnapshotterStateful(eng engine.Engine, cfg config.Config) (Provider, func(), func(config.MuMuCaptureConfig) uint64, func() CaptureState) {
	return newSelectableSnapshotter(eng, cfg, true)
}

func NewSelectableSnapshotter(eng engine.Engine, cfg config.Config) (Provider, func(), func(config.MuMuCaptureConfig) uint64) {
	provider, close, selectTarget, _ := newSelectableSnapshotter(eng, cfg, false)
	return provider, close, selectTarget
}

func newSelectableSnapshotter(eng engine.Engine, cfg config.Config, _ bool) (Provider, func(), func(config.MuMuCaptureConfig) uint64, func() CaptureState) {
	var selectionMu sync.Mutex
	selected := cfg.Capture.MuMu
	var revision, activeRevision uint64
	var appliedConfig = cfg.Capture.MuMu
	var stateMu sync.RWMutex
	var lastCaptureError string
	var stateSource string
	setSelection := func(next config.MuMuCaptureConfig) uint64 {
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
	var client *mumu.Client
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
	methods := cfg.Capture.PreferredMethods
	if len(methods) == 0 {
		methods = []string{"mumu-sdk"}
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
			cfg.Capture.MuMu = next
			stateMu.Lock()
			appliedConfig = next
			stateSource = ""
			lastCaptureError = ""
			stateMu.Unlock()
			retryAfter = time.Time{}
			activeRevision = rev
		}
		var f Frame
		for _, method := range methods {
			started := time.Now()
			setAttempt(started, method)
			if method == "mumu-sdk" {
				if client == nil {
					if time.Now().Before(retryAfter) {
						f = Frame{Hold: true, Err: fmt.Errorf("MuMu截图接口重连等待中：%w", retryError), CaptureStarted: started, CaptureMethod: method}
						continue
					}
					o := cfg.Capture.MuMu
					var err error
					options := mumu.Options{InstallDir: o.InstallDir, DLLPath: o.DLLPath, Instance: o.Instance, DisplayID: o.DisplayID, Package: o.Package}
					// An explicit installation directory is itself a manual target,
					// including legacy configs that predate the Selection field.
					// Otherwise a saved root with instance 0 could be silently
					// replaced by auto-discovery. Only an explicit "auto" selection
					// is allowed to take the automatic path.
					manual := o.Selection == "manual" || (o.Selection != "auto" &&
						(o.InstallDir != "" || o.Instance != 0 || o.DLLPath != ""))
					if manual {
						client, err = mumu.Open(options)
					} else {
						client, err = mumu.OpenAuto(options)
					}
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
				img, err := client.Capture()
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
				f = AnalyzeImage(img, eng, mode, started, captured, client.Source())
			} else if method == "printwindow-fullcontent" {
				f = snapshot(eng, mode)
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
		appliedRevision := activeRevision
		requested := selected
		selectionMu.Unlock()
		stateMu.RLock()
		applied := appliedConfig
		lastError := lastCaptureError
		source := stateSource
		stateMu.RUnlock()
		return CaptureState{
			RequestedRevision: requestedRevision,
			AppliedRevision:   appliedRevision,
			Requested:         requested,
			Applied:           applied,
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
	var previous [32]byte
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
				f := inner()
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
				hash := imageFingerprint(f)
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

// Hash visible pixels, geometry and source. Padding bytes are not observations;
// a different image geometry or capture coordinate system is a new observation.
func imageFingerprint(f Frame) [32]byte {
	h := sha256.New()
	var bounds [32]byte
	r := f.Img.Rect
	for i, value := range []int{r.Min.X, r.Min.Y, r.Max.X, r.Max.Y} {
		binary.LittleEndian.PutUint64(bounds[i*8:], uint64(value))
	}
	h.Write(bounds[:])
	h.Write([]byte(f.CaptureMethod))
	rowBytes := r.Dx() * 4
	if f.Img.Stride == rowBytes {
		h.Write(f.Img.Pix[:rowBytes*r.Dy()])
	} else {
		for y := 0; y < r.Dy(); y++ {
			start := y * f.Img.Stride
			h.Write(f.Img.Pix[start : start+rowBytes])
		}
	}
	var sum [32]byte
	copy(sum[:], h.Sum(nil))
	return sum
}
