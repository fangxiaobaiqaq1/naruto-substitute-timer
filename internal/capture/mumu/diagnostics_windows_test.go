//go:build windows && amd64

package mumu

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

func TestProbeJSONRedactsPathsAndHelperOutput(t *testing.T) {
	installRoot := `C:\Users\Private\AppData\Local\Temp\MuMu`
	dllPath := installRoot + `\nx_main\sdk\external_renderer_ipc.dll`
	uncPath := `\\server\share\MuMu\MuMuManager.exe`
	token := "token=private-probe-token"
	result := ProbeResult{
		StartedAt: time.Now(), EndedAt: time.Now(),
		Requested:     Options{InstallDir: installRoot, DLLPath: dllPath, Package: "com.private.target", Instance: 2, DisplayID: 1, Manual: true},
		ResolvedRoot:  installRoot,
		SDKCandidates: []SDKFile{{Path: dllPath, Size: 123, SHA256: "hash", Error: "stderr " + token}},
		SelectedSDK:   SDKFile{Path: dllPath, Size: 123},
		FailureStage:  ProbeStageConnect, Error: "helper output " + uncPath + " " + token,
		Steps: []ProbeStep{
			{Stage: ProbeStageResolveRoot, Detail: installRoot},
			{Stage: ProbeStageFindSDK, Detail: dllPath},
			{Stage: ProbeStageConnect, Detail: "MuMu · " + installRoot, Error: "stdout " + token},
			{Stage: ProbeStageCapture, Detail: "1600 × 900"},
		},
	}
	data, err := json.Marshal(result.SanitizedForExport())
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, forbidden := range []string{installRoot, dllPath, uncPath, token, "helper output", "stderr", "com.private.target"} {
		if strings.Contains(text, forbidden) {
			t.Fatal("private probe value leaked")
		}
	}
	for _, want := range []string{ProbeStageResolveRoot, ProbeStageFindSDK, ProbeStageConnect, ProbeStageCapture, "connected", "1600 × 900", "probe_failed"} {
		if !strings.Contains(text, want) {
			t.Fatalf("sanitized probe lost %q: %s", want, text)
		}
	}
}
