package config

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
)

const DefaultPath = "config.json"

// Keep our readers out of the brief replacement window on Windows. External
// readers may keep the old snapshot; a sharing violation leaves that file intact.
var fileMu sync.RWMutex

func LoadOrCreate(path string) (Config, error) {
	cfg, notes, err := LoadOrCreateWithNotes(path)
	printNotes(path, notes)
	return cfg, err
}

// LoadOrCreateWithNotes is LoadOrCreate returning LoadWithNotes' notes
// instead of printing them, for a caller that logs them itself.
func LoadOrCreateWithNotes(path string) (Config, []string, error) {
	if path == "" {
		path = DefaultPath
	}
	cfg, notes, err := LoadWithNotes(path)
	if err == nil {
		return cfg, notes, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return Config{}, nil, err
	}
	cfg = Default()
	if err := Save(path, cfg); err != nil {
		return Config{}, nil, err
	}
	return cfg, nil, nil
}

// printNotes is the warning log of Load and LoadOrCreate (stderr).
func printNotes(path string, notes []string) {
	for _, note := range notes {
		fmt.Fprintf(os.Stderr, "config %q: %s\n", path, note)
	}
}

func Save(path string, cfg Config) error {
	if path == "" {
		path = DefaultPath
	}
	data, err := encode(cfg)
	if err != nil {
		return err
	}
	return writeFile(path, data)
}

// Writer serializes the saves of one settings owner without holding the
// owner's state lock during the disk write (fsync; antivirus scans can take
// hundreds of ms on Windows). Take a Snapshot while holding that lock: it only
// validates and encodes. Release the lock, then call Save. Snapshots are
// numbered in the order they were taken, so a slow older save can never put
// older settings over newer ones that were already written. The zero value is
// ready to use.
type Writer struct {
	taken   atomic.Uint64
	mu      sync.Mutex
	written uint64
}

// Snapshot is one encoded settings state waiting to be written.
type Snapshot struct {
	w    *Writer
	seq  uint64
	path string
	data []byte
	err  error
}

// Snapshot validates and encodes cfg now; no file I/O.
func (w *Writer) Snapshot(path string, cfg Config) Snapshot {
	if path == "" {
		path = DefaultPath
	}
	data, err := encode(cfg)
	return Snapshot{w: w, seq: w.taken.Add(1), path: path, data: data, err: err}
}

// Save writes the snapshot unless a newer one from the same Writer has
// already been written (it holds every change this one has).
func (s Snapshot) Save() error {
	if s.err != nil {
		return s.err
	}
	s.w.mu.Lock()
	defer s.w.mu.Unlock()
	if s.seq <= s.w.written {
		return nil
	}
	if err := writeFile(s.path, s.data); err != nil {
		return err
	}
	s.w.written = s.seq
	return nil
}

func encode(cfg Config) ([]byte, error) {
	if err := Validate(cfg); err != nil {
		return nil, fmt.Errorf("validate config: %w", err)
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode config: %w", err)
	}
	return append(data, '\n'), nil
}

func writeFile(path string, data []byte) error {
	fileMu.Lock()
	defer fileMu.Unlock()
	// Never truncate the current settings. A complete, synced sibling is renamed
	// in one commit; a write/close/commit failure leaves the old file untouched.
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+"-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary config %q: %w", path, err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err = tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write config %q: %w", path, err)
	}
	if err = tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("sync config %q: %w", path, err)
	}
	if err = tmp.Close(); err != nil {
		return fmt.Errorf("close config %q: %w", path, err)
	}
	if err = os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("replace config %q: %w", path, err)
	}
	return nil
}

// Load reads and validates path. What LoadWithNotes repairs is reported on
// stderr.
func Load(path string) (Config, error) {
	cfg, notes, err := LoadWithNotes(path)
	printNotes(path, notes)
	return cfg, err
}

// LoadWithNotes reads and validates path like Load and returns, instead of
// printing, a note for each setting it repaired rather than refusing the
// file. The only repair: calibration boxes outside the size limits, saved by
// versions without them, are dropped (the region stays automatic, which is
// what the engine does with such a box anyway); the file is not rewritten,
// the next settings save stores the repaired configuration.
func LoadWithNotes(path string) (Config, []string, error) {
	cfg := Default()
	if path == "" {
		return cfg, nil, Validate(cfg)
	}
	fileMu.RLock()
	defer fileMu.RUnlock()
	f, err := os.Open(path)
	if err != nil {
		return Config{}, nil, fmt.Errorf("open config: %w", err)
	}
	defer f.Close()
	r := bufio.NewReader(f)
	// Windows editors (Notepad) may save UTF-8 with a byte order mark.
	if mark, err := r.Peek(len(utf8BOM)); err == nil && bytes.Equal(mark, utf8BOM) {
		_, _ = r.Discard(len(utf8BOM))
	}
	if err := decodeStrict(r, &cfg); err != nil {
		return Config{}, nil, fmt.Errorf("decode config %q: %w", path, err)
	}
	notes := dropUnusableCalibrationBoxes(&cfg)
	if err := Validate(cfg); err != nil {
		return Config{}, nil, fmt.Errorf("validate config %q: %w", path, err)
	}
	return cfg, notes, nil
}

var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

func decodeStrict(r io.Reader, dst any) error {
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			return fmt.Errorf("multiple JSON values")
		}
		return err
	}
	return nil
}
