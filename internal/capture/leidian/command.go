package leidian

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/png"
	"os/exec"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"narutotimer/internal/imgsafe"
)

const (
	// maxCommandOutput bounds what one ldconsole/adb call may return on stdout.
	// A 1080p `screencap -p` PNG is a few MiB; 64 MiB covers any frame within
	// the 16 Mpx capture limit.
	maxCommandOutput = 64 << 20
	// maxCommandStderr keeps the start of stderr for error messages only.
	maxCommandStderr = 16 << 10
	// maxErrorDetail bounds command output quoted inside an error message.
	maxErrorDetail = 512
)

var errOutputLimit = errors.New("命令输出超过上限")

// cappedBuffer stores at most limit bytes. With discard set, the excess is
// dropped silently (stderr, kept for diagnostics only); otherwise the write
// fails, which stops os/exec from reading the pipe so the command ends instead
// of filling memory.
type cappedBuffer struct {
	buf      bytes.Buffer
	limit    int
	discard  bool
	exceeded bool
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	room := b.limit - b.buf.Len()
	if len(p) <= room {
		return b.buf.Write(p)
	}
	if room > 0 {
		b.buf.Write(p[:room])
	}
	b.exceeded = true
	if b.discard {
		return len(p), nil
	}
	return max(room, 0), errOutputLimit
}

// runCapped runs cmd with separate, bounded stdout and stderr. stdout is
// returned only on success; stderr never mixes into it (ADB prints daemon
// start-up messages there, which used to end up inside screenshot PNGs).
func runCapped(cmd *exec.Cmd, stdoutLimit int) ([]byte, error) {
	stdout := &cappedBuffer{limit: stdoutLimit}
	stderr := &cappedBuffer{limit: maxCommandStderr, discard: true}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	err := cmd.Run()
	if stdout.exceeded {
		err = fmt.Errorf("%w（%d 字节）", errOutputLimit, stdoutLimit)
	}
	if err != nil {
		return nil, commandError(cmd.Path, cmd.Args, err, stdout.buf.Bytes(), stderr.buf.Bytes())
	}
	return stdout.buf.Bytes(), nil
}

func commandError(name string, argv []string, err error, stdout, stderr []byte) error {
	args := ""
	if len(argv) > 1 {
		args = strings.Join(argv[1:], " ")
	}
	// Both streams: adb reports some outcomes ("already connected", "failed to
	// connect") on stdout even when it fails.
	var details []string
	for _, out := range [][]byte{stderr, stdout} {
		if detail := errorDetail(out); detail != "" {
			details = append(details, detail)
		}
	}
	if message := strings.Join(details, "; "); message != "" {
		return fmt.Errorf("%s %s: %w: %s", filepath.Base(name), args, err, message)
	}
	return fmt.Errorf("%s %s: %w", filepath.Base(name), args, err)
}

// errorDetail turns command output into a short, printable error detail:
// console text (UTF-8 or GBK) is trimmed and truncated; binary output such as
// a partial PNG is omitted.
func errorDetail(out []byte) string {
	if len(out) > 4*maxErrorDetail {
		out = out[:4*maxErrorDetail]
	}
	if bytes.IndexByte(out, 0) >= 0 {
		return ""
	}
	text := strings.TrimSpace(decodeConsoleText(out))
	if !utf8.ValidString(text) {
		return ""
	}
	if len(text) > maxErrorDetail {
		cut := maxErrorDetail
		for cut > 0 && !utf8.RuneStart(text[cut]) {
			cut--
		}
		text = text[:cut] + "…"
	}
	return text
}

// decodeScreencapPNG checks the PNG header against the capture limits before
// decoding, so a broken or hostile device cannot make the decoder allocate
// gigabytes. Valid screenshots decode to the same pixels as before.
func decodeScreencapPNG(data []byte) (*image.RGBA, error) {
	cfg, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("解析雷电 ADB PNG: %w", err)
	}
	if err := imgsafe.Capture.Check(cfg, "png"); err != nil {
		return nil, fmt.Errorf("雷电截图尺寸无效 %dx%d", cfg.Width, cfg.Height)
	}
	decoded, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("解析雷电 ADB PNG: %w", err)
	}
	return toRGBA(decoded), nil
}
