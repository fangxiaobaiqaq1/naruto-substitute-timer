//go:build windows && amd64

// Package mumu receives rendered screen pixels through MuMu's screenshot SDK.
// It does not inspect game memory, inject code, or send input events.
package mumu

import (
	"errors"
	"fmt"
	"image"
	"path/filepath"
	"runtime"
	"sync"
	"syscall"
	"unsafe"
)

type Options struct {
	InstallDir string `json:"install_dir"`
	DLLPath    string `json:"dll_path,omitempty"`
	Instance   int    `json:"instance"`
	DisplayID  int    `json:"display_id"`
	Package    string `json:"package,omitempty"`
}

type Client struct {
	mu                           sync.Mutex
	dll                          *syscall.DLL
	capture, disconnect, display *syscall.Proc
	handle                       uintptr
	opts                         Options
	dllPath                      string
	pixels                       []byte
}

func nativeCallError(err error) string {
	if err == nil {
		return ""
	}
	if errno, ok := err.(syscall.Errno); ok && errno == 0 {
		return ""
	}
	return err.Error()
}

// FindDLL searches inside the selected installation. Candidate enumeration is
// deterministic even if multiple device-version SDK copies exist.
func FindDLL(root string) (string, error) {
	candidates, err := FindDLLCandidates(root)
	if err != nil {
		return "", stageError(ProbeStageFindSDK, err)
	}
	return candidates[0].Path, nil
}

func Open(o Options) (*Client, error) {
	if o.InstallDir == "" {
		root, err := DiscoverInstallation()
		if err != nil {
			return nil, stageError(ProbeStageResolveRoot, err)
		}
		o.InstallDir = root
	}
	if o.Instance < 0 || o.DisplayID < 0 {
		return nil, stageError(ProbeStageResolveRoot, fmt.Errorf("MuMu instance/displayId must be non-negative"))
	}
	root, err := filepath.Abs(o.InstallDir)
	if err != nil {
		return nil, stageError(ProbeStageResolveRoot, err)
	}
	o.InstallDir = filepath.Clean(root)
	path := o.DLLPath
	if path == "" {
		path, err = FindDLL(o.InstallDir)
		if err != nil {
			return nil, err
		}
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return nil, stageError(ProbeStageFindSDK, err)
	}
	dll, err := syscall.LoadDLL(path)
	if err != nil {
		return nil, stageError(ProbeStageLoadSDK, fmt.Errorf("load MuMu screenshot SDK: %w", err))
	}
	client := &Client{dll: dll, opts: o, dllPath: path}
	connect, err := dll.FindProc("nemu_connect")
	if err == nil {
		client.capture, err = dll.FindProc("nemu_capture_display")
	}
	if err == nil {
		client.disconnect, err = dll.FindProc("nemu_disconnect")
	}
	if err != nil {
		_ = dll.Release()
		return nil, stageError(ProbeStageExports, err)
	}
	client.display, _ = dll.FindProc("nemu_get_display_id")
	utf16Path, err := syscall.UTF16PtrFromString(o.InstallDir)
	if err != nil {
		_ = dll.Release()
		return nil, stageError(ProbeStageResolveRoot, err)
	}
	handle, _, callErr := connect.Call(uintptr(unsafe.Pointer(utf16Path)), uintptr(o.Instance))
	runtime.KeepAlive(utf16Path)
	if int32(handle) <= 0 {
		_ = dll.Release()
		detail := fmt.Sprintf("MuMu SDK could not connect to instance %d in %s; api_return=%d", o.Instance, o.InstallDir, int32(handle))
		if nativeErr := nativeCallError(callErr); nativeErr != "" {
			detail += "; win32_last_error=" + nativeErr
		}
		return nil, stageError(ProbeStageConnect, errors.New(detail))
	}
	client.handle = handle
	return client, nil
}

func (c *Client) DLLPath() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.dllPath
}

