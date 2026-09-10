package ui

import (
	"fyne.io/fyne/v2"
	fynetest "fyne.io/fyne/v2/test"
	"image/png"
	"narutotimer/internal/buildinfo"
	"narutotimer/internal/config"
	"narutotimer/internal/updates"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAboutAndCaptureTabsRenderAndSelectDatedHistory(t *testing.T) {
	a := fynetest.NewApp()
	defer a.Quit()
	a.Settings().SetTheme(newChromaTheme())
	t.Setenv("LOCALAPPDATA", t.TempDir())
	old := buildinfo.Version
	buildinfo.Version = "v0.2.0"
	defer func() { buildinfo.Version = old }()
	first := updates.Release{Tag: "v0.2.0", Name: "关于与更新、多开选择", Body: "## 新增功能\n- 版本信息与在线更新\n- 按日期选择历史日志\n- MuMu 实例选择\n\n保留设置，重启即可完成更新。", Published: time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC), URL: updates.RepositoryURL + "/releases/tag/v0.2.0"}
	previous := first
	previous.Tag = "v0.1.6"
	previous.Published = previous.Published.Add(-24 * time.Hour)
	previous.Body = "修复新版训练场工具栏识别。"
	if e := updates.SaveFeed(updates.Feed{Latest: first, Releases: []updates.Release{first, previous}, Checked: time.Now()}); e != nil {
		t.Fatal(e)
	}
	s := &session{cfg: config.Default(), cfgPath: filepath.Join(t.TempDir(), "config.json"), done: make(chan struct{}), captureSource: "MuMu 实例 3 · 玩家主号"}
	s.win = fynetest.NewTempWindow(t, s.overlayContent())
	s.openAbout()
	defer s.settings.Close()
	if s.settingsTabs.SelectedIndex() != 2 {
		t.Fatal("about tab not selected")
	}
	if overlayButton(s.settings.Content(), "检查更新") == nil {
		t.Fatal("update action unavailable")
	}
	for _, page := range []struct {
		index int
		name  string
	}{{2, "about"}, {1, "capture"}, {0, "general"}} {
		s.settingsTabs.SelectIndex(page.index)
		s.settings.Resize(fyne.NewSize(660, 720))
		img := s.settings.Canvas().Capture()
		if img.Bounds().Dx() > 700 {
			t.Fatal("settings overflow", img.Bounds())
		}
		if dir := os.Getenv("TIMER_UI_PREVIEW_DIR"); dir != "" {
			os.MkdirAll(dir, 0755)
			f, e := os.Create(filepath.Join(dir, page.name+".png"))
			if e != nil {
				t.Fatal(e)
			}
			png.Encode(f, img)
			f.Close()
		}
	}
}
