package ui

import (
	"errors"
	"fmt"
	"image"
	"image/color"
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"narutotimer/internal/config"
	"narutotimer/internal/draw"
	"narutotimer/internal/frame"
)

// 区域校准（docs/区域校准.md）：玩家在一帧对局画面上框出每侧的豆和头像。
// 这里只有 Windows 的画框窗口；识别怎么用这些框由引擎决定（WithCalibrationApply）。

// calibTarget is one of the four boxes, in the contract's order.
type calibTarget int

const (
	calibLeftBeads calibTarget = iota
	calibRightBeads
	calibLeftAvatar
	calibRightAvatar
	calibTargetCount
)

var calibTargetNames = [calibTargetCount]string{"左侧豆", "右侧豆", "左侧头像", "右侧头像"}

// Outline colours, distinct from each other and readable on the game HUD.
var calibTargetColors = [calibTargetCount]color.NRGBA{
	clockLive,                        // 左侧豆
	clockWarn,                        // 右侧豆
	{R: 124, G: 232, B: 140, A: 255}, // 左侧头像
	{R: 255, G: 128, B: 214, A: 255}, // 右侧头像
}

var (
	calibSurfaceBG = color.NRGBA{R: 4, G: 14, B: 20, A: 255}
	calibTagBG     = color.NRGBA{R: 11, G: 26, B: 36, A: 215}
	// Bead-centre dots: white with a dark rim shows on lit and empty beads alike.
	calibDotFill    = color.NRGBA{R: 255, G: 255, B: 255, A: 255}
	calibDotOutline = color.NRGBA{A: 230}
)

func (t calibTarget) beads() bool { return t == calibLeftBeads || t == calibRightBeads }
func (t calibTarget) left() bool  { return t == calibLeftBeads || t == calibLeftAvatar }

const calibrationHint = "选好目标后在画面上按住左键拖出方框：豆框紧贴 4 颗普通豆（柱间第 5、6 颗不框），小点应落在每颗豆中间；头像框只框头像，不含名字和血条。"

var (
	errCalibrationProfile = errors.New("请在对局或训练场画面上校准")
	errCalibrationMixed   = errors.New("未保存的框分别画在训练场和决斗场画面上，请在同一画面上重画")
	// errInvalidCalibration wraps a validation failure: nothing was applied.
	errInvalidCalibration = errors.New("校准框无效")
)

func calibProfileName(profile string) string {
	switch profile {
	case "camp":
		return "训练场"
	case "duel":
		return "决斗场"
	}
	return ""
}

// calibrationRects lists a calibration's boxes in calibTarget order as copies.
func calibrationRects(c *config.RegionCalibration) [calibTargetCount]*config.NormalizedRect {
	var out [calibTargetCount]*config.NormalizedRect
	if c == nil {
		return out
	}
	for t, r := range [calibTargetCount]*config.NormalizedRect{c.LeftBeads, c.RightBeads, c.LeftAvatar, c.RightAvatar} {
		if r != nil {
			v := *r
			out[t] = &v
		}
	}
	return out
}

func newRegionCalibration(profile string, rects [calibTargetCount]*config.NormalizedRect) *config.RegionCalibration {
	return &config.RegionCalibration{Profile: profile,
		LeftBeads: rects[calibLeftBeads], RightBeads: rects[calibRightBeads],
		LeftAvatar: rects[calibLeftAvatar], RightAvatar: rects[calibRightAvatar]}
}

// cloneCalibration deep-copies c: no rect pointer is ever shared between the
// session, the window and the engine.
func cloneCalibration(c *config.RegionCalibration) *config.RegionCalibration {
	if c == nil {
		return nil
	}
	return newRegionCalibration(c.Profile, calibrationRects(c))
}

// calibrationBeadCenters are a bead box's four implied bead centres as frame
// fractions, bead 0 outermost (leftmost on the left, rightmost on the right).
func calibrationBeadCenters(r config.NormalizedRect, left bool) [4]config.NormalizedPoint {
	var out [4]config.NormalizedPoint
	for i := range out {
		offset := (float64(i) + 0.5) * r.Width / 4
		x := r.X + offset
		if !left {
			x = r.X + r.Width - offset
		}
		out[i] = config.NormalizedPoint{X: x, Y: r.Y + r.Height/2}
	}
	return out
}

// calibBox is a box drawn on a snapshot, with the layout of that frame.
type calibBox struct {
	rect    config.NormalizedRect
	profile string
}

