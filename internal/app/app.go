//go:build gdiui

// Package app 旧版 Win32 GDI 前端，默认不编译。正式界面在 internal/ui（Fyne）。
package app

import (
	"fmt"
	"image"
	"image/color"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"narutotimer/internal/config"
	"narutotimer/internal/frame"
	"narutotimer/internal/win32"
)

const (
	idTimer = 1

	hitNone = -1
	hitDump = iota
	hitRefresh
	hitSwap
	hitMini
	hitTop
	hitQuit
	hitPickLeft
	hitPickRight
	hitRemember
)

const (
	viewPick = iota
	viewPanel
	viewMini
)

var (
	colBGTop    = color.RGBA{R: 28, G: 32, B: 52, A: 255}
	colBGBottom = color.RGBA{R: 12, G: 14, B: 24, A: 255}
	colAccent   = color.RGBA{R: 245, G: 158, B: 11, A: 255}
	colTitle    = color.RGBA{R: 250, G: 190, B: 88, A: 255}
	colText     = color.RGBA{R: 232, G: 236, B: 244, A: 255}
	colDim      = color.RGBA{R: 142, G: 152, B: 176, A: 255}
	colLine     = color.RGBA{R: 64, G: 76, B: 106, A: 255}
	colDanger   = color.RGBA{R: 214, G: 84, B: 84, A: 255}
	colBeadBlue = color.RGBA{R: 42, G: 164, B: 219, A: 255}
	colBeadGold = color.RGBA{R: 255, G: 196, B: 72, A: 255}
	colBeadDark = color.RGBA{R: 30, G: 40, B: 62, A: 255}
	colBtn      = color.RGBA{R: 46, G: 54, B: 78, A: 255}
)

func logf(format string, args ...any) {
	if os.Getenv("NARUTO_DEBUG") != "" {
		fmt.Fprintf(os.Stderr, "[app] "+format+"\n", args...)
	}
}

var gApp *app

type app struct {
	cfg      config.Config
	cfgPath  string
	provider frame.Provider

	hwnd uintptr
	proc uintptr

	mu        sync.Mutex
	view      int
	beads     []frame.Bead
	statusStr string
	fighting  bool
	fightHits int
	leaveHits int
	side      string // left / right
	remember  bool
	mini      bool
	topmost   bool
	hover     int
	dumpShot  bool
	lastDump  string

	trigger chan struct{}
	pending *frame.Frame
	sticky  bool

	lastPing     atomic.Int64
	lastCapMs    atomic.Int64
	skipCount    int
	tickN        int
	lastClockKey int

	leftClock  sideClock
	rightClock sideClock
	panelBuf   *image.RGBA
}

func Run(cfg config.Config, provider frame.Provider) error {
	if provider == nil {
		return &runError{"provider 不能为 nil"}
	}
	if err := config.Validate(cfg); err != nil {
		return err
	}
	a := &app{
		cfg:      cfg,
		cfgPath:  config.DefaultPath,
		provider: provider,
		hover:    hitNone,
		remember: cfg.UI.RememberSide,
		side:     cfg.UI.PlayerSide,
	}
	if a.side != "left" && a.side != "right" {
		a.view = viewPick
		a.side = ""
	} else {
		a.view = viewPanel
	}
	gApp = a
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	return a.run()
}

type runError struct{ msg string }

func (e *runError) Error() string { return e.msg }

func (a *app) winW() int {
	if a.mini {
		return max(200, a.cfg.UI.MiniWidth)
	}
	return max(400, a.cfg.UI.WindowWidth)
}

func (a *app) winH() int {
	if a.mini {
		return max(96, a.cfg.UI.MiniHeight)
	}
	return max(280, a.cfg.UI.WindowHeight)
}

func (a *app) pollMS(fighting bool) uint32 {
	if fighting {
		return uint32(max(80, a.cfg.UI.PollIntervalMS))
	}
	return uint32(max(200, a.cfg.UI.IdlePollIntervalMS))
}

func (a *app) cooldown() time.Duration {
	return time.Duration(a.cfg.UI.SubstituteCooldownSeconds * float64(time.Second))
}

func (a *app) confirmNeed() int {
	n := a.cfg.Tracking.MinimumConfirmFrames
	if n < 1 {
		return 1
	}
	return n
}

