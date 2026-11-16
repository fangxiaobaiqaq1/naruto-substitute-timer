package diagnostics

import (
	"bufio"
	"compress/gzip"
	"encoding/binary"
	"errors"
	"io"
	"runtime/pprof"
	"strings"
)

const (
	performanceProfileMaxDecoded = int64(64 << 20)
	performanceProfileMaxField   = int64(1 << 20)
	performanceProfileMaxLine    = 64 << 10
)

var (
	errPerformanceProfileFormat = errors.New("invalid performance profile encoding")
	errPerformanceProfileLimit  = errors.New("performance profile exceeds read limit")
	errPerformanceProfilePanic  = errors.New("performance profile collection or sanitization panicked")
)

// writeSanitizedPerformanceProfile never sends the original profile to disk or
// destination. Only heap/debug=0 and goroutine/debug=2 have a sanitization
// contract. The caller must bound destination and publish it only after a nil
// error: a failed stream may have written partial, sanitized output.
func writeSanitizedPerformanceProfile(kind string, destination io.Writer, debug int) error {
	var sanitize func(io.Reader, io.Writer) error
	switch {
	case kind == "heap" && debug == 0:
		sanitize = func(source io.Reader, destination io.Writer) error {
			return sanitizePerformanceHeapProfile(source, destination, performanceProfileMaxDecoded, performanceProfileMaxField)
		}
	case kind == "goroutine" && debug == 2:
		sanitize = func(source io.Reader, destination io.Writer) error {
			return sanitizePerformanceGoroutineProfile(source, destination, performanceProfileMaxDecoded, performanceProfileMaxLine)
		}
	default:
		return errors.New("unsupported performance profile kind or debug mode")
	}
	if destination == nil {
		return errors.New("missing performance profile destination")
	}
	profile := pprof.Lookup(kind)
	if profile == nil {
		return errors.New("runtime performance profile unavailable")
	}
	return streamSanitizedPerformanceProfile(destination, func(w io.Writer) error {
		return profile.WriteTo(w, debug)
	}, sanitize)
}

// A pipe applies backpressure without retaining the raw profile. Every exit,
// including a destination panic, cancels the producer and joins it. Panic values
// are deliberately not formatted: they can themselves contain private paths.
func streamSanitizedPerformanceProfile(destination io.Writer, produce func(io.Writer) error, sanitize func(io.Reader, io.Writer) error) (err error) {
	reader, writer := io.Pipe()
	done := make(chan error, 1)
	go func() {
		var produceErr error
		returned := false
		defer func() {
			_ = recover()
			if !returned {
				produceErr = errPerformanceProfilePanic
			}
			_ = writer.CloseWithError(produceErr)
			done <- produceErr
		}()
		produceErr = produce(writer)
		returned = true
	}()
	returned := false
	defer func() {
		_ = recover()
		if !returned {
			err = errPerformanceProfilePanic
		}
		_ = reader.CloseWithError(err)
		if produceErr := <-done; err == nil {
			err = produceErr
		}
	}()
	err = sanitize(reader, performanceProfileCheckedWriter{destination})
	returned = true
	return err
}

// gzip and io.Copy must also see a short write when a broken Writer omits its
// error. In particular, a truncated gzip header must not count as success.
type performanceProfileCheckedWriter struct{ io.Writer }

func (w performanceProfileCheckedWriter) Write(p []byte) (int, error) {
	n, err := w.Writer.Write(p)
	if err == nil && n != len(p) {
		err = io.ErrShortWrite
	}
	return n, err
}

func sanitizePerformanceHeapProfile(source io.Reader, destination io.Writer, maxDecoded, maxField int64) error {
	compressed, err := gzip.NewReader(source)
	if err != nil {
		return err
	}
	defer compressed.Close()
	// The extra byte distinguishes an exact-limit profile from an oversized
	// one. Reading to EOF also validates gzip trailers and producer errors.
	limited := &io.LimitedReader{R: compressed, N: maxDecoded + 1}
	encoded := gzip.NewWriter(performanceProfileCheckedWriter{destination})
	err = sanitizePerformanceProfileProto(bufio.NewReader(limited), encoded, maxField)
	if limited.N == 0 {
		return errPerformanceProfileLimit
	}
	if err != nil {
		return err // Do not finalize a gzip stream with invalid/truncated input.
	}
	return encoded.Close()
}