// mergeCalibration applies the boxes the user drew or cleared (touched) to the
// saved calibration; untouched boxes keep their saved value. New boxes must
// come from camp/duel frames of one layout. Saved boxes drawn on the other
// layout cannot share the new profile: they are dropped and returned, so the
// caller can ask first. nil means every region is automatic.
func mergeCalibration(saved *config.RegionCalibration, drawn [calibTargetCount]*calibBox, touched [calibTargetCount]bool) (*config.RegionCalibration, []calibTarget, error) {
	profile := ""
	for t, box := range drawn {
		if !touched[t] || box == nil {
			continue
		}
		if box.profile != "camp" && box.profile != "duel" {
			return nil, nil, errCalibrationProfile
		}
		if profile != "" && profile != box.profile {
			return nil, nil, errCalibrationMixed
		}
		profile = box.profile
	}
	rects := calibrationRects(saved)
	var dropped []calibTarget
	if profile == "" {
		if saved != nil {
			profile = saved.Profile
		}
	} else if saved != nil && saved.Profile != profile {
		for t := range rects {
			if !touched[t] && rects[t] != nil {
				rects[t] = nil
				dropped = append(dropped, calibTarget(t))
			}
		}
	}
	empty := true
	for t := range rects {
		if touched[t] {
			rects[t] = nil
			if drawn[t] != nil {
				r := drawn[t].rect
				rects[t] = &r
			}
		}
		if rects[t] != nil {
			empty = false
		}
	}
	if empty {
		return nil, dropped, nil
	}
	return newRegionCalibration(profile, rects), dropped, nil
}

// ---- frame snapshots ----

// calibrationCopyInterval spaces frame copies while the window is open:
// 刷新画面 gets a frame at most this old without copying every 16 ms frame.
var calibrationCopyInterval = 200 * time.Millisecond

// calibrationSnapshot is a private copy of one captured frame. It is never
// modified after it is published.
type calibrationSnapshot struct {
	img     *image.RGBA // origin (0,0)
	profile string      // camp/duel on a recognised match frame, else ""
}

func newCalibrationSnapshot(f frame.Frame) *calibrationSnapshot {
	snap := &calibrationSnapshot{img: copyFrameImage(f.Img)}
	// Only a recognised match frame proves its layout; on other pages the
	// engine's profile is a guess.
	if f.Fighting && (f.LayoutProfile == "camp" || f.LayoutProfile == "duel") {
		snap.profile = f.LayoutProfile
	}
	return snap
}

// copyFrameImage deep-copies a captured frame (MuMu reuses a ring buffer).
// Padded or offset images are copied row by row to a packed (0,0) image.
func copyFrameImage(src *image.RGBA) *image.RGBA {
	b := src.Bounds()
	if b.Min == (image.Point{}) && src.Stride == 4*b.Dx() {
		return draw.CopyRGBA(src)
	}
	dst := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	row := 4 * b.Dx()
	for y := 0; y < b.Dy(); y++ {
		at := src.PixOffset(b.Min.X, b.Min.Y+y)
		copy(dst.Pix[y*dst.Stride:y*dst.Stride+row], src.Pix[at:at+row])
	}
	return dst
}

// calibrationFeed hands the open calibration window copies of captured frames.
// While it is closed the capture loop pays one atomic load per frame.
type calibrationFeed struct {
	open   atomic.Bool
	mu     sync.Mutex
	latest *calibrationSnapshot
	copied time.Time
	notify func() // called once, after the first copy since start
}

func (c *calibrationFeed) start(notify func()) {
	c.mu.Lock()
	c.latest, c.copied, c.notify = nil, time.Time{}, notify
	c.open.Store(true)
	c.mu.Unlock()
}

// stop ends copying and drops the last copy.
func (c *calibrationFeed) stop() {
	c.mu.Lock()
	c.open.Store(false)
	c.latest, c.copied, c.notify = nil, time.Time{}, nil
	c.mu.Unlock()
}

func (c *calibrationFeed) snapshot() *calibrationSnapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.latest
}

func (c *calibrationFeed) offer(f frame.Frame, now time.Time) {
	if !c.open.Load() || f.Err != nil || f.Img == nil || f.Img.Bounds().Empty() {
		return
	}
	c.mu.Lock()
	due := c.latest == nil || now.Sub(c.copied) >= calibrationCopyInterval || now.Before(c.copied)
	c.mu.Unlock()
	if !due {
		return
	}
	snap := newCalibrationSnapshot(f) // the copy runs outside the lock
	c.mu.Lock()
	if !c.open.Load() {
		c.mu.Unlock()
		return
	}
	c.latest, c.copied = snap, now
	notify := c.notify
	c.notify = nil
	c.mu.Unlock()
	if notify != nil {
		notify()
	}
}

