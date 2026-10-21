package ui

import (
	"image"
	"image/color"
	"math"
	"sort"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/widget"

	"narutotimer/internal/config"
)

// 校准窗口的画面：本帧缩放显示，上面是自动识别实际用的区域（白色虚线、十字）
// 和四个可编辑的框；拖动画框、移动、调大小，方向键微调。

// ---- geometry ----

const (
	calibMinBoxUnits  = 4 // a smaller drag on screen is a slip, not a box
	calibMinBoxPixels = 3 // nor can a thinner box hold a bead or an avatar
	// calibGripReach (surface units) around an edge of the selected box grabs
	// that edge.
	calibGripReach = 6
	// A box side shorter than calibGripSpan on screen (a bead row in a small
	// window) has no handles across it: pressing near it moves the box.
	calibGripSpan = 18
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

// pixelPoint maps frame pixel coordinates to a surface position.
func (f calibrationFit) pixelPoint(px, py float64) fyne.Position {
	return fyne.NewPos(float32(f.x+px*f.scale), float32(f.y+py*f.scale))
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
	return insideFrame(config.NormalizedRect{X: round(x), Y: round(y), Width: round(w), Height: round(h)})
}

// insideFrame trims float overshoot past the frame's right or bottom edge.
func insideFrame(r config.NormalizedRect) config.NormalizedRect {
	for r.X+r.Width > 1 {
		r.Width = math.Nextafter(r.Width, 0)
	}
	for r.Y+r.Height > 1 {
		r.Height = math.Nextafter(r.Height, 0)
	}
	return r
}

// ---- moving and resizing ----

// calibGrip is the set of box edges a drag moves; all four move the box.
type calibGrip uint8

const (
	gripLeft calibGrip = 1 << iota
	gripRight
	gripTop
	gripBottom
	gripMove = gripLeft | gripRight | gripTop | gripBottom
)

// adjustCalibrationBox moves the grip's edges of r by whole frame pixels
// (dx, dy) of a w×h frame. Edges that do not move keep their exact value, so
// a default box keeps its sub-pixel placement (every bead centre moves by
// exactly the drag). The result lies inside the frame and keeps at least
// calibMinBoxPixels; a moved box keeps its size.
func adjustCalibrationBox(r config.NormalizedRect, grip calibGrip, dx, dy float64, w, h int) config.NormalizedRect {
	W, H := float64(w), float64(h)
	fdx, fdy := dx/W, dy/H
	x0, x1, y0, y1 := r.X, r.X+r.Width, r.Y, r.Y+r.Height
	if grip == gripMove {
		fdx = math.Max(math.Min(fdx, 1-x1), -x0)
		fdy = math.Max(math.Min(fdy, 1-y1), -y0)
		return insideFrame(config.NormalizedRect{X: math.Max(0, x0+fdx), Y: math.Max(0, y0+fdy), Width: r.Width, Height: r.Height})
	}
	minW, minH := calibMinBoxPixels/W, calibMinBoxPixels/H
	if grip&gripLeft != 0 {
		x0 = math.Min(x0+fdx, x1-minW)
	}
	if grip&gripRight != 0 {
		x1 = math.Max(x1+fdx, x0+minW)
	}
	if grip&gripTop != 0 {
		y0 = math.Min(y0+fdy, y1-minH)
	}
	if grip&gripBottom != 0 {
		y1 = math.Max(y1+fdy, y0+minH)
	}
	x0, y0, x1, y1 = math.Max(x0, 0), math.Max(y0, 0), math.Min(x1, 1), math.Min(y1, 1)
	return insideFrame(config.NormalizedRect{X: x0, Y: y0, Width: x1 - x0, Height: y1 - y0})
}

// calibHit is what a press at p grabs of a box shown at (at, size): with
// edges, an edge or corner within calibGripReach; else the inside (moving).
// 0 = not this box.
func calibHit(p, at fyne.Position, size fyne.Size, edges bool) calibGrip {
	x0, y0, x1, y1 := at.X, at.Y, at.X+size.Width, at.Y+size.Height
	const reach = calibGripReach
	thinX, thinY := size.Width < calibGripSpan, size.Height < calibGripSpan
	var g calibGrip
	if edges {
		if !thinX && p.Y >= y0-reach && p.Y <= y1+reach {
			inner := fyne.Min(reach, size.Width/4)
			if p.X >= x0-reach && p.X <= x0+inner {
				g |= gripLeft
			} else if p.X >= x1-inner && p.X <= x1+reach {
				g |= gripRight
			}
		}
		if !thinY && p.X >= x0-reach && p.X <= x1+reach {
			inner := fyne.Min(reach, size.Height/4)
			if p.Y >= y0-reach && p.Y <= y0+inner {
				g |= gripTop
			} else if p.Y >= y1-inner && p.Y <= y1+reach {
				g |= gripBottom
			}
		}
		if g != 0 {
			return g
		}
	}
	var mx, my float32
	if thinX {
		mx = reach
	}
	if thinY {
		my = reach
	}
	if p.X >= x0-mx && p.X <= x1+mx && p.Y >= y0-my && p.Y <= y1+my {
		return gripMove
	}
	return 0
}

// calibGrab is what a press grabbed: an edge set of a target's box, or
// nothing (grip 0: the drag draws a new box for the selected target).
type calibGrab struct {
	target calibTarget
	grip   calibGrip
	rect   config.NormalizedRect // the box when it was grabbed
}

// hitAt is what a press at p (surface units) grabs: an edge or the inside of
// the selected box, else the inside of another box (smallest first), else
// nothing, which draws a new box.
func (c *calibrationWindow) hitAt(p fyne.Position) calibGrab {
	none := calibGrab{target: c.target}
	fit := c.surface.fit()
	if !fit.ok() {
		return none
	}
	shown := c.shown()
	if box := shown[c.target]; box.state != calibBoxNone {
		at, size := fit.rect(box.rect)
		if g := calibHit(p, at, size, true); g != 0 {
			return calibGrab{c.target, g, box.rect}
		}
	}
	var others []calibTarget
	for t, box := range shown {
		if calibTarget(t) != c.target && box.state != calibBoxNone {
			others = append(others, calibTarget(t))
		}
	}
	sort.SliceStable(others, func(i, j int) bool {
		a, b := shown[others[i]].rect, shown[others[j]].rect
		return a.Width*a.Height < b.Width*b.Height
	})
	for _, t := range others {
		at, size := fit.rect(shown[t].rect)
		if calibHit(p, at, size, false) == gripMove {
			return calibGrab{t, gripMove, shown[t].rect}
		}
	}
	return none
}

// ---- image surface ----

// calibrationSurface shows the snapshot scaled to fit with the automatic
// regions and every box. A drag on the selected box moves it (inside) or
// resizes it (edges, corners); a press on another box selects it; a drag
// elsewhere draws a new box for the selected target. Arrow keys nudge.
type calibrationSurface struct {
	widget.BaseWidget
	c        *calibrationWindow
	dragging bool
	from, to fyne.Position // surface coordinates
	grab     calibGrab
	grabbed  bool                   // grab is from MouseDown, not yet used by a drag
	live     *config.NormalizedRect // the grabbed box while it is dragged
	hover    calibGrip              // what the pointer would grab (cursor)
	shift    bool                   // Shift is held: arrow keys resize
}

var (
	_ desktop.Mouseable   = (*calibrationSurface)(nil)
	_ desktop.Hoverable   = (*calibrationSurface)(nil)
	_ desktop.Cursorable  = (*calibrationSurface)(nil)
	_ desktop.Keyable     = (*calibrationSurface)(nil)
	_ fyne.Draggable      = (*calibrationSurface)(nil)
	_ fyne.Focusable      = (*calibrationSurface)(nil)
	_ fyne.WidgetRenderer = (*calibrationRenderer)(nil)
)

func newCalibrationSurface(c *calibrationWindow) *calibrationSurface {
	v := &calibrationSurface{c: c}
	v.ExtendBaseWidget(v)
	return v
}

func (v *calibrationSurface) CreateRenderer() fyne.WidgetRenderer { return newCalibrationRenderer(v) }

// Cursor shows what a press would do: resize arrows on an edge, a hand on a
// box (move or select), a crosshair to draw (also on corners).
func (v *calibrationSurface) Cursor() desktop.Cursor {
	g := v.hover
	if v.dragging {
		g = v.grab.grip
	}
	switch g {
	case gripMove:
		return desktop.PointerCursor
	case gripLeft, gripRight:
		return desktop.HResizeCursor
	case gripTop, gripBottom:
		return desktop.VResizeCursor
	}
	return desktop.CrosshairCursor
}

// fit is where the snapshot is drawn at the surface's current size.
func (v *calibrationSurface) fit() calibrationFit {
	if v.c.snap == nil {
		return calibrationFit{}
	}
	b := v.c.snap.img.Bounds()
	return fitCalibrationImage(b.Dx(), b.Dy(), v.Size())
}

// MouseDown starts over even if the previous drag never got its DragEnd
// (button released outside the window). The press decides what a drag does
// and selects a box it lands on; the surface takes the keyboard focus.
func (v *calibrationSurface) MouseDown(ev *desktop.MouseEvent) {
	v.dragging, v.live, v.grabbed = false, nil, false
	if v.c.snap == nil {
		return
	}
	v.c.win.Canvas().Focus(v)
	if ev.Button == desktop.MouseButtonSecondary || ev.Button == desktop.MouseButtonTertiary {
		return
	}
	v.press(ev.Position)
	v.grabbed = true
}

func (v *calibrationSurface) MouseUp(*desktop.MouseEvent) {}

func (v *calibrationSurface) press(p fyne.Position) {
	v.grab = v.c.hitAt(p)
	if v.grab.target != v.c.target {
		v.c.targets.SetSelected(calibTargetNames[v.grab.target])
	}
}

func (v *calibrationSurface) MouseIn(ev *desktop.MouseEvent) { v.MouseMoved(ev) }
func (v *calibrationSurface) MouseMoved(ev *desktop.MouseEvent) {
	v.hover = v.c.hitAt(ev.Position).grip
}
func (v *calibrationSurface) MouseOut() { v.hover = 0 }

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
		if !v.grabbed {
			v.press(v.from)
		}
		v.grabbed = false
	}
	v.to = ev.Position
	v.live = nil
	if fit := v.fit(); v.grab.grip != 0 && fit.ok() {
		// Whole frame pixels: a box moved away and back is unchanged.
		dx := math.Round(float64(v.to.X-v.from.X) / fit.scale)
		dy := math.Round(float64(v.to.Y-v.from.Y) / fit.scale)
		r := adjustCalibrationBox(v.grab.rect, v.grab.grip, dx, dy, fit.w, fit.h)
		v.live = &r
	}
	v.Refresh()
}

