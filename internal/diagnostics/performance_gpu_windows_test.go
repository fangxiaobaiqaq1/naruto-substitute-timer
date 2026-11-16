//go:build windows

package diagnostics

import (
	"runtime"
	"strings"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestPerformancePDHNativeBuffer(t *testing.T) {
	name := "pid_123_luid_0x00000000_0x000012ab_phys_0_eng_2_engtype_3D"
	encoded, err := windows.UTF16FromString(name)
	if err != nil {
		t.Fatal(err)
	}
	size := uint32(24 + len(encoded)*2)
	buffer := make([]uint64, (int(size)+7)/8)
	base := unsafe.Pointer(&buffer[0])
	item := (*performancePDHItem)(base)
	item.Name = uintptr(unsafe.Add(base, 24))
	item.Value = performancePDHValue{Status: 1, Double: 37.5}
	copy(unsafe.Slice((*uint16)(unsafe.Add(base, 24)), len(encoded)), encoded)
	calls := 0
	visit := func(got string, v float64, status uint32) {
		calls++
		if got != name || v != 37.5 || status != 1 {
			t.Errorf("native row = %q, %v, %v", got, v, status)
		}
	}
	if reason := performanceWindowsPDHVisitBuffer(buffer, size, 1, visit); reason != "" || calls != 1 {
		t.Fatalf("native buffer decode: reason=%q calls=%d", reason, calls)
	}
	for _, address := range []uintptr{0, uintptr(base) - 2, uintptr(base), uintptr(base) + 22, uintptr(base) + 25, uintptr(base) + uintptr(size)} {
		item.Name = address
		if reason := performanceWindowsPDHVisitBuffer(buffer, size, 1, visit); reason == "" {
			t.Errorf("accepted invalid name pointer %#x", address)
		}
	}
	item.Name = uintptr(unsafe.Add(base, 24))
	for _, tc := range []struct{ size, count uint32 }{
		{23, 1}, {uint32(len(buffer)*8 + 1), 1}, {size, performancePDHMaxItems + 1}, {size, ^uint32(0)},
	} {
		if reason := performanceWindowsPDHVisitBuffer(buffer, tc.size, tc.count, visit); reason == "" {
			t.Errorf("accepted invalid size/count: %+v", tc)
		}
	}
	if reason := performanceWindowsPDHVisitBuffer(nil, 1, 1, visit); reason == "" {
		t.Fatal("accepted nil buffer")
	}
	runtime.KeepAlive(buffer)
}

func TestPerformancePDHNativeStringBound(t *testing.T) {
	const size = 24 + 258*2
	buffer := make([]uint64, (size+7)/8)
	base := unsafe.Pointer(&buffer[0])
	item := (*performancePDHItem)(base)
	item.Name = uintptr(unsafe.Add(base, 24))
	name := unsafe.Slice((*uint16)(unsafe.Add(base, 24)), 258)
	for i := range name {
		name[i] = 'a'
	}
	name[257] = 0 // NUL exists but exceeds the permitted bound.
	if reason := performanceWindowsPDHVisitBuffer(buffer, size, 1, func(string, float64, uint32) { t.Fatal("visited an overlong instance") }); reason == "" {
		t.Fatal("unbounded native string scan")
	}
	runtime.KeepAlive(buffer)
}

func TestPerformancePDHUnavailableReasons(t *testing.T) {
	for _, tc := range []struct {
		code     uint32
		fragment string
	}{
		{performancePDHNoData, "no instances/data"},
		{0xc0000bb8, "WDDM"}, {0xc0000bb9, "WDDM"},
		{0xc0000bdb, "access denied"}, {5, "access denied"},
		{0xc0000bba, "warm-up"}, {0x800007d7, "warm-up"},
	} {
		if reason := performancePDHStatus("test", tc.code); !strings.Contains(reason, tc.fragment) {
			t.Errorf("status %#x = %q, missing %q", tc.code, reason, tc.fragment)
		}
	}
	if reason := performanceWindowsPDHReadArray(0, nil, func(string, float64, uint32) { t.Fatal("visited missing counter") }); reason == "" {
		t.Fatal("missing counter is not an available zero")
	}
	if reason := performanceWindowsPDHReadArray(1, func() bool { return true }, func(string, float64, uint32) { t.Fatal("visited canceled counter") }); !strings.Contains(reason, "canceled") {
		t.Fatal("canceled request must not enter another native PDH call")
	}
}
