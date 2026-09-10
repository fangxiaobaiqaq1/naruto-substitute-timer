//go:build windows && amd64 && cgo

package ocr

import (
	"crypto/sha256"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
	"narutotimer/internal/buildinfo"
)

//go:embed neuraldata/vcruntime/*.dll
var vcRuntimeFiles embed.FS

// Keep third-party terms with the standalone EXE as well as the ZIP package.
//
//go:embed neuraldata/*LICENSE.md neuraldata/*NOTICES.md neuraldata/provenance.json neuraldata/vcruntime/*.md neuraldata/vcruntime/*.json
var runtimeNotices embed.FS

var runtimeLibraryNames = []string{"vcruntime140.dll", "vcruntime140_1.dll", "msvcp140.dll", "msvcp140_1.dll", "onnxruntime.dll"}

type runtimeModules struct {
	handles []windows.Handle
	paths   map[string]string
}

func (m *runtimeModules) close() {
	for i := len(m.handles) - 1; i >= 0; i-- {
		_ = windows.FreeLibrary(m.handles[i])
	}
	m.handles = nil
}

type runtimeLoadError struct {
	Library string
	Err     error
	Path    string
	Loaded  map[string]string
}

func (e *runtimeLoadError) Error() string {
	return fmt.Sprintf("加载 %s 失败: %v; 路径 %s", e.Library, e.Err, e.Path)
}
func (e *runtimeLoadError) Unwrap() error { return e.Err }

type runtimeContextError struct {
	Err    error
	Loaded map[string]string
}

func (e *runtimeContextError) Error() string { return e.Err.Error() }
func (e *runtimeContextError) Unwrap() error { return e.Err }

func prepareLocalRuntime() (string, *runtimeModules, error) {
	files := map[string][]byte{"onnxruntime.dll": recognitionRuntime}
	hash := sha256.New()
	for _, name := range runtimeLibraryNames {
		if name != "onnxruntime.dll" {
			data, err := vcRuntimeFiles.ReadFile("neuraldata/vcruntime/" + name)
			if err != nil {
				return "", nil, err
			}
			files[name] = data
		}
		hash.Write([]byte(name))
		digest := sha256.Sum256(files[name])
		hash.Write(digest[:])
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", nil, err
	}
	dir := filepath.Join(cache, "naruto-timer", "ocr", fmt.Sprintf("%x", hash.Sum(nil)))
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", nil, err
	}
	for _, name := range runtimeLibraryNames {
		if err := ensureRuntimeFile(filepath.Join(dir, name), files[name]); err != nil {
			return "", nil, fmt.Errorf("释放内置 %s 失败: %w", name, err)
		}
	}
	if err := fs.WalkDir(runtimeNotices, "neuraldata", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		data, err := runtimeNotices.ReadFile(path)
		if err != nil {
			return err
		}
		target := filepath.Join(dir, "licenses", filepath.FromSlash(strings.TrimPrefix(path, "neuraldata/")))
		if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			return err
		}
		return ensureRuntimeFile(target, data)
	}); err != nil {
		return "", nil, fmt.Errorf("释放第三方许可失败: %w", err)
	}
	loaded := &runtimeModules{paths: make(map[string]string)}
	for _, name := range runtimeLibraryNames {
		path := filepath.Join(dir, name)
		// A full path to ONNX alone does NOT constrain its dependent DLL search.
		// Preload the complete VC dependency closure in order and exclude CWD/PATH.
		h, err := windows.LoadLibraryEx(path, 0, windows.LOAD_LIBRARY_SEARCH_DLL_LOAD_DIR|windows.LOAD_LIBRARY_SEARCH_SYSTEM32)
		if err != nil {
			loaded.close()
			return "", nil, &runtimeLoadError{Library: name, Err: err, Path: path, Loaded: loaded.paths}
		}
		loaded.handles = append(loaded.handles, h)
		actual, err := moduleFilename(h)
		if err != nil || !strings.EqualFold(filepath.Clean(actual), filepath.Clean(path)) {
			loaded.close()
			return "", nil, fmt.Errorf("OCR DLL 路径冲突: %s; 期望 %s; 实际 %s (%v)", name, path, actual, err)
		}
		loaded.paths[name] = actual
	}
	return filepath.Join(dir, "onnxruntime.dll"), loaded, nil
}