func wndProcCallback(hwnd uintptr, msg uint32, wParam, lParam uintptr) uintptr {
	if gApp == nil {
		r, _, _ := win32.ProcDefWindowProcW.Call(hwnd, uintptr(msg), wParam, lParam)
		return r
	}
	return gApp.wndProc(hwnd, msg, wParam, lParam)
}

func (a *app) run() error {
	logf("run: 开始")
	win32.ProcSetProcessDPIAware.Call()
	hInst, _, _ := win32.ProcGetModuleHandleW.Call(0)
	if hInst == 0 {
		return &runError{"GetModuleHandleW 失败"}
	}
	className, _ := syscall.UTF16PtrFromString("NarutoTimerApp")
	a.proc = syscall.NewCallback(wndProcCallback)
	cursor, _, _ := win32.ProcLoadCursorW.Call(0, win32.IDIArrow)
	wc := win32.WndClassExW{
		Size:      uint32(unsafe.Sizeof(win32.WndClassExW{})),
		Style:     0,
		WndProc:   a.proc,
		Instance:  hInst,
		Cursor:    cursor,
		ClassName: className,
	}
	if r, _, e := win32.ProcRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc))); r == 0 {
		return &runError{"RegisterClassExW: " + e.Error()}
	}
	ow, oh := win32.WindowSizeForClient(a.winW(), a.winH(), win32.WSFixedFrame)
	title, _ := syscall.UTF16PtrFromString("替身计时器")
	hwnd, _, e := win32.ProcCreateWindowExW.Call(
		0, uintptr(unsafe.Pointer(className)), uintptr(unsafe.Pointer(title)),
		win32.WSFixedFrame,
		win32.CWUsedDefault, win32.CWUsedDefault, uintptr(ow), uintptr(oh),
		0, 0, hInst, 0,
	)
	if hwnd == 0 {
		return &runError{"CreateWindowExW: " + e.Error()}
	}
	a.hwnd = hwnd
	a.trigger = make(chan struct{})
	go a.workerLoop()
	go a.watchdogLoop()
	win32.ProcShowWindow.Call(hwnd, win32.SWShow)
	win32.ProcUpdateWindow.Call(hwnd)
	win32.ProcSetTimer.Call(hwnd, idTimer, uintptr(a.pollMS(false)), 0)
	if a.view != viewPick {
		a.requestCapture()
		a.setStatus("正在获取画面…")
	} else {
		a.setStatus("请选择你在这一局的位置")
	}
	var m win32.Msg
	for {
		r, _, e := win32.ProcGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		a.lastPing.Store(time.Now().UnixMilli())
		switch int32(r) {
		case 0:
			return nil
		case -1:
			return &runError{"GetMessageW: " + e.Error()}
		}
		win32.ProcTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		win32.ProcDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
	}
}

func (a *app) workerLoop() {
	for range a.trigger {
		func() {
			defer func() {
				if r := recover(); r != nil {
					logf("worker PANIC: %v", r)
				}
			}()
			t0 := time.Now()
			f := a.provider()
			a.lastCapMs.Store(time.Since(t0).Milliseconds())
			logf("worker: 采集完成 err=%v beads=%d fight=%v hold=%v 耗时=%dms", f.Err, len(f.Beads), f.Fighting, f.Hold, a.lastCapMs.Load())
			a.mu.Lock()
			a.pending = &f
			sticky := a.sticky
			a.sticky = false
			a.mu.Unlock()
			if sticky {
				select {
				case a.trigger <- struct{}{}:
				default:
				}
			}
		}()
	}
}

func (a *app) requestCapture() {
	if a.trigger == nil {
		return
	}
	select {
	case a.trigger <- struct{}{}:
	default:
		a.mu.Lock()
		a.sticky = true
		a.mu.Unlock()
	}
	a.consumePending()
}

