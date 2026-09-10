// Package ui 悬浮窗只显示左右替身读秒，设置单独开。
package ui

import (
	"context"
	"fmt"
	"image/color"
	"narutotimer/internal/buildinfo"
	"os"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	timerapp "narutotimer/internal/app"
	"narutotimer/internal/catalog"
	"narutotimer/internal/config"
	"narutotimer/internal/frame"
	"narutotimer/internal/identity"
	"narutotimer/internal/ninja"
	"narutotimer/internal/win32"
)

var overlayTitle = "替身 · " + buildinfo.Version

type session struct {
	captureCancel  context.CancelFunc
	captureRefresh func()
	sourceRevision uint64
	captureControl func(config.MuMuCaptureConfig) uint64
	captureSource  string
	initialAbout   bool
	settingsTabs   *container.AppTabs
	aboutCancel    context.CancelFunc
	diagnosticsState
	done                  chan struct{}
	stopOnce              sync.Once
	textControl           func(bool)
	textStatus, textError string
	textStatusLabel       *widget.Label
	cfg                   config.Config
	cfgPath               string
	provider              frame.Provider

	mu                  sync.Mutex
	beads               []frame.Bead
	status              string
	fighting            bool
	fightHits           int
	leaveHits           int
	sceneObservedAt     time.Time
	holdFreeze          bool
	inheritOnReturn     bool
	syncLeft            bool
	syncRight           bool
	scene               string
	hold                bool // Scene/capture uncertainty, never an individual bean's confidence.
	captureLost         bool
	engine              string
	layoutProfile       string
	observationSpace    timerapp.ObservationSpace
	side                string
	autoIdentity        identity.Readout
	autoIdentityAt      time.Time
	myName              string
	oppName             string
	leftNinja           string
	leftNinjaCandidate  string
	rightNinjaCandidate string
	rightNinja          string
	ninjaDisplays       [2]ninjaDisplay
	ninjaDisplayAt      time.Time
	leftSlots           int
	rightSlots          int
	remember            bool
	dumpNext            bool
	busy                bool

	left  timerapp.SideClock
	right timerapp.SideClock
	subs  *catalog.Table

	clockOnce       sync.Once
	lastCD          string
	lastEventText   string
	lastClockColor  color.Color
	lastAlt         string
	lastAltColor    color.Color
	lastDual        bool
	clockRevision   uint64
	overlayRevision uint64

	overlay      *fyne.Container
	win          fyne.Window
	settings     fyne.Window
	settingsSide *widget.RadioGroup
	cd           *canvas.Text
	altCD        *canvas.Text
	primaryLabel *canvas.Text
	alternateBox *fyne.Container
	eventTag     *canvas.Text
	tag          *canvas.Text
	info         *canvas.Text
	topmost      bool
}

type Option func(*session)

func WithTextRecognitionControl(set func(bool)) Option {
	return func(s *session) { s.textControl = set }
}

func Run(cfg config.Config, provider frame.Provider, options ...Option) error {
	if provider == nil {
		return fmt.Errorf("provider 不能为 nil")
	}
	if err := config.Validate(cfg); err != nil {
		return err
	}
	identity.SetMineNames(cfg.UI.PlayerNames)
	a := app.NewWithID("narutotimer.app")
	a.Settings().SetTheme(newChromaTheme())
	w := a.NewWindow(overlayTitle)
	w.SetMaster() // Closing the timer also quits hidden settings windows.
	s := &session{
		done:     make(chan struct{}),
		cfg:      cfg,
		cfgPath:  config.DefaultPath,
		provider: provider,
		remember: cfg.UI.RememberSide,
		side:     "",
		win:      w,
		topmost:  cfg.UI.AlwaysOnTop,
	}
	for _, option := range options {
		option(s)
	}
	w.SetOnClosed(s.stop)
	defer func() { s.stop(); s.diagnosticWriters.Wait() }()
	if len(cfg.UI.PlayerNames) > 0 {
		s.myName = cfg.UI.PlayerNames[0]
	}
	s.restoreSide()
	w.SetContent(s.overlayContent())
	s.installDrawTrace()
	if cfg.DebugOn() {
		if err := s.startDiagnostics(false); err != nil {
			s.diagnosticError = err.Error()
		}
	}
	w.SetPadded(false)
	w.Resize(fyne.NewSize(float32(max(220, cfg.UI.MiniWidth)), float32(max(118, cfg.UI.MiniHeight+24))))
	w.SetFixedSize(true)
	w.CenterOnScreen()
	go s.loop()
	go func() {
		if !s.wait(250 * time.Millisecond) {
			return
		}
		// 整窗和主题背景都保持不透明，避免底下的游戏或文字干扰读秒。
		win32.ApplyWindowAlpha(overlayTitle, 255)
		s.mu.Lock()
		top := s.topmost
		s.mu.Unlock()
		if top {
			applyTopmost(overlayTitle, true)
		}
	}()
	if s.initialAbout {
		w.Show()
		s.openAbout()
	}
	w.ShowAndRun()
	return nil
}

