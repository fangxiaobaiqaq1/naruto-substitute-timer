//go:build gdiui

// 预览模式辅助：设置 NARUTO_PREVIEW=panel|demo|placeholder|about 后启动，
// 首次 paint 完成后把客户区存为 preview.png 并退出（不弹交互界面），供 UI 审核。
package app

import (
	"image"
	"image/png"
	"os"
	"unsafe"

	"narutotimer/internal/frame"
	"narutotimer/internal/win32"
)

var procGetCurrentObject = win32.Gdi32.NewProc("GetCurrentObject")

// previewDemoBeads 返回一组模拟豆（左 2 可用 + 1 金色充能，右 1 可用）。
func previewDemoBeads() []frame.Bead {
	return []frame.Bead{
		{Label: "L1", Dark: false}, {Label: "L2", Dark: false},
		{Label: "L3", Gold: true}, {Label: "L4", Dark: true},
		{Label: "R1", Dark: false}, {Label: "R2", Dark: true},
		{Label: "R3", Dark: true}, {Label: "R4", Dark: true},
	}
}

// saveDCAsPNG 把内存 DC 当前选中的位图读出并存为 PNG（BGRA→RGBA）。
// 调用方保证 hdc 里选中了 w×h 的 32 位兼容位图。
func saveDCAsPNG(hdc uintptr, w, h int, name string) {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	var bmi win32.BitmapInfo
	bmi.Header.Size = uint32(unsafe.Sizeof(win32.BitmapInfoHeader{}))
	bmi.Header.Width = int32(w)
	bmi.Header.Height = -int32(h)
	bmi.Header.Planes = 1
	bmi.Header.BitCount = 32
	bmi.Header.Compression = win32.BIRgb
	cur, _, _ := procGetCurrentObject.Call(hdc, 7) // OBJ_BITMAP=7
	if cur == 0 {
		return
	}
	r, _, _ := win32.ProcGetDIBits.Call(hdc, cur, 0, uintptr(h),
		uintptr(unsafe.Pointer(&img.Pix[0])), uintptr(unsafe.Pointer(&bmi)), win32.DIBRGB)
	if r == 0 {
		return
	}
	for i := 0; i < w*h; i++ {
		j := i * 4
		img.Pix[j], img.Pix[j+2] = img.Pix[j+2], img.Pix[j]
		img.Pix[j+3] = 255
	}
	f, err := os.Create(name)
	if err != nil {
		return
	}
	defer f.Close()
	png.Encode(f, img)
}