func (v *calibrationSurface) DragEnd() {
	if !v.dragging {
		return
	}
	v.dragging = false
	grab, live := v.grab, v.live
	v.live = nil
	switch {
	case grab.grip == 0:
		v.c.finishBox(v.from, v.to)
	case live != nil:
		v.c.finishAdjust(grab.target, *live, grab.grip)
	default:
		v.Refresh()
	}
}

func (v *calibrationSurface) FocusGained()   {}
func (v *calibrationSurface) FocusLost()     { v.shift = false }
func (v *calibrationSurface) TypedRune(rune) {}

// TypedKey nudges the selected box one frame pixel; with Shift its right or
// bottom edge.
func (v *calibrationSurface) TypedKey(ev *fyne.KeyEvent) {
	var dx, dy float64
	switch ev.Name {
	case fyne.KeyLeft:
		dx = -1
	case fyne.KeyRight:
		dx = 1
	case fyne.KeyUp:
		dy = -1
	case fyne.KeyDown:
		dy = 1
	default:
		return
	}
	v.c.nudge(dx, dy, v.shift)
}

func (v *calibrationSurface) KeyDown(ev *fyne.KeyEvent) {
	if ev.Name == desktop.KeyShiftLeft || ev.Name == desktop.KeyShiftRight {
		v.shift = true
	}
}

