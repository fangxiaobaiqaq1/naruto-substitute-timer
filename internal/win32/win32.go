// Package win32 提供本工具所需的最小 Win32 API 绑定（纯 Go，无 cgo）。
// 供多个 UI 包（校准工具、计时器前端）共用，避免各自重复绑定。
package win32

import (
	"fmt"
	"image"
	"image/color"
	"syscall"
	"unsafe"
)

// ---------- DLL 与函数 ----------

var (
	User32   = syscall.NewLazyDLL("user32.dll")
	Kernel32 = syscall.NewLazyDLL("kernel32.dll")
	Gdi32    = syscall.NewLazyDLL("gdi32.dll")
	Msimg32  = syscall.NewLazyDLL("msimg32.dll")

	ProcSetProcessDPIAware         = User32.NewProc("SetProcessDPIAware")
	ProcRegisterClassExW           = User32.NewProc("RegisterClassExW")
	ProcCreateWindowExW            = User32.NewProc("CreateWindowExW")
	ProcDefWindowProcW             = User32.NewProc("DefWindowProcW")
	ProcGetMessageW                = User32.NewProc("GetMessageW")
	ProcPeekMessageW               = User32.NewProc("PeekMessageW")
	ProcTranslateMessage           = User32.NewProc("TranslateMessage")
	ProcDispatchMessageW           = User32.NewProc("DispatchMessageW")
	ProcPostQuitMessage            = User32.NewProc("PostQuitMessage")
	ProcSetTimer                   = User32.NewProc("SetTimer")
	ProcKillTimer                  = User32.NewProc("KillTimer")
	ProcInvalidateRect             = User32.NewProc("InvalidateRect")
	ProcUpdateWindow               = User32.NewProc("UpdateWindow")
	ProcShowWindow                 = User32.NewProc("ShowWindow")
	ProcBeginPaint                 = User32.NewProc("BeginPaint")
	ProcEndPaint                   = User32.NewProc("EndPaint")
	ProcFillRect                   = User32.NewProc("FillRect")
	ProcSetWindowTextW             = User32.NewProc("SetWindowTextW")
	ProcSendMessageW               = User32.NewProc("SendMessageW")
	ProcLoadCursorW                = User32.NewProc("LoadCursorW")
	ProcSetCursor                  = User32.NewProc("SetCursor")
	ProcSetWindowPos               = User32.NewProc("SetWindowPos")
	ProcAdjustWindowRectEx         = User32.NewProc("AdjustWindowRectEx")
	ProcGetWindowRect              = User32.NewProc("GetWindowRect")
	ProcGetClientRect              = User32.NewProc("GetClientRect")
	ProcGetWindowLongPtrW          = User32.NewProc("GetWindowLongPtrW")
	ProcSetWindowLongPtrW          = User32.NewProc("SetWindowLongPtrW")
	ProcSetLayeredWindowAttributes = User32.NewProc("SetLayeredWindowAttributes")

	ProcGetModuleHandleW = Kernel32.NewProc("GetModuleHandleW")
	ProcSetLastError     = Kernel32.NewProc("SetLastError")

	ProcGetStockObject         = Gdi32.NewProc("GetStockObject")
	ProcCreateSolidBrush       = Gdi32.NewProc("CreateSolidBrush")
	ProcPolygon                = Gdi32.NewProc("Polygon")
	ProcRoundRect              = Gdi32.NewProc("RoundRect")
	ProcStretchDIBits          = Gdi32.NewProc("StretchDIBits")
	ProcCreateFontW            = Gdi32.NewProc("CreateFontW")
	ProcSelectObject           = Gdi32.NewProc("SelectObject")
	ProcDeleteObject           = Gdi32.NewProc("DeleteObject")
	ProcSetTextColor           = Gdi32.NewProc("SetTextColor")
	ProcSetBkMode              = Gdi32.NewProc("SetBkMode")
	ProcTextOutW               = Gdi32.NewProc("TextOutW")
	ProcCreatePen              = Gdi32.NewProc("CreatePen")
	ProcMoveToEx               = Gdi32.NewProc("MoveToEx")
	ProcLineTo                 = Gdi32.NewProc("LineTo")
	ProcCreateCompatibleDC     = Gdi32.NewProc("CreateCompatibleDC")
	ProcCreateCompatibleBitmap = Gdi32.NewProc("CreateCompatibleBitmap")
	ProcBitBlt                 = Gdi32.NewProc("BitBlt")
	ProcDeleteDC               = Gdi32.NewProc("DeleteDC")
	ProcAlphaBlend             = Msimg32.NewProc("AlphaBlend")
	ProcSelectClipRgn          = Gdi32.NewProc("SelectClipRgn")
	ProcCreateRoundRectRgn     = Gdi32.NewProc("CreateRoundRectRgn")
	ProcGetStockBrush          = Gdi32.NewProc("GetStockObject")
	ProcGetDIBits              = Gdi32.NewProc("GetDIBits")
)