func (s *session) overlayContent() fyne.CanvasObject {
	s.cd = canvas.NewText("—", clockIdle)
	s.cd.TextSize = 44
	s.cd.TextStyle = fyne.TextStyle{Bold: true}
	s.cd.Alignment = fyne.TextAlignCenter
	s.primaryLabel = canvas.NewText("15秒", tagIdle)
	s.primaryLabel.TextSize = 10
	s.primaryLabel.Alignment = fyne.TextAlignCenter
	s.primaryLabel.Hide()
	s.altCD = canvas.NewText("—", clockIdle)
	s.altCD.TextSize = 32
	s.altCD.TextStyle = fyne.TextStyle{Bold: true}
	s.altCD.Alignment = fyne.TextAlignCenter
	altLabel := canvas.NewText("10秒", tagIdle)
	altLabel.TextSize = 10
	altLabel.Alignment = fyne.TextAlignCenter
	separator := canvas.NewText(" / ", tagIdle)
	separator.TextSize = 24
	s.alternateBox = container.NewHBox(container.NewCenter(separator), container.NewVBox(altLabel, s.altCD))
	s.alternateBox.Hide()
	primaryBox := container.NewVBox(s.primaryLabel, s.cd)
	s.eventTag = canvas.NewText("第 0 次", tagIdle)
	s.eventTag.TextSize = 13
	s.eventTag.TextStyle = fyne.TextStyle{Bold: true}
	s.tag = canvas.NewText("对面·待认边", tagIdle)
	s.tag.TextSize = 13
	s.tag.Alignment = fyne.TextAlignCenter
	s.info = canvas.NewText("等待画面", tagIdle)
	s.info.TextSize = 11
	s.info.Alignment = fyne.TextAlignCenter

	set := widget.NewButton("设置", s.openSettings)
	swap := widget.NewButton("换边", s.swapSide)
	diag := widget.NewButton("诊断", s.openDiagnostics)
	about := widget.NewButton("关于", s.openAbout)
	about.Importance = widget.LowImportance
	set.Importance = widget.LowImportance
	swap.Importance = widget.LowImportance
	diag.Importance = widget.LowImportance
	glass := canvas.NewRectangle(glassBG)
	body := container.NewBorder(nil, container.NewGridWithColumns(4, swap, set, diag, about), nil, nil,
		container.NewVBox(
			container.NewCenter(s.tag),
			container.NewCenter(container.NewHBox(primaryBox, s.alternateBox, container.NewCenter(s.eventTag))),
			container.NewCenter(s.info),
		),
	)
	s.overlay = container.NewStack(glass, body)
	return s.overlay
}

// The current policy is user-confirmed: all primary clocks are 15 seconds.
// Only 照美冥[五代目水影] additionally displays a simultaneous 10-second estimate.
func (s *session) cooldown() time.Duration { return ninja.DefaultCooldown }

func (s *session) opponentNinja() string {
	if manual := strings.TrimSpace(s.cfg.UI.NinjaQuery); manual != "" {
		return manual
	}
	switch s.side {
	case "left":
		return s.rightNinja
	case "right":
		return s.leftNinja
	}
	return ""
}

func (s *session) opponentClock() *timerapp.SideClock {
	switch s.side {
	case "left":
		return &s.right
	case "right":
		return &s.left
	}
	return nil
}