// offerCalibrationFrame runs on the capture goroutine right after the
// provider returns, while the frame's pixels are still valid.
func (s *session) offerCalibrationFrame(f frame.Frame) {
	if !s.calibrationFeed.open.Load() {
		return
	}
	s.mu.Lock()
	current := f.SourceRevision == s.sourceRevision && !s.stopped()
	s.mu.Unlock()
	if current {
		s.calibrationFeed.offer(f, time.Now())
	}
}

// calibration returns a private copy of the saved region calibration.
func (s *session) calibration() *config.RegionCalibration {
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneCalibration(s.cfg.Layout.Calibration)
}

// setCalibration stores the region calibration (nil = all automatic), saves
// the settings and hands the engine hook a private copy. A validation error
// (errInvalidCalibration) changes nothing; any other error is a failed write
// after the calibration was applied, like the other settings.
func (s *session) setCalibration(next *config.RegionCalibration) error {
	next = cloneCalibration(next)
	s.mu.Lock()
	cfg := s.cfg
	cfg.Layout.Calibration = next
	if err := config.Validate(cfg); err != nil {
		s.mu.Unlock()
		return fmt.Errorf("%w：%v", errInvalidCalibration, err)
	}
	s.cfg.Layout.Calibration = next
	err := s.saveSettingsLocked()
	apply := s.calibrationApply
	s.mu.Unlock()
	if apply == nil {
		return err
	}
	apply(cloneCalibration(next))
	// Beads read with the old regions are no baseline for the new ones.
	s.mu.Lock()
	s.left.ResyncObservation()
	s.right.ResyncObservation()
	s.ninjaDisplays = [2]ninjaDisplay{}
	s.mu.Unlock()
	return err
}

// ---- window ----

// calibrationWindow is the 区域校准 window. All of its state lives only while
// it is open and is touched on the UI goroutine only.
type calibrationWindow struct {
	s       *session
	win     fyne.Window
	snap    *calibrationSnapshot      // shown frame; nil until one arrives
	saved   *config.RegionCalibration // as saved; untouched boxes show this
	drawn   [calibTargetCount]*calibBox
	touched [calibTargetCount]bool // drawn or cleared since the last save
	target  calibTarget
	done    bool

	surface *calibrationSurface
	targets *widget.RadioGroup
	// layout lets the player name the layout of a frame the engine did not
	// recognise as a match, which is what an unsupported resolution looks like.
	layout  *widget.RadioGroup
	info    *widget.Label
	status  *widget.Label
	confirm *dialog.ConfirmDialog // the open confirmation, if any
}

// openCalibration shows the calibration window; an open one is only brought
// forward. Frames are copied for it only while it is open.
func (s *session) openCalibration() {
	if s.stopped() {
		return
	}
	if c := s.calibrationWin; c != nil {
		c.win.Show()
		c.win.RequestFocus()
		return
	}
	w := fyne.CurrentApp().NewWindow("区域校准 · 豆与头像")
	c := newCalibrationWindow(s, w)
	s.calibrationWin = c
	addToggleMiniShortcut(w, s)
	w.Resize(fyne.NewSize(960, 620))
	w.Show()
	w.RequestFocus()
	s.calibrationFeed.start(func() { fyne.Do(c.frameArrived) })
}

func newCalibrationWindow(s *session, w fyne.Window) *calibrationWindow {
	c := &calibrationWindow{s: s, win: w, saved: s.calibration()}
	c.surface = newCalibrationSurface(c)
	c.targets = widget.NewRadioGroup(calibTargetNames[:], nil)
	c.targets.Horizontal, c.targets.Required = true, true
	c.targets.SetSelected(calibTargetNames[calibLeftBeads])
	c.targets.OnChanged = c.selectTarget
	c.layout = widget.NewRadioGroup([]string{"训练场", "决斗场"}, func(string) {
		if c.snap != nil {
			c.showSnapshot(c.snap)
		}
	})
	c.layout.Horizontal = true
	c.layout.Hide()
	c.info = widget.NewLabel("等待画面…")
	c.status = widget.NewLabel(calibrationHint)
	c.status.Wrapping = fyne.TextWrapWord

	refresh := widget.NewButtonWithIcon("刷新画面", theme.ViewRefreshIcon(), c.refreshFrame)
	clearSelected := widget.NewButtonWithIcon("清除所选", theme.ContentClearIcon(), c.clearSelected)
	restore := widget.NewButtonWithIcon("全部恢复自动", theme.HistoryIcon(), c.confirmRestoreAuto)
	save := widget.NewButtonWithIcon("保存", theme.DocumentSaveIcon(), c.confirmSave)
	save.Importance = widget.HighImportance
	closeButton := widget.NewButtonWithIcon("关闭", theme.CancelIcon(), c.requestClose)

	top := container.NewVBox(container.NewBorder(nil, nil, widget.NewLabel("框选"), c.info, c.targets), c.layout)
	bottom := container.NewVBox(c.status,
		container.NewHBox(refresh, clearSelected, restore, layout.NewSpacer(), save, closeButton))
	w.SetContent(container.NewStack(canvas.NewRectangle(panelBG),
		container.NewPadded(container.NewBorder(top, bottom, nil, nil, c.surface))))
	w.SetCloseIntercept(c.requestClose)
	w.SetOnClosed(c.closed)
	return c
}