// ---------- 常量 ----------

const (
	CSHRedraw       = 0x0002
	CSVRedraw       = 0x0001
	WSOverlappedWin = 0x00CF0000
	WSChild         = 0x40000000
	WSVisible       = 0x10000000
	BSAutoCheckbox  = 0x0003
	SSLeft          = 0x0000
	CWUsedDefault   = uintptr(0x80000000) // CW_USEDEFAULT

	WMCommand     = 0x0111
	WMPaint       = 0x000F
	WMTimer       = 0x0113
	WMDestroy     = 0x0002
	WMSize        = 0x0005
	WMEraseBkgnd  = 0x0014
	WMPrint       = 0x0317
	WMPrintClient = 0x0318
	WMMouseMove   = 0x0200
	WMLButtonDown = 0x0201
	WMLButtonUp   = 0x0202
	WMSetCursor   = 0x0020
	HTClient      = 0x0001

	// SetWindowPos 标志
	HWNDTopMost   = uintptr(^uintptr(0))     // -1：置顶
	HWNDNoTopMost = uintptr(^uintptr(0) - 1) // -2：取消置顶
	SWPNoMove     = 0x0002
	SWPNoSize     = 0x0001
	SWPNoZOrder   = 0x0004
	SWPNoActivate = 0x0010
	SWPShowWindow = 0x0040

	GWLExStyle  = ^uintptr(19) // -20，Get/SetWindowLongPtr 的 GWLP_EXSTYLE
	WSExLayered = 0x00080000
	LWAColorKey = 0x00000001
	LWAAlpha    = 0x00000002

	WSCaption     = 0x00C00000
	WSSysMenu     = 0x00080000
	WSMinimizeBox = 0x00020000
	WSFixedFrame  = 0x00CA0000 // caption+sysmenu+minimize，无厚边框

	// 光标
	IDCArrow = 32512
	IDCHand  = 32649

	BMGetCheck = 0x00F0
	BMSetCheck = 0x00F1
	BSTChecked = 0x0001

	SWShow     = 5
	BlackBrush = 4
	IDIArrow   = 32512
	DIBRGB     = 0
	SrcCopy    = 0x00CC0020
	PMRemove   = 0x0001
	BIRgb      = 0

	NullBrushStock = 5 // NULL_BRUSH（配合笔画圆角矩形描边）
	ACSrcOver      = 0x00

	DTLeft       = 0x0000
	DTCenter     = 0x0001
	DTVCenter    = 0x0004
	DTSingleLine = 0x0020

	// GDI 文本
	Transparent    = 1
	DefaultCharSet = 1
	FWNormal       = 400
	FWBold         = 700
	ClipDefault    = 0
)

// BlendFunction 对应 Win32 BLENDFUNCTION（AlphaBlend 用）。
type BlendFunction struct {
	BlendOp         byte
	BlendFlags      byte
	SourceConstantA byte
	AlphaFormat     byte
}

// ---------- 绘制辅助 ----------

