package ui

import (
	"fmt"
	"math"
	"path/filepath"
	"strconv"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"narutotimer/internal/sound"
)

const defaultWarnSoundLabel = "内置提示音（叮咚）"

// warnSoundLabel names the configured reminder for the settings card.
func warnSoundLabel(path string) string {
	if path == "" {
		return defaultWarnSoundLabel
	}
	return "自定义：" + filepath.Base(path)
}

// warnSoundDir keeps imported reminders next to config.json.
func (s *session) warnSoundDir() string {
	dir := filepath.Dir(s.cfgPath)
	if dir == "" {
		dir = "."
	}
	return filepath.Join(dir, "sounds")
}

// parseWarnSeconds accepts 0.1–15.0 with at most one decimal ("3", "2.5").
func parseWarnSeconds(text string) (float64, error) {
	text = strings.TrimSpace(strings.ReplaceAll(text, "，", "."))
	v, err := strconv.ParseFloat(text, 64)
	if err != nil || math.IsNaN(v) || v < 0.1 || v > 15 || math.Abs(v*10-math.Round(v*10)) > 1e-6 {
		return 0, fmt.Errorf("请输入 0.1–15.0 之间的秒数，最多一位小数")
	}
	return math.Round(v*10) / 10, nil
}

func formatWarnSeconds(v float64) string { return strconv.FormatFloat(v, 'f', 1, 64) }

func (s *session) setWarnSeconds(v float64) error {
	s.mu.Lock()
	s.cfg.UI.WarnSeconds = v
	snapshot := s.settingsSnapshotLocked()
	s.lastClockColor = nil
	s.mu.Unlock()
	err := snapshot.Save()
	s.refreshClock()
	return err
}

func (s *session) setWarnRed(on bool) error {
	s.mu.Lock()
	s.cfg.UI.WarnRed = on
	snapshot := s.settingsSnapshotLocked()
	s.lastClockColor = nil // Force the clock to repaint in the new colour.
	s.mu.Unlock()
	err := snapshot.Save()
	s.refreshClock()
	return err
}

func (s *session) setWarnSound(on bool) error {
	s.mu.Lock()
	s.cfg.UI.WarnSound = on
	snapshot := s.settingsSnapshotLocked()
	s.mu.Unlock()
	return snapshot.Save()
}

func (s *session) setWarnSoundFile(path string) error {
	s.mu.Lock()
	s.cfg.UI.WarnSoundFile = path
	snapshot := s.settingsSnapshotLocked()
	s.mu.Unlock()
	return snapshot.Save()
}

// warnControls is the optional last-3-seconds card. Both options are off by
// default; the sound picker stays usable so a file can be tried first.
func (s *session) warnControls(w fyne.Window) fyne.CanvasObject {
	s.mu.Lock()
	red, beep, file, secs := s.cfg.UI.WarnRed, s.cfg.UI.WarnSound, s.cfg.UI.WarnSoundFile, s.cfg.UI.WarnSeconds
	s.mu.Unlock()

	secsEntry := widget.NewEntry()
	secsEntry.SetText(formatWarnSeconds(secs))
	secsNote := widget.NewLabel("")
	secsNote.Importance = widget.DangerImportance
	secsNote.Hide()
	secsEntry.OnChanged = func(text string) {
		v, err := parseWarnSeconds(text)
		if err != nil {
			secsNote.SetText(err.Error())
			secsNote.Show()
			return
		}
		secsNote.Hide()
		showSettingsError(s.setWarnSeconds(v), w)
	}
	secsRow := container.NewBorder(nil, nil, widget.NewLabel("提醒秒数"), widget.NewLabel("秒（默认 3.0）"), secsEntry)

	redChk := widget.NewCheck("进入提醒秒数后倒计时变红（关闭时为琥珀色）", nil)
	redChk.SetChecked(red)
	redChk.OnChanged = func(on bool) { showSettingsError(s.setWarnRed(on), w) }
	beepChk := widget.NewCheck("进入提醒秒数时播放提示音", nil)
	beepChk.SetChecked(beep)
	beepChk.OnChanged = func(on bool) { showSettingsError(s.setWarnSound(on), w) }

	current := widget.NewLabel("当前音效：" + warnSoundLabel(file))
	current.Truncation = fyne.TextTruncateEllipsis
	preview := widget.NewButtonWithIcon("试听", theme.MediaPlayIcon(), func() {
		s.mu.Lock()
		path := s.cfg.UI.WarnSoundFile
		s.mu.Unlock()
		if err := sound.Play(path); err != nil {
			dialog.ShowError(err, w)
		}
	})
	importBtn := widget.NewButtonWithIcon("导入音效…", theme.FolderOpenIcon(), func() {
		picker := dialog.NewFileOpen(func(r fyne.URIReadCloser, err error) {
			if err != nil {
				dialog.ShowError(err, w)
				return
			}
			if r == nil {
				return
			}
			defer r.Close()
			stored, err := sound.Import(r, r.URI().Name(), s.warnSoundDir())
			if err == nil {
				err = s.setWarnSoundFile(stored)
			}
			if err != nil {
				dialog.ShowError(err, w)
				return
			}
			current.SetText("当前音效：" + warnSoundLabel(stored))
		}, w)
		picker.SetFilter(storage.NewExtensionFileFilter(sound.Extensions))
		picker.Show()
	})
	resetBtn := widget.NewButtonWithIcon("恢复默认音效", theme.ContentUndoIcon(), func() {
		if err := s.setWarnSoundFile(""); err != nil {
			dialog.ShowError(err, w)
			return
		}
		current.SetText("当前音效：" + defaultWarnSoundLabel)
	})
	note := widget.NewLabel("提醒秒数可填 0.1–15.0（一位小数）。变红和提示音默认关闭，可单独开启。音效支持 WAV / MP3（5 MB 以内），导入后复制保存到配置目录的 sounds 文件夹；每次倒计时只响一次。")
	note.Wrapping = fyne.TextWrapWord
	note.Importance = widget.LowImportance
	return container.NewVBox(secsRow, secsNote, redChk, beepChk, current,
		container.NewGridWithColumns(3, preview, importBtn, resetBtn), note)
}