func (a *app) consumePending() {
	a.mu.Lock()
	pending := a.pending
	a.pending = nil
	if pending == nil {
		a.mu.Unlock()
		return
	}
	needDump := a.dumpShot && pending.Img != nil && pending.Err == nil && !pending.Hold
	if needDump {
		a.dumpShot = false
	}
	needRedraw := false
	if pending.Hold {
		if pending.Err != nil && a.statusStr != pending.Err.Error() {
			a.statusStr = pending.Err.Error()
			needRedraw = true
		}
		a.dumpShot = false
	} else if pending.Err == nil {
		wasFight := a.fighting
		a.applyFight(pending.Fighting)
		if a.fighting != wasFight || !sameBeads(a.beads, pending.Beads) {
			needRedraw = true
		}
		a.beads = pending.Beads
		if pending.Status != "" {
			a.statusStr = pending.Status
		}
		if a.fighting {
			lc, rc := countReady(pending.Beads)
			cd := a.cooldown()
			need := a.confirmNeed()
			at := pending.CapturedAt
			a.leftClock.observe(lc, true, at, cd, need)
			a.rightClock.observe(rc, true, at, cd, need)
		}
	} else {
		msg := "错误: " + pending.Err.Error()
		if a.statusStr != msg {
			a.statusStr = msg
			needRedraw = true
		}
		a.dumpShot = false
	}
	a.mu.Unlock()

	if needDump {
		path, err := SaveBeadDump(pending.Img, pending.Beads)
		a.mu.Lock()
		if err != nil {
			a.statusStr = "保存截图失败: " + err.Error()
		} else {
			a.lastDump = path
			a.statusStr = "已保存 " + path
		}
		a.mu.Unlock()
		needRedraw = true
	}
	if needRedraw {
		a.redraw()
	}
}

func (a *app) applyFight(raw bool) {
	enterN := a.cfg.Tracking.EnterFightFrames
	leaveN := a.cfg.Tracking.LeaveFightFrames
	if enterN < 1 {
		enterN = 1
	}
	if leaveN < 1 {
		leaveN = 2
	}
	if raw {
		a.leaveHits = 0
		a.fightHits++
		if !a.fighting && a.fightHits >= enterN {
			a.fighting = true
		}
		return
	}
	a.fightHits = 0
	if !a.fighting {
		return
	}
	a.leaveHits++
	if a.leaveHits >= leaveN {
		a.fighting = false
	}
}

func sameBeads(a, b []frame.Bead) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Label != b[i].Label || a[i].Dark != b[i].Dark || a[i].Gold != b[i].Gold {
			return false
		}
	}
	return true
}

func (a *app) setStatus(s string) {
	a.mu.Lock()
	a.statusStr = s
	a.mu.Unlock()
	a.redraw()
}

func (a *app) watchdogLoop() {
	for {
		time.Sleep(2 * time.Second)
		last := a.lastPing.Load()
		if last > 0 && time.Now().UnixMilli()-last > 15000 {
			logf("WATCHDOG: 主线程无响应")
		}
	}
}

func (a *app) wndProc(hwnd uintptr, msg uint32, wParam, lParam uintptr) uintptr {
	a.lastPing.Store(time.Now().UnixMilli())
	switch msg {
	case win32.WMTimer:
		a.mu.Lock()
		view := a.view
		fighting := a.fighting
		clockOn := a.leftClock.active() || a.rightClock.active()
		a.mu.Unlock()
		win32.ProcSetTimer.Call(hwnd, idTimer, uintptr(a.pollMS(fighting)), 0)
		if view == viewPick {
			return 0
		}
		a.tickN++
		if a.tickN%2 == 0 {
			if a.lastCapMs.Load() > 2000 && a.skipCount < 2 {
				a.skipCount++
			} else {
				a.skipCount = 0
				a.requestCapture()
			}
		} else {
			a.consumePending()
		}
		if clockOn {
			now := time.Now()
			key := a.leftClock.displayKey(now)*100000 + a.rightClock.displayKey(now)
			if key != a.lastClockKey {
				a.lastClockKey = key
				a.redraw()
			}
		}
		return 0
	case win32.WMPaint:
		a.paint()
		return 0
	case win32.WMPrint, win32.WMPrintClient:
		return 0
	case win32.WMEraseBkgnd:
		return 1
	case win32.WMMouseMove:
		x := int32(int16(lParam & 0xFFFF))
		y := int32(int16(lParam >> 16))
		h := a.hitTest(x, y)
		if h != a.hover {
			a.hover = h
			a.redraw()
		}
		return 0
	case win32.WMLButtonDown:
		x := int32(int16(lParam & 0xFFFF))
		y := int32(int16(lParam >> 16))
		a.onClick(a.hitTest(x, y))
		return 0
	case win32.WMSetCursor:
		if lParam&0xFFFF == win32.HTClient {
			id := win32.IDCArrow
			if a.hover >= 0 {
				id = win32.IDCHand
			}
			hCur, _, _ := win32.ProcLoadCursorW.Call(0, uintptr(id))
			win32.ProcSetCursor.Call(hCur)
			return 1
		}
	case win32.WMDestroy:
		win32.ProcPostQuitMessage.Call(0)
		return 0
	}
	r, _, _ := win32.ProcDefWindowProcW.Call(hwnd, uintptr(msg), wParam, lParam)
	return r
}

