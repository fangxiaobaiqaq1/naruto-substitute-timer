//go:build windows

package ocr

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"golang.org/x/sys/windows"
	"image"
	"image/png"
	"os/exec"
	"path/filepath"
	"syscall"
	"unicode/utf16"
)

//go:embed worker.ps1
var workerScript string

// NewSystem uses the operating system's installed zh-Hans-CN recognizer. The
// process starts on first use, not during game capture or test construction.
func NewSystem() Recognizer {
	script := utf16.Encode([]rune(workerScript))
	encoded := make([]byte, len(script)*2)
	for i, v := range script {
		binary.LittleEndian.PutUint16(encoded[i*2:], v)
	}
	return newProcessRecognizer(func(ctx context.Context) *exec.Cmd {
		// Never search PATH/current directory for a shell and never interpolate OCR
		// text or paths into commands. Requests are JSON over private anonymous pipes.
		systemDir, err := windows.GetSystemDirectory()
		if err != nil {
			cmd := exec.CommandContext(ctx, "unused")
			cmd.Err = err
			return cmd
		}
		exe := filepath.Join(systemDir, "WindowsPowerShell", "v1.0", "powershell.exe")
		cmd := exec.CommandContext(ctx, exe, "-NoLogo", "-NoProfile", "-NonInteractive", "-EncodedCommand", base64.StdEncoding.EncodeToString(encoded))
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
		return cmd
	})
}

func encodeImage(img image.Image) (string, error) {
	if img == nil || img.Bounds().Empty() || img.Bounds().Dx() > 2400 || img.Bounds().Dy() > 2400 {
		return "", fmt.Errorf("invalid OCR image dimensions")
	}
	var out bytes.Buffer
	encoder := png.Encoder{CompressionLevel: png.BestSpeed}
	if err := encoder.Encode(&out, img); err != nil {
		return "", err
	}
	if out.Len() > 3<<20 {
		return "", fmt.Errorf("OCR image exceeds size limit")
	}
	return base64.StdEncoding.EncodeToString(out.Bytes()), nil
}
