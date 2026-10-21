package capture

import (
	"reflect"
	"testing"
	"time"

	"narutotimer/internal/config"
)

func TestLeidianMethodChain(t *testing.T) {
	for _, tc := range []struct {
		name      string
		preferred []string
		want      []string
	}{
		{"default MuMu config", []string{MethodMuMuSDK}, []string{MethodLeidianADBRaw, MethodLeidianADB}},
		{"empty", nil, []string{MethodLeidianADBRaw, MethodLeidianADB}},
		{"explicit adb only", []string{MethodLeidianADB}, []string{MethodLeidianADB}},
		{"ordered with others", []string{MethodMuMuSDK, MethodLeidianADBRaw, MethodPrintWindow, MethodLeidianADB, MethodLeidianADBRaw}, []string{MethodLeidianADBRaw, MethodLeidianADB}},
	} {
		got := LeidianMethodChain(config.CaptureConfig{PreferredMethods: tc.preferred})
		if !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("%s: got %v want %v", tc.name, got, tc.want)
		}
	}
}

func TestLeidianCaptureTimeout(t *testing.T) {
	for ms, want := range map[int]time.Duration{1200: 1100 * time.Millisecond, 350: 300 * time.Millisecond, 0: 300 * time.Millisecond} {
		if got := LeidianCaptureTimeout(ms); got != want {
			t.Fatalf("timeout %d: got %v want %v", ms, got, want)
		}
	}
}
