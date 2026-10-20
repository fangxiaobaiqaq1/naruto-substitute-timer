package ui

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/storage"

	"narutotimer/internal/config"
)

// 方案文件（docs/区域校准.md「方案文件」）：Windows 与安卓读写同一种 JSON，
// 只含已校准的框（缺省 = 自动）；frameWidth/Height 只作提示。

const (
	calibrationSchemeKind    = "naruto-timer-calibration"
	calibrationSchemeVersion = 1
	// A scheme is a few hundred bytes; a file this big is something else.
	calibrationSchemeMaxBytes = 1 << 20
	// Frame aspects further apart than this get a warning on import.
	calibrationAspectTolerance = 0.02
)

// errCalibrationScheme wraps every reason a file cannot be imported.
var errCalibrationScheme = errors.New("不能导入这个方案文件")

// calibrationSchemeKeys are the boxes' JSON keys in calibTarget order, which
// is also the order they are written in.
var calibrationSchemeKeys = [calibTargetCount]string{"leftBeads", "rightBeads", "leftAvatar", "rightAvatar"}

// calibrationScheme is one scheme file: the profile with only the calibrated
// boxes, and the size of the frame they were drawn on (0 = unknown).
type calibrationScheme struct {
	Calibration             *config.RegionCalibration
	FrameWidth, FrameHeight int
}

// calibrationRectValid is config validation's rule for a box (inside the
// frame, positive size), with finite numbers.
func calibrationRectValid(r config.NormalizedRect) bool {
	for _, v := range [...]float64{r.X, r.Y, r.Width, r.Height} {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return false
		}
	}
	return r.X >= 0 && r.X <= 1 && r.Y >= 0 && r.Y <= 1 && r.Width > 0 && r.Height > 0 && r.X+r.Width <= 1 && r.Y+r.Height <= 1
}

// encodeCalibrationScheme writes s in the documented layout: one key per
// line, each box on one line, only the calibrated boxes.
func encodeCalibrationScheme(s calibrationScheme) ([]byte, error) {
	c := s.Calibration
	switch {
	case c == nil:
		return nil, errors.New("没有方案")
	case c.Profile != "camp" && c.Profile != "duel":
		return nil, errors.New("profile 应为 camp（训练场）或 duel（决斗场）")
	case s.FrameWidth <= 0 || s.FrameHeight <= 0:
		return nil, errors.New("画面大小未知")
	}
	for t, r := range calibrationRects(c) {
		if r != nil && !calibrationRectValid(*r) {
			return nil, fmt.Errorf("%s（%s）超出画面或宽高不大于 0", calibTargetNames[t], calibrationSchemeKeys[t])
		}
	}
	text := func(v any) string {
		b, _ := json.Marshal(v) // strings and finite numbers only
		return string(b)
	}
	var b bytes.Buffer
	fmt.Fprintf(&b, "{\n  \"kind\": %s,\n  \"version\": %d,\n  \"frameWidth\": %d,\n  \"frameHeight\": %d,\n  \"profile\": %s",
		text(calibrationSchemeKind), calibrationSchemeVersion, s.FrameWidth, s.FrameHeight, text(c.Profile))
	for t, r := range calibrationRects(c) {
		if r != nil {
			fmt.Fprintf(&b, ",\n  %s: {\"x\": %s, \"y\": %s, \"width\": %s, \"height\": %s}",
				text(calibrationSchemeKeys[t]), text(r.X), text(r.Y), text(r.Width), text(r.Height))
		}
	}
	b.WriteString("\n}\n")
	return b.Bytes(), nil
}

