// Package gui 提供替身计时器的纯 Win32 可视化窗口（无 cgo、无第三方 GUI 框架）。
//
// 解耦约定：本包不 import internal/win 与 internal/detect —— 它只通过
// Provider 回调获取数据，业务（窗口查找、截屏、豆位检测）由调用方组装。
package gui

import (
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"runtime"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"narutotimer/internal/draw"
	"narutotimer/internal/frame"
	"narutotimer/internal/win32"
)

// Run 启动 GUI 主循环，阻塞直到窗口关闭。必须在主 goroutine 调用。
func Run(provider frame.Provider) error {
	// Win32 硬约束：窗口/定时器/消息队列绑定创建它们的 OS 线程。
	// Go goroutine 可跨线程迁移，消息循环一旦换线程就会在空队列上
	// 永远等待（窗口假死/AppHang）。UI 生命周期必须锁线程。
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if provider == nil {
		return &runError{"provider 不能为 nil"}
	}
	a := &app{provider: provider, showBeads: true}
	return a.run()
}

type runError struct{ msg string }

func (e *runError) Error() string { return e.msg }

// ---------- 控件与主窗口 ----------

const (
	idCheck   = 1001 // 显示豆位框 CheckBox
	idRefresh = 1002 // 刷新 Button
	idStatus  = 1003 // 状态文本 Static
	idTimer   = 1
	timerMs   = 500 // 自动刷新间隔
)

const (
	winW = 1200
	winH = 820
	barH = 40 // 顶部控制条高度
)

type app struct {
	provider frame.Provider

	hwnd    uintptr
	hCheck  uintptr
	hBtn    uintptr
	hStatus uintptr

	mu         sync.Mutex
	raw        frame.Frame // 最近一次 provider 结果（原始截图）
	display    *image.RGBA // 画好框的显示图（nil = 无内容）
	statusStr  string
	showBeads  bool
	frameSaved bool // 调试：是否已保存首帧

	// worker 事件驱动：触发采集（无缓冲），worker 完成后写 pending。
	// 主线程只消费 pending，PrintWindow 卡住也不会阻塞 UI。
	trigger chan struct{}
	pending *frame.Frame

	proc uintptr // WndProc 回调指针（防 GC）
}

// gApp 是包级全局 app 指针，供包级 WndProc 回调使用（避免方法值/闭包回调的兼容性问题）。
var gApp *app

// wndProcCallback 是注册到窗口类的包级回调，转发给 gApp。
func wndProcCallback(hwnd uintptr, msg uint32, wParam, lParam uintptr) uintptr {
	if gApp == nil {
		r, _, _ := win32.ProcDefWindowProcW.Call(hwnd, uintptr(msg), wParam, lParam)
		return r
	}
	return gApp.wndProc(hwnd, msg, wParam, lParam)
}

// run 注册窗口类、创建窗口、进入消息循环。
func (a *app) run() error {
	writeStep("1 enter run")
	// watchdog：卡死时 dump 全部 goroutine 栈并退出（调试用，验证后移除）
	go func() {
		time.Sleep(30 * time.Second)
		buf := make([]byte, 1<<20)
		n := runtime.Stack(buf, true)
		os.WriteFile("gui_dump.txt", buf[:n], 0o644)
		os.Exit(3)
	}()
	win32.ProcSetProcessDPIAware.Call()

	hInst, _, _ := win32.ProcGetModuleHandleW.Call(0)
	if hInst == 0 {
		return &runError{"GetModuleHandleW 失败"}
	}

	className, _ := syscall.UTF16PtrFromString("NarutoTimerGUI")
	gApp = a
	a.proc = syscall.NewCallback(wndProcCallback)
	writeStep("2 callback ok")
	cursor, _, _ := win32.ProcLoadCursorW.Call(0, win32.IDIArrow)

	wc := win32.WndClassExW{
		Size:      uint32(unsafe.Sizeof(win32.WndClassExW{})),
		Style:     win32.CSHRedraw | win32.CSVRedraw,
		WndProc:   a.proc,
		Instance:  hInst,
		Cursor:    cursor,
		ClassName: className,
	}
	if r, _, e := win32.ProcRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc))); r == 0 {
		return &runError{"RegisterClassExW: " + e.Error()}
	}
	writeStep("3 register ok")

	title, _ := syscall.UTF16PtrFromString("替身计时器 - 视觉校准")
	hwnd, _, e := win32.ProcCreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(className)),
		uintptr(unsafe.Pointer(title)),
		win32.WSOverlappedWin,
		win32.CWUsedDefault, win32.CWUsedDefault, winW, winH,
		0, 0, hInst, 0,
	)
	writeStep("4 createwindow done")
	if hwnd == 0 {
		return &runError{"CreateWindowExW: " + e.Error()}
	}
	a.hwnd = hwnd

	if err := a.createControls(hInst); err != nil {
		return err
	}
	writeStep("5 controls ok")

	// worker goroutine：事件驱动采集帧（PrintWindow 卡住不影响 UI）
	a.trigger = make(chan struct{})
	go a.workerLoop()
	a.refresh() // 触发首帧
	writeStep("6 first refresh ok")
	win32.ProcShowWindow.Call(hwnd, win32.SWShow)
	win32.ProcUpdateWindow.Call(hwnd)
	win32.ProcSetTimer.Call(hwnd, idTimer, timerMs, 0)
	writeStep("7 loop start")

	var m win32.Msg
	for {
		r, _, e := win32.ProcGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		switch int32(r) {
		case 0: // WM_QUIT
			return nil
		case -1:
			return &runError{"GetMessageW: " + e.Error()}
		}
		win32.ProcTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		win32.ProcDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
	}
}