// DrawImage 用 StretchDIBits 把 RGBA 图绘制到指定矩形（自顶向下、BGRA 交换）。
func DrawImage(hdc uintptr, img *image.RGBA, dx, dy, dw, dh int32) {
	iw, ih := img.Bounds().Dx(), img.Bounds().Dy()
	if iw <= 0 || ih <= 0 {
		return
	}

	var bmi BitmapInfo
	bmi.Header.Size = uint32(unsafe.Sizeof(BitmapInfoHeader{}))
	bmi.Header.Width = int32(iw)
	bmi.Header.Height = -int32(ih) // 自顶向下
	bmi.Header.Planes = 1
	bmi.Header.BitCount = 32
	bmi.Header.Compression = BIRgb

	// DIB 像素为 BGRA，需交换 R/B 后拷贝
	pix := make([]byte, iw*ih*4)
	src := img.Pix
	for i := 0; i < iw*ih; i++ {
		j := i * 4
		pix[j] = src[j+2] // B
		pix[j+1] = src[j+1]
		pix[j+2] = src[j] // R
		pix[j+3] = 0xFF
	}

	ProcStretchDIBits.Call(
		hdc,
		uintptr(dx), uintptr(dy), uintptr(dw), uintptr(dh),
		0, 0, uintptr(iw), uintptr(ih),
		uintptr(unsafe.Pointer(&pix[0])),
		uintptr(unsafe.Pointer(&bmi)),
		DIBRGB, SrcCopy,
	)
}

// fontCache 进程级字体句柄缓存：paint 每帧调用 DrawText 十几次，
// 每次都 CreateFontW/DeleteObject 是浪费，按 (height,weight,face) 缓存。
// 仅主线程（paint）访问，无需加锁。句柄不主动释放，进程退出时系统回收。
var fontCache = map[string]uintptr{}

func getFont(height, weight int, face string) uintptr {
	key := fmt.Sprintf("%d|%d|%s", height, weight, face)
	if h, ok := fontCache[key]; ok {
		return h
	}
	facePtr, _ := syscall.UTF16PtrFromString(face)
	hFont, _, _ := ProcCreateFontW.Call(
		uintptr(int32(-height)), // cHeight（负值 = 字符高度）
		0, 0, 0,                 // cWidth, cEscapement, cOrientation
		uintptr(weight), // cWeight
		0, 0, 0,         // bItalic, bUnderline, bStrikeOut
		DefaultCharSet, // iCharSet
		0, 0, 0, 0,     // iOutPrecision, iClipPrecision, iQuality, iPitchAndFamily
		uintptr(unsafe.Pointer(facePtr)), // pszFaceName
	)
	if hFont != 0 {
		fontCache[key] = hFont
	}
	return hFont
}

// DrawText 用 GDI 字体在 hdc 上绘制 UTF-8 文本（支持中文）。
// height > 0 为字符像素高度；weight 用 FWNormal/FWBold；face 如 "Microsoft YaHei UI"。
func DrawText(hdc uintptr, s string, x, y, height, weight int, c color.RGBA, face string) {
	if s == "" || hdc == 0 {
		return
	}
	hFont := getFont(height, weight, face)
	if hFont == 0 {
		return
	}
	old, _, _ := ProcSelectObject.Call(hdc, hFont)
	u16 := syscall.StringToUTF16(s) // 注意：字符数 = len(u16)，不是 len(s)！
	ProcSetTextColor.Call(hdc,
		uintptr(uint32(c.B)<<16|uint32(c.G)<<8|uint32(c.R)))
	ProcSetBkMode.Call(hdc, Transparent)
	ProcTextOutW.Call(hdc, uintptr(x), uintptr(y),
		uintptr(unsafe.Pointer(&u16[0])), uintptr(len(u16)-1)) // 去掉结尾 NUL
	ProcSelectObject.Call(hdc, old)
}

