package leidian

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"image"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

func rawPayload(w, h, format uint32, colorspace bool, pix []byte) []byte {
	var buf bytes.Buffer
	_ = binary.Write(&buf, binary.LittleEndian, [3]uint32{w, h, format})
	if colorspace {
		_ = binary.Write(&buf, binary.LittleEndian, uint32(1))
	}
	buf.Write(pix)
	return buf.Bytes()
}

func readRequest(r io.Reader) (string, error) {
	var length [4]byte
	if _, err := io.ReadFull(r, length[:]); err != nil {
		return "", err
	}
	var n int
	if _, err := fmt.Sscanf(string(length[:]), "%04x", &n); err != nil {
		return "", err
	}
	body := make([]byte, n)
	_, err := io.ReadFull(r, body)
	return string(body), err
}

func failReply(message string) string { return fmt.Sprintf("FAIL%04x%s", len(message), message) }

// fakeADB serves one connection: replies[i] answers the i-th request; after
// the last request payload is streamed and the connection closed.
func fakeADB(t *testing.T, conn net.Conn, replies []string, payload []byte, requests chan<- string) {
	t.Helper()
	go func() {
		defer conn.Close()
		for _, reply := range replies {
			request, err := readRequest(conn)
			if err != nil {
				return
			}
			requests <- request
			if _, err := io.WriteString(conn, reply); err != nil || reply != "OKAY" {
				return
			}
		}
		_, _ = conn.Write(payload)
	}()
}

func pipeScreencap(t *testing.T, replies []string, payload []byte) (*RawScreencap, chan string) {
	requests := make(chan string, 4)
	raw := &RawScreencap{Serial: "127.0.0.1:5555", Timeout: 2 * time.Second, Dial: func(context.Context) (io.ReadWriteCloser, error) {
		client, server := net.Pipe()
		fakeADB(t, server, replies, payload, requests)
		return client, nil
	}}
	return raw, requests
}

func testRGBA(w, h int, alpha byte) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	copy(img.Pix, randomBytes(len(img.Pix), int64(w*h)))
	for i := 3; i < len(img.Pix); i += 4 {
		img.Pix[i] = alpha
	}
	return img
}

func TestRawScreencapHeaders(t *testing.T) {
	for _, colorspace := range []bool{false, true} {
		want := testRGBA(37, 19, 255)
		raw, requests := pipeScreencap(t, []string{"OKAY", "OKAY"}, rawPayload(37, 19, 1, colorspace, want.Pix))
		got, err := raw.Capture(context.Background(), nil)
		if err != nil {
			t.Fatalf("colorspace=%v: %v", colorspace, err)
		}
		if got.Rect != want.Rect || !bytes.Equal(got.Pix, want.Pix) {
			t.Fatalf("colorspace=%v: pixel mismatch", colorspace)
		}
		if first, second := <-requests, <-requests; first != "host:transport:127.0.0.1:5555" || second != "exec:screencap" {
			t.Fatalf("requests = %q, %q", first, second)
		}
	}
}

func TestRawScreencapForceOpaqueAndReuse(t *testing.T) {
	src := testRGBA(16, 8, 0)
	want := image.NewRGBA(src.Rect)
	copy(want.Pix, src.Pix)
	for i := 3; i < len(want.Pix); i += 4 {
		want.Pix[i] = 255
	}
	raw, _ := pipeScreencap(t, []string{"OKAY", "OKAY"}, rawPayload(16, 8, 1, true, src.Pix))
	raw.ForceOpaque = true
	dst := image.NewRGBA(image.Rect(0, 0, 16, 8))
	got, err := raw.Capture(context.Background(), dst)
	if err != nil {
		t.Fatal(err)
	}
	if got != dst || !bytes.Equal(got.Pix, want.Pix) {
		t.Fatalf("dst reused=%v, pixels equal=%v", got == dst, bytes.Equal(got.Pix, want.Pix))
	}
}