func (c *calibrationWindow) closed() {
	c.done = true
	if c.s.calibrationWin == c {
		c.s.calibrationWin = nil
		c.s.calibrationFeed.stop()
	}
	c.snap = nil
}

// requestClose asks before dropping boxes that were not saved.
func (c *calibrationWindow) requestClose() {
	if !c.dirty() {
		c.win.Close()
		return
	}
	c.ask("有未保存的框", "关闭后这些框不会保存，识别仍按原来的设置。", "关闭", c.win.Close)
}

func (c *calibrationWindow) ask(title, message, confirm string, yes func()) {
	if c.confirm != nil {
		c.confirm.Hide()
	}
	d := dialog.NewConfirm(title, message, func(ok bool) {
		c.confirm = nil
		if ok {
			yes()
		}
	}, c.win)
	d.SetConfirmText(confirm)
	d.SetDismissText("取消")
	c.confirm = d
	d.Show()
}

// frameArrived shows the first frame copied after the window opened.
func (c *calibrationWindow) frameArrived() {
	if c.done || c.snap != nil {
		return
	}
	if snap := c.s.calibrationFeed.snapshot(); snap != nil {
		c.showSnapshot(snap)
	}
}

func (c *calibrationWindow) refreshFrame() {
	snap := c.s.calibrationFeed.snapshot()
	if snap == nil {
		c.setStatus("还没有采集到画面：请确认模拟器已连接、计时器正在采集。")
		return
	}
	c.showSnapshot(snap)
}

// profile is the frame's recognised layout, else the one the player picked.
func (c *calibrationWindow) profile() string {
	if c.snap != nil && c.snap.profile != "" {
		return c.snap.profile
	}
	switch c.layout.Selected {
	case "训练场":
		return "camp"
	case "决斗场":
		return "duel"
	}
	return ""
}

func (c *calibrationWindow) showSnapshot(snap *calibrationSnapshot) {
	c.snap = snap
	b := snap.img.Bounds()
	if snap.profile == "" {
		c.layout.Show()
	} else {
		c.layout.Hide()
	}
	name := calibProfileName(c.profile())
	if name == "" {
		name = "未识别的画面"
	}
	c.info.SetText(fmt.Sprintf("%s · %d×%d", name, b.Dx(), b.Dy()))
	c.surface.Refresh()
	text := calibrationHint
	switch {
	case c.profile() == "":
		text = "没认出这是对局画面（分辨率特殊时常见）。如果这是对局或训练场，请在上方选择训练场/决斗场再框选；否则进入对局后点「刷新画面」。"
	case c.saved != nil && c.saved.Profile != c.profile():
		text = fmt.Sprintf("已保存的框画在%s画面上，这里是%s，位置会略有偏差；在本画面上保存时，未重画的框恢复自动。",
			calibProfileName(c.saved.Profile), calibProfileName(c.profile()))
	}
	c.setStatus(text + c.pendingText())
}

func (c *calibrationWindow) setStatus(text string) { c.status.SetText(text) }

func (c *calibrationWindow) selectTarget(name string) {
	for t, n := range calibTargetNames {
		if n == name {
			c.target = calibTarget(t)
		}
	}
	c.surface.Refresh()
}

func (c *calibrationWindow) dirty() bool {
	for _, touched := range c.touched {
		if touched {
			return true
		}
	}
	return false
}