func (a *app) hitTest(x, y int32) int {
	for id, r := range a.hitRects() {
		if inRect(x, y, r) {
			return id
		}
	}
	return hitNone
}

func (a *app) hitRects() map[int]win32.Rect {
	w, h := int32(a.winW()), int32(a.winH())
	out := map[int]win32.Rect{}
	if a.view == viewPick {
		out[hitPickLeft] = win32.Rect{Left: 40, Top: h/2 - 10, Right: w/2 - 16, Bottom: h/2 + 70}
		out[hitPickRight] = win32.Rect{Left: w/2 + 16, Top: h/2 - 10, Right: w - 40, Bottom: h/2 + 70}
		out[hitRemember] = win32.Rect{Left: w/2 - 80, Top: h - 56, Right: w/2 + 80, Bottom: h - 28}
		return out
	}
	if a.view == viewMini {
		out[hitMini] = win32.Rect{Left: w - 92, Top: 6, Right: w - 48, Bottom: 28}
		out[hitTop] = win32.Rect{Left: w - 44, Top: 6, Right: w - 8, Bottom: 28}
		return out
	}
	btnY := h - 56
	out[hitDump] = win32.Rect{Left: 16, Top: btnY, Right: 116, Bottom: btnY + 40}
	out[hitRefresh] = win32.Rect{Left: 124, Top: btnY, Right: 214, Bottom: btnY + 40}
	out[hitSwap] = win32.Rect{Left: 222, Top: btnY, Right: 312, Bottom: btnY + 40}
	out[hitMini] = win32.Rect{Left: 320, Top: btnY, Right: 410, Bottom: btnY + 40}
	out[hitQuit] = win32.Rect{Left: w - 96, Top: btnY, Right: w - 16, Bottom: btnY + 40}
	out[hitTop] = win32.Rect{Left: w - 80, Top: 12, Right: w - 16, Bottom: 36}
	return out
}

func inRect(x, y int32, r win32.Rect) bool {
	return x >= r.Left && x < r.Right && y >= r.Top && y < r.Bottom
}

func (a *app) onClick(id int) {
	switch id {
	case hitDump:
		a.requestDump()
	case hitRefresh:
		a.requestCapture()
	case hitSwap:
		a.pickSide(map[string]string{"left": "right", "right": "left"}[a.side])
	case hitMini:
		a.toggleMini()
	case hitTop:
		a.toggleTopmost()
	case hitQuit:
		win32.ProcPostQuitMessage.Call(0)
	case hitPickLeft:
		a.pickSide("left")
	case hitPickRight:
		a.pickSide("right")
	case hitRemember:
		a.remember = !a.remember
		a.redraw()
	}
}

func (a *app) pickSide(side string) {
	if side != "left" && side != "right" {
		return
	}
	a.mu.Lock()
	a.side = side
	a.view = viewPanel
	a.leftClock.reset()
	a.rightClock.reset()
	if a.remember {
		a.cfg.UI.PlayerSide = side
		a.cfg.UI.RememberSide = true
	} else {
		a.cfg.UI.PlayerSide = "ask"
		a.cfg.UI.RememberSide = false
	}
	cfg := a.cfg
	path := a.cfgPath
	a.mu.Unlock()
	_ = config.Save(path, cfg)
	a.resizeTo(a.winW(), a.winH())
	a.setStatus("已选择" + sideName(side) + " · 等待对局")
	a.requestCapture()
}

