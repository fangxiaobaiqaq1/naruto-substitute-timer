package frame

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"image"
	"sync"
	"sync/atomic"
	"time"

	"narutotimer/internal/capture"
	"narutotimer/internal/capture/helper"
	"narutotimer/internal/capture/mumu"
	"narutotimer/internal/config"
	"narutotimer/internal/detect"
	"narutotimer/internal/engine"
	"narutotimer/internal/win"
)

// captureMethodForConfiguredClient separates the configured capture-method
// identity from the client-provided source label. SDK/ADB client labels contain
// target details and must not select UI observation timing policy.
func captureMethodForConfiguredClient(method, source string) string {
	switch method {
	case capture.MethodMuMuSDK, capture.MethodLeidianADB:
		return method
	default:
		return source
	}
}

// captureTimeout is the caller's bounded observation deadline. It applies to
// each attempted provider and deliberately remains independent from the longer
// MuMu helper transaction deadline.
func captureTimeout(timeoutMS int) time.Duration {
	timeout := time.Duration(timeoutMS) * time.Millisecond
	if timeout <= 0 {
		return 1200 * time.Millisecond
	}
	return timeout
}

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
	return newSelectableSnapshotter(eng, cfg, "")
}

// NewSelectableSnapshotterConfigStatefulWithExecutable supplies the app binary
// used to isolate runtime MuMu SDK calls in short-lived helper processes.
func NewSelectableSnapshotterConfigStatefulWithExecutable(eng engine.Engine, cfg config.Config, executable string) (Provider, func(), func(config.CaptureConfig) uint64, func() CaptureState) {
	return newSelectableSnapshotter(eng, cfg, executable)
}

func NewSelectableSnapshotter(eng engine.Engine, cfg config.Config) (Provider, func(), func(config.MuMuCaptureConfig) uint64) {
	provider, close, selectTarget, _ := NewSelectableSnapshotterStateful(eng, cfg)
	return provider, close, selectTarget
}

func newSelectableSnapshotterLegacy(eng engine.Engine, cfg config.Config) (Provider, func(), func(config.MuMuCaptureConfig) uint64, func() CaptureState) {
	provider, close, selectConfig, state := newSelectableSnapshotter(eng, cfg, "")
	selectTarget := func(next config.MuMuCaptureConfig) uint64 {
		current := state().RequestedCapture
		current.MuMu = next
		return selectConfig(current)
	}
	return provider, close, selectTarget, state
}