func (v *calibrationSurface) KeyUp(ev *fyne.KeyEvent) {
	if ev.Name == desktop.KeyShiftLeft || ev.Name == desktop.KeyShiftRight {
		v.shift = false
	}
}

// ---- name tags ----

// calibTag is a name on a dark plate.
type calibTag struct {
	bg   *canvas.Rectangle
	text *canvas.Text
}

func newCalibTag(text string, fg, bg color.Color, size float32) *calibTag {
	t := &calibTag{bg: canvas.NewRectangle(bg), text: canvas.NewText(text, fg)}
	t.bg.CornerRadius = 3
	t.text.TextSize = size
	return t
}

func (t *calibTag) size() fyne.Size { return t.text.MinSize().AddWidthHeight(8, 2) }

func (t *calibTag) set(text string, bold bool) {
	if t.text.Text != text || t.text.TextStyle.Bold != bold {
		t.text.Text, t.text.TextStyle.Bold = text, bold
		t.text.Refresh()
	}
}

func (t *calibTag) show(at fyne.Position) {
	t.bg.Move(at)
	t.bg.Resize(t.size())
	t.bg.Show()
	t.text.Move(at.AddXY(4, 1))
	t.text.Resize(t.text.MinSize())
	t.text.Show()
}

func (t *calibTag) hide() {
	t.bg.Hide()
	t.text.Hide()
}