// writeStep 写入启动步骤日志（调试用，每次覆盖）。
func writeStep(s string) {
	os.WriteFile("gui_step.txt", []byte(s+"\n"), 0o644)
}

func (a *app) createControls(hInst uintptr) error {
	checkText, _ := syscall.UTF16PtrFromString("显示豆位框")
	btnText, _ := syscall.UTF16PtrFromString("刷新")
	buttonCls, _ := syscall.UTF16PtrFromString("BUTTON")
	staticCls, _ := syscall.UTF16PtrFromString("STATIC")

	h, _, e := win32.ProcCreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(buttonCls)),
		uintptr(unsafe.Pointer(checkText)),
		win32.WSChild|win32.WSVisible|win32.BSAutoCheckbox,
		10, 9, 110, 24, a.hwnd, idCheck, hInst, 0)
	if h == 0 {
		return &runError{"创建 CheckBox: " + e.Error()}
	}
	a.hCheck = h

	h, _, e = win32.ProcCreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(buttonCls)),
		uintptr(unsafe.Pointer(btnText)),
		win32.WSChild|win32.WSVisible,
		130, 7, 70, 28, a.hwnd, idRefresh, hInst, 0)
	if h == 0 {
		return &runError{"创建 Button: " + e.Error()}
	}
	a.hBtn = h

	h, _, e = win32.ProcCreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(staticCls)),
		0,
		win32.WSChild|win32.WSVisible|win32.SSLeft,
		220, 12, winW-230, 22, a.hwnd, idStatus, hInst, 0)
	if h == 0 {
		return &runError{"创建 Static: " + e.Error()}
	}
	a.hStatus = h

	win32.ProcSendMessageW.Call(a.hCheck, win32.BMSetCheck, win32.BSTChecked, 0)
	return nil
}

// workerLoop 在独立 goroutine 中采集数据帧：收到触发才调 provider，
// 完成后写入 pending。provider（含 PrintWindow）卡住时仅阻塞本 goroutine。
func (a *app) workerLoop() {
	for range a.trigger {
		frame := a.provider()
		a.mu.Lock()
		a.pending = &frame
		a.mu.Unlock()
	}
}

// refresh 触发一次采集（非阻塞），并消费已完成的最新帧。
// 在 UI 主线程（WM_TIMER / 按钮 / 首帧）调用。
func (a *app) refresh() {
	if a.trigger != nil {
		select {
		case a.trigger <- struct{}{}:
		default: // worker 仍在忙（上一帧未完成），跳过本次触发
		}
	}
	a.consumePending()
}

// consumePending 取走 worker 的最新帧并重建显示图。
func (a *app) consumePending() {
	a.mu.Lock()
	pending := a.pending
	a.pending = nil
	a.mu.Unlock()
	if pending == nil {
		return
	}
	a.raw = *pending
	a.rebuild()
	a.redraw()

	// 调试日志：每次刷新覆盖写入，便于远程排查
	debugLine := ""
	if pending.Err != nil {
		debugLine = "ERR: " + pending.Err.Error()
	} else {
		debugLine = fmt.Sprintf("img=%dx%d beads=%d showBeads=%v status=%q",
			pending.Img.Bounds().Dx(), pending.Img.Bounds().Dy(), len(pending.Beads), a.showBeads, pending.Status)
	}
	os.WriteFile("gui_debug.txt", []byte(debugLine+"\n"), 0o644)
}