// Only the top-level string_table (field 6) is changed. Other messages remain
// opaque, including their string indexes. Keep duplicate strings and original
// encodings of all unchanged fields; do not rebuild or deduplicate the table.
func sanitizePerformanceProfileProto(source *bufio.Reader, destination io.Writer, maxField int64) error {
	destination = performanceProfileCheckedWriter{destination}
	stringsSeen := false
	for {
		tag, err := readPerformanceProfileVarint(source)
		if err == io.EOF {
			if !stringsSeen {
				return errPerformanceProfileFormat
			}
			return nil
		}
		if err != nil {
			return err
		}
		field, wire := tag.value>>3, tag.value&7
		if field == 0 || field > (1<<29)-1 || (field == 6 && wire != 2) {
			return errPerformanceProfileFormat
		}
		if wire != 0 && wire != 1 && wire != 2 && wire != 5 {
			return errPerformanceProfileFormat // Go pprof does not use groups.
		}
		if _, err := destination.Write(tag.raw[:tag.n]); err != nil {
			return err
		}
		switch wire {
		case 0:
			value, err := readPerformanceProfileVarint(source)
			if err != nil {
				return performanceProfileRequired(err)
			}
			if _, err := destination.Write(value.raw[:value.n]); err != nil {
				return err
			}
		case 1, 5:
			size := int64(8)
			if wire == 5 {
				size = 4
			}
			if _, err := io.CopyN(destination, source, size); err != nil {
				return performanceProfileRequired(err)
			}
		case 2:
			length, err := readPerformanceProfileVarint(source)
			if err != nil {
				return performanceProfileRequired(err)
			}
			if length.value > uint64(maxField) {
				return errPerformanceProfileLimit
			}
			if field != 6 {
				if _, err := destination.Write(length.raw[:length.n]); err != nil {
					return err
				}
				if _, err := io.CopyN(destination, source, int64(length.value)); err != nil {
					return performanceProfileRequired(err)
				}
				continue
			}
			text := make([]byte, int(length.value))
			if _, err := io.ReadFull(source, text); err != nil {
				return performanceProfileRequired(err)
			}
			if !stringsSeen && len(text) != 0 {
				return errPerformanceProfileFormat // pprof requires string_table[0] == "".
			}
			stringsSeen = true
			original := string(text)
			safe := performanceProfilePathBase(original)
			if safe != original {
				length.n = binary.PutUvarint(length.raw[:], uint64(len(safe)))
			}
			if _, err := destination.Write(length.raw[:length.n]); err != nil {
				return err
			}
			if _, err := io.WriteString(destination, safe); err != nil {
				return err
			}
		}
	}
}

type performanceProfileVarint struct {
	value uint64
	raw   [binary.MaxVarintLen64]byte
	n     int
}

func readPerformanceProfileVarint(source io.ByteReader) (value performanceProfileVarint, err error) {
	for i := 0; i < len(value.raw); i++ {
		b, err := source.ReadByte()
		if err != nil {
			if i > 0 {
				err = performanceProfileRequired(err)
			}
			return value, err
		}
		value.raw[i], value.n = b, i+1
		if i == 9 && b > 1 {
			return value, errPerformanceProfileFormat
		}
		value.value |= uint64(b&0x7f) << (7 * i)
		if b < 0x80 {
			return value, nil
		}
	}
	return value, errPerformanceProfileFormat
}

func performanceProfileRequired(err error) error {
	if err == io.EOF {
		return io.ErrUnexpectedEOF
	}
	return err
}

func performanceProfileAbsolutePath(text string) bool {
	return strings.HasPrefix(text, "/") || strings.HasPrefix(text, `\\`) ||
		(len(text) >= 3 && ((text[0] >= 'A' && text[0] <= 'Z') || (text[0] >= 'a' && text[0] <= 'z')) &&
			text[1] == ':' && (text[2] == '/' || text[2] == '\\'))
}

func performanceProfilePathBase(text string) string {
	if !performanceProfileAbsolutePath(text) {
		return text
	}
	end := len(text)
	for end > 0 && (text[end-1] == '/' || text[end-1] == '\\') {
		end--
	}
	base := text[strings.LastIndexAny(text[:end], `/\`)+1 : end]
	if base == "" || (len(base) == 2 && base[1] == ':') {
		return "[path]"
	}
	return base
}

func sanitizePerformanceGoroutineProfile(source io.Reader, destination io.Writer, maxBytes int64, maxLine int) error {
	limited := &io.LimitedReader{R: source, N: maxBytes + 1}
	lines := bufio.NewReaderSize(limited, maxLine+1)
	destination = performanceProfileCheckedWriter{destination}
	for {
		line, readErr := lines.ReadSlice('\n')
		if limited.N == 0 || len(line) > maxLine || readErr == bufio.ErrBufferFull {
			return errPerformanceProfileLimit
		}
		if len(line) > 0 {
			safe, err := sanitizePerformanceStackLine(string(line))
			if err != nil {
				return err
			}
			if _, err := io.WriteString(destination, safe); err != nil {
				return err
			}
		}
		if readErr != nil {
			if readErr == io.EOF {
				return nil
			}
			return readErr
		}
	}
}

func sanitizePerformanceStackLine(line string) (string, error) {
	start := 0
	for start < len(line) && (line[start] == ' ' || line[start] == '\t') {
		start++
	}
	if !performanceProfileAbsolutePath(line[start:]) {
		return line, nil
	}
	// Search from the right: a drive letter and the filename itself can have
	// colons, and paths may have spaces. Preserve :line and all debug suffixes.
	for colon := len(line) - 1; colon >= start; colon-- {
		if line[colon] != ':' {
			continue
		}
		end := colon + 1
		for end < len(line) && line[end] >= '0' && line[end] <= '9' {
			end++
		}
		if end == colon+1 || (end < len(line) && !strings.ContainsRune(" \t\r\n", rune(line[end]))) {
			continue
		}
		return line[:start] + performanceProfilePathBase(line[start:colon]) + line[colon:], nil
	}
	// Never copy a partial absolute source path (for example a truncated dump).
	return "", errPerformanceProfileFormat
}