func (s *session) dualClockText(now time.Time) (bool, string, color.NRGBA) {
	clock := s.opponentClock()
	if clock == nil || !ninja.DualCooldown(s.opponentNinja()) {
		return false, "", clockIdle
	}
	remaining, confirmed := clock.LatestRemainingFor(now, ninja.AlternateCooldown)
	if !confirmed {
		return true, "—", clockIdle
	}
	if remaining <= 0 {
		return true, "就绪", clockIdle
	}
	return true, timerapp.FormatCD([]float64{remaining}), clockColor([]float64{remaining})
}

func (s *session) loop() {
	s.clockOnce.Do(func() { go s.clockLoop() })
	for {
		select {
		case <-s.done:
			return
		default:
		}
		started := time.Now()
		s.captureOnce()
		// 采集和检测已经占用了本轮预算；处理超时则从下一轮重新计时，
		// 不积压旧帧，也不在每轮处理结束后再额外等一个完整间隔。
		if !s.wait(nextCaptureWait(s.poll(), time.Since(started))) {
			return
		}
	}
}

func (s *session) stop() {
	s.stopOnce.Do(func() {
		close(s.done)
		s.stopDiagnostics()
		if s.aboutCancel != nil {
			s.aboutCancel()
		}
		if s.captureCancel != nil {
			s.captureCancel()
		}
	})
}

func (s *session) stopped() bool {
	select {
	case <-s.done:
		return true
	default:
		return false
	}
}

func (s *session) wait(delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-s.done:
		return false
	case <-timer.C:
		return true
	}
}

func nextCaptureWait(interval, elapsed time.Duration) time.Duration {
	delay := interval - elapsed
	if delay < time.Millisecond {
		return time.Millisecond
	}
	return delay
}

func (s *session) clockLoop() {
	for {
		select {
		case <-s.done:
			return
		default:
		}
		s.refreshClock()
		if !s.wait(s.nextTenthWait()) {
			return
		}
	}
}

func (s *session) nextTenthWait() time.Duration {
	s.mu.Lock()
	now := time.Now()
	secs := s.oppRemaining(now)
	s.mu.Unlock()
	if len(secs) == 0 {
		return 200 * time.Millisecond
	}
	return timerapp.NextTenthDelay(secs[0])
}

func (s *session) oppRemaining(now time.Time) []float64 {
	left := s.left.LatestRemaining(now)
	right := s.right.LatestRemaining(now)
	switch s.side {
	case "left":
		return right
	case "right":
		return left
	default:
		// A running clock alone cannot tell us which player is the opponent.
		return nil
	}
}

func (s *session) detectedText() string {
	switch s.side {
	case "left":
		return fmt.Sprintf("第 %d 次", s.right.EventCount())
	case "right":
		return fmt.Sprintf("第 %d 次", s.left.EventCount())
	default:
		// Until the player side is known, never label one side as the opponent.
		return fmt.Sprintf("左%d次 / 右%d次", s.left.EventCount(), s.right.EventCount())
	}
}

func (s *session) poll() time.Duration {
	s.mu.Lock()
	ms := s.cfg.UI.IdlePollIntervalMS
	if s.fighting {
		ms = s.cfg.UI.PollIntervalMS
	}
	s.mu.Unlock()
	if ms < 16 {
		ms = 16
	}
	return time.Duration(ms) * time.Millisecond
}

