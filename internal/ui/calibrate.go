package ui

import (
	"errors"
	"fmt"
	"image"
	"image/color"
	"math"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"narutotimer/internal/config"
	"narutotimer/internal/draw"
	"narutotimer/internal/frame"
)

// 区域校准（docs/区域校准.md）：玩家在一帧对局画面上框出每侧的豆和头像。
// 这里只有 Windows 的画框窗口；识别怎么用这些框由引擎决定（WithCalibrationApply）。
// 窗口先画出这一帧自动识别实际用的区域（引擎报告的每帧几何），没校准的框以
// 本帧的默认框（自动）为起点；只有玩家动过的框才会保存，没动过的一切照旧。
// 绘图与拖动见 calibrate_surface.go，方案文件见 calibrate_scheme.go。

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
	// The automatic analysis is thin, white and dashed over a dark shade (so it
	// shows on bright HUD parts too); editable boxes are solid and coloured.
	calibAutoColor = color.NRGBA{R: 240, G: 248, B: 252, A: 235}
	calibAutoShade = color.NRGBA{A: 110}
	calibAutoTagBG = color.NRGBA{R: 4, G: 14, B: 20, A: 190}
)

func (t calibTarget) beads() bool { return t == calibLeftBeads || t == calibRightBeads }
func (t calibTarget) left() bool  { return t == calibLeftBeads || t == calibLeftAvatar }

// side is the target's index in frame.Frame.Geometry (0 left, 1 right).
func (t calibTarget) side() int {
	if t.left() {
		return 0
	}
	return 1
}

// Status line texts.
const (
	calibrationHint = "框里拖动可移动，拖边或角调大小，空白处拖动重画；点一下框后用方向键微调 1 像素（按住 Shift 调大小）。豆框紧贴 4 颗普通豆（柱间第 5、6 颗不框），小点落在每颗豆中间；头像框只框头像，不含名字和血条。九喇嘛水门、侠隐佐助、百战鼬的豆偏低，请在普通忍者身上框豆。"
	// calibrationAutoHint explains the overlay of a frame the engine analysed.
	calibrationAutoHint      = "白色虚线是这一帧自动识别实际用的区域（十字是豆心）；带「自动」的框是默认方案，不改就保持自动。"
	calibrationNoGeometry    = "这一帧没有自动识别区域（大厅、结算等画面，或不支持的分辨率），只能直接画框。"
	calibrationStaleGeometry = "这一帧是保存前分析的，按新设置识别的区域要等下一帧。"
)

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