// RectOutline 用单像素笔绘制矩形边框。
func RectOutline(hdc uintptr, x0, y0, x1, y1 int, c color.RGBA) {
	if hdc == 0 {
		return
	}
	col := uintptr(uint32(c.B)<<16 | uint32(c.G)<<8 | uint32(c.R))
	pen, _, _ := ProcCreatePen.Call(0, 1, col) // PS_SOLID
	if pen == 0 {
		return
	}
	old, _, _ := ProcSelectObject.Call(hdc, pen)
	ProcMoveToEx.Call(hdc, uintptr(x0), uintptr(y0), 0)
	ProcLineTo.Call(hdc, uintptr(x1), uintptr(y0))
	ProcLineTo.Call(hdc, uintptr(x1), uintptr(y1))
	ProcLineTo.Call(hdc, uintptr(x0), uintptr(y1))
	ProcLineTo.Call(hdc, uintptr(x0), uintptr(y0))
	ProcSelectObject.Call(hdc, old)
	ProcDeleteObject.Call(pen)
}

// ---------- 结构 ----------

// WndClassExW 对应 Win32 WNDCLASSEXW（x64 布局）。
type WndClassExW struct {
	Size       uint32
	Style      uint32
	WndProc    uintptr
	ClsExtra   int32
	WndExtra   int32
	Instance   uintptr
	Icon       uintptr
	Cursor     uintptr
	Background uintptr
	MenuName   *uint16
	ClassName  *uint16
	IconSm     uintptr
}

// Msg 对应 Win32 MSG（x64 布局）。
type Msg struct {
	Hwnd    uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	PtX     int32
	PtY     int32
}

// PaintStruct 对应 Win32 PAINTSTRUCT（x64 布局，reserved 32 字节）。
type PaintStruct struct {
	Hdc      uintptr
	FErase   int32
	RcLeft   int32
	RcTop    int32
	RcRight  int32
	RcBottom int32
	FUpdate  int32
	Reserved [32]byte
}

// Rect 对应 Win32 RECT。
type Rect struct {
	Left, Top, Right, Bottom int32
}

func (r Rect) Width() int32  { return r.Right - r.Left }
func (r Rect) Height() int32 { return r.Bottom - r.Top }

// WindowSizeForClient 把客户区尺寸换成带标题栏的外框尺寸。
func WindowSizeForClient(cw, ch int, style uint32) (int, int) {
	r := Rect{Right: int32(cw), Bottom: int32(ch)}
	ProcAdjustWindowRectEx.Call(uintptr(unsafe.Pointer(&r)), uintptr(style), 0, 0)
	return int(r.Width()), int(r.Height())
}

// BitmapInfoHeader 对应 Win32 BITMAPINFOHEADER。
type BitmapInfoHeader struct {
	Size          uint32
	Width         int32
	Height        int32
	Planes        uint16
	BitCount      uint16
	Compression   uint32
	SizeImage     uint32
	XPelsPerMeter int32
	YPelsPerMeter int32
	ClrUsed       uint32
	ClrImportant  uint32
}

// BitmapInfo 对应 Win32 BITMAPINFO。
type BitmapInfo struct {
	Header BitmapInfoHeader
	Colors [1]struct{ B, G, R, A byte }
}

// ---------- 弹窗与错误提示 ----------

var (
	ProcMessageBoxW      = User32.NewProc("MessageBoxW")
	ProcGetConsoleWindow = syscall.NewLazyDLL("kernel32.dll").NewProc("GetConsoleWindow")
)

// HasConsole 判断进程是否带控制台（-H windowsgui 构建时无控制台）。
func HasConsole() bool {
	r, _, _ := ProcGetConsoleWindow.Call()
	return r != 0
}