func (a *app) toggleMini() {
	a.mu.Lock()
	a.mini = !a.mini
	if a.mini {
		a.view = viewMini
		a.topmost = true
	} else {
		a.view = viewPanel
	}
	a.mu.Unlock()
	if a.mini {
		a.applyTopmost(true)
	}
	a.resizeTo(a.winW(), a.winH())
	a.redraw()
}

func (a *app) toggleTopmost() {
	a.topmost = !a.topmost
	a.applyTopmost(a.topmost)
	a.redraw()
}

func (a *app) applyTopmost(on bool) {
	h := win32.HWNDNoTopMost
	if on {
		h = win32.HWNDTopMost
	}
	win32.ProcSetWindowPos.Call(a.hwnd, h, 0, 0, 0, 0,
		uintptr(win32.SWPNoMove|win32.SWPNoSize|win32.SWPNoActivate))
}

func (a *app) resizeTo(cw, ch int) {
	ow, oh := win32.WindowSizeForClient(cw, ch, win32.WSFixedFrame)
	win32.ProcSetWindowPos.Call(a.hwnd, 0, 0, 0, uintptr(ow), uintptr(oh),
		uintptr(win32.SWPNoMove|win32.SWPNoZOrder|win32.SWPNoActivate))
}

func (a *app) requestDump() {
	a.mu.Lock()
	a.dumpShot = true
	a.mu.Unlock()
	a.setStatus("正在保存带框截图…")
	a.requestCapture()
}

func (a *app) redraw() {
	if a.hwnd != 0 {
		win32.ProcInvalidateRect.Call(a.hwnd, 0, 0)
	}
}

func (a *app) paint() {
	var ps win32.PaintStruct
	hdc, _, _ := win32.ProcBeginPaint.Call(a.hwnd, uintptr(unsafe.Pointer(&ps)))
	if hdc == 0 {
		return
	}
	defer win32.ProcEndPaint.Call(a.hwnd, uintptr(unsafe.Pointer(&ps)))
	w, h := a.winW(), a.winH()
	mdc, _, _ := win32.ProcCreateCompatibleDC.Call(hdc)
	bmp, _, _ := win32.ProcCreateCompatibleBitmap.Call(hdc, uintptr(w), uintptr(h))
	old, _, _ := win32.ProcSelectObject.Call(mdc, bmp)
	a.paintBG(mdc, w, h)

	a.mu.Lock()
	view := a.view
	beads := a.beads
	status := a.statusStr
	side := a.side
	fighting := a.fighting
	left := a.leftClock.remaining(time.Now())
	right := a.rightClock.remaining(time.Now())
	a.mu.Unlock()
	capMs := a.lastCapMs.Load()

	if os.Getenv("NARUTO_PREVIEW") == "demo" {
		beads = previewDemoBeads()
		side = "left"
		view = viewPanel
		fighting = true
		left = []float64{13.5}
	}

	switch view {
	case viewPick:
		a.paintPick(mdc, w, h)
	case viewMini:
		a.paintMini(mdc, w, h, beads, side, left, right, fighting)
	default:
		a.paintPanel(mdc, w, h, beads, status, side, left, right, fighting, capMs)
	}

	if os.Getenv("NARUTO_PREVIEW") != "" {
		saveDCAsPNG(mdc, w, h, "preview.png")
		win32.ProcSelectObject.Call(mdc, old)
		win32.ProcDeleteObject.Call(bmp)
		win32.ProcDeleteDC.Call(mdc)
		win32.ProcPostQuitMessage.Call(0)
		return
	}
	win32.ProcBitBlt.Call(hdc, 0, 0, uintptr(w), uintptr(h), mdc, 0, 0, win32.SrcCopy)
	win32.ProcSelectObject.Call(mdc, old)
	win32.ProcDeleteObject.Call(bmp)
	win32.ProcDeleteDC.Call(mdc)
}