// calibArea is an axis-aligned area in surface units.
type calibArea struct{ x0, y0, x1, y1 float32 }

func calibAreaOf(at fyne.Position, size fyne.Size) calibArea {
	return calibArea{at.X, at.Y, at.X + size.Width, at.Y + size.Height}
}

func (a calibArea) overlap(b calibArea) float32 {
	w := fyne.Min(a.x1, b.x1) - fyne.Max(a.x0, b.x0)
	h := fyne.Min(a.y1, b.y1) - fyne.Max(a.y0, b.y0)
	if w <= 0 || h <= 0 {
		return 0
	}
	return w * h
}

// calibTagPlacer keeps name tags off each other, off box outlines and off
// the bead crosses: each tag takes its first candidate that is clear, else
// the one overlapping least.
type calibTagPlacer struct {
	bounds calibArea
	taken  []calibArea
}

func (p *calibTagPlacer) avoidOutline(at fyne.Position, size fyne.Size) {
	const m = 2
	x0, y0, x1, y1 := at.X, at.Y, at.X+size.Width, at.Y+size.Height
	p.taken = append(p.taken, calibArea{x0 - m, y0 - m, x1 + m, y0 + m}, calibArea{x0 - m, y1 - m, x1 + m, y1 + m},
		calibArea{x0 - m, y0 - m, x0 + m, y1 + m}, calibArea{x1 - m, y0 - m, x1 + m, y1 + m})
}

func (p *calibTagPlacer) place(size fyne.Size, candidates []fyne.Position) fyne.Position {
	best, least := fyne.Position{}, float32(math.Inf(1))
	for _, c := range candidates {
		c.X = fyne.Max(p.bounds.x0, fyne.Min(c.X, p.bounds.x1-size.Width))
		c.Y = fyne.Max(p.bounds.y0, fyne.Min(c.Y, p.bounds.y1-size.Height))
		a := calibAreaOf(c, size)
		var overlap float32
		for _, t := range p.taken {
			overlap += a.overlap(t)
		}
		if overlap < least {
			best, least = c, overlap
		}
		if overlap == 0 {
			break
		}
	}
	p.taken = append(p.taken, calibAreaOf(best, size))
	return best
}