func (s *session) captureOnce() {
	s.mu.Lock()
	if s.busy || s.stopped() {
		s.mu.Unlock()
		return
	}
	s.busy = true
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.busy = false
		s.mu.Unlock()
	}()

	recorder := s.currentDiagnostics()
	f := s.provider()
	trace := traceRef{recorder: recorder}
	if recorder != nil {
		trace.id = recorder.Observe(f, time.Now())
	}
	s.mu.Lock()
	if s.stopped() {
		s.mu.Unlock()
		return
	}
	if f.SourceRevision != s.sourceRevision {
		s.mu.Unlock()
		return
	}
	s.traceFrame = trace
	width, height := 0, 0
	if f.Img != nil {
		width, height = f.Img.Bounds().Dx(), f.Img.Bounds().Dy()
	}
	if s.observationSpace.Update(width, height, f.CaptureMethod) {
		s.left.ResyncObservation()
		s.right.ResyncObservation()
		s.ninjaDisplays = [2]ninjaDisplay{}
	}
	if f.Status != "" {
		s.status = f.Status
	}
	if f.Err != nil {
		s.status = f.Err.Error()
	}
	s.scene = f.Scene
	s.captureSource = f.CaptureMethod
	s.textStatus, s.textError = f.TextStatus, f.TextError
	s.captureLost = f.Err != nil
	s.hold = f.Hold || s.captureLost
	// 豆数只展示这次可信对局画面的观测。旧豆不能冒充当前豆；
	// 已确认的冷却独立保存在 SideClock 中，不受显示清空影响。
	if f.Fighting && f.Err == nil {
		s.updateHUD(f)
		s.commitBeads(f.Beads)
	} else {
		s.updateNinjaDisplay(f)
		s.commitBeads(nil)
	}
	if f.Engine != "" {
		s.engine = f.Engine
	}
	if f.Hold || f.Err != nil {
		if f.Err != nil && (s.holdFreeze || s.syncLeft || s.syncRight) {
			s.left.ResyncObservation()
			s.right.ResyncObservation()
		}
		s.leaveHits = 0 // Exit evidence must be consecutive, not accumulated across animations.
		s.invalidateObservation(f.CapturedAt)
		if isHoldScene(s.cfg.Scene.HoldScenes, f.Scene) {
			s.inheritOnReturn = f.Scene == "vs" && s.fighting
			s.holdFreeze = true
		}
		s.maybeAutoSide(f)
		s.mu.Unlock()
		s.refreshOverlay()
		return
	}
	if f.Fighting && f.LayoutProfile != "" {
		if s.layoutProfile != "" && s.layoutProfile != f.LayoutProfile {
			// Different HUDs belong to different matches, never a bean drop.
			s.left.Reset()
			s.right.Reset()
			s.fighting, s.holdFreeze = false, false
			s.fightHits, s.leaveHits = 0, 0
			s.syncLeft, s.syncRight = false, false
		}
		s.layoutProfile = f.LayoutProfile
	}
	s.applyScene(f)
	s.maybeAutoSide(f)
	if !s.fighting || !f.Fighting {
		s.invalidateObservation(f.CapturedAt)
		s.mu.Unlock()
		s.refreshOverlay()
		return
	}
	// A bean's sparkle is not a scene transition. visibleReady exposes each
	// uncertain side as '?' and observeBeads invalidates only that side's votes.
	// Leave hold bound to the actual scene/capture decision above.
	if f.Status != "" {
		s.status = f.Status
	}
	if s.fighting {
		_, leftBefore := s.left.LastEvent()
		_, rightBefore := s.right.LastEvent()
		s.observeBeads(f)
		s.traceConfirmed(trace, leftBefore, rightBefore, time.Now())
		lc, rc := countReady(f.Beads)
		s.debugf("fight=%v scene=%s hold=%v beads L=%d R=%d side=%s leftCD=%s rightCD=%s",
			f.Fighting, f.Scene, f.Hold, lc, rc, s.side,
			timerapp.FormatCD(s.left.Remaining(f.CapturedAt)),
			timerapp.FormatCD(s.right.Remaining(f.CapturedAt)))
	}
	dump := s.dumpNext
	s.dumpNext = false
	beads := append([]frame.Bead(nil), s.beads...)
	s.mu.Unlock()

	if dump && f.Img != nil {
		path, err := timerapp.SaveBeadDump(f.Img, beads)
		s.mu.Lock()
		if err != nil {
			s.status = "保存截图失败: " + err.Error()
		} else {
			s.status = "已保存 " + path
		}
		s.mu.Unlock()
	}
	s.refreshOverlay()
}

func (s *session) invalidateObservation(capturedAt time.Time) {
	s.left.InvalidateObservation(capturedAt)
	s.right.InvalidateObservation(capturedAt)
}

