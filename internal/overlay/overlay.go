// Package overlay 提供透明置顶覆盖层：把菱形检测框直接画在 MuMu 窗口上。
//
// 用途：校准——确认豆位算法算出的菱形框是否套准游戏中的豆子。
// 不截屏、不显示截图，只画框；透明背景、鼠标穿透、跟随目标窗口移动。
//
// 原理：WS_EX_LAYERED 分层窗口 + SetLayeredWindowAttributes 黑色色键透明，
// 窗口内除绘制的框外全透明；WS_EX_TRANSPARENT 让鼠标事件穿透到下层窗口。
package overlay

import (
	"runtime"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"narutotimer/internal/detect"
	"narutotimer/internal/win"
)

var (
	u32 = syscall.NewLazyDLL("user32.dll")
	g32 = syscall.NewLazyDLL("gdi32.dll")
	k32 = syscall.NewLazyDLL("kernel32.dll")
)

const (
	wsExLayered     = 0x00080000
	wsExTransparent = 0x00000020
	wsExTopmost     = 0x00000008
	wsExNoActivate  = 0x08000000
	wsExToolWindow  = 0x00000080
	wsPopup         = 0x80000000

	hwndTopmost   = ^uintptr(0) // -1
	swpNoActivate = 0x0010
	swpNoSize     = 0x0001
	swpNoMove     = 0x0002

	lwaColorKey = 0x00000001
	psSolid     = 0
	blackBrush  = 4
	nullBrush   = 5
	swShowNA    = 8

	wmDestroy = 0x0002
)

// Config 覆盖层配置。
type Config struct {
	// BeadColor 菱形框颜色（BGR 0x00BBGGRR 同 CreatePen 的 COLORREF）。
	// 默认亮紫（游戏中罕见，便于人工核对框位）。
	BeadColor uint32
	// FollowMs 跟随目标窗口的刷新间隔（毫秒）。
	FollowMs int
}

// DefaultConfig 默认配置：亮紫框、33ms 跟随。
func DefaultConfig() Config {
	return Config{BeadColor: 0x00FF00FF, FollowMs: 33}
}

// Overlay 是一个透明覆盖层。Start 启动，Stop 停止。
type Overlay struct {
	cfg    Config
	target uintptr // 目标窗口（MuMu）

	hwnd uintptr
	proc uintptr // WndProc 回调（防 GC）

	mu      sync.Mutex
	running bool
	stopCh  chan struct{}
}

var (
	classOnce sync.Once
	className *uint16
	classProc uintptr
)

func overlayWndProc(hwnd, msg, wp, lp uintptr) uintptr {
	r, _, _ := u32.NewProc("DefWindowProcW").Call(hwnd, msg, wp, lp)
	return r
}

// registerClass 注册覆盖层窗口类（只注册一次）。
func registerClass(hInst uintptr) {
	classOnce.Do(func() {
		className, _ = syscall.UTF16PtrFromString("NarutoOverlay")
		classProc = syscall.NewCallback(overlayWndProc)
		type wndClassExW struct {
			Size, Style            uint32
			WndProc                uintptr
			ClsExtra, WndExtra     int32
			Instance, Icon, Cursor uintptr
			Background             uintptr
			MenuName, ClassName    *uint16
			IconSm                 uintptr
		}
		wc := wndClassExW{
			Size:      uint32(unsafe.Sizeof(wndClassExW{})),
			WndProc:   classProc,
			Instance:  hInst,
			ClassName: className,
		}
		wc.Background, _, _ = g32.NewProc("GetStockObject").Call(blackBrush)
		u32.NewProc("RegisterClassExW").Call(uintptr(unsafe.Pointer(&wc)))
	})
}