func moduleFilename(h windows.Handle) (string, error) {
	b := make([]uint16, 32768)
	n, err := windows.GetModuleFileName(h, &b[0], uint32(len(b)))
	if err != nil {
		return "", err
	}
	return windows.UTF16ToString(b[:n]), nil
}

func ensureRuntimeFile(path string, data []byte) error {
	digest := sha256.Sum256(data)
	stored, err := os.ReadFile(path)
	if err == nil && sha256.Sum256(stored) == digest {
		return nil
	}
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	// Repair truncated or stale cache content as well as first-use extraction.
	if err := atomicRuntimeWrite(path, data); err != nil {
		// A concurrent worker can win the atomic install and lock the correct DLL.
		other, readErr := os.ReadFile(path)
		if readErr == nil && sha256.Sum256(other) == digest {
			return nil
		}
		return err
	}
	return nil
}

func atomicRuntimeWrite(path string, data []byte) error {
	file, err := os.CreateTemp(filepath.Dir(path), "runtime-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	_, err = file.Write(data)
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	from, err := windows.UTF16PtrFromString(file.Name())
	if err != nil {
		return err
	}
	to, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(from, to, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
}

func runtimeErrorSummary(err error) string {
	var code syscall.Errno
	var load *runtimeLoadError
	if errors.As(err, &load) && errors.As(err, &code) {
		reason := "原生运行库加载失败"
		switch code {
		case 1114:
			reason = "DLL 初始化失败"
		case 126:
			reason = "DLL 或其依赖不可用"
		case 193:
			reason = "DLL 格式或架构不匹配"
		case 5:
			reason = "DLL 访问被拒绝"
		}
		return fmt.Sprintf("%s，Windows 错误 %d，%s。完整原因见 OCR 启动日志。", reason, uint32(code), load.Library)
	}
	detail := []rune(strings.Join(strings.Fields(err.Error()), " "))
	if len(detail) > 220 {
		detail = append(detail[:220], '…')
	}
	return string(detail) + "；完整原因见 OCR 启动日志。"
}

func writeRuntimeFailure(err error) {
	var load *runtimeLoadError
	var context *runtimeContextError
	var paths map[string]string
	if errors.As(err, &load) {
		paths = load.Loaded
	} else if errors.As(err, &context) {
		paths = context.Loaded
	}
	writeRuntimeReport("ocr-runtime-error.json", paths, err)
}
func writeRuntimeStatus(paths map[string]string) {
	writeRuntimeReport("ocr-runtime-status.json", paths, nil)
}

func writeRuntimeReport(name string, paths map[string]string, cause error) {
	dir := RuntimeLogDirectory()
	if dir == "" || os.MkdirAll(dir, 0700) != nil {
		return
	}
	var code syscall.Errno
	detail := ""
	executable, _ := os.Executable()
	if cause != nil {
		detail = cause.Error()
		errors.As(cause, &code)
	}
	// Fixed-size metadata snapshots; successful retries never erase the last
	// failure. No image, player name, or recognition payload is recorded here.
	report := struct {
		At           time.Time         `json:"at"`
		Version      string            `json:"version"`
		Executable   string            `json:"executable"`
		PID          int               `json:"pid"`
		Error        string            `json:"error,omitempty"`
		WindowsError uint32            `json:"windows_error,omitempty"`
		Libraries    map[string]string `json:"libraries,omitempty"`
	}{time.Now(), buildinfo.Version, executable, os.Getpid(), detail, uint32(code), paths}
	data, err := json.MarshalIndent(report, "", "  ")
	if err == nil && len(data) <= 64<<10 {
		_ = atomicRuntimeWrite(filepath.Join(dir, name), data)
	}
}