func (s *session) observeBeads(f frame.Frame) {
	// The same pixels may be polled repeatedly, especially at reduced source FPS.
	if f.Duplicate {
		return
	}
	gap := time.Duration(s.cfg.UI.PollIntervalMS*3) * time.Millisecond
	if gap < 150*time.Millisecond {
		gap = 150 * time.Millisecond
	}
	s.left.SetObservationGap(gap)
	s.right.SetObservationGap(gap)
	lc, rc := countReady(f.Beads)
	cd := s.cooldown()
	need := s.cfg.Tracking.MinimumConfirmFrames
	if sideObservable(f.Beads, true) && len(sideBeads(f.Beads, true)) == s.expectedSlots(true) {
		if s.syncLeft {
			if s.inheritOnReturn {
				s.syncLeft = !s.left.ResumeInheritedObservation(f.CapturedAt)
			} else {
				s.left.SyncReady(lc)
				s.syncLeft = false
			}
		}
		s.left.Observe(lc, true, f.CapturedAt, cd, need)
	} else {
		s.left.InvalidateObservation(f.CapturedAt)
	}
	if sideObservable(f.Beads, false) && len(sideBeads(f.Beads, false)) == s.expectedSlots(false) {
		if s.syncRight {
			if s.inheritOnReturn {
				s.syncRight = !s.right.ResumeInheritedObservation(f.CapturedAt)
			} else {
				s.right.SyncReady(rc)
				s.syncRight = false
			}
		}
		s.right.Observe(rc, true, f.CapturedAt, cd, need)
	} else {
		s.right.InvalidateObservation(f.CapturedAt)
	}
}

func sideObservable(beads []frame.Bead, left bool) bool {
	side := sideBeads(beads, left)
	if len(side) == 0 {
		return false
	}
	for _, b := range side {
		if b.Unknown || (!b.Lit && !b.Gold && !b.Dark) || (b.Dark && (b.Lit || b.Gold)) {
			return false
		}
	}
	return true
}

func (s *session) commitBeads(next []frame.Bead) bool {
	s.beads = append(s.beads[:0], next...)
	return true
}

func readyCount(beads []frame.Bead) int {
	n := 0
	for _, b := range beads {
		if !b.Unknown && (b.Lit || b.Gold) && !b.Dark {
			n++
		}
	}
	return n
}

func sideBeads(beads []frame.Bead, left bool) []frame.Bead {
	var out []frame.Bead
	for _, b := range beads {
		if len(b.Label) == 0 {
			continue
		}
		if left && b.Label[0] != 'R' {
			out = append(out, b)
		}
		if !left && b.Label[0] == 'R' {
			out = append(out, b)
		}
	}
	return out
}

func (s *session) applyFight(raw bool) {
	enterN := s.cfg.Tracking.EnterFightFrames
	leaveN := s.cfg.Tracking.LeaveFightFrames
	if enterN < 1 {
		enterN = 1
	}
	if leaveN < 10 {
		leaveN = 10
	}
	if raw {
		s.leaveHits = 0
		s.fightHits++
		if !s.fighting && s.fightHits >= enterN {
			s.fighting = true
		}
		return
	}
	s.fightHits = 0
	if s.fighting {
		s.leaveHits++
		if s.leaveHits >= leaveN {
			s.fighting = false
			s.left.Reset()
			s.right.Reset()
			s.syncLeft, s.syncRight = false, false
		}
	}
}

func (s *session) applyScene(f frame.Frame) {
	if !f.CapturedAt.IsZero() {
		if !s.sceneObservedAt.IsZero() && !f.CapturedAt.After(s.sceneObservedAt) {
			s.leaveHits = 0
			return
		}
		s.sceneObservedAt = f.CapturedAt
	}
	if f.Hold || f.Err != nil || !isEndScene(s.cfg.Scene.EndScenes, f.Scene) {
		s.leaveHits = 0
	}
	if isHoldScene(s.cfg.Scene.HoldScenes, f.Scene) {
		s.inheritOnReturn = f.Scene == "vs" && s.fighting
		s.holdFreeze = true
		return
	}
	if f.Hold || f.Err != nil {
		return
	}
	if f.Fighting {
		if s.holdFreeze {
			s.syncLeft, s.syncRight = true, true
			// Resume each inherited baseline only when that side has a fresh,
			// complete observation; first-frame transition glints cannot replace it.
			s.holdFreeze = false
		}
		s.applyFight(true)
		return
	}
	// A static result page is valid persistence evidence on fresh captures;
	// duplicate pixels only prohibit bean-event votes, not scene confirmation.
	if isEndScene(s.cfg.Scene.EndScenes, f.Scene) {
		s.applyFight(false)
	}
}