// boxes is what the window shows: drawn or cleared boxes over saved ones.
func (c *calibrationWindow) boxes() [calibTargetCount]*config.NormalizedRect {
	out := calibrationRects(c.saved)
	for t := range out {
		if c.touched[t] {
			out[t] = nil
			if c.drawn[t] != nil {
				r := c.drawn[t].rect
				out[t] = &r
			}
		}
	}
	return out
}

func (c *calibrationWindow) pendingText() string {
	var names []string
	for t, touched := range c.touched {
		if touched {
			names = append(names, calibTargetNames[t])
		}
	}
	if len(names) == 0 {
		return ""
	}
	return "未保存：" + strings.Join(names, "、") + "。"
}

// finishBox turns a finished drag (surface coordinates) into the selected box.
func (c *calibrationWindow) finishBox(from, to fyne.Position) {
	if c.snap == nil {
		return
	}
	name := calibTargetNames[c.target]
	r, ok := calibrationBoxFromDrag(c.surface.fit(), from, to)
	if ok {
		c.drawn[c.target] = &calibBox{rect: r, profile: c.profile()}
		c.touched[c.target] = true
	}
	c.surface.Refresh()
	if !ok {
		c.setStatus("框太小，已忽略：按住左键拖出整个" + name + "区域。" + c.pendingText())
		return
	}
	text := name + "已框好。"
	if c.target.beads() {
		text = name + "已框好，4 个小点应落在每颗豆中间。"
	}
	if c.profile() == "" {
		text += "先在上方选择训练场或决斗场，否则不能保存。"
	}
	c.setStatus(text + c.pendingText())
}

func (c *calibrationWindow) clearSelected() {
	t := c.target
	name := calibTargetNames[t]
	if c.boxes()[t] == nil {
		c.setStatus(name + "没有框，已是自动识别。" + c.pendingText())
		return
	}
	c.drawn[t], c.touched[t] = nil, true
	c.surface.Refresh()
	c.setStatus(name + "已清除，保存后恢复自动识别。" + c.pendingText())
}

func (c *calibrationWindow) confirmSave() {
	if !c.dirty() {
		c.setStatus("没有改动：先选目标，在画面上拖出方框。")
		return
	}
	saved := c.s.calibration()
	next, dropped, err := mergeCalibration(saved, c.drawn, c.touched)
	if err != nil {
		c.showSaveError(err)
		return
	}
	if len(dropped) == 0 {
		c.save()
		return
	}
	names := make([]string, len(dropped))
	for i, t := range dropped {
		names[i] = calibTargetNames[t]
	}
	c.ask("布局不同", fmt.Sprintf("已保存的%s是在%s画面上框的，和这次的%s画面布局不同，保存后会恢复自动识别（可在本画面上重画）。",
		strings.Join(names, "、"), calibProfileName(saved.Profile), calibProfileName(next.Profile)),
		"仍然保存", func() { c.save() })
}

// save merges the drawn and cleared boxes into the saved calibration, applies
// it and writes config.json. It returns the error shown to the user.
func (c *calibrationWindow) save() error {
	next, _, err := mergeCalibration(c.s.calibration(), c.drawn, c.touched)
	if err == nil {
		err = c.s.setCalibration(next)
	}
	if err != nil && (errors.Is(err, errCalibrationProfile) || errors.Is(err, errCalibrationMixed) || errors.Is(err, errInvalidCalibration)) {
		c.showSaveError(err)
		return err
	}
	c.saved = cloneCalibration(next)
	c.drawn, c.touched = [calibTargetCount]*calibBox{}, [calibTargetCount]bool{}
	c.surface.Refresh()
	if next == nil {
		c.setStatus("已保存：豆和头像全部自动识别" + c.live() + "。")
	} else {
		c.setStatus("已保存" + c.live() + "（" + calibProfileName(next.Profile) + "画面上画的框）。")
	}
	showSettingsError(err, c.win)
	return err
}

// live says so only when an engine hook applies the change at once.
func (c *calibrationWindow) live() string {
	if c.s.calibrationApply != nil {
		return "，立即生效"
	}
	return ""
}

func (c *calibrationWindow) showSaveError(err error) {
	text := err.Error()
	if errors.Is(err, errCalibrationProfile) {
		text = "当前画面不是对局或训练场，无法确定布局。" + errCalibrationProfile.Error() + "：进入后点「刷新画面」，再重画这些框。"
	}
	c.setStatus("未保存：" + text)
	dialog.ShowInformation("不能保存", text, c.win)
}

func (c *calibrationWindow) confirmRestoreAuto() {
	if c.saved == nil && !c.dirty() {
		c.setStatus("没有校准框，已是自动识别。")
		return
	}
	c.ask("全部恢复自动", "清除全部校准框（包括未保存的），豆和头像都改回自动识别？", "恢复自动", func() { c.restoreAuto() })
}