// calibSlide is n+1 positions from start in steps of step.
func calibSlide(start fyne.Position, step fyne.Delta, n int) []fyne.Position {
	out := make([]fyne.Position, 0, n+1)
	for i := 0; i <= n; i++ {
		out = append(out, start.AddXY(float32(i)*step.DX, float32(i)*step.DY))
	}
	return out
}

// ---- renderer ----

// calibAutoNames label a side's name search region and avatar window.
var calibAutoNames = [2]string{"名字", "头像窗口"}

const (
	calibDash          = 5 // dash and gap of the automatic regions, surface units
	calibDashGap       = 3
	calibDashesPerSide = 60 // longer sides get longer dashes
)

type calibrationRenderer struct {
	v     *calibrationSurface
	bg    *canvas.Rectangle
	img   *canvas.Image
	empty *canvas.Text
	// The frame's automatic analysis, per side: name region and avatar
	// window as dashes over a dark shade, a cross per sampled bead centre
	// (shade and light lines), and their tags.
	autoShade [2][2]*canvas.Rectangle
	autoDash  [2][2][]*canvas.Line
	crosses   [2][]*canvas.Line
	autoTags  [2][2]*calibTag
	// The editable boxes.
	boxes   [calibTargetCount]*canvas.Rectangle
	dots    [2][4]*canvas.Circle // implied bead centres of the two bead boxes
	tags    [calibTargetCount]*calibTag
	band    *canvas.Rectangle // the box being drawn
	objects []fyne.CanvasObject
}

func newCalibrationRenderer(v *calibrationSurface) *calibrationRenderer {
	r := &calibrationRenderer{v: v, bg: canvas.NewRectangle(calibSurfaceBG),
		img: &canvas.Image{FillMode: canvas.ImageFillStretch, ScaleMode: canvas.ImageScaleSmooth}}
	r.bg.StrokeColor, r.bg.StrokeWidth = panelBorder, 1
	r.empty = canvas.NewText("等待画面…", tagIdle)
	r.empty.Alignment = fyne.TextAlignCenter
	for side := range r.autoShade {
		for k := range r.autoShade[side] {
			shade := canvas.NewRectangle(transparentColor)
			shade.StrokeColor, shade.StrokeWidth = calibAutoShade, 3
			r.autoShade[side][k] = shade
			r.autoTags[side][k] = newCalibTag(calibAutoNames[k], calibAutoColor, calibAutoTagBG, 11)
		}
	}
	for t := range r.boxes {
		r.boxes[t] = canvas.NewRectangle(transparentColor)
		r.boxes[t].StrokeColor = calibTargetColors[t]
		r.tags[t] = newCalibTag(calibTargetNames[t], calibTargetColors[t], calibTagBG, 12)
	}
	for t := range r.dots {
		for i := range r.dots[t] {
			dot := canvas.NewCircle(calibDotFill)
			dot.StrokeColor, dot.StrokeWidth = calibDotOutline, 1
			r.dots[t][i] = dot
		}
	}
	r.band = canvas.NewRectangle(transparentColor)
	r.band.StrokeWidth = 1.5
	r.rebuild()
	r.update()
	return r
}

// rebuild lists the objects bottom to top: frame, automatic analysis, boxes
// and dots, then all tags (a neighbouring outline never crosses a name).
func (r *calibrationRenderer) rebuild() {
	objects := []fyne.CanvasObject{r.bg, r.img}
	for side := range r.autoShade {
		for k := range r.autoShade[side] {
			objects = append(objects, r.autoShade[side][k])
			for _, l := range r.autoDash[side][k] {
				objects = append(objects, l)
			}
		}
		for _, l := range r.crosses[side] {
			objects = append(objects, l)
		}
	}
	for _, box := range r.boxes {
		objects = append(objects, box)
	}
	for t := range r.dots {
		for _, dot := range r.dots[t] {
			objects = append(objects, dot)
		}
	}
	for side := range r.autoTags {
		for _, tag := range r.autoTags[side] {
			objects = append(objects, tag.bg, tag.text)
		}
	}
	for _, tag := range r.tags {
		objects = append(objects, tag.bg, tag.text)
	}
	r.objects = append(objects, r.band, r.empty)
}