// sameCalibrationRect: equal up to float noise (1e-9 of the frame is far
// below a pixel), as after moving a box away and back by whole pixels.
func sameCalibrationRect(a, b config.NormalizedRect) bool {
	return math.Abs(a.X-b.X) <= 1e-9 && math.Abs(a.Y-b.Y) <= 1e-9 && math.Abs(a.Width-b.Width) <= 1e-9 && math.Abs(a.Height-b.Height) <= 1e-9
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

// calibBoxState is where a target's editable box comes from.
type calibBoxState int

const (
	calibBoxNone    calibBoxState = iota // none: automatic without a frame analysis, or cleared
	calibBoxAuto                         // the frame's default box: stays automatic, never saved as is
	calibBoxSaved                        // the saved calibration
	calibBoxPending                      // drawn, moved, resized or imported since the last save
)

var calibBoxSuffix = [...]string{"", "（自动）", "（已保存）", "（未保存）"}

type calibShown struct {
	rect  config.NormalizedRect
	state calibBoxState
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
	// geometry is what the engine used on this frame (pixels in img), zero
	// when it did not analyse the HUD.
	geometry [2]frame.SideGeometry
	gen      uint64 // calibrationFeed.gen when the frame's capture began
}

func newCalibrationSnapshot(f frame.Frame) *calibrationSnapshot {
	snap := &calibrationSnapshot{img: copyFrameImage(f.Img)}
	// Only a recognised match frame proves its layout; on other pages the
	// engine's profile is a guess.
	if f.Fighting && (f.LayoutProfile == "camp" || f.LayoutProfile == "duel") {
		snap.profile = f.LayoutProfile
	}
	// Pixels follow the copy to (0,0); the boxes are fractions of the bounds.
	origin := f.Img.Bounds().Min
	for i, g := range f.Geometry {
		g.Beads = slices.Clone(g.Beads)
		for j := range g.Beads {
			g.Beads[j] = g.Beads[j].Sub(origin)
		}
		if !g.NameROI.Empty() {
			g.NameROI = g.NameROI.Sub(origin)
		}
		if !g.AvatarROI.Empty() {
			g.AvatarROI = g.AvatarROI.Sub(origin)
		}
		snap.geometry[i] = g
	}
	return snap
}

func (s *calibrationSnapshot) hasGeometry() bool {
	for _, g := range s.geometry {
		if len(g.Beads) > 0 || !g.NameROI.Empty() || !g.AvatarROI.Empty() || g.BeadBox.Width > 0 || g.AvatarBox.Width > 0 {
			return true
		}
	}
	return false
}

// defaultBox is the frame's default box for t (docs/区域校准.md 每帧几何):
// saved unchanged it would reproduce this frame's analysis.
func (s *calibrationSnapshot) defaultBox(t calibTarget) (config.NormalizedRect, bool) {
	g := s.geometry[t.side()]
	r := g.AvatarBox
	if t.beads() {
		r = g.BeadBox
	}
	return r, r.Width > 0 && r.Height > 0
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
	open atomic.Bool
	// gen counts calibrations handed to the engine (setCalibration). A frame
	// whose capture began at generation g was analysed with calibration g or
	// a later one.
	gen       atomic.Uint64
	mu        sync.Mutex
	latest    *calibrationSnapshot
	copied    time.Time
	notify    func() // called once, after the first copy of generation ≥ notifyGen
	notifyGen uint64
}

func (c *calibrationFeed) start(notify func()) {
	c.mu.Lock()
	c.latest, c.copied, c.notify, c.notifyGen = nil, time.Time{}, notify, 0
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

// await replaces the pending notification: notify is called once, after
// the first copy of a frame captured at generation gen or later.
func (c *calibrationFeed) await(gen uint64, notify func()) {
	c.mu.Lock()
	c.notify, c.notifyGen = notify, gen
	c.mu.Unlock()
}

func (c *calibrationFeed) offer(f frame.Frame, now time.Time) { c.offerAt(f, now, c.gen.Load()) }

// offerAt copies f, captured at generation gen, when a copy is due: the
// first one, one per calibrationCopyInterval and the first of a newer
// generation (the regions after 保存).
func (c *calibrationFeed) offerAt(f frame.Frame, now time.Time, gen uint64) {
	if !c.open.Load() || f.Err != nil || f.Img == nil || f.Img.Bounds().Empty() {
		return
	}
	c.mu.Lock()
	due := c.latest == nil || gen > c.latest.gen || now.Sub(c.copied) >= calibrationCopyInterval || now.Before(c.copied)
	c.mu.Unlock()
	if !due {
		return
	}
	snap := newCalibrationSnapshot(f) // the copy runs outside the lock
	snap.gen = gen
	c.mu.Lock()
	if !c.open.Load() || (c.latest != nil && gen < c.latest.gen) {
		c.mu.Unlock()
		return
	}
	c.latest, c.copied = snap, now
	var notify func()
	if c.notify != nil && gen >= c.notifyGen {
		notify, c.notify = c.notify, nil
	}
	c.mu.Unlock()
	if notify != nil {
		notify()
	}
}

// offerCalibrationFrame runs on the capture goroutine right after the
// provider returns, while the frame's pixels are still valid. gen is the
// calibration generation read before the capture began.
func (s *session) offerCalibrationFrame(f frame.Frame, gen uint64) {
	if !s.calibrationFeed.open.Load() {
		return
	}
	s.mu.Lock()
	current := f.SourceRevision == s.sourceRevision && !s.stopped()
	s.mu.Unlock()
	if current {
		s.calibrationFeed.offerAt(f, time.Now(), gen)
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
	// Frames whose capture begins from now on show the new regions.
	s.calibrationFeed.gen.Add(1)
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
	touched [calibTargetCount]bool // drawn, adjusted, imported or cleared since the last save
	target  calibTarget
	done    bool
	// savedText is the last 保存/恢复自动 message, kept when the frame
	// analysed with the new calibration replaces the shown one.
	savedText string

	surface *calibrationSurface
	targets *widget.RadioGroup
	// layout lets the player name the layout of a frame the engine did not
	// recognise as a match, which is what an unsupported resolution looks like.
	layout   *widget.RadioGroup
	showAuto *widget.Check
	info     *widget.Label
	status   *widget.Label
	confirm  *dialog.ConfirmDialog // the open confirmation, if any
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
	c.showAuto = widget.NewCheck("显示自动识别区域", nil)
	c.showAuto.SetChecked(true)
	c.showAuto.OnChanged = func(bool) { c.surface.Refresh() }
	c.info = widget.NewLabel("等待画面…")
	c.status = widget.NewLabel(calibrationHint)
	// Chinese has no spaces to wrap at: wrap anywhere, full lines.
	c.status.Wrapping = fyne.TextWrapBreak

	refresh := widget.NewButtonWithIcon("刷新画面", theme.ViewRefreshIcon(), c.refreshFrame)
	clearSelected := widget.NewButtonWithIcon("清除所选", theme.ContentClearIcon(), c.clearSelected)
	restore := widget.NewButtonWithIcon("全部恢复自动", theme.HistoryIcon(), c.confirmRestoreAuto)
	importScheme := widget.NewButtonWithIcon("导入方案", theme.DownloadIcon(), c.importScheme)
	exportScheme := widget.NewButtonWithIcon("导出方案", theme.UploadIcon(), c.exportScheme)
	save := widget.NewButtonWithIcon("保存", theme.DocumentSaveIcon(), c.confirmSave)
	save.Importance = widget.HighImportance
	closeButton := widget.NewButtonWithIcon("关闭", theme.CancelIcon(), c.requestClose)

	top := container.NewVBox(container.NewBorder(nil, nil, widget.NewLabel("框选"), container.NewHBox(c.showAuto, c.info), c.targets), c.layout)
	bottom := container.NewVBox(c.status,
		container.NewHBox(refresh, clearSelected, restore, importScheme, exportScheme, layout.NewSpacer(), save, closeButton))
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
		c.awaitFresh() // a save while it was being captured
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

// fresh reports whether the shown frame was analysed with the calibration
// the engine has now. After 保存 the old frame's regions are history: its
// automatic regions and default boxes are hidden until the next frame.
func (c *calibrationWindow) fresh() bool {
	return c.snap != nil && c.snap.gen == c.s.calibrationFeed.gen.Load()
}

// autoShown reports whether the frame's automatic regions are drawn.
func (c *calibrationWindow) autoShown() bool {
	return c.showAuto.Checked && c.fresh() && c.snap.hasGeometry()
}

// awaitFresh shows the first frame analysed with the calibration just
// applied, so its regions follow what was saved. The shown frame stays if
// the player started editing again or the new frame has no regions.
func (c *calibrationWindow) awaitFresh() {
	if c.snap == nil || c.fresh() {
		return
	}
	c.s.calibrationFeed.await(c.s.calibrationFeed.gen.Load(), func() { fyne.Do(c.freshArrived) })
}

func (c *calibrationWindow) freshArrived() {
	snap := c.s.calibrationFeed.snapshot()
	if c.done || snap == nil || snap == c.snap || snap.gen != c.s.calibrationFeed.gen.Load() || c.dirty() {
		return
	}
	if c.snap != nil && c.snap.hasGeometry() && !snap.hasGeometry() {
		c.setStatus(c.savedText + "新画面没有自动识别区域：回到对局后点「刷新画面」查看按新设置识别的区域。")
		return
	}
	c.showSnapshot(snap)
	if c.savedText != "" && snap.hasGeometry() {
		c.setStatus(c.savedText + "画面已刷新，虚线是按新设置识别的区域。")
	}
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
	c.setStatus(c.frameText() + c.pendingText())
}

// frameText describes the shown frame: its layout, whether its automatic
// regions are shown and how to draw.
func (c *calibrationWindow) frameText() string {
	regions := calibrationAutoHint
	switch {
	case !c.snap.hasGeometry():
		regions = calibrationNoGeometry
	case !c.fresh():
		regions = calibrationStaleGeometry
	}
	switch {
	case c.profile() == "":
		return "没认出这是对局画面（分辨率特殊时常见）。如果这是对局或训练场，请在上方选择训练场/决斗场再框选；否则进入对局后点「刷新画面」。" + regions
	case c.saved != nil && c.saved.Profile != c.profile():
		return fmt.Sprintf("已保存的框画在%s画面上，这里是%s，位置会略有偏差；在本画面上保存时，未重画的框恢复自动。",
			calibProfileName(c.saved.Profile), calibProfileName(c.profile())) + regions
	}
	return regions + calibrationHint
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

// boxes is what 保存 would store (before profile checks): drawn, adjusted
// or cleared boxes over the saved ones. Automatic default boxes are not in it.
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

// shown is each target's editable box: the pending edit, else the saved box,
// else the shown frame's default box (automatic). Cleared boxes show nothing:
// what automatic uses there is known only from a frame analysed after 保存.
func (c *calibrationWindow) shown() [calibTargetCount]calibShown {
	var out [calibTargetCount]calibShown
	saved := calibrationRects(c.saved)
	for t := range out {
		switch {
		case c.touched[t]:
			if c.drawn[t] != nil {
				out[t] = calibShown{c.drawn[t].rect, calibBoxPending}
			}
		case saved[t] != nil:
			out[t] = calibShown{*saved[t], calibBoxSaved}
		default:
			if r, ok := c.defaultBox(calibTarget(t)); ok {
				out[t] = calibShown{r, calibBoxAuto}
			}
		}
	}
	return out
}

// defaultBox is the shown frame's default box for t while its analysis is
// current.
func (c *calibrationWindow) defaultBox(t calibTarget) (config.NormalizedRect, bool) {
	if !c.fresh() {
		return config.NormalizedRect{}, false
	}
	return c.snap.defaultBox(t)
}

// baseline is what t shows without an edit: the saved box, else the default.
func (c *calibrationWindow) baseline(t calibTarget) (config.NormalizedRect, bool) {
	if r := calibrationRects(c.saved)[t]; r != nil {
		return *r, true
	}
	return c.defaultBox(t)
}

// boxProfile is the layout of t's current box ("" for an automatic one).
func (c *calibrationWindow) boxProfile(t calibTarget) string {
	if c.touched[t] && c.drawn[t] != nil {
		return c.drawn[t].profile
	}
	if c.saved != nil && calibrationRects(c.saved)[t] != nil {
		return c.saved.Profile
	}
	return ""
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

// setEdited makes r the pending box of t. Back where it started (the saved
// or the automatic box) it is no edit at all, so 保存 leaves it alone.
func (c *calibrationWindow) setEdited(t calibTarget, r config.NormalizedRect) {
	if base, ok := c.baseline(t); ok && sameCalibrationRect(base, r) {
		c.drawn[t], c.touched[t] = nil, false
		return
	}
	// The frame's layout; on an unrecognised frame a box keeps its own.
	profile := c.profile()
	if profile == "" {
		profile = c.boxProfile(t)
	}
	c.drawn[t], c.touched[t] = &calibBox{rect: r, profile: profile}, true
}

// finishAdjust stores a moved or resized box of t.
func (c *calibrationWindow) finishAdjust(t calibTarget, r config.NormalizedRect, grip calibGrip) {
	if c.snap == nil {
		return
	}
	c.setEdited(t, r)
	c.surface.Refresh()
	name := calibTargetNames[t]
	if !c.touched[t] {
		c.setStatus(name + "回到了原来的位置，不算改动。" + c.pendingText())
		return
	}
	text := name + "已移动。"
	if grip != gripMove {
		text = name + "已调整大小。"
	}
	if t.beads() {
		text += "4 个小点应落在每颗豆中间。"
	}
	if c.drawn[t].profile == "" {
		text += "先在上方选择训练场或决斗场，否则不能保存。"
	}
	c.setStatus(text + c.pendingText())
}

// nudge moves the selected box by whole frame pixels, or with resize moves
// its right/bottom edge (arrow keys, Shift+arrow keys).
func (c *calibrationWindow) nudge(dx, dy float64, resize bool) {
	if c.snap == nil || c.surface.dragging {
		return
	}
	t := c.target
	box := c.shown()[t]
	if box.state == calibBoxNone {
		c.setStatus(calibTargetNames[t] + "还没有框：先在画面上拖出方框。" + c.pendingText())
		return
	}
	grip := gripMove
	if resize {
		grip = 0
		if dx != 0 {
			grip |= gripRight
		}
		if dy != 0 {
			grip |= gripBottom
		}
	}
	b := c.snap.img.Bounds()
	c.finishAdjust(t, adjustCalibrationBox(box.rect, grip, dx, dy, b.Dx(), b.Dy()), grip)
}

// clearSelected returns the selected target to automatic: at once for a box
// that was never saved, on 保存 for a saved one.
func (c *calibrationWindow) clearSelected() {
	t := c.target
	name := calibTargetNames[t]
	saved := calibrationRects(c.saved)[t] != nil
	switch {
	case c.touched[t] && c.drawn[t] == nil:
		c.setStatus(name + "已清除，保存后恢复自动识别。" + c.pendingText())
		return
	case !saved && !c.touched[t]:
		c.setStatus(name + "没有校准，已是自动识别。" + c.pendingText())
		return
	case !saved:
		c.drawn[t], c.touched[t] = nil, false
		c.surface.Refresh()
		c.setStatus(name + "已恢复自动识别。" + c.pendingText())
		return
	}
	c.drawn[t], c.touched[t] = nil, true
	c.surface.Refresh()
	c.setStatus(name + "已清除，保存后恢复自动识别。" + c.pendingText())
}

// confirmSave saves the changed boxes. Without changes nothing is written and
// the engine hook is not called: untouched default boxes stay automatic.
func (c *calibrationWindow) confirmSave() {
	if !c.dirty() {
		c.setStatus("没有改动：带「自动」的框不改就保持自动识别，不用保存。")
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

// save merges the changed and cleared boxes into the saved calibration,
// applies it and writes config.json. It returns the error shown to the user.
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
	if next == nil {
		c.savedText = "已保存：豆和头像全部自动识别" + c.live() + "。"
	} else {
		c.savedText = "已保存" + c.live() + "（" + calibProfileName(next.Profile) + "画面上画的框）。"
	}
	c.afterSave()
	showSettingsError(err, c.win)
	return err
}

// afterSave shows the saved state and waits for a frame analysed with it.
func (c *calibrationWindow) afterSave() {
	c.surface.Refresh()
	text := c.savedText
	if c.snap != nil && !c.fresh() {
		text += "正在等新画面显示按新设置识别的区域…"
	}
	c.setStatus(text)
	c.awaitFresh()
}

// live says so only when an engine hook applies the change at once.
func (c *calibrationWindow) live() string {
	if c.s.calibrationApply != nil {
		return "，立即生效"
	}
	return ""
}

// calibrationErrorText words a save (or export) error for the player.
func calibrationErrorText(err error) string {
	if errors.Is(err, errCalibrationProfile) {
		return "当前画面不是对局或训练场，无法确定布局。" + errCalibrationProfile.Error() + "：进入后点「刷新画面」，再重画这些框。"
	}
	return err.Error()
}

func (c *calibrationWindow) showSaveError(err error) {
	text := calibrationErrorText(err)
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
	c.savedText = "已全部恢复自动识别" + c.live() + "。"
	c.afterSave()
	showSettingsError(err, c.win)
	return err
}