// restoreAuto clears every box (calibration = nil), saves and applies it.
func (c *calibrationWindow) restoreAuto() error {
	err := c.s.setCalibration(nil)
	if errors.Is(err, errInvalidCalibration) {
		c.showSaveError(err)
		return err
	}
	c.saved = nil
	c.drawn, c.touched = [calibTargetCount]*calibBox{}, [calibTargetCount]bool{}
	c.surface.Refresh()
	c.setStatus("已全部恢复自动识别" + c.live() + "。")
	showSettingsError(err, c.win)
	return err
}

// ---- geometry ----

const (
	calibMinBoxUnits  = 4 // a smaller drag on screen is a slip, not a box
	calibMinBoxPixels = 3 // nor can a thinner box hold a bead or an avatar
)

// calibrationFit places a w×h frame inside the surface: centred, aspect kept.
type calibrationFit struct {
	x, y  float64 // top-left of the drawn frame, surface units
	scale float64 // surface units per frame pixel
	w, h  int     // frame pixels
}

func fitCalibrationImage(w, h int, area fyne.Size) calibrationFit {
	if w <= 0 || h <= 0 || area.Width <= 0 || area.Height <= 0 {
		return calibrationFit{}
	}
	scale := math.Min(float64(area.Width)/float64(w), float64(area.Height)/float64(h))
	return calibrationFit{x: (float64(area.Width) - scale*float64(w)) / 2, y: (float64(area.Height) - scale*float64(h)) / 2,
		scale: scale, w: w, h: h}
}

func (f calibrationFit) ok() bool { return f.scale > 0 }

// pixel maps a surface position to frame pixels, clamped to the frame.
func (f calibrationFit) pixel(p fyne.Position) (float64, float64) {
	x := (float64(p.X) - f.x) / f.scale
	y := (float64(p.Y) - f.y) / f.scale
	return math.Min(math.Max(x, 0), float64(f.w)), math.Min(math.Max(y, 0), float64(f.h))
}

// point maps frame fractions to a surface position.
func (f calibrationFit) point(nx, ny float64) fyne.Position {
	return fyne.NewPos(float32(f.x+nx*float64(f.w)*f.scale), float32(f.y+ny*float64(f.h)*f.scale))
}

func (f calibrationFit) rect(r config.NormalizedRect) (fyne.Position, fyne.Size) {
	at := f.point(r.X, r.Y)
	end := f.point(r.X+r.Width, r.Y+r.Height)
	return at, fyne.NewSize(end.X-at.X, end.Y-at.Y)
}

// calibrationBoxFromDrag maps a drag on the surface to a box: surface units →
// frame pixels (clamped to the frame, whole pixels) → fractions of the frame.
func calibrationBoxFromDrag(f calibrationFit, from, to fyne.Position) (config.NormalizedRect, bool) {
	if !f.ok() {
		return config.NormalizedRect{}, false
	}
	x0, y0 := f.pixel(from)
	x1, y1 := f.pixel(to)
	left, right := math.Round(math.Min(x0, x1)), math.Round(math.Max(x0, x1))
	top, bottom := math.Round(math.Min(y0, y1)), math.Round(math.Max(y0, y1))
	w, h := right-left, bottom-top
	if w < calibMinBoxPixels || h < calibMinBoxPixels || w*f.scale < calibMinBoxUnits || h*f.scale < calibMinBoxUnits {
		return config.NormalizedRect{}, false
	}
	fw, fh := float64(f.w), float64(f.h)
	return frameFraction(left/fw, top/fh, w/fw, h/fh), true
}

// frameFraction rounds to 6 decimals (far below a pixel even at 4K) and keeps
// the box inside the frame, as config validation requires.
func frameFraction(x, y, w, h float64) config.NormalizedRect {
	round := func(v float64) float64 { return math.Round(v*1e6) / 1e6 }
	r := config.NormalizedRect{X: round(x), Y: round(y), Width: round(w), Height: round(h)}
	for r.X+r.Width > 1 {
		r.Width = math.Nextafter(r.Width, 0)
	}
	for r.Y+r.Height > 1 {
		r.Height = math.Nextafter(r.Height, 0)
	}
	return r
}

// ---- image surface ----

// calibrationSurface shows the snapshot scaled to fit with every box, and
// turns a drag into a box for the selected target.
type calibrationSurface struct {
	widget.BaseWidget
	c        *calibrationWindow
	dragging bool
	from, to fyne.Position // surface coordinates
}

