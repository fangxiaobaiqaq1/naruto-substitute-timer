package win

import (
	"errors"
	"image"
	"image/color"
	"syscall"
	"unsafe"
)

var (
	gdi32 = syscall.NewLazyDLL("gdi32.dll")

	procCreateCompatibleDC = gdi32.NewProc("CreateCompatibleDC")
	procCreateDIBSection   = gdi32.NewProc("CreateDIBSection")
	procSelectObject       = gdi32.NewProc("SelectObject")
	procDeleteDC           = gdi32.NewProc("DeleteDC")
	procDeleteObject       = gdi32.NewProc("DeleteObject")

	user32GetDC       = user32.NewProc("GetDC")
	user32ReleaseDC   = user32.NewProc("ReleaseDC")
	user32PrintWindow = user32.NewProc("PrintWindow")
)

const (
	biRGB       = 0
	dibRGBColor = 0
	// pwRenderFullContent 让 PrintWindow 抓取 GPU 渲染/被遮挡窗口的内容（Win8.1+）。
	pwRenderFullContent = 0x00000002
)

type bmiHeader struct {
	BiSize          uint32
	BiWidth         int32
	BiHeight        int32
	BiPlanes        uint16
	BiBitCount      uint16
	BiCompression   uint32
	BiSizeImage     uint32
	BiXPelsPerMeter int32
	BiYPelsPerMeter int32
	BiClrUsed       uint32
	BiClrImportant  uint32
}

// CaptureClient 截取窗口客户区。
//
// 只抓目标窗口自身：PrintWindow 整窗，再裁客户区。
// 不用 BitBlt：那是屏幕合成，被其它窗口挡住就会采到覆盖物。
// PrintWindow 若按客户区尺寸画，会把标题栏压进画面、豆位整体下移。
func CaptureClient(hwnd uintptr) (*image.RGBA, error) {
	wr, err := getWindowRect(hwnd)
	if err != nil {
		return nil, err
	}
	cr, err := ClientScreenRect(hwnd)
	if err != nil {
		return nil, err
	}
	ww, wh := int(wr.Width()), int(wr.Height())
	cw, ch := int(cr.Width()), int(cr.Height())
	if ww <= 0 || wh <= 0 || cw <= 0 || ch <= 0 {
		return nil, errors.New("窗口客户区尺寸无效")
	}
	ox := int(cr.Left - wr.Left)
	oy := int(cr.Top - wr.Top)
	if ox < 0 {
		ox = 0
	}
	if oy < 0 {
		oy = 0
	}
	if ox+cw > ww {
		cw = ww - ox
	}
	if oy+ch > wh {
		ch = wh - oy
	}
	if cw <= 0 || ch <= 0 {
		return nil, errors.New("客户区裁切无效")
	}

	screenDC, _, _ := user32GetDC.Call(0)
	if screenDC == 0 {
		return nil, errors.New("获取屏幕 DC 失败")
	}
	defer user32ReleaseDC.Call(0, screenDC)

	memDC, _, _ := procCreateCompatibleDC.Call(screenDC)
	if memDC == 0 {
		return nil, errors.New("创建内存 DC 失败")
	}
	defer procDeleteDC.Call(memDC)

	header := bmiHeader{
		BiSize:        uint32(unsafe.Sizeof(bmiHeader{})),
		BiWidth:       int32(ww),
		BiHeight:      -int32(wh),
		BiPlanes:      1,
		BiBitCount:    32,
		BiCompression: biRGB,
	}
	var bitsPtr unsafe.Pointer
	bmp, _, _ := procCreateDIBSection.Call(screenDC,
		uintptr(unsafe.Pointer(&header)),
		dibRGBColor,
		uintptr(unsafe.Pointer(&bitsPtr)),
		0, 0)
	if bmp == 0 {
		return nil, errors.New("创建 DIB 位图失败")
	}
	defer procDeleteObject.Call(bmp)
	procSelectObject.Call(memDC, bmp)

	ok, _, _ := user32PrintWindow.Call(hwnd, memDC, pwRenderFullContent)
	if ok == 0 {
		return nil, errors.New("PrintWindow 失败（未回退屏幕截图，避免采到覆盖窗口）")
	}
	// 拖动/缩放期间客户区屏幕矩形会变，这一帧不可信。
	if wr2, err2 := getWindowRect(hwnd); err2 == nil {
		if wr2.Width() != wr.Width() || wr2.Height() != wr.Height() ||
			abs32(wr2.Left-wr.Left) > 2 || abs32(wr2.Top-wr.Top) > 2 {
			return nil, errWindowBusy
		}
	}
	if cr2, err2 := ClientScreenRect(hwnd); err2 == nil {
		if cr2.Width() != cr.Width() || cr2.Height() != cr.Height() ||
			abs32(cr2.Left-cr.Left) > 2 || abs32(cr2.Top-cr.Top) > 2 {
			return nil, errWindowBusy
		}
	}

	src := unsafe.Slice((*byte)(bitsPtr), ww*wh*4)
	img := image.NewRGBA(image.Rect(0, 0, cw, ch))
	for y := 0; y < ch; y++ {
		sy := oy + y
		if sy < 0 || sy >= wh {
			continue
		}
		for x := 0; x < cw; x++ {
			sx := ox + x
			if sx < 0 || sx >= ww {
				continue
			}
			i := (sy*ww + sx) * 4
			img.SetRGBA(x, y, color.RGBA{
				R: src[i+2],
				G: src[i+1],
				B: src[i],
				A: 255,
			})
		}
	}
	if imageLooksBlank(img) {
		return nil, errors.New("截到空画面，丢弃本帧")
	}
	return img, nil
}

var errWindowBusy = errors.New("窗口正在移动或缩放，丢弃本帧")

func IsBusyFrame(err error) bool {
	return err != nil && errors.Is(err, errWindowBusy)
}

func abs32(v int32) int32 {
	if v < 0 {
		return -v
	}
	return v
}

func imageLooksBlank(img *image.RGBA) bool {
	if img == nil {
		return true
	}
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= 0 || h <= 0 {
		return true
	}
	var sum, n int
	for y := b.Min.Y; y < b.Max.Y; y += 16 {
		for x := b.Min.X; x < b.Max.X; x += 16 {
			c := img.RGBAAt(x, y)
			sum += int(c.R) + int(c.G) + int(c.B)
			n++
		}
	}
	return n == 0 || sum/n < 18
}