// decodeCalibrationScheme reads a scheme file. Keys match exactly, unknown
// keys are ignored and a missing or null box is automatic. A wrong kind or
// version, a bad profile or a box outside the frame is an error wrapping
// errCalibrationScheme, worded for the player.
func decodeCalibrationScheme(data []byte) (calibrationScheme, error) {
	fail := func(format string, args ...any) (calibrationScheme, error) {
		return calibrationScheme{}, fmt.Errorf("%w：%s", errCalibrationScheme, fmt.Sprintf(format, args...))
	}
	null := func(v json.RawMessage) bool { return string(bytes.TrimSpace(v)) == "null" }
	if len(data) > calibrationSchemeMaxBytes {
		return fail("文件太大，不是校准方案")
	}
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf")) // 记事本另存的 UTF-8 BOM
	var raw map[string]json.RawMessage
	if json.Unmarshal(data, &raw) != nil || raw == nil {
		return fail("文件内容不是 JSON 对象（文件损坏或不是方案文件）")
	}
	var kind string
	if json.Unmarshal(raw["kind"], &kind) != nil || kind != calibrationSchemeKind {
		return fail("kind 不是 %s，这不是计时器导出的校准方案", calibrationSchemeKind)
	}
	var version float64
	if v, ok := raw["version"]; !ok || null(v) || json.Unmarshal(v, &version) != nil {
		return fail("缺少版本号 version")
	} else if version != calibrationSchemeVersion {
		return fail("方案版本 %s 不支持，只能导入版本 %d 的方案（请更新计时器）", bytes.TrimSpace(v), calibrationSchemeVersion)
	}
	var profile string
	if json.Unmarshal(raw["profile"], &profile) != nil || (profile != "camp" && profile != "duel") {
		return fail("profile 应为 camp（训练场）或 duel（决斗场）")
	}
	var rects [calibTargetCount]*config.NormalizedRect
	for t, key := range calibrationSchemeKeys {
		v, ok := raw[key]
		if !ok || null(v) {
			continue
		}
		name := fmt.Sprintf("%s（%s）", calibTargetNames[t], key)
		var fields map[string]json.RawMessage
		if json.Unmarshal(v, &fields) != nil || fields == nil {
			return fail("%s应为 {\"x\", \"y\", \"width\", \"height\"}", name)
		}
		var r config.NormalizedRect
		values := [...]*float64{&r.X, &r.Y, &r.Width, &r.Height}
		for i, field := range [...]string{"x", "y", "width", "height"} {
			value, ok := fields[field]
			if !ok || null(value) || json.Unmarshal(value, values[i]) != nil {
				return fail("%s缺少数值 %s", name, field)
			}
		}
		if !calibrationRectValid(r) {
			return fail("%s超出画面或宽高不大于 0", name)
		}
		rects[t] = &r
	}
	// The frame size is only a hint: anything but a positive number is unknown.
	size := func(key string) int {
		var v float64
		if json.Unmarshal(raw[key], &v) != nil || !(v >= 1 && v <= 1e6) {
			return 0
		}
		return int(math.Round(v))
	}
	return calibrationScheme{Calibration: newRegionCalibration(profile, rects), FrameWidth: size("frameWidth"), FrameHeight: size("frameHeight")}, nil
}

// calibrationAspectChange is how far the aspect of a w×h frame is from that
// of the current cw×ch frame, as a fraction of the current one (0 = unknown).
func calibrationAspectChange(w, h, cw, ch int) float64 {
	if w <= 0 || h <= 0 || cw <= 0 || ch <= 0 {
		return 0
	}
	return math.Abs(float64(w)*float64(ch)/(float64(h)*float64(cw)) - 1)
}

func calibrationSchemeFileName(w, h int) string {
	return fmt.Sprintf("校准方案-%dx%d.json", w, h)
}

// showCalibrationFileDialog shows an import/export file dialog; tests replace it.
var showCalibrationFileDialog = func(d *dialog.FileDialog) { d.Show() }

// ---- export ----

// exportScheme writes the calibration as 保存 would store it now (the saved
// boxes with the pending edits) to a scheme file the player picks.
func (c *calibrationWindow) exportScheme() {
	data, summary, err := c.exportData()
	if err != nil {
		c.setStatus("未导出：" + err.Error())
		dialog.ShowInformation("不能导出方案", err.Error(), c.win)
		return
	}
	b := c.snap.img.Bounds()
	d := dialog.NewFileSave(func(w fyne.URIWriteCloser, err error) { c.writeScheme(w, err, data, summary) }, c.win)
	d.SetFileName(calibrationSchemeFileName(b.Dx(), b.Dy()))
	d.SetFilter(storage.NewExtensionFileFilter([]string{".json"}))
	showCalibrationFileDialog(d)
}

// exportData is the scheme file for the shown frame and a summary of it for
// the status line. Untouched automatic boxes are not in it.
func (c *calibrationWindow) exportData() ([]byte, string, error) {
	if c.snap == nil {
		return nil, "", errors.New("还没有画面：方案要记下画面大小，请等画面出现后再导出。")
	}
	next, dropped, err := mergeCalibration(c.s.calibration(), c.drawn, c.touched)
	if err != nil {
		return nil, "", errors.New(calibrationErrorText(err))
	}
	if next == nil {
		return nil, "", errors.New("没有校准框：豆和头像都是自动识别，没有要导出的方案。")
	}
	b := c.snap.img.Bounds()
	data, err := encodeCalibrationScheme(calibrationScheme{Calibration: next, FrameWidth: b.Dx(), FrameHeight: b.Dy()})
	if err != nil {
		return nil, "", err
	}
	var names []string
	for t, r := range calibrationRects(next) {
		if r != nil {
			names = append(names, calibTargetNames[t])
		}
	}
	summary := fmt.Sprintf("%s的方案（%s画面，%d×%d", strings.Join(names, "、"), calibProfileName(next.Profile), b.Dx(), b.Dy())
	if len(dropped) > 0 {
		summary += "；画在另一种布局上的已保存框不在其中"
	}
	return data, summary + "）", nil
}