func newCalibrationSurface(c *calibrationWindow) *calibrationSurface {
	v := &calibrationSurface{c: c}
	v.ExtendBaseWidget(v)
	return v
}

func (v *calibrationSurface) CreateRenderer() fyne.WidgetRenderer { return newCalibrationRenderer(v) }

func (v *calibrationSurface) Cursor() desktop.Cursor { return desktop.CrosshairCursor }

// fit is where the snapshot is drawn at the surface's current size.
func (v *calibrationSurface) fit() calibrationFit {
	if v.c.snap == nil {
		return calibrationFit{}
	}
	b := v.c.snap.img.Bounds()
	return fitCalibrationImage(b.Dx(), b.Dy(), v.Size())
}

// MouseDown starts over even if the previous drag never got its DragEnd
// (button released outside the window).
func (v *calibrationSurface) MouseDown(*desktop.MouseEvent) { v.dragging = false }
func (v *calibrationSurface) MouseUp(*desktop.MouseEvent)   {}

// Dragged follows Fyne's desktop driver: Position is the pointer relative to
// the surface and Dragged the movement since the previous event, so the drag
// started at the first event's Position − Dragged.
func (v *calibrationSurface) Dragged(ev *fyne.DragEvent) {
	if v.c.snap == nil {
		return
	}
	if !v.dragging {
		v.dragging = true
		v.from = ev.Position.Subtract(ev.Dragged)
	}
	v.to = ev.Position
	v.Refresh()
}

func (v *calibrationSurface) DragEnd() {
	if !v.dragging {
		return
	}
	v.dragging = false
	v.c.finishBox(v.from, v.to)
}

type calibrationRenderer struct {
	v       *calibrationSurface
	bg      *canvas.Rectangle
	img     *canvas.Image
	empty   *canvas.Text
	boxes   [calibTargetCount]*canvas.Rectangle
	tagBG   [calibTargetCount]*canvas.Rectangle
	tags    [calibTargetCount]*canvas.Text
	dots    [2][4]*canvas.Circle // implied bead centres of the two bead boxes
	band    *canvas.Rectangle    // the box being dragged
	objects []fyne.CanvasObject
}

func newCalibrationRenderer(v *calibrationSurface) *calibrationRenderer {
	r := &calibrationRenderer{v: v, bg: canvas.NewRectangle(calibSurfaceBG),
		img: &canvas.Image{FillMode: canvas.ImageFillStretch, ScaleMode: canvas.ImageScaleSmooth}}
	r.bg.StrokeColor, r.bg.StrokeWidth = panelBorder, 1
	r.empty = canvas.NewText("等待画面…", tagIdle)
	r.empty.Alignment = fyne.TextAlignCenter
	r.objects = []fyne.CanvasObject{r.bg, r.img, r.empty}
	for t := range r.boxes {
		r.boxes[t] = canvas.NewRectangle(transparentColor)
		r.boxes[t].StrokeColor = calibTargetColors[t]
		r.objects = append(r.objects, r.boxes[t])
	}
	for t := range r.dots {
		for i := range r.dots[t] {
			dot := canvas.NewCircle(calibDotFill)
			dot.StrokeColor, dot.StrokeWidth = calibDotOutline, 1
			r.dots[t][i] = dot
			r.objects = append(r.objects, dot)
		}
	}
	// Names above every outline, so a neighbouring box never crosses a name.
	for t := range r.tags {
		r.tagBG[t] = canvas.NewRectangle(calibTagBG)
		r.tagBG[t].CornerRadius = 3
		r.tags[t] = canvas.NewText(calibTargetNames[t], calibTargetColors[t])
		r.tags[t].TextSize = 12
		r.objects = append(r.objects, r.tagBG[t], r.tags[t])
	}
	r.band = canvas.NewRectangle(transparentColor)
	r.band.StrokeWidth = 1.5
	r.objects = append(r.objects, r.band)
	r.update()
	return r
}

func (r *calibrationRenderer) Destroy()                     {}
func (r *calibrationRenderer) Objects() []fyne.CanvasObject { return r.objects }
func (r *calibrationRenderer) MinSize() fyne.Size           { return fyne.NewSize(320, 180) }