// SetWindowOpacity changes one known native window. It deliberately uses the
// HWND supplied by Fyne instead of a title lookup, so another timer instance
// cannot accidentally receive the setting. Color-key transparency is never
// used: the OpenGL canvas must remain clickable.
func SetWindowOpacity(hwnd uintptr, opacity float64) error {
	if hwnd == 0 {
		return fmt.Errorf("window handle is unavailable")
	}
	if opacity <= 0 || opacity > 1.00 {
		return fmt.Errorf("opacity %.2f is outside (0,1.00]", opacity)
	}
	// Get/SetWindowLongPtrW may validly return zero. Clear LastError first so
	// that zero can be distinguished from a real Win32 failure.
	ProcSetLastError.Call(0)
	style, _, callErr := ProcGetWindowLongPtrW.Call(hwnd, GWLExStyle)
	if style == 0 && callErr != nil && callErr != syscall.Errno(0) {
		return fmt.Errorf("read window style: %w", callErr)
	}
	next := style
	if opacity < 1 {
		next |= WSExLayered
	} else {
		next &^= WSExLayered
	}
	if next != style {
		ProcSetLastError.Call(0)
		previous, _, callErr := ProcSetWindowLongPtrW.Call(hwnd, GWLExStyle, next)
		if previous == 0 && callErr != nil && callErr != syscall.Errno(0) {
			return fmt.Errorf("update window style: %w", callErr)
		}
	}
	if opacity == 1 {
		return nil
	}
	alpha := byte(opacity*255 + 0.5)
	if alpha == 0 {
		alpha = 1
	}
	ok, _, callErr := ProcSetLayeredWindowAttributes.Call(hwnd, 0, uintptr(alpha), LWAAlpha)
	if ok == 0 {
		if callErr == nil || callErr == syscall.Errno(0) {
			return fmt.Errorf("set window opacity failed")
		}
		return fmt.Errorf("set window opacity: %w", callErr)
	}
	return nil
}

// ApplyWindowAlpha remains for the older Win32 callers. New Fyne code must use
// SetWindowOpacity with its actual HWND rather than a globally searchable title.
func ApplyWindowAlpha(title string, alpha byte) {
	ptr, err := syscall.UTF16PtrFromString(title)
	if err != nil {
		return
	}
	hwnd, _, _ := User32.NewProc("FindWindowW").Call(0, uintptr(unsafe.Pointer(ptr)))
	if hwnd == 0 {
		return
	}
	if alpha == 0 {
		alpha = 1
	}
	_ = SetWindowOpacity(hwnd, float64(alpha)/255)
}

// MsgBoxError 弹错误对话框（GUI 模式下代替 stderr 输出）。
func MsgBoxError(title, text string) {
	titlePtr, _ := syscall.UTF16PtrFromString(title)
	textPtr, _ := syscall.UTF16PtrFromString(text)
	const mbIconError = 0x10
	ProcMessageBoxW.Call(0, uintptr(unsafe.Pointer(textPtr)),
		uintptr(unsafe.Pointer(titlePtr)), mbIconError)
}

// FillRoundRect 用纯色画圆角矩形（brush 自动创建/销毁）。
func FillRoundRect(hdc uintptr, x0, y0, x1, y1, rx, ry int32, c color.RGBA) {
	col := uint32(c.B)<<16 | uint32(c.G)<<8 | uint32(c.R)
	brush, _, _ := ProcCreateSolidBrush.Call(uintptr(col))
	if brush == 0 {
		return
	}
	old, _, _ := ProcSelectObject.Call(hdc, brush)
	ProcRoundRect.Call(hdc, uintptr(x0), uintptr(y0), uintptr(x1), uintptr(y1), uintptr(rx), uintptr(ry))
	ProcSelectObject.Call(hdc, old)
	ProcDeleteObject.Call(brush)
}

// FillDiamond 用纯色画填充菱形（中心 cx,cy，半宽 hw，半高 hh）。
func FillDiamond(hdc uintptr, cx, cy, hw, hh int32, c color.RGBA) {
	col := uint32(c.B)<<16 | uint32(c.G)<<8 | uint32(c.R)
	brush, _, _ := ProcCreateSolidBrush.Call(uintptr(col))
	if brush == 0 {
		return
	}
	old, _, _ := ProcSelectObject.Call(hdc, brush)
	pts := [4][2]int32{{cx, cy - hh}, {cx + hw, cy}, {cx, cy + hh}, {cx - hw, cy}}
	var raw [8]int32
	for i, p := range pts {
		raw[i*2] = p[0]
		raw[i*2+1] = p[1]
	}
	ProcPolygon.Call(hdc, uintptr(unsafe.Pointer(&raw[0])), 4)
	ProcSelectObject.Call(hdc, old)
	ProcDeleteObject.Call(brush)
}

