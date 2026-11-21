// Package sound plays the optional last-3-seconds reminder. Only the built-in
// chime and user-imported WAV/MP3 files are played; nothing is downloaded.
package sound

import (
	_ "embed"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// DefaultWAV is a synthesized two-note chime (22.05 kHz mono, 0.57 s).
//
//go:embed default.wav
var DefaultWAV []byte

// MaxImportBytes bounds an imported reminder file.
const MaxImportBytes = 5 << 20

// Extensions lists the importable formats.
var Extensions = []string{".wav", ".mp3"}

// Supported reports whether path has an importable extension.
func Supported(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	for _, e := range Extensions {
		if ext == e {
			return true
		}
	}
	return false
}

// Play starts the reminder asynchronously: the built-in chime when path is
// empty, otherwise the imported file. A newer call replaces a playing one.
func Play(path string) error {
	if path == "" {
		return playDefault()
	}
	if !Supported(path) {
		return fmt.Errorf("不支持的音效格式：%s（支持 WAV / MP3）", filepath.Ext(path))
	}
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("找不到音效文件：%w", err)
	}
	return playFile(path)
}

// Import copies src into dir as warn-sound<ext> and returns the stored path.
// The copy keeps working after the original is moved or deleted.
func Import(src io.Reader, name, dir string) (string, error) {
	if !Supported(name) {
		return "", fmt.Errorf("不支持的音效格式：%s（支持 WAV / MP3）", filepath.Ext(name))
	}
	data, err := io.ReadAll(io.LimitReader(src, MaxImportBytes+1))
	if err != nil {
		return "", err
	}
	if len(data) == 0 {
		return "", errors.New("音效文件是空的")
	}
	if len(data) > MaxImportBytes {
		return "", fmt.Errorf("音效文件超过 %d MB", MaxImportBytes>>20)
	}
	ext := strings.ToLower(filepath.Ext(name))
	if ext == ".wav" && (len(data) < 12 || string(data[:4]) != "RIFF" || string(data[8:12]) != "WAVE") {
		return "", errors.New("不是有效的 WAV 文件")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	for _, e := range Extensions {
		_ = os.Remove(filepath.Join(dir, "warn-sound"+e))
	}
	dst := filepath.Join(dir, "warn-sound"+ext)
	tmp := dst + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, dst); err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	return dst, nil
}