// rebuild 从原始截图生成显示图（按开关画豆位框），并更新状态栏文本。
func (a *app) rebuild() {
	writeStep("r1 rebuild enter")
	a.mu.Lock()
	defer a.mu.Unlock()

	raw := a.raw
	status := raw.Status
	if raw.Err != nil {
		status = "错误: " + raw.Err.Error()
	}
	if status != a.statusStr && a.hStatus != 0 {
		a.statusStr = status
		s, _ := syscall.UTF16PtrFromString(status)
		win32.ProcSetWindowTextW.Call(a.hStatus, uintptr(unsafe.Pointer(s)))
		writeStep("r2 settext done")
	}

	a.display = nil
	if raw.Img == nil {
		writeStep("r3 img nil")
		return
	}
	img := draw.CopyRGBA(raw.Img)
	writeStep("r4 copy ok")
	if a.showBeads {
		for _, b := range raw.Beads {
			drawBead(img, b)
		}
		writeStep("r5 beads drawn")
	}
	a.display = img
	writeStep("r6 rebuild done")
}

func (a *app) redraw() {
	if a.hwnd == 0 {
		return
	}
	win32.ProcInvalidateRect.Call(a.hwnd, 0, 1)
	writeStep("w1 invalidate done")
	win32.ProcUpdateWindow.Call(a.hwnd)
	writeStep("w2 update done")
}

// wndProc 窗口消息处理。
func (a *app) wndProc(hwnd uintptr, msg uint32, wParam, lParam uintptr) uintptr {
	switch msg {
	case win32.WMCommand:
		id := wParam & 0xFFFF
		switch id {
		case idCheck:
			r, _, _ := win32.ProcSendMessageW.Call(hwnd, win32.BMGetCheck, 0, 0)
			a.mu.Lock()
			a.showBeads = r == win32.BSTChecked
			a.mu.Unlock()
			a.rebuild()
			a.redraw()
		case idRefresh:
			a.refresh()
		}
	case win32.WMTimer:
		if wParam == idTimer {
			a.refresh()
		}
	case win32.WMPaint:
		a.paint()
	case win32.WMEraseBkgnd:
		return 1 // 禁止擦背景，避免闪烁
	case win32.WMSize:
		a.redraw()
	case win32.WMDestroy:
		win32.ProcKillTimer.Call(hwnd, idTimer)
		win32.ProcPostQuitMessage.Call(0)
	}
	return a.defWndProc(hwnd, msg, wParam, lParam)
}

func (a *app) defWndProc(hwnd uintptr, msg uint32, wParam, lParam uintptr) uintptr {
	r, _, _ := win32.ProcDefWindowProcW.Call(hwnd, uintptr(msg), wParam, lParam)
	return r
}

// paint 把显示图绘制到客户区（fit 等比缩放）。
func (a *app) paint() {
	var ps win32.PaintStruct
	hdc, _, _ := win32.ProcBeginPaint.Call(a.hwnd, uintptr(unsafe.Pointer(&ps)))
	if hdc == 0 {
		return
	}
	defer win32.ProcEndPaint.Call(a.hwnd, uintptr(unsafe.Pointer(&ps)))

	// 清背景（黑色）
	black, _, _ := win32.ProcGetStockObject.Call(win32.BlackBrush)
	rc := win32.Rect{Right: winW, Bottom: winH}
	win32.ProcFillRect.Call(hdc, uintptr(unsafe.Pointer(&rc)), black)

	a.mu.Lock()
	img := a.display
	a.mu.Unlock()
	if img == nil {
		return
	}

	// 调试：首次绘制时保存显示帧，便于远程核对
	if !a.frameSaved {
		a.frameSaved = true
		png.Encode(mustFile("gui_frame.png"), img)
	}

	iw, ih := img.Bounds().Dx(), img.Bounds().Dy()
	availW, availH := int32(winW), int32(winH-barH)
	scale := float64(availW) / float64(iw)
	if s := float64(availH) / float64(ih); s < scale {
		scale = s
	}
	dw := int32(float64(iw) * scale)
	dh := int32(float64(ih) * scale)
	dx := (availW - dw) / 2
	dy := barH + (availH-dh)/2

	win32.DrawImage(hdc, img, dx, dy, dw, dh)
}

// mustFile 打开文件供写入（失败返回 nil，编码时忽略）。
func mustFile(name string) *os.File {
	f, err := os.Create(name)
	if err != nil {
		return nil
	}
	return f
}

// ---------- 图像绘制（Go 侧） ----------

// drawBead 画一颗豆：红色菱形框 + 编号标注（纯显示层，不影响识别）。
func drawBead(img *image.RGBA, b frame.Bead) {
	draw.Diamond(img, b.X, b.Y, 13, 18, color.RGBA{R: 255, G: 40, B: 40, A: 255})
	draw.Label(img, b.X+8, b.Y-15, b.Label, color.RGBA{R: 255, G: 255, B: 255, A: 255}, 2)
}
