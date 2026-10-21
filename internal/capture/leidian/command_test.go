package leidian

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"

	"narutotimer/internal/imgsafe/imgsafetest"
)

// UR-11 PoC: png.Decode allocated ~34 GB for this 72-byte PNG before the old
// size check ran (fatal OOM); the header is now checked first.
func TestDecodeScreencapPNGRejectsDimensionBombBeforeDecoding(t *testing.T) {
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	img, err := decodeScreencapPNG(imgsafetest.DimensionBombPNG())
	runtime.ReadMemStats(&after)
	if err == nil || img != nil || !strings.Contains(err.Error(), "65535x65535") {
		t.Fatalf("bomb not rejected by its header: %v", err)
	}
	if grew := after.TotalAlloc - before.TotalAlloc; grew > 1<<20 {
		t.Fatalf("rejecting the bomb allocated %d bytes", grew)
	}
}

func TestDecodeScreencapPNGKeepsPixels(t *testing.T) {
	src := image.NewNRGBA(image.Rect(0, 0, 64, 36))
	for i := range src.Pix {
		src.Pix[i] = byte(i*13 + 1)
	}
	for i := 3; i < len(src.Pix); i += 4 {
		src.Pix[i] = 255
	}
	src.Set(5, 5, color.NRGBA{R: 9, G: 8, B: 7, A: 200}) // force the non-opaque path
	var buf bytes.Buffer
	if err := png.Encode(&buf, src); err != nil {
		t.Fatal(err)
	}
	decoded, err := png.Decode(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	want := toRGBA(decoded)
	got, err := decodeScreencapPNG(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if got.Rect != want.Rect || !bytes.Equal(got.Pix, want.Pix) {
		t.Fatal("decoded pixels differ from png.Decode + toRGBA")
	}
	if _, err := decodeScreencapPNG([]byte("* daemon not running; starting now\n")); err == nil {
		t.Fatal("non-PNG output accepted")
	}
}

// The test binary doubles as a fake adb/ldconsole.
func TestMain(m *testing.M) {
	switch os.Getenv("LEIDIAN_FAKE_COMMAND") {
	case "":
		os.Exit(m.Run())
	case "split":
		fmt.Fprint(os.Stderr, "* daemon not running; starting now at tcp:5037\n")
		fmt.Fprint(os.Stdout, "device\n")
		os.Exit(0)
	case "flood":
		chunk := bytes.Repeat([]byte{0x89, 'P', 'N', 'G'}, 1<<14)
		for i := 0; i < 1<<12; i++ { // 256 MiB unless the reader gives up
			if _, err := os.Stdout.Write(chunk); err != nil {
				os.Exit(3)
			}
		}
		os.Exit(0)
	case "fail":
		fmt.Fprint(os.Stdout, "already connected to 127.0.0.1:5555\n")
		fmt.Fprint(os.Stderr, strings.Repeat("e", 5000)+"\n")
		os.Exit(1)
	case "binary-fail":
		os.Stdout.Write([]byte{0x89, 'P', 'N', 'G', 0, 0, 0, 13})
		os.Exit(1)
	}
	os.Exit(2)
}

func fakeCommand(t *testing.T, mode string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	cmd.Env = append(os.Environ(), "LEIDIAN_FAKE_COMMAND="+mode)
	return cmd
}

func TestRunCappedKeepsStderrOutOfStdout(t *testing.T) {
	out, err := runCapped(fakeCommand(t, "split"), maxCommandOutput)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "device\n" {
		t.Fatalf("stdout = %q; stderr must not be mixed in", out)
	}
}

func TestRunCappedStopsAtStdoutLimit(t *testing.T) {
	out, err := runCapped(fakeCommand(t, "flood"), 1<<20)
	if err == nil || out != nil || !errors.Is(err, errOutputLimit) {
		t.Fatalf("flood not stopped: %d bytes, %v", len(out), err)
	}
}

func TestRunCappedErrorsQuoteBoundedText(t *testing.T) {
	_, err := runCapped(fakeCommand(t, "fail"), maxCommandOutput)
	if err == nil || !strings.Contains(err.Error(), "already connected") {
		t.Fatalf("stdout detail lost: %v", err)
	}
	if len(err.Error()) > 2*maxErrorDetail+256 {
		t.Fatalf("error message not truncated: %d bytes", len(err.Error()))
	}
	_, err = runCapped(fakeCommand(t, "binary-fail"), maxCommandOutput)
	if err == nil || strings.Contains(err.Error(), "PNG") {
		t.Fatalf("binary output quoted in error: %q", err)
	}
}

func TestErrorDetail(t *testing.T) {
	if got := errorDetail([]byte("  error: device offline \r\n")); got != "error: device offline" {
		t.Fatalf("got %q", got)
	}
	if got := errorDetail([]byte{'a', 0, 'b'}); got != "" {
		t.Fatalf("binary output kept: %q", got)
	}
	long := errorDetail([]byte(strings.Repeat("雷", 400)))
	if len(long) > maxErrorDetail+len("…") || !strings.HasSuffix(long, "…") {
		t.Fatalf("long detail = %d bytes", len(long))
	}
}
