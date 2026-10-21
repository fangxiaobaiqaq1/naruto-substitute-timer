package support

import (
	"archive/zip"
	"context"
	"encoding/json"
	"image"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"narutotimer/internal/config"
	"narutotimer/internal/diagnostics"
	"narutotimer/internal/frame"
)

func imageForTest() *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, 64, 36))
	for i := range img.Pix {
		img.Pix[i] = byte(i * 31)
	}
	return img
}

func fakeUser(t *testing.T, profile, name string) {
	t.Helper()
	old := userIdentity
	userIdentity = func() (string, string) { return profile, name }
	t.Cleanup(func() { userIdentity = old })
}

func readZip(t *testing.T, path string) map[string]*zip.File {
	t.Helper()
	z, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { z.Close() })
	files := map[string]*zip.File{}
	for _, f := range z.File {
		files[f.Name] = f
	}
	return files
}

func zipText(t *testing.T, f *zip.File) string {
	t.Helper()
	rc, err := f.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	data, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// supportSession records a real diagnostics session in which the identity
// pipeline reported our account and an opponent on every frame.
func supportSession(t *testing.T, root string, cfg config.Config, images bool) string {
	t.Helper()
	r, err := diagnostics.New(diagnostics.Options{Root: root, Config: cfg, RecordFrames: images, Replay: true, ReplayInterval: time.Nanosecond})
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(-time.Second)
	for i := 0; i < 3; i++ {
		f := frame.Frame{Img: imageForTest(), CaptureStarted: at, CapturedAt: at.Add(time.Millisecond),
			AnalysisStarted: at.Add(2 * time.Millisecond), AnalyzedAt: at.Add(3 * time.Millisecond),
			CaptureMethod: "test", Scene: "fight", Fighting: true, LeftNinja: "宇智波佐助", PlayerSide: "left",
			PlayerName: "鸣人", OppName: "佐助大人", Status: "已连接 MuMu · 1280x720 · 决斗场对局中 · 鸣人 vs 佐助大人 · rgb"}
		r.Observe(f, at.Add(4*time.Millisecond))
		at = at.Add(10 * time.Millisecond)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	return r.Snapshot().Directory
}

// GE-04: by default the bundle must not contain account names (own or
// opponents') or the Windows user name, but stay useful: names become
// consistent pseudonyms, paths keep their shape.
func TestExportBundleRedactsAccountNamesAndUserName(t *testing.T) {
	root := t.TempDir()
	profile := filepath.Join(root, "Users", "xiaoming")
	fakeUser(t, profile, "xiaoming")
	cfg := config.Default()
	cfg.UI.PlayerNames = []string{"鸣人", "Naruto Alt"}
	cfg.Capture.MuMu.InstallDir = filepath.Join(profile, "MuMu", "nx_main")
	session := supportSession(t, filepath.Join(profile, "AppData", "Roaming", "NarutoTimer", "debug", "sessions"), cfg, false)
	configPath := filepath.Join(profile, "AppData", "Roaming", "NarutoTimer", "config.json")
	if err := config.Save(configPath, cfg); err != nil {
		t.Fatal(err)
	}
	path, err := exportBundle(context.Background(), BundleOptions{Root: filepath.Join(root, "out"), SessionDir: session, Config: cfg, ConfigPath: configPath,
		Version: "v9.9.9", Executable: filepath.Join(profile, "Downloads", "timer-app.exe")})
	if err != nil {
		t.Fatal(err)
	}
	files := readZip(t, path)
	for _, want := range []string{"session/capture.jsonl", "session/session.json", "session/replay/manifest.json", "config/saved.json", "config/runtime.json", "environment.json", "summary.md"} {
		if files[want] == nil {
			t.Fatalf("missing %s", want)
		}
	}
	all := ""
	for name, f := range files {
		text := zipText(t, f)
		for _, secret := range []string{"鸣人", "佐助大人", "Naruto Alt", "xiaoming"} {
			if strings.Contains(text, secret) {
				t.Errorf("%s leaks %q", name, secret)
			}
		}
		all += text
	}
	capture := zipText(t, files["session/capture.jsonl"])
	for _, want := range []string{`"player_name":"我方账号#1"`, `"opponent_name":"对手账号#1"`, "我方账号#1 vs 对手账号#1", `"left_ninja":"宇智波佐助"`} {
		if !strings.Contains(capture, want) {
			t.Errorf("capture.jsonl lacks %s:\n%s", want, capture)
		}
	}
	var saved config.Config
	if err := json.Unmarshal([]byte(zipText(t, files["config/saved.json"])), &saved); err != nil {
		t.Fatalf("redacted config is not valid JSON: %v", err)
	}
	if len(saved.UI.PlayerNames) != 2 || saved.UI.PlayerNames[0] != "我方账号#1" || saved.UI.PlayerNames[1] != "我方账号#2" {
		t.Fatalf("playerNames = %q", saved.UI.PlayerNames)
	}
	if !strings.Contains(saved.Capture.MuMu.InstallDir, "%USERPROFILE%") {
		t.Fatalf("install dir not redacted: %q", saved.Capture.MuMu.InstallDir)
	}
	if !strings.Contains(zipText(t, files["environment.json"]), "%USERPROFILE%") {
		t.Fatal("executable path not redacted")
	}
	summary := zipText(t, files["summary.md"])
	for _, line := range DescribeBundle(BundleContents{HasSession: true}) {
		if !strings.Contains(summary, line) {
			t.Fatalf("summary.md does not list %q", line)
		}
	}
	var manifest Manifest
	if err := json.Unmarshal([]byte(zipText(t, files["manifest.json"])), &manifest); err != nil || manifest.AccountNames != "pseudonymized" || manifest.UserPaths != "redacted" {
		t.Fatalf("manifest = %+v, %v", manifest, err)
	}
}

func TestExportBundleIncludesAccountNamesOnlyByOptIn(t *testing.T) {
	root := t.TempDir()
	fakeUser(t, filepath.Join(root, "Users", "xiaoming"), "xiaoming")
	cfg := config.Default()
	cfg.UI.PlayerNames = []string{"鸣人"}
	session := supportSession(t, filepath.Join(root, "Users", "xiaoming", "sessions"), cfg, false)
	path, err := exportBundle(context.Background(), BundleOptions{Root: filepath.Join(root, "out"), SessionDir: session, Config: cfg, Version: "v9.9.9", IncludeAccountNames: true})
	if err != nil {
		t.Fatal(err)
	}
	files := readZip(t, path)
	capture := zipText(t, files["session/capture.jsonl"])
	if !strings.Contains(capture, "佐助大人") || !strings.Contains(capture, "鸣人") {
		t.Fatal("opt-in did not keep account names")
	}
	if strings.Contains(zipText(t, files["session/session.json"]), "xiaoming") {
		t.Fatal("user name kept although only account names were opted in")
	}
}

func TestRedactorHandlesEscapedPathsAndBrokenLines(t *testing.T) {
	fakeUser(t, `C:\Users\Xiao Ming`, "Xiao Ming")
	red := newRedactor(false, []string{"鸣人"})
	got := red.text(`{"dir":"C:\\Users\\Xiao Ming\\AppData\\x","other":"D:\\Users\\bob\\y","pub":"C:\\Users\\Public\\z"}`)
	for _, want := range []string{`%USERPROFILE%\\AppData`, `D:\\Users\\%USERNAME%\\y`, `C:\\Users\\Public\\z`} {
		if !strings.Contains(got, want) {
			t.Fatalf("%q lacks %q", got, want)
		}
	}
	lines := string(red.jsonLines([]byte("{\"player_name\":\"鸣人\",\"opponent_name\":\"佐助\",\"status\":\"鸣人 vs 佐助\"}\n{\"opponent_name\":\"佐\n")))
	if strings.Contains(lines, "佐") || strings.Contains(lines, "鸣人") || !strings.Contains(lines, "redacted_unparsable_line") {
		t.Fatalf("jsonl = %s", lines)
	}
}

// GE-06: PNG/JPEG are stored (re-deflating them ran at ~20 MiB/s and made the
// fixed 25 s deadline fail); the deadline grows with the files to copy.
func TestExportBundleStoresImagesAndSizesDeadline(t *testing.T) {
	root := t.TempDir()
	fakeUser(t, filepath.Join(root, "home"), "tester")
	cfg := config.Default()
	session := supportSession(t, filepath.Join(root, "sessions"), cfg, true)
	frames, _ := filepath.Glob(filepath.Join(session, "frames", "*.png"))
	replays, _ := filepath.Glob(filepath.Join(session, "replay", "*.jpg"))
	if len(frames) == 0 || len(replays) == 0 {
		t.Fatalf("test session has no images: %d PNG, %d JPEG", len(frames), len(replays))
	}
	path, err := exportBundle(context.Background(), BundleOptions{Root: filepath.Join(root, "out"), SessionDir: session, Config: cfg, Version: "v9.9.9", IncludeImages: true})
	if err != nil {
		t.Fatal(err)
	}
	images := 0
	for name, f := range readZip(t, path) {
		switch {
		case isImageFile(name):
			images++
			if f.Method != zip.Store {
				t.Errorf("%s compressed with method %d", name, f.Method)
			}
		case f.Method != zip.Deflate:
			t.Errorf("%s not deflated", name)
		}
	}
	if images < len(frames)+len(replays) {
		t.Fatalf("only %d images in bundle", images)
	}
	small := ExportTimeout(session, false)
	big := filepath.Join(session, "frames", "big.png")
	if err := os.WriteFile(big, make([]byte, 400<<20>>4), 0o600); err != nil { // 25 MiB
		t.Fatal(err)
	}
	if ExportTimeout(session, true) <= small || small < 30*time.Second {
		t.Fatalf("deadline does not grow with the bundle: %v vs %v", ExportTimeout(session, true), small)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := exportBundle(ctx, BundleOptions{Root: filepath.Join(root, "out"), SessionDir: session, Config: cfg, IncludeImages: true}); err == nil {
		t.Fatal("cancelled export succeeded")
	}
}
