package hudtext

import (
	"context"
	"image"
	"io"
	"sync"
	"time"

	"narutotimer/internal/config"
	"narutotimer/internal/engine"
	"narutotimer/internal/match"
	"narutotimer/internal/ocr"
)

const requestInterval = 400 * time.Millisecond
const refreshInterval = 2 * time.Second
const resultMaxAge = 2 * time.Second
const retryDelay = 5 * time.Second

type frameKey struct {
	bounds  image.Rectangle
	regions [2]image.Rectangle
	profile string
}
type job struct {
	generation uint64
	key        frameKey
	at         time.Time
	strips     [2]strip
}
type completion struct {
	job    job
	titles [2]Title
	err    error
	proofs [2][2]*glyphEvidence // per side: ninja, exact account
}
type valueState struct {
	candidate, accepted string
	hits                int
	at                  time.Time
}

func (s *valueState) observe(value string, at time.Time) {
	if value == "" {
		*s = valueState{}
		return
	}
	if value != s.candidate {
		*s = valueState{candidate: value, hits: 1, at: at}
		return
	}
	if at.After(s.at) {
		s.hits++
		s.at = at
	}
	if s.hits >= 2 {
		s.accepted = value
	}
}

type sideState struct {
	signature                [32]byte
	ninja, account           valueState
	candidate                valueState
	ninjaProof, accountProof *glyphEvidence
}

// Engine decorates pixel detection with optional text evidence. The hot path
// NEVER waits for recognition, writes a PNG, starts a process or queues frames.
// One in-flight request and one result bound CPU, memory and stale work.
type Engine struct {
	inner                 engine.Engine
	reader                ocr.Recognizer
	cfg                   config.Config
	names                 func() []string
	dict                  Dictionary
	mu                    sync.Mutex
	enabled, closed, busy bool
	generation            uint64
	key                   frameKey
	haveKey               bool
	lastAt, nextAt        time.Time
	pausedAt              time.Time // Nonzero only during a bounded same-scene hold.
	retryAt               time.Time // backend failure, independent of scene generation
	sides                 [2]sideState
	status, lastError     string
	jobs                  chan job
	results               chan completion
	ctx                   context.Context
	cancel                context.CancelFunc
	wg                    sync.WaitGroup
}

func New(inner engine.Engine, cfg config.Config, reader ocr.Recognizer, names func() []string) *Engine {
	ctx, cancel := context.WithCancel(context.Background())
	if names == nil {
		stored := append([]string(nil), cfg.UI.PlayerNames...)
		names = func() []string { return stored }
	}
	e := &Engine{inner: inner, reader: reader, cfg: cfg, names: names, dict: NewDictionary(cfg.UI.SubstituteTable), enabled: cfg.UI.AutoTextRecognition, status: "waiting", jobs: make(chan job, 1), results: make(chan completion, 1), ctx: ctx, cancel: cancel}
	e.wg.Add(1)
	go e.work()
	return e
}

func (e *Engine) work() {
	defer e.wg.Done()
	for {
		select {
		case <-e.ctx.Done():
			return
		case request := <-e.jobs:
			if e.ctx.Err() != nil {
				return
			}
			sheet := buildSheet(request.strips)
			ctx, cancel := context.WithTimeout(e.ctx, 3*time.Second)
			lines, err := e.reader.Read(ctx, sheet.image)
			cancel()
			out := completion{job: request, err: err}
			if err == nil {
				for i, raw := range sheet.texts(lines) {
					out.titles[i] = e.dict.consensus(raw)
					out.proofs[i][0] = sheet.evidence(lines, request, i, out.titles[i].Ninja)
					if out.titles[i].Account != "" {
						out.proofs[i][1] = sheet.evidence(lines, request, i, "("+out.titles[i].Account+")")
					}
				}
			}
			select {
			case e.results <- out:
			case <-e.ctx.Done():
				return
			}
		}
	}
}

func (e *Engine) invalidate() {
	e.generation++
	e.haveKey = false
	e.sides = [2]sideState{}
	e.nextAt = time.Time{}
	e.pausedAt = time.Time{}
}

func (e *Engine) backendFailed(at time.Time) {
	e.status = "unavailable"
	e.lastError = "系统中文 OCR 暂不可用，仍使用模板/手动选择"
	e.retryAt = at.Add(retryDelay)
}

func (e *Engine) SetEnabled(enabled bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed || e.enabled == enabled {
		return
	}
	e.enabled = enabled
	e.invalidate()
	e.lastError = ""
	e.retryAt = time.Time{}
	if enabled {
		e.status = "waiting"
	} else {
		e.status = "off"
	}
}

