//go:build windows && amd64

package mumu

import (
	"path/filepath"
	"testing"
)

func TestProbeRecordsEndTimeOnFailure(t *testing.T) {
	result := Probe(Options{InstallDir: filepath.Join(t.TempDir(), "missing-mumu")})
	if result.EndedAt.IsZero() {
		t.Fatal("Probe left EndedAt unset")
	}
	if result.EndedAt.Before(result.StartedAt) {
		t.Fatalf("Probe ended before it started: start=%s end=%s", result.StartedAt, result.EndedAt)
	}
	if result.FailureStage != ProbeStageFindSDK {
		t.Fatalf("FailureStage = %q, want %q", result.FailureStage, ProbeStageFindSDK)
	}
}