// Start 创建并显示覆盖层，开始跟随目标窗口。非阻塞（跟随循环在后台 goroutine）。
// target 为目标窗口句柄（MuMu 客户区窗口）。
func Start(target uintptr, cfg Config) (*Overlay, error) {
	if cfg.FollowMs <= 0 {
		cfg.FollowMs = 33
	}
	o := &Overlay{cfg: cfg, target: target, stopCh: make(chan struct{})}

	hInst, _, _ := k32.NewProc("GetModuleHandleW").Call(0)
	registerClass(hInst)

	cr, err := win.ClientScreenRect(target)
	if err != nil {
		return nil, err
	}
	hwnd, _, _ := u32.NewProc("CreateWindowExW").Call(
		wsExLayered|wsExTransparent|wsExTopmost|wsExNoActivate|wsExToolWindow,
		uintptr(unsafe.Pointer(className)), 0, wsPopup,
		uintptr(cr.Left), uintptr(cr.Top), uintptr(cr.Width()), uintptr(cr.Height()),
		0, 0, hInst, 0)
	if hwnd == 0 {
		return nil, syscall.EINVAL
	}
	o.hwnd = hwnd
	u32.NewProc("SetLayeredWindowAttributes").Call(hwnd, 0, 0, lwaColorKey)
	u32.NewProc("ShowWindow").Call(hwnd, swShowNA)
	u32.NewProc("SetWindowPos").Call(hwnd, hwndTopmost, 0, 0, 0, 0,
		swpNoSize|swpNoMove|swpNoActivate)

	o.mu.Lock()
	o.running = true
	o.mu.Unlock()

	go o.followLoop()
	return o, nil
}

// followLoop 周期性跟随目标窗口位置/尺寸并重画框。
func (o *Overlay) followLoop() {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	d := time.Duration(o.cfg.FollowMs) * time.Millisecond
	for {
		select {
		case <-o.stopCh:
			return
		default:
		}
		cr, err := win.ClientScreenRect(o.target)
		if err != nil {
			time.Sleep(d)
			continue
		}
		u32.NewProc("SetWindowPos").Call(o.hwnd, hwndTopmost,
			uintptr(cr.Left), uintptr(cr.Top), uintptr(cr.Width()), uintptr(cr.Height()),
			swpNoActivate)
		o.draw(int(cr.Width()), int(cr.Height()))
		time.Sleep(d)
	}
}

// draw 清空为透明底并按豆位算法画菱形框。
func (o *Overlay) draw(w, h int) {
	dc, _, _ := u32.NewProc("GetDC").Call(o.hwnd)
	if dc == 0 {
		return
	}
	defer u32.NewProc("ReleaseDC").Call(o.hwnd, dc)

	br, _, _ := g32.NewProc("GetStockObject").Call(blackBrush)
	type rect struct{ Left, Top, Right, Bottom int32 }
	r := rect{0, 0, int32(w), int32(h)}
	u32.NewProc("FillRect").Call(dc, uintptr(unsafe.Pointer(&r)), br)

	pos := detect.Layout(w, h, detect.ModeAuto, detect.DefaultBeads())
	pen, _, _ := g32.NewProc("CreatePen").Call(psSolid, 2, uintptr(o.cfg.BeadColor))
	if pen == 0 {
		return
	}
	oldPen, _, _ := g32.NewProc("SelectObject").Call(dc, pen)
	nb, _, _ := g32.NewProc("GetStockObject").Call(nullBrush)
	oldBr, _, _ := g32.NewProc("SelectObject").Call(dc, nb)
	for _, p := range pos {
		diamond(dc, p.X, p.Y, detect.BeadW, detect.BeadH)
	}
	g32.NewProc("SelectObject").Call(dc, oldPen)
	g32.NewProc("SelectObject").Call(dc, oldBr)
	g32.NewProc("DeleteObject").Call(pen)
}

func diamond(dc uintptr, cx, cy, w, h int) {
	moveTo(dc, cx, cy-h/2)
	lineTo(dc, cx+w/2, cy)
	lineTo(dc, cx, cy+h/2)
	lineTo(dc, cx-w/2, cy)
	lineTo(dc, cx, cy-h/2)
}

func moveTo(dc uintptr, x, y int) {
	g32.NewProc("MoveToEx").Call(dc, uintptr(x), uintptr(y), 0)
}
func lineTo(dc uintptr, x, y int) {
	g32.NewProc("LineTo").Call(dc, uintptr(x), uintptr(y))
}

// Stop 停止跟随并销毁覆盖层窗口。
func (o *Overlay) Stop() {
	o.mu.Lock()
	if !o.running {
		o.mu.Unlock()
		return
	}
	o.running = false
	close(o.stopCh)
	hwnd := o.hwnd
	o.mu.Unlock()
	if hwnd != 0 {
		u32.NewProc("DestroyWindow").Call(hwnd)
	}
}

// Running 返回覆盖层是否在运行。
func (o *Overlay) Running() bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.running
}
