//go:build windows && amd64

package support

import (
	"archive/zip"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"narutotimer/internal/capture/mumu"
	"narutotimer/internal/config"
	"narutotimer/internal/frame"
)

func zipText(t *testing.T, path string) map[string]string {
	t.Helper()
	z, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer z.Close()
	out := make(map[string]string, len(z.File))
	for _, file := range z.File {
		reader, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, readErr := io.ReadAll(reader)
		closeErr := reader.Close()
		if readErr != nil || closeErr != nil {
			t.Fatalf("read %s: %v %v", file.Name, readErr, closeErr)
		}
		out[file.Name] = string(data)
	}
	return out
}

func imageMember(name string) bool {
	ext := strings.ToLower(filepath.Ext(name))
	return ext == ".png" || ext == ".jpg" || ext == ".jpeg"
}

func jsonText(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestExportBundleAllowListsSessionFiles(t *testing.T) {
	root := t.TempDir()
	session := filepath.Join(root, "session")
	if err := os.MkdirAll(session, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, text := range map[string]string{
		"session.json": "{}", "capture.jsonl": "{}\n", "report.json": "{}", "report.md": "# report", "secret.txt": "must not appear", "raw.png": "not included by default",
	} {
		if err := os.WriteFile(filepath.Join(session, name), []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	path, err := ExportBundle(context.Background(), BundleOptions{Root: root, SessionDir: session, Config: config.Default(), ConfigPath: filepath.Join(root, "missing-config.json"), Version: "v0.2.2"})
	if err != nil {
		t.Fatal(err)
	}
	members := zipText(t, path)
	for _, want := range []string{"application.json", "environment.json", "config/runtime.json", "config/saved.json", "capture/state.json", "mumu/inventory.json", "summary.md", "manifest.json", "session/session.json", "session/capture.jsonl", "session/report.json", "session/report.md"} {
		if _, ok := members[want]; !ok {
			t.Fatalf("missing %s: %v", want, members)
		}
	}
	for _, forbidden := range []string{"session/secret.txt", "session/raw.png", "session/replay/frame.jpg"} {
		if _, ok := members[forbidden]; ok {
			t.Fatalf("unexpected private/raw file: %v", forbidden)
		}
	}
}

func TestExportBundlePrivacyAllowlist(t *testing.T) {
	root := t.TempDir()
	session := filepath.Join(root, "session")
	if err := os.MkdirAll(filepath.Join(session, "replay"), 0o700); err != nil {
		t.Fatal(err)
	}
	secretPath := `C:\Users\Private\AppData\Local\Temp\MuMu\nx_main\external_renderer_ipc.dll`
	uncPath := `\\server\share\MuMu\MuMuManager.exe`
	token := "token=not-for-export"
	requestID := "request_id=5e16b660eeea4d46bf6fa9c0802c88cc"
	serial := "127.0.0.1:5555"
	packageName := "com.private.naruto"
	for name, text := range map[string]string{
		"session.json": jsonText(t, map[string]any{
			"schema_version": 1, "started_at": "2024-01-02T03:04:05Z", "record_frames": true, "replay": true, "hud_evidence": true,
			"max_recording_bytes": 123, "max_queued_bytes": 45, "max_observations": 6, "config": secretPath,
		}),
		"capture.jsonl": jsonText(t, map[string]any{
			"type": "capture", "frame_id": 1, "source_sequence": 2, "source_revision": 3, "capture_method": secretPath,
			"width": 1600, "height": 900, "valid_for_latency": true, "raw_state": "saved", "error_category": token,
			"error": "helper stderr " + uncPath + " " + token + " " + requestID, "helper_outcome": requestID, "helper_reap": secretPath,
		}) + "\n",
		"frames.jsonl": jsonText(t, map[string]any{"type": "raw_frame", "frame_id": 1, "path": secretPath, "error": token}) + "\n",
		"report.json": jsonText(t, map[string]any{
			"schema_version": 1, "generated_at": "2024-01-02T03:04:05Z", "started_at": "2024-01-02T03:04:05Z", "ended_at": "2024-01-02T03:05:05Z", "confirmed_events": 7,
			"status": map[string]any{"attempts": 8, "valid_frames": 9, "recorded_frames": 10, "dropped_frames": 11, "dropped_logs": 12, "directory": secretPath, "last_error": token},
		}),
		"report.md": "raw report " + secretPath + " " + token,
		"replay/manifest.json": jsonText(t, map[string]any{
			"schema_version": 2, "requested_duration": "180s", "max_width": 720, "format": "JPEG", "quality": 75, "byte_cap": 64, "saved": 1, "dropped": 2, "evicted": 3,
			"frames": []any{map[string]any{"image": secretPath, "state_evidence": map[string]any{"error": token}}},
		}),
	} {
		if err := os.WriteFile(filepath.Join(session, name), []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(session, "frames"), 0o700); err != nil {
		t.Fatal(err)
	}
	maliciousImageName := `C__Users_Private_UNC_server_token_request_id.png`
	if err := os.WriteFile(filepath.Join(session, "frames", maliciousImageName), []byte("synthetic image"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Capture.Provider = "leidian-adb"
	cfg.Capture.MuMu.InstallDir = secretPath
	cfg.Capture.MuMu.DLLPath = secretPath
	cfg.Capture.MuMu.Package = packageName
	cfg.Capture.Leidian.ADBPath = secretPath
	cfg.Capture.Leidian.Serial = serial
	cfg.Capture.Leidian.Package = packageName
	options := BundleOptions{Root: root, SessionDir: session, Config: cfg, ConfigPath: secretPath, Version: "v-test", Executable: secretPath,
		CaptureState: frame.CaptureState{RequestedRevision: 4, AppliedRevision: 3, Source: secretPath, LastError: "helper stderr " + token, Method: secretPath},
		Inventory:    mumu.Inventory{GeneratedAt: time.Now(), Errors: []string{"raw " + token}, Processes: []mumu.ProcessEvidence{{PID: 7654, Executable: secretPath, CommandLine: requestID, Root: secretPath}}, Installations: []mumu.Installation{{Root: secretPath, ManagerPath: uncPath, Error: "raw " + token, Instances: []mumu.Instance{{Root: secretPath, PID: 999, Running: true}}}}},
		Probe:        &mumu.ProbeResult{Requested: mumu.Options{InstallDir: secretPath, DLLPath: secretPath, Package: packageName}, ResolvedRoot: secretPath, SDKCandidates: []mumu.SDKFile{{Path: secretPath, Error: "stderr " + token}}, SelectedSDK: mumu.SDKFile{Path: secretPath}, Error: "stderr " + token, FailureStage: requestID, Steps: []mumu.ProbeStep{{Stage: requestID, Detail: secretPath, Error: requestID}}},
	}
	path, err := ExportBundle(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	members := zipText(t, path)
	all := ""
	for _, text := range members {
		all += text
	}
	for _, forbidden := range []string{secretPath, uncPath, token, requestID, serial, packageName, "7654", "999", "helper stderr", "external_renderer_ipc.dll", "MuMuManager.exe"} {
		if strings.Contains(all, forbidden) {
			t.Fatalf("support bundle leaked %q", forbidden)
		}
	}
	for _, want := range []string{"leidian-adb", "capture_failed", "inventory_failed", "probe_failed", "other", "1600", "900", "requested_revision", "installation_count"} {
		if !strings.Contains(all, want) {
			t.Fatalf("support bundle lost stable diagnostic %q", want)
		}
	}

	options.IncludeImages = true
	optedInPath, err := ExportBundle(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	optedInMembers := zipText(t, optedInPath)
	if _, ok := optedInMembers["images/frame-000001.png"]; !ok {
		t.Fatal("explicit image opt-in did not retain the allowlisted image")
	}
	for name, text := range optedInMembers {
		if strings.Contains(name, maliciousImageName) || strings.Contains(name, "C__Users") || strings.Contains(name, "UNC_server") || strings.Contains(name, "token") || strings.Contains(name, "request_id") {
			t.Fatalf("image source filename leaked into ZIP member name: %s", name)
		}
		if imageMember(name) {
			continue
		}
		for _, forbidden := range []string{maliciousImageName, "C__Users", "UNC_server", "token", "request_id", secretPath, uncPath, token, requestID, serial, packageName, "7654", "999", "helper stderr", "external_renderer_ipc.dll", "MuMuManager.exe"} {
			if strings.Contains(text, forbidden) {
				t.Fatalf("opt-in support bundle leaked %q in text member %s", forbidden, name)
			}
		}
	}
}

func assertObjectAllowlist(t *testing.T, data, name string, allowed map[string]bool) map[string]any {
	t.Helper()
	var value map[string]any
	if err := json.Unmarshal([]byte(data), &value); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	for key := range value {
		if !allowed[key] {
			t.Fatalf("%s contains non-allowlisted field %q", name, key)
		}
	}
	return value
}

func TestBundleJSONSchemasAreAllowlisted(t *testing.T) {
	root := t.TempDir()
	session := filepath.Join(root, "session")
	if err := os.MkdirAll(filepath.Join(session, "replay"), 0o700); err != nil {
		t.Fatal(err)
	}
	for name, text := range map[string]string{
		"session.json":         `{"schema_version":1,"record_frames":true,"replay":true,"hud_evidence":true}`,
		"capture.jsonl":        `{"type":"capture","frame_id":1,"source_sequence":2,"source_revision":3,"capture_method":"mumu-sdk","width":1600,"height":900,"valid_for_latency":true,"raw_state":"saved","helper_request_started":"2024-01-02T03:04:00.123Z","helper_deadline":"2024-01-02T03:04:01.123Z","helper_outcome":"success","helper_reap":"completed"}` + "\n",
		"frames.jsonl":         `{"type":"raw_frame","frame_id":1,"error":"write failure"}` + "\n",
		"report.json":          `{"schema_version":1,"confirmed_events":2,"status":{"attempts":3}}`,
		"report.md":            "raw report replaced",
		"replay/manifest.json": `{"schema_version":2,"format":"JPEG","frames":[{}]}`,
	} {
		if err := os.WriteFile(filepath.Join(session, name), []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	path, err := ExportBundle(context.Background(), BundleOptions{Root: root, SessionDir: session, Config: config.Default(), Version: "v-test", Probe: &mumu.ProbeResult{Width: 1600, Height: 900, Steps: []mumu.ProbeStep{{Stage: mumu.ProbeStageCapture, OK: true}}}})
	if err != nil {
		t.Fatal(err)
	}
	members := zipText(t, path)
	allowed := map[string]map[string]bool{
		"application.json":    {"schema_version": true, "version": true, "generated_at": true, "feedback_qq_group": true},
		"environment.json":    {"platform": true},
		"config/runtime.json": {"schema_version": true, "capture_provider": true, "capture_selection": true, "preferred_methods": true, "capture_timeout_ms": true, "tracking_poll_ms": true, "tracking_window_frames": true, "minimum_confirm_frames": true, "enter_fight_frames": true, "leave_fight_frames": true, "unknown_hold_ms": true, "stale_after_ms": true, "reset_after_ms": true, "reference_width": true, "reference_height": true},
		"config/saved.json":   {"schema_version": true, "available": true},
		"capture/state.json":  {"requested_revision": true, "applied_revision": true, "provider": true, "method": true, "error_category": true, "updated_at": true},
		"mumu/inventory.json": {"generated_at": true, "installation_count": true, "instance_count": true, "running_count": true, "process_count": true, "error_category": true},
		"manifest.json":       {"schema_version": true, "bundle_schema": true, "generated_at": true, "version": true, "files": true, "includes_images": true},
	}
	for name, keys := range allowed {
		assertObjectAllowlist(t, members[name], name, keys)
	}
	probe := assertObjectAllowlist(t, members["mumu/sdk-probe.json"], "mumu/sdk-probe.json", map[string]bool{"started_at": true, "ended_at": true, "sdk_candidate_count": true, "selected_sdk_found": true, "width": true, "height": true, "image_available": true, "failure_stage": true, "error_category": true, "steps": true})
	steps, ok := probe["steps"].([]any)
	if !ok || len(steps) != 1 {
		t.Fatalf("probe steps = %#v, want one allowlisted step", probe["steps"])
	}
	step, ok := steps[0].(map[string]any)
	if !ok {
		t.Fatalf("probe step type = %T", steps[0])
	}
	for key := range step {
		if !map[string]bool{"stage": true, "started_at": true, "duration_ms": true, "ok": true, "error_category": true}[key] {
			t.Fatalf("mumu/sdk-probe.json step contains non-allowlisted field %q", key)
		}
	}
	assertObjectAllowlist(t, members["session/session.json"], "session/session.json", map[string]bool{"schema_version": true, "started_at": true, "record_frames": true, "replay": true, "hud_evidence": true, "max_recording_bytes": true, "max_queued_bytes": true, "max_observations": true})
	captureKeys := map[string]bool{"type": true, "frame_id": true, "source_sequence": true, "source_revision": true, "capture_started": true, "captured_at": true, "received_at": true, "helper_request_started": true, "helper_deadline": true, "capture_method": true, "width": true, "height": true, "valid_for_latency": true, "raw_state": true, "duplicate": true, "hold": true, "error_category": true, "helper_outcome": true, "helper_reap": true, "helper_origin_outcome": true, "helper_terminal": true}
	for _, name := range []string{"session/capture.jsonl", "session/frames.jsonl"} {
		for _, line := range strings.Split(strings.TrimSpace(members[name]), "\n") {
			row := assertObjectAllowlist(t, line, name, captureKeys)
			if name == "session/capture.jsonl" {
				if row["helper_request_started"] != "2024-01-02T03:04:00.123Z" || row["helper_deadline"] != "2024-01-02T03:04:01.123Z" {
					t.Fatalf("helper timing was not preserved: %#v", row)
				}
				if row["source_revision"] != float64(3) || row["helper_outcome"] != "success" || row["helper_reap"] != "completed" {
					t.Fatalf("helper correlation was not preserved: %#v", row)
				}
			}
		}
	}
	assertObjectAllowlist(t, members["session/report.json"], "session/report.json", map[string]bool{"schema_version": true, "generated_at": true, "started_at": true, "ended_at": true, "attempts": true, "valid_frames": true, "recorded_frames": true, "dropped_frames": true, "dropped_logs": true, "confirmed_events": true})
	assertObjectAllowlist(t, members["session/replay/manifest.json"], "session/replay/manifest.json", map[string]bool{"schema_version": true, "requested_duration": true, "cutoff": true, "actual_start": true, "actual_end": true, "max_width": true, "format": true, "quality": true, "byte_cap": true, "saved": true, "dropped": true, "evicted": true, "frame_count": true})
	if strings.Contains(members["manifest.json"], "session_dir") || strings.Contains(members["config/runtime.json"], "installDir") {
		t.Fatal("full config or local session directory remained in exported schema")
	}
}

func TestAllowedSessionFilesIncludesImagesOnlyByExplicitOptIn(t *testing.T) {
	session := t.TempDir()
	for dir, name := range map[string]string{"frames": "raw.png", "hud": "crop.png", "replay": "frame.jpg"} {
		if err := os.MkdirAll(filepath.Join(session, dir), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(session, dir, name), []byte("image"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(session, "replay", "manifest.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	without := allowedSessionFiles(session, false)
	with := allowedSessionFiles(session, true)
	if len(without) != 1 || without[0] != "replay/manifest.json" {
		t.Fatalf("default allowlist leaked image: %v", without)
	}
	for _, want := range []string{"frames/raw.png", "hud/crop.png", "replay/frame.jpg", "replay/manifest.json"} {
		found := false
		for _, name := range with {
			found = found || name == want
		}
		if !found {
			t.Fatalf("explicit allowlist missing %s: %v", want, with)
		}
	}
}

func TestExportBundleVersionAllowlist(t *testing.T) {
	root := t.TempDir()
	malicious := "v1.2.3\nC:\\Users\\Private token=request-id"
	path, err := ExportBundle(context.Background(), BundleOptions{Root: root, Config: config.Default(), Version: malicious})
	if err != nil {
		t.Fatal(err)
	}
	members := zipText(t, path)
	for name, text := range members {
		if strings.Contains(name, "token") || strings.Contains(name, "request-id") || strings.Contains(name, "Users") || strings.Contains(text, malicious) {
			t.Fatalf("malicious version leaked in %s: %q", name, text)
		}
	}
	for _, name := range []string{"application.json", "manifest.json", "summary.md"} {
		if !strings.Contains(members[name], "unknown") {
			t.Fatalf("%s did not use the safe unknown version: %q", name, members[name])
		}
	}
	path, err = ExportBundle(context.Background(), BundleOptions{Root: root, Config: config.Default(), Version: "v0.2.7"})
	if err != nil {
		t.Fatal(err)
	}
	members = zipText(t, path)
	if !strings.Contains(members["manifest.json"], "v0.2.7") || !strings.Contains(members["application.json"], "v0.2.7") {
		t.Fatal("safe version was not preserved")
	}
}

func TestExportBundleDoesNotOverwriteEarlierBundle(t *testing.T) {
	root := t.TempDir()
	options := BundleOptions{Root: root, Config: config.Default(), Version: "v0.2.2"}
	first, err := ExportBundle(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	second, err := ExportBundle(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatalf("second export overwrote first bundle: %s", first)
	}
	if _, err := os.Stat(first); err != nil {
		t.Fatalf("first bundle disappeared: %v", err)
	}
}