func isHoldScene(holds []string, scene string) bool {
	if scene == "" {
		return false
	}
	for _, h := range holds {
		if h == scene {
			return true
		}
	}
	return false
}

func isEndScene(ends []string, scene string) bool {
	if scene == "" {
		return false
	}
	for _, e := range ends {
		if e == scene {
			return true
		}
	}
	return false
}

func (s *session) maybeAutoSide(f frame.Frame) {
	if f.Err != nil {
		return
	}
	if f.Scene != "" && f.Scene != "fight" && f.Scene != "vs" {
		s.clearAutoIdentity()
		return
	}
	if !manualSide(f.PlayerSide) {
		return
	}
	if f.PlayerName != "" {
		mine := false
		for _, name := range s.cfg.UI.PlayerNames {
			if identity.NormalizeName(name) == identity.NormalizeName(f.PlayerName) {
				mine = true
				break
			}
		}
		// A frame already in flight when the account changed is not current evidence.
		if !mine {
			return
		}
	}
	s.autoIdentity = identity.Readout{Side: f.PlayerSide, Mine: f.PlayerName, Opp: f.OppName}
	s.autoIdentityAt = time.Now()
	// Remember the latest recognition even under a manual lock so switching back
	// to auto can take effect immediately. It must never override that lock.
	if manualSide(s.cfg.UI.PlayerSide) {
		return
	}
	s.side = f.PlayerSide
	if f.PlayerName != "" {
		s.myName = f.PlayerName
	}
	s.oppName = f.OppName
	// Recognition is runtime state. Persisting it asynchronously can overwrite a
	// newer settings save, and must never turn "auto" into a saved left/right.
}

func clockColor(secs []float64) color.NRGBA {
	if len(secs) == 0 {
		return clockIdle
	}
	if timerapp.DisplayTenths(secs[0]) <= 30 {
		return clockWarn
	}
	return clockLive
}

func (s *session) refreshClock() {
	s.refreshClockAt(time.Now())
}

func (s *session) refreshClockAt(now time.Time) {
	if s.stopped() || s.cd == nil {
		return
	}
	s.mu.Lock()
	secs := s.oppRemaining(now)
	eventText := s.detectedText()
	txt := timerapp.FormatCD(secs)
	col := clockColor(secs)
	dual, altText, altColor := s.dualClockText(now)
	same := s.lastCD == txt && s.lastEventText == eventText && s.lastClockColor == col && s.lastDual == dual && s.lastAlt == altText && s.lastAltColor == altColor
	trace := s.traceFrame
	leftEvent, rightEvent := s.visibleEventSerials()
	if same && (trace.id == 0 || trace == s.traceClock) {
		s.mu.Unlock()
		return
	}
	s.lastCD, s.lastEventText, s.lastClockColor = txt, eventText, col
	s.lastDual, s.lastAlt, s.lastAltColor = dual, altText, altColor
	s.clockRevision++
	revision := s.clockRevision
	s.mu.Unlock()
	trace.mark("queued", time.Now())
	fyne.Do(func() {
		if s.stopped() {
			return
		}
		s.mu.Lock()
		current := s.clockRevision == revision
		s.mu.Unlock()
		if !current {
			return
		}
		clockSize := s.cd.MinSize()
		layoutChanged := false
		if s.alternateBox != nil {
			layoutChanged = s.alternateBox.Visible() != dual || s.altCD.Text != altText
			if dual {
				s.primaryLabel.Show()
				s.alternateBox.Show()
				s.cd.TextSize = 32
			} else {
				s.primaryLabel.Hide()
				s.alternateBox.Hide()
				s.cd.TextSize = 44
			}
			s.altCD.Text, s.altCD.Color = altText, altColor
			s.altCD.Refresh()
		}
		var badgeSize fyne.Size
		if s.eventTag != nil {
			badgeSize = s.eventTag.MinSize()
		}
		if s.cd.Text != txt || s.cd.Color != col {
			s.cd.Text = txt
			s.cd.Color = col
			s.cd.Refresh()
		}
		if s.eventTag != nil && s.eventTag.Text != eventText {
			s.eventTag.Text = eventText
			s.eventTag.Refresh()
		}
		// canvas.Text.Refresh repaints but does not reflow the neighboring badge.
		// Going from "—" to a multi-digit clock must also recompute its row layout.
		if s.overlay != nil && (layoutChanged || s.cd.MinSize() != clockSize || (s.eventTag != nil && s.eventTag.MinSize() != badgeSize)) {
			s.overlay.Refresh()
		}
		if trace.recorder != nil && trace.id != 0 {
			trace.recorder.CarryEvents(trace.id, leftEvent, rightEvent)
		}
		s.traceApplied(trace, false)
	})
}