func (e *Engine) Analyze(img *image.RGBA) engine.Result { return e.AnalyzeAt(img, time.Now()) }
func (e *Engine) AnalyzeAt(img *image.RGBA, at time.Time) engine.Result {
	var res engine.Result
	if timed, ok := e.inner.(engine.TimedEngine); ok {
		res = timed.AnalyzeAt(img, at)
	} else {
		res = e.inner.Analyze(img)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if at.IsZero() {
		at = time.Now()
	}
	if !e.lastAt.IsZero() && at.Before(e.lastAt) {
		e.invalidate()
		// Replay/clock reversal starts a new time domain, not an infinite backoff.
		e.retryAt = time.Time{}
		e.lastError = ""
	}
	e.lastAt = at
	active := !e.closed && e.enabled && img != nil && res.Fighting && !res.Uncertain && (res.LayoutProfile == "duel" || res.LayoutProfile == "camp")
	var regions [2]image.Rectangle
	if active {
		var ok bool
		regions, ok = nameRegions(img, e.cfg.Layout, res.LayoutProfile)
		active = ok
	}
	if !active {
		if !e.pauseText(img, res, at) && e.haveKey {
			e.invalidate()
		}
		select {
		case done := <-e.results:
			e.busy = false
			if done.err != nil && e.enabled && !e.closed {
				e.backendFailed(at)
			}
		default:
		}
		if !e.enabled || e.closed {
			res.TextStatus = "off"
		} else {
			res.TextStatus = "waiting"
		}
		return res
	}
	if !e.pausedAt.IsZero() && at.Sub(e.pausedAt) > textPauseTTL {
		e.invalidate()
	}
	e.pausedAt = time.Time{}
	key := frameKey{bounds: img.Bounds(), regions: regions, profile: res.LayoutProfile}
	if !e.haveKey || e.key != key {
		e.invalidate()
		e.key = key
		e.haveKey = true
		e.status = "waiting"
	}
	signatures, white := e.validateTextPixels(img, regions)
	select {
	case done := <-e.results:
		e.busy = false
		if done.err != nil {
			// Backend health outlives scene generations; only text evidence is stale.
			e.backendFailed(at)
		} else if done.job.generation == e.generation && done.job.key == key {
			if at.Sub(done.job.at) >= 0 && at.Sub(done.job.at) <= resultMaxAge {
				e.status = "pending"
				e.lastError = ""
				e.retryAt = time.Time{}
				for i, title := range done.titles {
					unchanged := signatures[i] == done.job.strips[i].signature
					s := &e.sides[i]
					observeBound(&s.ninja, &s.ninjaProof, title.Ninja, done.proofs[i][0], img, done.job.at, unchanged)
					observeBound(&s.account, &s.accountProof, title.Account, done.proofs[i][1], img, done.job.at, unchanged)
					if unchanged {
						s.candidate.observe(title.Candidate, done.job.at)
					}
				}
			}
		}
	default:
	}
	var known [2]Title
	for i, s := range e.sides {
		known[i] = Title{Ninja: s.ninja.accepted, Account: s.account.accepted}
		known[i].Candidate = s.candidate.accepted
	}
	used := false
	if res.LeftNinja == "" && known[0].Ninja != "" {
		res.LeftNinja = known[0].Ninja
		used = true
	}
	if res.RightNinja == "" && known[1].Ninja != "" {
		res.RightNinja = known[1].Ninja
		used = true
	}
	if res.LeftNinja == "" {
		res.LeftNinjaCandidate = known[0].Candidate
	}
	if res.RightNinja == "" {
		res.RightNinjaCandidate = known[1].Candidate
	}
	// Account matching remains exact and unique. OCR words are not commands,
	// fuzzy identities, aliases, or permission to override a template/manual side.
	if res.PlayerSide == "" {
		mine := [2]bool{}
		for _, name := range e.names() {
			for i, title := range known {
				if name != "" && name == title.Account {
					mine[i] = true
				}
			}
		}
		if mine[0] != mine[1] {
			i := 0
			res.PlayerSide = "left"
			if mine[1] {
				i = 1
				res.PlayerSide = "right"
			}
			res.PlayerName = known[i].Account
			used = true
			// Unconfigured opponent account OCR is diagnostic only: a plausible
			// repeated misreading must not be promoted to a named known identity.
		}
	}
	needs := res.LeftNinja == "" || res.RightNinja == "" || (len(e.names()) > 0 && res.PlayerSide == "")
	// Derive UI status from CURRENT evidence/work. A remembered "ready" is not
	// evidence after the glyphs disappear, and an old error must not hide retry.
	switch {
	case needs && at.Before(e.retryAt):
		e.status = "unavailable"
	case used:
		e.status = "ready"
	case !needs:
		e.status = "template"
	case e.busy:
		e.status = "reading"
	case white[0] >= 12 || white[1] >= 12:
		e.status = "pending"
	default:
		e.status = "waiting"
	}
	// Even a stable base gets infrequent re-reads, allowing a later exact
	// version to be acquired. Missing-side retries cannot erase a proven field.
	if (needs || used) && !e.busy && !at.Before(e.nextAt) && !at.Before(e.retryAt) && (white[0] >= 12 || white[1] >= 12) {
		request := job{generation: e.generation, key: key, at: at}
		for i, roi := range regions {
			request.strips[i] = strip{img: match.CropRGBA(img, roi), signature: signatures[i], white: white[i]}
		}
		select {
		case e.jobs <- request:
			e.busy = true
			e.nextAt = at.Add(requestInterval)
			if !needs {
				e.nextAt = at.Add(refreshInterval)
			}
			if e.status != "ready" {
				e.status = "reading"
			}
		default:
		}
	}
	res.TextStatus, res.TextError = e.status, ""
	if e.status == "unavailable" {
		res.TextError = e.lastError
	}
	return res
}

func (e *Engine) Close() error {
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return nil
	}
	e.closed = true
	e.invalidate()
	e.mu.Unlock()
	e.cancel()
	err := e.reader.Close()
	e.wg.Wait()
	if closer, ok := e.inner.(io.Closer); ok {
		if closeErr := closer.Close(); err == nil {
			err = closeErr
		}
	}
	return err
}