func (a *app) paintBG(hdc uintptr, w, h int) {
	if a.panelBuf == nil || a.panelBuf.Bounds().Dx() != w || a.panelBuf.Bounds().Dy() != h {
		a.panelBuf = image.NewRGBA(image.Rect(0, 0, w, h))
		for y := 0; y < h; y++ {
			t := float64(y) / float64(max(1, h))
			r := byte(int(colBGTop.R) + int(float64(int(colBGBottom.R)-int(colBGTop.R))*t))
			g := byte(int(colBGTop.G) + int(float64(int(colBGBottom.G)-int(colBGTop.G))*t))
			b := byte(int(colBGTop.B) + int(float64(int(colBGBottom.B)-int(colBGTop.B))*t))
			for x := 0; x < w; x++ {
				o := (y*w + x) * 4
				a.panelBuf.Pix[o] = r
				a.panelBuf.Pix[o+1] = g
				a.panelBuf.Pix[o+2] = b
				a.panelBuf.Pix[o+3] = 255
			}
		}
	}
	win32.DrawImage(hdc, a.panelBuf, 0, 0, int32(w), int32(h))
}

func (a *app) paintPick(hdc uintptr, w, h int) {
	win32.DrawText(hdc, "这一局你在哪一边？", 28, 36, 22, win32.FWBold, colTitle, "Microsoft YaHei UI")
	win32.DrawText(hdc, "计时跟所选侧走，对侧仍显示豆数", 28, 72, 13, win32.FWNormal, colDim, "Microsoft YaHei UI")
	rects := a.hitRects()
	a.drawBtn(hdc, rects[hitPickLeft], "我是左边", a.hover == hitPickLeft, false)
	a.drawBtn(hdc, rects[hitPickRight], "我是右边", a.hover == hitPickRight, false)
	mark := "□ 记住选择"
	if a.remember {
		mark = "■ 记住选择"
	}
	a.drawBtn(hdc, rects[hitRemember], mark, a.hover == hitRemember, a.remember)
}

func (a *app) paintPanel(hdc uintptr, w, h int, beads []frame.Bead, status, side string, left, right []float64, fighting bool, capMs int64) {
	win32.DrawText(hdc, "替身计时器", 20, 12, 20, win32.FWBold, colTitle, "Microsoft YaHei UI")
	win32.DrawText(hdc, "当前："+sideName(side), 160, 18, 13, win32.FWNormal, colAccent, "Microsoft YaHei UI")
	rects := a.hitRects()
	a.drawBtn(hdc, rects[hitTop], topLabel(a.topmost), a.hover == hitTop, a.topmost)

	lc, rc := countReady(beads)
	mine, opp := lc, rc
	mineT, oppT := left, right
	if side == "right" {
		mine, opp = rc, lc
		mineT, oppT = right, left
	}
	cd := FormatCD(mineT)
	win32.DrawText(hdc, cd, w/2-len(cd)*18, 58, 48, win32.FWBold, colAccent, "Microsoft YaHei UI")
	sub := "等待放替身"
	if len(mineT) == 1 {
		sub = "替身冷却"
	} else if len(mineT) > 1 {
		sub = fmt.Sprintf("冷却中 ×%d", len(mineT))
	}
	win32.DrawText(hdc, sub, w/2-len([]rune(sub))*8, 112, 13, win32.FWNormal, colDim, "Microsoft YaHei UI")

	fight := "等待对局"
	fc := colDim
	if fighting {
		fight = "对局中"
		fc = colAccent
	}
	win32.DrawText(hdc, fight, 20, 140, 14, win32.FWBold, fc, "Microsoft YaHei UI")
	win32.DrawText(hdc, fmt.Sprintf("我 %d   对侧 %d", mine, opp), w-160, 140, 14, win32.FWNormal, colText, "Microsoft YaHei UI")
	if len(oppT) > 0 {
		win32.DrawText(hdc, "对侧 "+FormatCD(oppT), w-160, 160, 12, win32.FWNormal, colDim, "Microsoft YaHei UI")
	}

	a.paintBeads(hdc, 24, 196, "左", "L", beads)
	a.paintBeads(hdc, w/2+8, 196, "右", "R", beads)

	a.drawBtn(hdc, rects[hitDump], "保存截图", a.hover == hitDump, false)
	a.drawBtn(hdc, rects[hitRefresh], "刷新", a.hover == hitRefresh, false)
	a.drawBtn(hdc, rects[hitSwap], "换边", a.hover == hitSwap, false)
	a.drawBtn(hdc, rects[hitMini], "迷你", a.hover == hitMini, a.mini)
	a.drawBtn(hdc, rects[hitQuit], "退出", a.hover == hitQuit, false)

	info := parseWindowInfo(status)
	if info != "" {
		info += fmt.Sprintf("  ·  %dms", capMs)
		win32.DrawText(hdc, info, 16, h-18, 11, win32.FWNormal, colDim, "Microsoft YaHei UI")
	}
}