func (s *session) refreshOverlay() {
	if s.stopped() || s.tag == nil {
		return
	}
	s.mu.Lock()
	side := s.side
	trainingNeedsSide := s.scene == "fight" && s.layoutProfile == "camp" && !manualSide(side)
	ninjaName := s.opponentNinja()
	if strings.TrimSpace(s.cfg.UI.NinjaQuery) == "" && (s.hold || s.captureLost || !s.fighting) {
		ninjaName = ""
	}
	recentName, needsRecheck := s.recentOpponentDisplay()
	candidate := ""
	if side == "left" {
		candidate = s.rightNinjaCandidate
	} else if side == "right" {
		candidate = s.leftNinjaCandidate
	}
	info := s.statusLine()
	textStatus := s.textStatusText()
	s.overlayRevision++
	revision := s.overlayRevision
	trace := s.traceFrame
	s.mu.Unlock()
	trace.mark("queued", time.Now())
	label := "对面·待认边"
	if trainingNeedsSide {
		label = "训练场·请选我方边"
	}
	col := tagIdle
	switch side {
	case "left":
		label, col = "对面·右", tagMine
	case "right":
		label, col = "对面·左", tagMine
	}
	if manualSide(side) && ninjaName != "" {
		label += " · " + ninja.ShortLabel(ninjaName)
	} else if manualSide(side) && recentName != "" {
		label += " · " + ninja.ShortLabel(recentName)
		if needsRecheck {
			label += "（待复核）"
		}
	} else if manualSide(side) {
		if candidate != "" {
			label += " · " + ninja.ShortLabel(candidate) + "（待确认）"
		} else {
			label += " · 忍者未确认"
		}
	}
	fyne.Do(func() {
		if s.stopped() {
			return
		}
		s.mu.Lock()
		current := s.overlayRevision == revision
		s.mu.Unlock()
		if !current {
			return
		}
		layoutChanged := s.tag.Text != label || (s.info != nil && s.info.Text != info)
		if s.textStatusLabel != nil && s.textStatusLabel.Text != textStatus {
			s.textStatusLabel.SetText(textStatus)
		}
		if s.tag.Text != label || s.tag.Color != col {
			s.tag.Text = label
			s.tag.Color = col
			s.tag.Refresh()
		}
		if s.info != nil && s.info.Text != info {
			s.info.Text = info
			s.info.Refresh()
		}
		if layoutChanged && s.overlay != nil {
			s.overlay.Refresh()
		}
		s.traceApplied(trace, true)
		s.refreshDiagnosticLabel()
	})
	s.refreshClock()
}

func (s *session) statusLine() string {
	scene := s.scene
	mode := "等待"
	switch {
	case s.captureLost:
		mode = "采集异常"
	case s.holdFreeze:
		mode = "画面切换"
	case s.hold:
		mode = "待识别"
	case s.fighting:
		mode = "对局"
	}
	name := sceneName(scene)
	if s.captureLost {
		name = "等待画面"
	} else if s.hold && scene == "" {
		name = "画面未识别"
	}
	if !s.captureLost && scene == "fight" && s.layoutProfile == "camp" {
		name = "训练场"
	}
	if !s.captureLost && s.fighting && s.hold && (scene == "" || scene == "blank" || scene == "fight") {
		// The match is still open, but this animation cannot supply bean
		// observations. Expose that distinction rather than implying exit.
		switch s.layoutProfile {
		case "duel":
			name, mode = "决斗场", "画面遮挡"
		case "camp":
			name, mode = "训练场", "画面遮挡"
		}
	}
	line := fmt.Sprintf("%s · %s  豆 左%s/右%s", name, mode,
		s.visibleReady(true), s.visibleReady(false))
	return line
}

