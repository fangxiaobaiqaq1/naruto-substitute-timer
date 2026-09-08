//go:build opencv

package cvbind

/*
#cgo CXXFLAGS: -std=c++17 -IF:/opencv/install/include
#cgo LDFLAGS: -LF:/opencv/install/x64/mingw/lib -lopencv_imgproc4130 -lopencv_core4130 -lstdc++
#include "cvbind.h"
*/
import "C"

import (
	"fmt"
	"image"
	"unsafe"

	"narutotimer/internal/config"
)

type Sample struct {
	X, Y                int
	Light, Dark, Gold, Other int
}

func AnalyzeBeads(img *image.RGBA, xs, ys []int, sampleW, sampleH, searchRadius int, cfg config.VisionConfig) ([]Sample, error) {
	if img == nil || len(xs) == 0 || len(xs) != len(ys) {
		return nil, fmt.Errorf("invalid bead sample input")
	}
	n := len(xs)
	cx := make([]C.int, n)
	cy := make([]C.int, n)
	for i := 0; i < n; i++ {
		cx[i] = C.int(xs[i])
		cy[i] = C.int(ys[i])
	}
	ranges := packRanges(cfg)
	outL := make([]C.int, n)
	outD := make([]C.int, n)
	outG := make([]C.int, n)
	outO := make([]C.int, n)
	outX := make([]C.int, n)
	outY := make([]C.int, n)

	rc := C.cv_analyze_beads(
		(*C.uchar)(unsafe.Pointer(&img.Pix[0])),
		C.int(img.Bounds().Dx()),
		C.int(img.Bounds().Dy()),
		C.int(img.Stride),
		(*C.int)(unsafe.Pointer(&cx[0])),
		(*C.int)(unsafe.Pointer(&cy[0])),
		C.int(n),
		C.int(sampleW),
		C.int(sampleH),
		C.int(searchRadius),
		(*C.double)(unsafe.Pointer(&ranges[0])),
		C.int(len(cfg.Dark)),
		C.int(len(cfg.Light)),
		C.int(len(cfg.Gold)),
		(*C.int)(unsafe.Pointer(&outL[0])),
		(*C.int)(unsafe.Pointer(&outD[0])),
		(*C.int)(unsafe.Pointer(&outG[0])),
		(*C.int)(unsafe.Pointer(&outO[0])),
		(*C.int)(unsafe.Pointer(&outX[0])),
		(*C.int)(unsafe.Pointer(&outY[0])),
	)
	if rc != 0 {
		return nil, fmt.Errorf("opencv analyze failed: %d", int(rc))
	}
	out := make([]Sample, n)
	for i := 0; i < n; i++ {
		out[i] = Sample{
			X:     int(outX[i]),
			Y:     int(outY[i]),
			Light: int(outL[i]),
			Dark:  int(outD[i]),
			Gold:  int(outG[i]),
			Other: int(outO[i]),
		}
	}
	return out, nil
}

func packRanges(cfg config.VisionConfig) []C.double {
	all := append(append(append([]config.HSVRange{}, cfg.Dark...), cfg.Light...), cfg.Gold...)
	out := make([]C.double, 0, len(all)*6)
	for _, r := range all {
		out = append(out,
			C.double(r.HMin), C.double(r.HMax),
			C.double(r.SMin), C.double(r.SMax),
			C.double(r.VMin), C.double(r.VMax),
		)
	}
	if len(out) == 0 {
		out = []C.double{0}
	}
	return out
}