func TestRawScreencapErrors(t *testing.T) {
	pix := testRGBA(8, 4, 255).Pix
	for _, tc := range []struct {
		name    string
		replies []string
		payload []byte
		want    string
	}{
		{"transport FAIL", []string{failReply("device '127.0.0.1:5555' not found")}, nil, "not found"},
		{"exec FAIL", []string{"OKAY", failReply("closed")}, nil, "FAIL: closed"},
		{"truncated", []string{"OKAY", "OKAY"}, rawPayload(8, 4, 1, false, pix[:len(pix)-9]), "payload"},
		{"bad length", []string{"OKAY", "OKAY"}, rawPayload(8, 4, 1, true, append(append([]byte{}, pix...), 1, 2)), "length"},
		{"bad format", []string{"OKAY", "OKAY"}, rawPayload(8, 4, 5, false, pix), "format"},
		{"bad size", []string{"OKAY", "OKAY"}, rawPayload(0, 4, 1, false, []byte{0, 0, 0, 0}), "size"},
		{"short header", []string{"OKAY", "OKAY"}, []byte{1, 2, 3}, "header"},
	} {
		raw, _ := pipeScreencap(t, tc.replies, tc.payload)
		if _, err := raw.Capture(context.Background(), nil); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s: err=%v, want %q", tc.name, err, tc.want)
		}
	}
}

func TestRawScreencapDisablesAfterConsecutiveFailures(t *testing.T) {
	good := rawPayload(4, 2, 1, false, testRGBA(4, 2, 255).Pix)
	bad := rawPayload(4, 2, 2, false, testRGBA(4, 2, 255).Pix)
	payloads := [][]byte{bad, bad, good, bad, bad, bad}
	call := 0
	raw := &RawScreencap{Serial: "s", Dial: func(context.Context) (io.ReadWriteCloser, error) {
		client, server := net.Pipe()
		fakeADB(t, server, []string{"OKAY", "OKAY"}, payloads[call], make(chan string, 4))
		call++
		return client, nil
	}}
	for i := 0; i < 6; i++ {
		_, err := raw.Capture(context.Background(), nil)
		if (err == nil) != (i == 2) {
			t.Fatalf("call %d err=%v", i, err)
		}
		if raw.Enabled() != (i < 5) {
			t.Fatalf("call %d enabled=%v", i, raw.Enabled())
		}
	}
	if _, err := raw.Capture(context.Background(), nil); err == nil || call != 6 {
		t.Fatalf("disabled transport must not dial: err=%v calls=%d", err, call)
	}
}

func TestRawScreencapTimeout(t *testing.T) {
	raw := &RawScreencap{Serial: "s", Timeout: 50 * time.Millisecond, Dial: func(context.Context) (io.ReadWriteCloser, error) {
		client, server := net.Pipe()
		go func() { _, _ = io.Copy(io.Discard, server) }() // never replies
		return client, nil
	}}
	started := time.Now()
	if _, err := raw.Capture(context.Background(), nil); err == nil || time.Since(started) > time.Second {
		t.Fatalf("expected prompt timeout, err=%v after %v", err, time.Since(started))
	}
}

func benchmarkRawParse(b *testing.B, w, h int) {
	payload := rawPayload(uint32(w), uint32(h), 1, true, testRGBA(w, h, 255).Pix)
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	reader := bytes.NewReader(payload)
	b.SetBytes(int64(len(payload)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		reader.Reset(payload)
		if _, err := readRawScreencap(reader, dst, true); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRawScreencapParse(b *testing.B) {
	b.Run("1280x720", func(b *testing.B) { benchmarkRawParse(b, 1280, 720) })
	b.Run("1920x1080", func(b *testing.B) { benchmarkRawParse(b, 1920, 1080) })
	b.Run("1920x1080-alloc", func(b *testing.B) {
		payload := rawPayload(1920, 1080, 1, false, testRGBA(1920, 1080, 255).Pix)
		reader := bytes.NewReader(payload)
		b.SetBytes(int64(len(payload)))
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			reader.Reset(payload)
			if _, err := readRawScreencap(reader, nil, true); err != nil {
				b.Fatal(err)
			}
		}
	})
}

func BenchmarkPNGDecodeConvert1080p(b *testing.B) {
	var buf bytes.Buffer
	if err := pngEncode(&buf, benchImage(1920, 1080)); err != nil {
		b.Fatal(err)
	}
	b.Run("legacy", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			img, _ := pngDecode(bytes.NewReader(buf.Bytes()))
			toRGBALegacy(img)
		}
	})
	b.Run("fast", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			img, _ := pngDecode(bytes.NewReader(buf.Bytes()))
			toRGBA(img)
		}
	})
}