// DrawImageAlpha 用 AlphaBlend 把带 alpha 通道的 RGBA 图合成到 hdc 指定矩形。
// 与 DrawImage（StretchDIBits，忽略 alpha）不同，本函数支持半透明面板/辉光。
func DrawImageAlpha(hdc uintptr, img *image.RGBA, dx, dy, dw, dh int32) {
	iw, ih := img.Bounds().Dx(), img.Bounds().Dy()
	if iw <= 0 || ih <= 0 || dw <= 0 || dh <= 0 {
		return
	}

	var bmi BitmapInfo
	bmi.Header.Size = uint32(unsafe.Sizeof(BitmapInfoHeader{}))
	bmi.Header.Width = int32(iw)
	bmi.Header.Height = -int32(ih) // 自顶向下
	bmi.Header.Planes = 1
	bmi.Header.BitCount = 32
	bmi.Header.Compression = BIRgb

	// AlphaBlend 要求预乘 BGRA 像素
	pix := make([]byte, iw*ih*4)
	src := img.Pix
	for i := 0; i < iw*ih; i++ {
		j := i * 4
		a := uint32(src[j+3])
		pix[j] = byte(uint32(src[j+2]) * a / 255)   // B
		pix[j+1] = byte(uint32(src[j+1]) * a / 255) // G
		pix[j+2] = byte(uint32(src[j]) * a / 255)   // R
		pix[j+3] = src[j+3]                         // A
	}

	sdc, _, _ := ProcCreateCompatibleDC.Call(hdc)
	if sdc == 0 {
		return
	}
	bmp, _, _ := ProcCreateCompatibleBitmap.Call(hdc, uintptr(iw), uintptr(ih))
	if bmp == 0 {
		ProcDeleteDC.Call(sdc)
		return
	}
	old, _, _ := ProcSelectObject.Call(sdc, bmp)
	ProcStretchDIBits.Call(
		sdc, 0, 0, uintptr(iw), uintptr(ih),
		0, 0, uintptr(iw), uintptr(ih),
		uintptr(unsafe.Pointer(&pix[0])),
		uintptr(unsafe.Pointer(&bmi)),
		DIBRGB, SrcCopy,
	)
	var bf BlendFunction
	bf.BlendOp = ACSrcOver
	bf.SourceConstantA = 255
	bf.AlphaFormat = 1 // AC_SRC_ALPHA
	ProcAlphaBlend.Call(
		hdc, uintptr(dx), uintptr(dy), uintptr(dw), uintptr(dh),
		sdc, 0, 0, uintptr(iw), uintptr(ih),
		uintptr(*(*uint32)(unsafe.Pointer(&bf))),
	)
	ProcSelectObject.Call(sdc, old)
	ProcDeleteObject.Call(bmp)
	ProcDeleteDC.Call(sdc)
}

// StrokeRoundRect 画圆角矩形描边（1px 笔，空心画刷）。
func StrokeRoundRect(hdc uintptr, x0, y0, x1, y1, rx, ry int32, c color.RGBA) {
	col := uint32(c.B)<<16 | uint32(c.G)<<8 | uint32(c.R)
	pen, _, _ := ProcCreatePen.Call(0, 1, uintptr(col))
	if pen == 0 {
		return
	}
	nullBrush, _, _ := ProcGetStockBrush.Call(NullBrushStock)
	oldPen, _, _ := ProcSelectObject.Call(hdc, pen)
	oldBrush, _, _ := ProcSelectObject.Call(hdc, nullBrush)
	ProcRoundRect.Call(hdc, uintptr(x0), uintptr(y0), uintptr(x1), uintptr(y1), uintptr(rx), uintptr(ry))
	ProcSelectObject.Call(hdc, oldBrush)
	ProcSelectObject.Call(hdc, oldPen)
	ProcDeleteObject.Call(pen)
}
