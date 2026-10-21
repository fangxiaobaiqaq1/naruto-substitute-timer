package leidian

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"io"
	"net"
	"strconv"
	"time"
)

const (
	// DefaultADBServerPort is the local adb host server port (adb -P default).
	DefaultADBServerPort = 5037
	// rawFormatRGBA8888 is PIXEL_FORMAT_RGBA_8888 in screencap's raw header.
	rawFormatRGBA8888 = 1
	// rawMaxFailures disables the raw transport for the session after this
	// many consecutive failures; each failure still falls back to PNG.
	rawMaxFailures = 3
)

// Dialer opens a stream to the adb host server. It is injectable so the
// protocol can be unit tested with net.Pipe.
type Dialer func(ctx context.Context) (io.ReadWriteCloser, error)

// TCPDialer dials the adb host server on 127.0.0.1:port.
func TCPDialer(port int) Dialer {
	if port <= 0 {
		port = DefaultADBServerPort
	}
	address := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	return func(ctx context.Context) (io.ReadWriteCloser, error) {
		var d net.Dialer
		return d.DialContext(ctx, "tcp", address)
	}
}

// RawScreencap talks the adb host protocol directly instead of spawning
// adb.exe, and asks the device for an uncompressed screencap. It skips
// process creation and on-device PNG encoding/host decoding.
type RawScreencap struct {
	Dial    Dialer
	Serial  string
	Timeout time.Duration
	// ForceOpaque sets alpha to 255. Callers enable it only after verifying
	// the PNG path yields opaque frames, so pixels stay identical.
	ForceOpaque bool

	failures int
	disabled bool
}

// Enabled reports whether the raw transport is still in use this session.
func (r *RawScreencap) Enabled() bool { return r != nil && !r.disabled }

// Disable turns the raw transport off for the session.
func (r *RawScreencap) Disable() { r.disabled = true }

// Capture fetches one frame. dst is reused when it has the right size;
// pass nil to always allocate. Any error means the caller should fall back
// to the PNG path for this frame.
func (r *RawScreencap) Capture(ctx context.Context, dst *image.RGBA) (*image.RGBA, error) {
	if r.disabled {
		return nil, errors.New("raw screencap disabled")
	}
	img, err := r.capture(ctx, dst)
	if err != nil {
		r.failures++
		if r.failures >= rawMaxFailures {
			r.disabled = true
		}
		return nil, err
	}
	r.failures = 0
	return img, nil
}

func (r *RawScreencap) capture(ctx context.Context, dst *image.RGBA) (*image.RGBA, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	timeout := r.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	dial := r.Dial
	if dial == nil {
		dial = TCPDialer(DefaultADBServerPort)
	}
	conn, err := dial(ctx)
	if err != nil {
		return nil, fmt.Errorf("adb server: %w", err)
	}
	defer conn.Close()
	if dc, ok := conn.(interface{ SetDeadline(time.Time) error }); ok {
		if deadline, ok := ctx.Deadline(); ok {
			_ = dc.SetDeadline(deadline)
		}
	}
	// Close the stream on cancel so blocked reads on non-deadline streams
	// (net.Pipe in tests honors deadlines too) return promptly.
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	return rawScreencap(conn, r.Serial, dst, r.ForceOpaque)
}

// rawScreencap runs the transport + exec:screencap exchange on rw.
func rawScreencap(rw io.ReadWriter, serial string, dst *image.RGBA, forceOpaque bool) (*image.RGBA, error) {
	if serial == "" {
		return nil, errors.New("adb serial is empty")
	}
	if err := adbRequest(rw, "host:transport:"+serial); err != nil {
		return nil, err
	}
	if err := adbRequest(rw, "exec:screencap"); err != nil {
		return nil, err
	}
	return readRawScreencap(rw, dst, forceOpaque)
}

// adbRequest sends a length-prefixed host request and reads OKAY/FAIL.
func adbRequest(rw io.ReadWriter, request string) error {
	if len(request) > 0xffff {
		return errors.New("adb request too long")
	}
	if _, err := io.WriteString(rw, fmt.Sprintf("%04x%s", len(request), request)); err != nil {
		return fmt.Errorf("adb %s: %w", request, err)
	}
	var status [4]byte
	if _, err := io.ReadFull(rw, status[:]); err != nil {
		return fmt.Errorf("adb %s status: %w", request, err)
	}
	switch string(status[:]) {
	case "OKAY":
		return nil
	case "FAIL":
		var length [4]byte
		if _, err := io.ReadFull(rw, length[:]); err != nil {
			return fmt.Errorf("adb %s: FAIL", request)
		}
		n, err := strconv.ParseUint(string(length[:]), 16, 16)
		if err != nil {
			return fmt.Errorf("adb %s: FAIL", request)
		}
		message := make([]byte, n)
		if _, err := io.ReadFull(rw, message); err != nil {
			return fmt.Errorf("adb %s: FAIL", request)
		}
		return fmt.Errorf("adb %s: FAIL: %s", request, message)
	default:
		return fmt.Errorf("adb %s: unexpected status %q", request, status[:])
	}
}

// readRawScreencap parses screencap's raw output: width, height and format
// as little-endian uint32, then a colorspace uint32 on newer Android (16-byte
// header) or none (12-byte header), then width*height*4 pixel bytes until EOF.
// The header length is inferred from the total stream length.
func readRawScreencap(r io.Reader, dst *image.RGBA, forceOpaque bool) (*image.RGBA, error) {
	var header [16]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return nil, fmt.Errorf("raw screencap header: %w", err)
	}
	w := binary.LittleEndian.Uint32(header[0:4])
	h := binary.LittleEndian.Uint32(header[4:8])
	format := binary.LittleEndian.Uint32(header[8:12])
	if w < 1 || h < 1 || w > 8192 || h > 8192 || uint64(w)*uint64(h) > 16777216 {
		return nil, fmt.Errorf("raw screencap size invalid %dx%d", w, h)
	}
	if format != rawFormatRGBA8888 {
		return nil, fmt.Errorf("raw screencap format %d is not RGBA_8888", format)
	}
	width, height := int(w), int(h)
	size := width * height * 4
	img := dst
	if img == nil || img.Rect != image.Rect(0, 0, width, height) || img.Stride != 4*width || len(img.Pix) != size {
		img = image.NewRGBA(image.Rect(0, 0, width, height))
	}
	pix := img.Pix
	// Assume a 12-byte header first: header[12:16] are the first pixel bytes.
	copy(pix[:4], header[12:16])
	if _, err := io.ReadFull(r, pix[4:]); err != nil {
		return nil, fmt.Errorf("raw screencap payload: %w", err)
	}
	var tail [5]byte
	n, err := io.ReadFull(r, tail[:])
	switch {
	case n == 0 && (err == io.EOF):
		// 12-byte header; pixels already in place.
	case n == 4 && err == io.ErrUnexpectedEOF:
		// 16-byte header: header[12:16] was colorspace, shift pixels down.
		copy(pix, pix[4:])
		copy(pix[size-4:], tail[:4])
	case err != nil && err != io.EOF && err != io.ErrUnexpectedEOF:
		return nil, fmt.Errorf("raw screencap payload: %w", err)
	default:
		return nil, fmt.Errorf("raw screencap length does not match %dx%d header", width, height)
	}
	if forceOpaque {
		for i := 3; i < size; i += 4 {
			pix[i] = 255
		}
	}
	return img, nil
}