// writeScheme finishes 导出方案 once the player picked a file (nil = cancelled).
func (c *calibrationWindow) writeScheme(w fyne.URIWriteCloser, err error, data []byte, summary string) error {
	if err == nil && w == nil {
		return nil
	}
	if err == nil {
		_, err = w.Write(data)
		if closeErr := w.Close(); err == nil {
			err = closeErr
		}
	}
	if err != nil {
		c.setStatus("导出失败：" + err.Error())
		dialog.ShowError(fmt.Errorf("导出方案失败：%w", err), c.win)
		return err
	}
	c.setStatus("已导出" + summary + "：" + w.URI().Name() + "。")
	return nil
}

// ---- import ----

// importScheme loads a scheme file into the editor as pending edits; nothing
// changes until 保存.
func (c *calibrationWindow) importScheme() {
	open := func() {
		d := dialog.NewFileOpen(func(r fyne.URIReadCloser, err error) { c.readScheme(r, err) }, c.win)
		d.SetFilter(storage.NewExtensionFileFilter([]string{".json"}))
		showCalibrationFileDialog(d)
	}
	if c.dirty() {
		c.ask("导入方案", "导入的方案会替换还没保存的框。", "继续导入", open)
		return
	}
	open()
}

// readScheme finishes 导入方案 once the player picked a file (nil = cancelled).
func (c *calibrationWindow) readScheme(r fyne.URIReadCloser, err error) error {
	if err == nil && r == nil {
		return nil
	}
	var data []byte
	if err == nil {
		data, err = io.ReadAll(io.LimitReader(r, calibrationSchemeMaxBytes+1))
		r.Close()
	}
	if err != nil {
		c.setStatus("导入失败：" + err.Error())
		dialog.ShowError(fmt.Errorf("读取方案文件失败：%w", err), c.win)
		return err
	}
	return c.loadScheme(data)
}

// loadScheme replaces the pending edits with the scheme: its boxes become
// pending boxes of its profile, and a saved box the scheme leaves out is
// cleared (automatic), so 保存 stores exactly the scheme. An invalid file
// changes nothing.
func (c *calibrationWindow) loadScheme(data []byte) error {
	scheme, err := decodeCalibrationScheme(data)
	if err != nil {
		c.setStatus("未导入：" + err.Error())
		dialog.ShowInformation("不能导入方案", err.Error(), c.win)
		return err
	}
	profile := scheme.Calibration.Profile
	rects, saved := calibrationRects(scheme.Calibration), calibrationRects(c.saved)
	var loaded, cleared []string
	for t := range rects {
		c.drawn[t], c.touched[t] = nil, false
		switch {
		case rects[t] != nil:
			c.drawn[t], c.touched[t] = &calibBox{rect: *rects[t], profile: profile}, true
			loaded = append(loaded, calibTargetNames[t])
		case saved[t] != nil:
			c.touched[t] = true
			cleared = append(cleared, calibTargetNames[t])
		}
	}
	c.surface.Refresh()
	text := "已导入" + calibProfileName(profile) + "画面上画的方案"
	if scheme.FrameWidth > 0 && scheme.FrameHeight > 0 {
		text += fmt.Sprintf("（%d×%d）", scheme.FrameWidth, scheme.FrameHeight)
	}
	if len(loaded) > 0 {
		text += "：" + strings.Join(loaded, "、") + "。"
	} else {
		text += "：没有校准框（全部自动）。"
	}
	if len(cleared) > 0 {
		text += "方案里没有" + strings.Join(cleared, "、") + "，保存后恢复自动。"
	}
	if c.snap != nil {
		b := c.snap.img.Bounds()
		if change := calibrationAspectChange(scheme.FrameWidth, scheme.FrameHeight, b.Dx(), b.Dy()); change > calibrationAspectTolerance {
			warning := fmt.Sprintf("方案画在 %d×%d 的画面上，和当前 %d×%d 的画面比例相差 %.1f%%，框的位置可能不准。",
				scheme.FrameWidth, scheme.FrameHeight, b.Dx(), b.Dy(), 100*change)
			text += warning
			dialog.ShowInformation("画面比例不同", warning+"请逐个检查后再保存。", c.win)
		}
		if p := c.profile(); p != "" && p != profile {
			text += "这里是" + calibProfileName(p) + "画面，框看起来会有偏差，识别时按布局换算。"
		}
	}
	c.setStatus(text + "检查后按「保存」生效。")
	return nil
}