// update applies the window state that is not geometry.
func (r *calibrationRenderer) update() {
	c := r.v.c
	var img image.Image
	if c.snap != nil {
		img = c.snap.img
	}
	if r.img.Image != img {
		r.img.Image = img
		r.img.Refresh() // re-rasterise only when the frame changes
	}
	for t, box := range r.boxes {
		box.StrokeWidth = 2
		if calibTarget(t) == c.target {
			box.StrokeWidth = 3
		}
	}
	col := calibTargetColors[c.target]
	r.band.StrokeColor = col
	r.band.FillColor = color.NRGBA{R: col.R, G: col.G, B: col.B, A: 48}
}

func (r *calibrationRenderer) Refresh() {
	r.update()
	r.Layout(r.v.Size())
	canvas.Refresh(r.v) // the image is not re-rasterised by this
}

func (r *calibrationRenderer) Layout(size fyne.Size) {
	r.bg.Move(fyne.NewPos(0, 0))
	r.bg.Resize(size)
	hide := func() {
		for t := range r.boxes {
			r.boxes[t].Hide()
			r.tagBG[t].Hide()
			r.tags[t].Hide()
		}
		for t := range r.dots {
			for _, dot := range r.dots[t] {
				dot.Hide()
			}
		}
		r.band.Hide()
	}
	c := r.v.c
	if c.snap == nil {
		hide()
		r.img.Hide()
		r.empty.Show()
		text := r.empty.MinSize()
		r.empty.Move(fyne.NewPos(0, (size.Height-text.Height)/2))
		r.empty.Resize(fyne.NewSize(size.Width, text.Height))
		return
	}
	r.empty.Hide()
	b := c.snap.img.Bounds()
	fit := fitCalibrationImage(b.Dx(), b.Dy(), size)
	if !fit.ok() {
		hide()
		r.img.Hide()
		return
	}
	r.img.Show()
	at, area := fit.rect(config.NormalizedRect{Width: 1, Height: 1})
	r.img.Move(at)
	r.img.Resize(area)
	for t, rect := range c.boxes() {
		r.layoutBox(calibTarget(t), rect, fit, size)
	}
	if !r.v.dragging {
		r.band.Hide()
		return
	}
	x0, y0 := fit.pixel(r.v.from)
	x1, y1 := fit.pixel(r.v.to)
	band := config.NormalizedRect{X: math.Min(x0, x1) / float64(fit.w), Y: math.Min(y0, y1) / float64(fit.h),
		Width: math.Abs(x1-x0) / float64(fit.w), Height: math.Abs(y1-y0) / float64(fit.h)}
	pos, bandSize := fit.rect(band)
	r.band.Move(pos)
	r.band.Resize(bandSize)
	r.band.Show()
}

func (r *calibrationRenderer) layoutBox(t calibTarget, rect *config.NormalizedRect, fit calibrationFit, size fyne.Size) {
	box, tagBG, tag := r.boxes[t], r.tagBG[t], r.tags[t]
	if t.beads() {
		for _, dot := range r.dots[t] {
			dot.Hide()
		}
	}
	if rect == nil {
		box.Hide()
		tagBG.Hide()
		tag.Hide()
		return
	}
	pos, boxSize := fit.rect(*rect)
	box.Move(pos)
	box.Resize(boxSize)
	box.Show()
	if t.beads() {
		// Small enough to see the bead around it.
		d := fyne.Min(boxSize.Width/4, boxSize.Height) * 0.35
		d = fyne.Max(3.5, fyne.Min(d, 8))
		for i, centre := range calibrationBeadCenters(*rect, t.left()) {
			dot := r.dots[t][i]
			dot.Move(fit.point(centre.X, centre.Y).SubtractXY(d/2, d/2))
			dot.Resize(fyne.NewSize(d, d))
			dot.Show()
		}
	}
	// Bead names go above their box (avatars sit at the top edge), avatar
	// names below, so the two names of one side never overlap.
	text := tag.MinSize()
	label := text.AddWidthHeight(8, 2)
	top, bottom := float32(fit.y), float32(fit.y+float64(fit.h)*fit.scale)
	above, below := pos.Y-label.Height-2, pos.Y+boxSize.Height+2
	y := below
	if (t.beads() && above >= top) || below+label.Height > bottom {
		y = above
	}
	x := pos.X
	if !t.left() {
		x = pos.X + boxSize.Width - label.Width
	}
	x = fyne.Max(0, fyne.Min(x, size.Width-label.Width))
	y = fyne.Max(0, fyne.Min(y, size.Height-label.Height))
	tagBG.Move(fyne.NewPos(x, y))
	tagBG.Resize(label)
	tagBG.Show()
	tag.Move(fyne.NewPos(x+4, y+1))
	tag.Resize(text)
	tag.Show()
}