func newSelectableSnapshotter(eng engine.Engine, cfg config.Config, executable string) (Provider, func(), func(config.CaptureConfig) uint64, func() CaptureState) {
	var selectionMu sync.Mutex
	selected := cfg.Capture
	var revision, activeRevision uint64
	var appliedConfig = cfg.Capture
	var stateMu sync.RWMutex
	var lastCaptureError string
	var stateSource string
	var cancelActiveHelper func() bool
	setSelection := func(next config.CaptureConfig) uint64 {
		selectionMu.Lock()
		selected = next
		revision++
		value := revision
		selectionMu.Unlock()
		if cancelActiveHelper != nil {
			cancelActiveHelper()
		}
		stateMu.Lock()
		lastCaptureError = ""
		stateMu.Unlock()
		return value
	}

	mode, _ := detect.ParseMode(cfg.Layout.ContentMode)
	var client capture.Client
	var helperLifecycle *helper.Lifecycle
	var helperLifecycleMu sync.Mutex
	setHelperLifecycle := func(next *helper.Lifecycle) {
		helperLifecycleMu.Lock()
		helperLifecycle = next
		helperLifecycleMu.Unlock()
	}
	cancelHelper := func() bool {
		helperLifecycleMu.Lock()
		active := helperLifecycle
		helperLifecycleMu.Unlock()
		if active == nil {
			return false
		}
		return active.Cancel()
	}
	shutdownHelper := func() bool {
		helperLifecycleMu.Lock()
		active := helperLifecycle
		helperLifecycleMu.Unlock()
		if active == nil {
			return false
		}
		return active.Close()
	}
	cancelActiveHelper = cancelHelper
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
		if c.Provider == capture.MethodLeidianADB {
			return []string{capture.MethodLeidianADB}
		}
		if c.Provider == capture.MethodMuMuSDK {
			return []string{capture.MethodMuMuSDK}
		}
		if len(c.PreferredMethods) > 0 {
			return c.PreferredMethods
		}
		return []string{capture.MethodMuMuSDK}
	}
	inner := func(ctx context.Context) Frame {
		selectionMu.Lock()
		next, rev := selected, revision
		selectionMu.Unlock()
		if rev != activeRevision {
			cancelHelper()
			setHelperLifecycle(nil)
			if client != nil {
				client.Close()
				client = nil
			}
			cfg.Capture = next
			stateMu.Lock()
			appliedConfig = next
			stateSource = ""
			lastCaptureError = ""
			stateMu.Unlock()
			retryAfter = time.Time{}
			activeRevision = rev
		}
		var f Frame
		for _, method := range preferredMethods(cfg.Capture) {
			started := time.Now()
			setAttempt(started, method)
			if method == capture.MethodMuMuSDK || method == capture.MethodLeidianADB {
				if client == nil {
					if time.Now().Before(retryAfter) {
						f = Frame{Hold: true, Err: fmt.Errorf("%s 重连等待中：%w", method, retryError), CaptureStarted: started, CaptureMethod: method}
						continue
					}
					var err error
					if method == capture.MethodMuMuSDK && executable != "" {
						runtimeLifecycle := &helper.Lifecycle{}
						setHelperLifecycle(runtimeLifecycle)
						client, err = capture.OpenRuntimeMuMu(executable, cfg.Capture, runtimeLifecycle)
					} else {
						client, err = capture.OpenConfigured(context.Background(), method, cfg.Capture)
					}
					if err != nil {
						setHelperLifecycle(nil)
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
				var (
					img *image.RGBA
					err error
				)
				captured := time.Now()
				if contextual, ok := client.(capture.ContextClient); ok {
					img, captured, err = contextual.CaptureContext(ctx)
				} else {
					img, err = client.Capture()
				}
				var transaction helper.Transaction
				if transactionClient, ok := client.(capture.HelperTransactionClient); ok {
					transaction = transactionClient.HelperTransaction()
				}
				if err != nil {
					client.Close()
					client = nil
					setHelperLifecycle(nil)
					retryError = err
					stateMu.Lock()
					stateSource = ""
					lastCaptureError = err.Error()
					stateMu.Unlock()
					// An isolated MuMu helper has been reaped at ctx expiry and can
					// safely be recreated on the next poll. Other adapters preserve
					// their existing reconnect backoff.
					if method == capture.MethodMuMuSDK && executable != "" && ctx.Err() != nil {
						retryAfter = time.Time{}
					} else {
						retryAfter = time.Now().Add(time.Second)
					}
					f = Frame{Hold: true, Err: err, CaptureStarted: started, CaptureMethod: method,
						HelperRequestStart: transaction.RequestStarted, HelperDeadline: transaction.Deadline,
						HelperOutcome: transaction.Outcome, HelperReap: transaction.Reap}
					continue
				}
				// Keep the client label for status/diagnostics while assigning a
				// stable configured-method identity to the observation.
				source := client.Source()
				f = AnalyzeImage(img, eng, mode, started, captured, source)
				f.CaptureMethod = captureMethodForConfiguredClient(method, source)
				if transactionClient, ok := client.(capture.HelperTransactionClient); ok {
					transaction := transactionClient.HelperTransaction()
					f.HelperRequestStart = transaction.RequestStarted
					f.HelperDeadline = transaction.Deadline
					f.HelperOutcome = transaction.Outcome
					f.HelperReap = transaction.Reap
				}
			} else if method == capture.MethodPrintWindow {
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
		shutdownHelper()
		setHelperLifecycle(nil)
		if client != nil {
			client.Close()
			client = nil
		}
	}
	// MuMu helpers retain the probe-sized transaction deadline inside their
	// worker context, while this synchronous caller stays bounded by the normal
	// capture timeout. A timed-out observation cancels the helper and reports a
	// pending/reap outcome; the worker clears busy only after it exits.
	provider, close := boundedProviderContextWithTimeout(inner, cleanup, func() time.Duration {
		return captureTimeout(cfg.Capture.TimeoutMS)
	}, func() time.Duration { return mumu.HelperTransactionTimeout }, cancelHelper, currentAttempt)
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
			return discardChangedRevision(f, before)
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

// discardChangedRevision preserves diagnostic correlation for a cancelled old
// target while making the UI reject its image/evidence by retaining the old
// source revision and clearing every successful-frame stage.
func discardChangedRevision(f Frame, revision uint64) Frame {
	f.SourceRevision = revision
	f.Img = nil
	f.CapturedAt, f.AnalysisStarted, f.AnalyzedAt = time.Time{}, time.Time{}, time.Time{}
	f.Hold = true
	f.Err = fmt.Errorf("正在切换模拟器，请稍候")
	return f
}

// boundedProvider preserves the legacy non-cancellable containment contract for
// in-process clients and focused tests.
func boundedProvider(inner Provider, cleanup func(), timeout time.Duration, attempts ...func() captureAttempt) (Provider, func()) {
	return boundedProviderContext(func(context.Context) Frame { return inner() }, cleanup, timeout, nil, attempts...)
}

// boundedProviderContext retains the single worker/one native call invariant.
// A ContextClient may use its request deadline to terminate an isolated helper;
// an in-process client remains busy until its native call actually returns.
func boundedProviderContext(inner func(context.Context) Frame, cleanup func(), timeout time.Duration, shutdownHelper func() bool, attempts ...func() captureAttempt) (Provider, func()) {
	return boundedProviderContextWithTimeout(inner, cleanup, func() time.Duration { return timeout }, func() time.Duration { return timeout }, shutdownHelper, attempts...)
}

type boundedRequest struct {
	result  chan Frame
	timeout time.Duration
}

func boundedProviderContextWithTimeout(inner func(context.Context) Frame, cleanup func(), timeoutFor, workerTimeoutFor func() time.Duration, shutdownHelper func() bool, attempts ...func() captureAttempt) (Provider, func()) {
	requests := make(chan boundedRequest, 1)
	done := make(chan struct{})
	var once sync.Once
	var busy atomic.Bool
	var activeTransaction atomic.Pointer[helper.Transaction]
	var terminalDiagnostic atomic.Pointer[Frame]
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
			case request := <-requests:
				select {
				case <-done:
					return
				default:
				}
				ctx, cancel := context.WithTimeout(context.Background(), workerTimeoutFor())
				f := inner(ctx)
				cancel()
				if f.HelperOutcome != "" {
					terminal := helper.Transaction{RequestStarted: f.HelperRequestStart, Deadline: f.HelperDeadline, Outcome: f.HelperOutcome, Reap: f.HelperReap}
					activeTransaction.Store(&terminal)
					// A caller may already have received the pending timeout. Retain
					// the eventual reap as its own held observation so it cannot be
					// lost between polling iterations or mistaken for a valid frame.
					if f.HelperReap == helper.ReapKilledReaped {
						terminalFrame := f
						terminalFrame.Img, terminalFrame.CapturedAt = nil, time.Time{}
						terminalFrame.Hold = true
						terminalFrame.HelperOriginOutcome = f.HelperOutcome
						terminalFrame.HelperTerminal = f.HelperReap
						terminalDiagnostic.Store(&terminalFrame)
					}
				}
				busy.Store(false)
				// Each request owns one buffer, so a timed-out caller cannot block
				// cleanup or cause its late frame to satisfy the next request.
				request.result <- f
			}
		}
	}()
	provider := func() Frame {
		requestedAt := time.Now()
		timeout := timeoutFor()
		failed := func(message, outcome string) Frame {
			f := Frame{Hold: true, Err: fmt.Errorf("%s", message), CaptureStarted: requestedAt, HelperRequestStart: requestedAt, HelperDeadline: requestedAt.Add(timeout), HelperOutcome: outcome, HelperReap: helper.ReapNotNeeded}
			if active := activeTransaction.Load(); active != nil {
				f.HelperRequestStart, f.HelperDeadline = active.RequestStarted, active.Deadline
				f.HelperOriginOutcome, f.HelperTerminal = active.Outcome, active.Reap
			}
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
			return failed("采集已关闭", helper.OutcomeSDKWorker)
		default:
		}
		if !delivering.CompareAndSwap(false, true) {
			return failed("另一调用正在接收画面，暂停接收新观测", helper.OutcomeBusy)
		}
		defer delivering.Store(false)
		if terminal := terminalDiagnostic.Swap(nil); terminal != nil {
			return *terminal
		}
		if !busy.CompareAndSwap(false, true) {
			return failed("上次采集仍在等待，暂停接收新观测", helper.OutcomeBusy)
		}
		deadline := time.Now().Add(timeout)
		result := make(chan Frame, 1)
		select {
		case <-done:
			busy.Store(false)
			return failed("采集已关闭", helper.OutcomeSDKWorker)
		case requests <- boundedRequest{result: result, timeout: timeout}:
			activeTransaction.Store(&helper.Transaction{RequestStarted: requestedAt, Deadline: requestedAt.Add(workerTimeoutFor()), Outcome: helper.OutcomeBusy, Reap: helper.ReapPending})
		}
		timer := time.NewTimer(time.Until(deadline))
		defer timer.Stop()
		select {
		case f := <-result:
			select {
			case <-done:
				return failed("采集已关闭", helper.OutcomeSDKWorker)
			default:
			}
			if time.Now().After(deadline) && shutdownHelper == nil {
				return failed("采集超时，已丢弃迟到画面", helper.OutcomeTimeout)
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
			return failed("采集已关闭", helper.OutcomeSDKWorker)
		case <-timer.C:
			if shutdownHelper != nil && shutdownHelper() {
				// Cancellation is non-blocking here: the synchronous UI loop receives
				// a held timeout immediately, while the worker owns kill/reap and keeps
				// busy set until it has rejected any late result.
				f := failed("采集超时，正在终止 MuMu 截图助手", helper.OutcomeTimeout)
				f.HelperReap = helper.ReapPending
				f.HelperOriginOutcome, f.HelperTerminal = helper.OutcomeTimeout, helper.ReapPending
				activeTransaction.Store(&helper.Transaction{RequestStarted: f.HelperRequestStart, Deadline: f.HelperDeadline, Outcome: helper.OutcomeTimeout, Reap: helper.ReapPending})
				return f
			}
			return failed("采集超时，等待原调用结束", helper.OutcomeTimeout)
		}
	}
	return provider, func() {
		once.Do(func() {
			// Helper-backed captures are externally cancellable: terminate and reap
			// before signalling shutdown, so timer-app cannot exit with a child or
			// temp request directory still alive. Legacy native calls have no
			// helper hook and retain their established non-blocking close behavior.
			if shutdownHelper != nil {
				shutdownHelper()
			}
			close(done)
		})
	}
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