func (s *session) expectedSlots(left bool) int {
	n := s.rightSlots
	if left {
		n = s.leftSlots
	}
	if n == 4 || n == 6 {
		return n
	}
	return s.cfg.Layout.BeadsPerSide
}

func (s *session) updateHUD(f frame.Frame) {
	if s.layoutProfile != "" && f.LayoutProfile != "" && s.layoutProfile != f.LayoutProfile {
		s.ninjaDisplays = [2]ninjaDisplay{}
	}
	s.updateNinjaDisplay(f)
	left, right := f.Slots(true, s.cfg.Layout.BeadsPerSide), f.Slots(false, s.cfg.Layout.BeadsPerSide)
	if s.leftSlots != 0 && s.leftSlots != left {
		s.left.ResyncObservation()
	}
	if s.rightSlots != 0 && s.rightSlots != right {
		s.right.ResyncObservation()
	}
	s.leftSlots, s.rightSlots = left, right
	s.leftNinja, s.rightNinja = f.LeftNinja, f.RightNinja
	s.leftNinjaCandidate, s.rightNinjaCandidate = f.LeftNinjaCandidate, f.RightNinjaCandidate
}

func (s *session) visibleReady(left bool) string {
	if !sideObservable(s.beads, left) || len(sideBeads(s.beads, left)) != s.expectedSlots(left) {
		return "?"
	}
	count := fmt.Sprint(readyCount(sideBeads(s.beads, left)))
	if s.expectedSlots(true) == 6 || s.expectedSlots(false) == 6 {
		return fmt.Sprintf("%s/%d", count, s.expectedSlots(left))
	}
	return count
}

func sceneName(scene string) string {
	switch scene {
	case "fight":
		return "决斗场"
	case "lobby":
		return "大厅"
	case "result":
		return "结算"
	case "vs":
		return "VS"
	case "queue":
		return "匹配"
	case "pick":
		return "选人"
	case "ban":
		return "禁用忍者"
	case "blank":
		return "画面遮挡"
	case "unsupported-resolution":
		return "画面比例未校准"
	case "":
		return "未知"
	default:
		return scene
	}
}

func countReady(beads []frame.Bead) (lc, rc int) {
	for _, b := range beads {
		if b.Unknown || b.Dark || len(b.Label) == 0 {
			continue
		}
		if !b.Lit && !b.Gold {
			continue
		}
		if b.Label[0] == 'R' {
			rc++
		} else {
			lc++
		}
	}
	return
}

func (s *session) debugf(format string, args ...any) {
	if !s.cfg.DebugOn() {
		return
	}
	fmt.Fprintf(os.Stderr, "[timer] "+format+"\n", args...)
}

func splitNames(s string) []string {
	s = strings.ReplaceAll(s, "，", ",")
	s = strings.ReplaceAll(s, "、", ",")
	s = strings.ReplaceAll(s, " ", ",")
	var out []string
	seen := map[string]bool{}
	for _, p := range strings.Split(s, ",") {
		n := identity.NormalizeName(p)
		if n == "" || seen[n] {
			continue
		}
		seen[n] = true
		out = append(out, n)
	}
	return out
}

func applyTopmost(title string, on bool) {
	ptr, err := syscall.UTF16PtrFromString(title)
	if err != nil {
		return
	}
	user32 := syscall.NewLazyDLL("user32.dll")
	find := user32.NewProc("FindWindowW")
	hwnd, _, _ := find.Call(0, uintptr(unsafe.Pointer(ptr)))
	if hwnd == 0 {
		return
	}
	h := win32.HWNDNoTopMost
	if on {
		h = win32.HWNDTopMost
	}
	win32.ProcSetWindowPos.Call(hwnd, h, 0, 0, 0, 0,
		uintptr(win32.SWPNoMove|win32.SWPNoSize|win32.SWPNoActivate))
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