func (a *app) paintMini(hdc uintptr, w, h int, beads []frame.Bead, side string, left, right []float64, fighting bool) {
	rects := a.hitRects()
	a.drawBtn(hdc, rects[hitMini], "回", a.hover == hitMini, false)
	a.drawBtn(hdc, rects[hitTop], "顶", a.hover == hitTop, a.topmost)
	secs := left
	if side == "right" {
		secs = right
	}
	cd := FormatCD(secs)
	win32.DrawText(hdc, cd, w/2-len(cd)*14, 28, 36, win32.FWBold, colAccent, "Microsoft YaHei UI")
	a.paintBeads(hdc, 10, h-48, "L", "L", beads)
	a.paintBeads(hdc, w/2+4, h-48, "R", "R", beads)
	if !fighting {
		win32.DrawText(hdc, "等待对局", 12, 8, 11, win32.FWNormal, colDim, "Microsoft YaHei UI")
	}
}

func (a *app) paintBeads(hdc uintptr, x, y int, title, prefix string, beads []frame.Bead) {
	win32.DrawText(hdc, title, x, y, 12, win32.FWBold, colDim, "Microsoft YaHei UI")
	by := map[string]frame.Bead{}
	for _, b := range beads {
		by[b.Label] = b
	}
	for i := 0; i < 4; i++ {
		label := fmt.Sprintf("%s%d", prefix, i+1)
		cx := int32(x + 22 + i*36)
		cy := int32(y + 28)
		b, ok := by[label]
		drawBead(hdc, cx, cy, ok && !b.Dark, ok && b.Gold, ok)
	}
}

func (a *app) drawBtn(hdc uintptr, r win32.Rect, text string, hover, on bool) {
	fill := colBtn
	if hover {
		fill = color.RGBA{R: 66, G: 76, B: 108, A: 255}
	}
	win32.FillRoundRect(hdc, r.Left, r.Top, r.Right, r.Bottom, 8, 8, fill)
	if on {
		win32.StrokeRoundRect(hdc, r.Left, r.Top, r.Right, r.Bottom, 8, 8, colAccent)
	}
	fs := 13
	tw := len([]rune(text)) * fs
	x := int(r.Left) + (int(r.Right-r.Left)-tw)/2
	y := int(r.Top) + (int(r.Bottom-r.Top)-fs)/2
	tc := colText
	if on {
		tc = colAccent
	}
	win32.DrawText(hdc, text, x, y, fs, win32.FWBold, tc, "Microsoft YaHei UI")
}

func drawBead(hdc uintptr, cx, cy int32, lit, gold, known bool) {
	if !known {
		win32.FillDiamond(hdc, cx, cy, 12, 16, color.RGBA{R: 36, G: 42, B: 60, A: 255})
		return
	}
	if gold {
		win32.FillDiamond(hdc, cx, cy, 14, 18, colBeadGold)
		return
	}
	if lit {
		win32.FillDiamond(hdc, cx, cy, 14, 18, colBeadBlue)
		return
	}
	win32.FillDiamond(hdc, cx, cy, 12, 16, colBeadDark)
}

func countReady(beads []frame.Bead) (lc, rc int) {
	for _, b := range beads {
		if b.Dark || len(b.Label) == 0 {
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

func parseWindowInfo(status string) string {
	parts := strings.Split(status, "|")
	if len(parts) == 0 {
		return ""
	}
	info := strings.TrimSpace(parts[0])
	if len(parts) >= 3 {
		info += " | " + strings.TrimSpace(parts[2])
	}
	if len(info) > 58 {
		info = info[:58]
	}
	return info
}

func sideName(side string) string {
	if side == "right" {
		return "右边"
	}
	if side == "left" {
		return "左边"
	}
	return "未选"
}

func topLabel(on bool) string {
	if on {
		return "置顶开"
	}
	return "置顶"
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