// Capture returns an owned top-down RGBA image. The SDK has no source frame
// timestamp; callers must report acquisition times rather than invent one.
func (c *Client) Capture() (*image.RGBA, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.handle == 0 {
		return nil, stageError(ProbeStageConnect, fmt.Errorf("MuMu capture is closed"))
	}
	id := c.opts.DisplayID
	if c.opts.Package != "" && c.display != nil {
		pkg, err := syscall.BytePtrFromString(c.opts.Package)
		if err != nil {
			return nil, stageError(ProbeStageDisplay, err)
		}
		result, _, _ := c.display.Call(c.handle, uintptr(unsafe.Pointer(pkg)), 0)
		runtime.KeepAlive(pkg)
		if int32(result) < 0 {
			return nil, stageError(ProbeStageDisplay, fmt.Errorf("MuMu display for %q is unavailable", c.opts.Package))
		}
		id = int(int32(result))
	}
	var width, height int32
	result, _, callErr := c.capture.Call(c.handle, uintptr(id), 0, uintptr(unsafe.Pointer(&width)), uintptr(unsafe.Pointer(&height)), 0)
	if int32(result) != 0 {
		detail := fmt.Sprintf("MuMu capture dimensions: api_return=%d", int32(result))
		if nativeErr := nativeCallError(callErr); nativeErr != "" {
			detail += "; win32_last_error=" + nativeErr
		}
		return nil, stageError(ProbeStageDimensions, errors.New(detail))
	}
	if width < 1 || height < 1 || width > 8192 || height > 8192 || int64(width)*int64(height) > 16777216 {
		return nil, stageError(ProbeStageDimensions, fmt.Errorf("invalid MuMu capture size %dx%d", width, height))
	}
	size := int(width) * int(height) * 4
	if cap(c.pixels) < size {
		c.pixels = make([]byte, size)
	} else {
		c.pixels = c.pixels[:size]
	}
	wantedWidth, wantedHeight := width, height
	result, _, callErr = c.capture.Call(c.handle, uintptr(id), uintptr(size), uintptr(unsafe.Pointer(&width)), uintptr(unsafe.Pointer(&height)), uintptr(unsafe.Pointer(&c.pixels[0])))
	runtime.KeepAlive(c.pixels)
	if int32(result) != 0 {
		detail := fmt.Sprintf("MuMu capture pixels: api_return=%d", int32(result))
		if nativeErr := nativeCallError(callErr); nativeErr != "" {
			detail += "; win32_last_error=" + nativeErr
		}
		return nil, stageError(ProbeStagePixels, errors.New(detail))
	}
	if width != wantedWidth || height != wantedHeight {
		return nil, stageError(ProbeStagePixels, fmt.Errorf("MuMu display resized during capture"))
	}
	return fromBottomUpRGBA(c.pixels, int(width), int(height)), nil
}

func fromBottomUpRGBA(src []byte, width, height int) *image.RGBA {
	image := image.NewRGBA(image.Rect(0, 0, width, height))
	stride := width * 4
	for y := 0; y < height; y++ {
		copy(image.Pix[y*stride:(y+1)*stride], src[(height-1-y)*stride:(height-y)*stride])
	}
	for index := 3; index < len(image.Pix); index += 4 {
		image.Pix[index] = 255
	}
	return image
}

func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.handle == 0 {
		return nil
	}
	c.disconnect.Call(c.handle)
	c.handle = 0
	c.pixels = nil
	return c.dll.Release()
}

func (c *Client) hasGame() bool {
	if c.opts.Package == "" {
		return true
	}
	if c.display == nil {
		return false
	}
	pkg, err := syscall.BytePtrFromString(c.opts.Package)
	if err != nil {
		return false
	}
	value, _, _ := c.display.Call(c.handle, uintptr(unsafe.Pointer(pkg)), 0)
	runtime.KeepAlive(pkg)
	return int32(value) >= 0
}