// lines returns n lines of pool, growing it; lines past n are hidden.
func (r *calibrationRenderer) lines(pool *[]*canvas.Line, n int, newLine func(i int) *canvas.Line) []*canvas.Line {
	grew := false
	for len(*pool) < n {
		*pool = append(*pool, newLine(len(*pool)))
		grew = true
	}
	for _, l := range (*pool)[n:] {
		l.Hide()
	}
	if grew {
		r.rebuild()
	}
	return (*pool)[:n]
}

func (r *calibrationRenderer) Destroy()                     {}
func (r *calibrationRenderer) Objects() []fyne.CanvasObject { return r.objects }
func (r *calibrationRenderer) MinSize() fyne.Size           { return fyne.NewSize(320, 180) }

// shownBoxes is the window's boxes with the one being dragged where it is.
func (r *calibrationRenderer) shownBoxes() [calibTargetCount]calibShown {
	shown := r.v.c.shown()
	if v := r.v; v.dragging && v.live != nil {
		shown[v.grab.target] = calibShown{*v.live, calibBoxPending}
	}
	return shown
}

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
	shown := r.shownBoxes()
	for t, box := range r.boxes {
		selected := calibTarget(t) == c.target
		box.StrokeWidth = 2
		if selected {
			box.StrokeWidth = 3
		}
		r.tags[t].set(calibTargetNames[t]+calibBoxSuffix[shown[t].state], selected)
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

func (r *calibrationRenderer) hideAll() {
	for t := range r.boxes {
		r.boxes[t].Hide()
		r.tags[t].hide()
	}
	for t := range r.dots {
		for _, dot := range r.dots[t] {
			dot.Hide()
		}
	}
	r.hideAuto()
	r.band.Hide()
}

func (r *calibrationRenderer) hideAuto() {
	for side := range r.autoShade {
		for k := range r.autoShade[side] {
			r.hideRegion(side, k)
		}
		for _, l := range r.crosses[side] {
			l.Hide()
		}
	}
}

func (r *calibrationRenderer) hideRegion(side, k int) {
	r.autoShade[side][k].Hide()
	for _, l := range r.autoDash[side][k] {
		l.Hide()
	}
	r.autoTags[side][k].hide()
}

func (r *calibrationRenderer) Layout(size fyne.Size) {
	r.bg.Move(fyne.NewPos(0, 0))
	r.bg.Resize(size)
	c := r.v.c
	if c.snap == nil {
		r.hideAll()
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
		r.hideAll()
		r.img.Hide()
		return
	}
	r.img.Show()
	at, area := fit.rect(config.NormalizedRect{Width: 1, Height: 1})
	r.img.Move(at)
	r.img.Resize(area)

	place := &calibTagPlacer{bounds: calibArea{0, 0, size.Width, size.Height}}
	shown := r.shownBoxes()
	var boxAt [calibTargetCount]fyne.Position
	var boxSize [calibTargetCount]fyne.Size
	for t, box := range shown {
		boxAt[t], boxSize[t] = r.layoutBox(calibTarget(t), box, fit)
		if box.state != calibBoxNone {
			place.avoidOutline(boxAt[t], boxSize[t])
		}
	}
	if c.autoShown() {
		r.layoutCrosses(fit, place)
	} else {
		r.hideAuto()
	}
	// A fixed order (the small bead boxes first) keeps names from jumping
	// when the selection changes.
	for t := range calibTargetCount {
		if shown[t].state == calibBoxNone {
			r.tags[t].hide()
			continue
		}
		r.placeBoxTag(t, boxAt[t], boxSize[t], place)
	}
	if c.autoShown() {
		r.layoutRegions(fit, place)
	}

	if !r.v.dragging || r.v.grab.grip != 0 {
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

// layoutBox places t's outline (and a bead box's implied centres).
func (r *calibrationRenderer) layoutBox(t calibTarget, shown calibShown, fit calibrationFit) (fyne.Position, fyne.Size) {
	box := r.boxes[t]
	if t.beads() {
		for _, dot := range r.dots[t] {
			dot.Hide()
		}
	}
	if shown.state == calibBoxNone {
		box.Hide()
		return fyne.Position{}, fyne.Size{}
	}
	pos, boxSize := fit.rect(shown.rect)
	box.Move(pos)
	box.Resize(boxSize)
	box.Show()
	if t.beads() {
		// Small enough to see the bead around it.
		d := calibDotSize(boxSize)
		for i, centre := range calibrationBeadCenters(shown.rect, t.left()) {
			dot := r.dots[t][i]
			dot.Move(fit.point(centre.X, centre.Y).SubtractXY(d/2, d/2))
			dot.Resize(fyne.NewSize(d, d))
			dot.Show()
		}
	}
	return pos, boxSize
}

func calibDotSize(box fyne.Size) float32 {
	d := fyne.Min(box.Width/4, box.Height) * 0.35
	return fyne.Max(3.5, fyne.Min(d, 8))
}

// placeBoxTag puts t's name next to its box at the outer side: bead names
// above (avatars sit at the top edge), avatar names below, sliding inward
// past whatever is in the way.
func (r *calibrationRenderer) placeBoxTag(t calibTarget, at fyne.Position, size fyne.Size, place *calibTagPlacer) {
	tag := r.tags[t]
	ts := tag.size()
	outer, step := at.X, float32(6)
	if !t.left() {
		outer, step = at.X+size.Width-ts.Width, -6
	}
	above, below := at.Y-ts.Height-2, at.Y+size.Height+2
	rows := []float32{above, below}
	if !t.beads() {
		rows = []float32{below, above}
	}
	n := int(size.Width/6) + 1
	var candidates []fyne.Position
	for _, y := range rows {
		candidates = append(candidates, calibSlide(fyne.NewPos(outer, y), fyne.NewDelta(step, 0), n)...)
	}
	tag.show(place.place(ts, candidates))
}

// layoutCrosses marks each side's sampled bead centres with a cross a little
// longer than the dots of the bead boxes.
func (r *calibrationRenderer) layoutCrosses(fit calibrationFit, place *calibTagPlacer) {
	for side, g := range r.v.c.snap.geometry {
		lines := r.lines(&r.crosses[side], 4*len(g.Beads), func(i int) *canvas.Line {
			if i%4 < 2 {
				l := canvas.NewLine(calibAutoShade)
				l.StrokeWidth = 3.5
				return l
			}
			l := canvas.NewLine(calibAutoColor)
			l.StrokeWidth = 1.5
			return l
		})
		// Arms reach past the dot of a bead box over the same bead but stay
		// clear of the neighbouring cross.
		pitch := float32(math.Inf(1))
		for i := 1; i < len(g.Beads); i++ {
			d := g.Beads[i].Sub(g.Beads[i-1])
			pitch = fyne.Min(pitch, float32(math.Hypot(float64(d.X), float64(d.Y))*fit.scale))
		}
		arm := fyne.Max(3, fyne.Min(7, 0.4*pitch))
		for i, p := range g.Beads {
			centre := fit.pixelPoint(float64(p.X), float64(p.Y))
			for j, l := range lines[4*i : 4*i+4] {
				reach := arm
				if j < 2 {
					reach++ // the shade frames the light line
				}
				if j%2 == 0 {
					l.Position1, l.Position2 = centre.AddXY(-reach, 0), centre.AddXY(reach, 0)
				} else {
					l.Position1, l.Position2 = centre.AddXY(0, -reach), centre.AddXY(0, reach)
				}
				l.Show()
			}
			place.taken = append(place.taken, calibArea{centre.X - arm, centre.Y - arm, centre.X + arm, centre.Y + arm})
		}
	}
}

// layoutRegions draws each side's name region and avatar window, clipped to
// the frame (the engine clips them too), as white dashes over a dark shade.
func (r *calibrationRenderer) layoutRegions(fit calibrationFit, place *calibTagPlacer) {
	frame := image.Rect(0, 0, fit.w, fit.h)
	for side, g := range r.v.c.snap.geometry {
		for k, roi := range [2]image.Rectangle{g.NameROI, g.AvatarROI} {
			clip := roi.Intersect(frame)
			if clip.Empty() {
				r.hideRegion(side, k)
				continue
			}
			at := fit.pixelPoint(float64(clip.Min.X), float64(clip.Min.Y))
			end := fit.pixelPoint(float64(clip.Max.X), float64(clip.Max.Y))
			a := calibArea{at.X, at.Y, end.X, end.Y}
			shade := r.autoShade[side][k]
			shade.Move(at)
			shade.Resize(fyne.NewSize(a.x1-a.x0, a.y1-a.y0))
			shade.Show()
			r.dashes(&r.autoDash[side][k], a)
			r.placeRegionTag(side, k, a, place)
		}
	}
}

// dashes lays a dashed outline of a out of pool.
func (r *calibrationRenderer) dashes(pool *[]*canvas.Line, a calibArea) {
	type segment struct{ from, to fyne.Position }
	var segments []segment
	edge := func(from, to fyne.Position) {
		length := float32(math.Max(math.Abs(float64(to.X-from.X)), math.Abs(float64(to.Y-from.Y))))
		period := fyne.Max(calibDash+calibDashGap, length/calibDashesPerSide)
		dash := period * calibDash / (calibDash + calibDashGap)
		for s := float32(0); s < length; s += period {
			e := fyne.Min(s+dash, length)
			at := func(d float32) fyne.Position {
				f := d / length
				return fyne.NewPos(from.X+(to.X-from.X)*f, from.Y+(to.Y-from.Y)*f)
			}
			segments = append(segments, segment{at(s), at(e)})
		}
	}
	tl, tr := fyne.NewPos(a.x0, a.y0), fyne.NewPos(a.x1, a.y0)
	bl, br := fyne.NewPos(a.x0, a.y1), fyne.NewPos(a.x1, a.y1)
	edge(tl, tr)
	edge(bl, br)
	edge(tl, bl)
	edge(tr, br)
	lines := r.lines(pool, len(segments), func(int) *canvas.Line {
		l := canvas.NewLine(calibAutoColor)
		l.StrokeWidth = 1.2
		return l
	})
	for i, s := range segments {
		lines[i].Position1, lines[i].Position2 = s.from, s.to
		lines[i].Show()
	}
}

// placeRegionTag names a region: 「名字」 inside at the end towards the middle
// of the screen, 「头像窗口」 below the window at the outer side (or inside).
func (r *calibrationRenderer) placeRegionTag(side, k int, a calibArea, place *calibTagPlacer) {
	tag := r.autoTags[side][k]
	ts := tag.size()
	left := side == 0
	var candidates []fyne.Position
	if k == 0 {
		inner, step := a.x1-ts.Width-3, float32(-10)
		if !left {
			inner, step = a.x0+3, 10
		}
		n := int((a.x1-a.x0-ts.Width)/10) + 1
		for _, y := range []float32{a.y0 + 2, a.y1 - ts.Height - 2} {
			candidates = append(candidates, calibSlide(fyne.NewPos(inner, y), fyne.NewDelta(step, 0), n)...)
		}
	} else {
		outer, inside := a.x0, a.x0+2
		if !left {
			outer, inside = a.x1-ts.Width, a.x1-ts.Width-2
		}
		candidates = append(candidates, calibSlide(fyne.NewPos(outer, a.y1+2), fyne.NewDelta(0, 6), 6)...)
		candidates = append(candidates, fyne.NewPos(inside, a.y1-ts.Height-2), fyne.NewPos(inside, a.y0+2))
	}
	tag.show(place.place(ts, candidates))
}
