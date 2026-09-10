//go:build windows && amd64

// Package mumu receives rendered screen pixels through MuMu's screenshot SDK.
// It does not inspect game memory, inject code, or send input events.
package mumu

import (
	"fmt"
	"image"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"syscall"
	"unsafe"
)

type Options struct {
	InstallDir string
	DLLPath    string
	Instance   int
	DisplayID  int
	Package    string
}

type Client struct {
	mu                           sync.Mutex
	dll                          *syscall.DLL
	capture, disconnect, display *syscall.Proc
	handle                       uintptr
	opts                         Options
	pixels                       []byte
}

// FindDLL searches only inside the user-selected installation, not the current
// working directory or PATH. SDK ABI reference:
// https://github.com/MaaXYZ/EmulatorExtras/tree/main/Mumu/external_renderer_ipc
func FindDLL(root string) (string, error) {
	for _, rel := range []string{"nx_main/sdk/external_renderer_ipc.dll", "shell/sdk/external_renderer_ipc.dll"} {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p, nil
		}
	}
	paths, _ := filepath.Glob(filepath.Join(root, "nx_device", "*", "shell", "sdk", "external_renderer_ipc.dll"))
	if len(paths) > 0 {
		return paths[len(paths)-1], nil
	}
	return "", fmt.Errorf("MuMu screenshot SDK not found under %q", root)
}

func Open(o Options) (*Client, error) {
	if o.InstallDir == "" {
		root, err := DiscoverInstallation()
		if err != nil {
			return nil, err
		}
		o.InstallDir = root
	}
	if o.Instance < 0 || o.DisplayID < 0 {
		return nil, fmt.Errorf("MuMu instance/displayId must be non-negative")
	}
	root, err := filepath.Abs(o.InstallDir)
	if err != nil {
		return nil, err
	}
	o.InstallDir = root
	p := o.DLLPath
	if p == "" {
		p, err = FindDLL(root)
		if err != nil {
			return nil, err
		}
	}
	p, err = filepath.Abs(p)
	if err != nil {
		return nil, err
	}
	dll, err := syscall.LoadDLL(p)
	if err != nil {
		return nil, fmt.Errorf("load MuMu screenshot SDK: %w", err)
	}
	c := &Client{dll: dll, opts: o}
	connect, err := dll.FindProc("nemu_connect")
	if err == nil {
		c.capture, err = dll.FindProc("nemu_capture_display")
	}
	if err == nil {
		c.disconnect, err = dll.FindProc("nemu_disconnect")
	}
	if err != nil {
		dll.Release()
		return nil, err
	}
	c.display, _ = dll.FindProc("nemu_get_display_id")
	path, err := syscall.UTF16PtrFromString(root)
	if err != nil {
		dll.Release()
		return nil, err
	}
	h, _, _ := connect.Call(uintptr(unsafe.Pointer(path)), uintptr(o.Instance))
	runtime.KeepAlive(path)
	if int32(h) <= 0 {
		dll.Release()
		return nil, fmt.Errorf("MuMu SDK could not connect to instance %d in %s", o.Instance, root)
	}
	c.handle = h
	return c, nil
}

// Capture returns an owned top-down RGBA image. The SDK has no source frame
// timestamp; callers must report acquisition times rather than invent one.
func (c *Client) Capture() (*image.RGBA, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.handle == 0 {
		return nil, fmt.Errorf("MuMu capture is closed")
	}
	id := c.opts.DisplayID
	if c.opts.Package != "" && c.display != nil {
		pkg, err := syscall.BytePtrFromString(c.opts.Package)
		if err != nil {
			return nil, err
		}
		r, _, _ := c.display.Call(c.handle, uintptr(unsafe.Pointer(pkg)), 0)
		runtime.KeepAlive(pkg)
		if int32(r) < 0 {
			return nil, fmt.Errorf("MuMu display for %q is unavailable", c.opts.Package)
		}
		id = int(int32(r))
	}
	var w, h int32
	r, _, _ := c.capture.Call(c.handle, uintptr(id), 0, uintptr(unsafe.Pointer(&w)), uintptr(unsafe.Pointer(&h)), 0)
	if int32(r) != 0 {
		return nil, fmt.Errorf("MuMu capture dimensions: code %d", int32(r))
	}
	if w < 1 || h < 1 || w > 8192 || h > 8192 || int64(w)*int64(h) > 16777216 {
		return nil, fmt.Errorf("invalid MuMu capture size %dx%d", w, h)
	}
	n := int(w) * int(h) * 4
	if cap(c.pixels) < n {
		c.pixels = make([]byte, n)
	} else {
		c.pixels = c.pixels[:n]
	}
	wantW, wantH := w, h
	r, _, _ = c.capture.Call(c.handle, uintptr(id), uintptr(n), uintptr(unsafe.Pointer(&w)), uintptr(unsafe.Pointer(&h)), uintptr(unsafe.Pointer(&c.pixels[0])))
	runtime.KeepAlive(c.pixels)
	if int32(r) != 0 {
		return nil, fmt.Errorf("MuMu capture pixels: code %d", int32(r))
	}
	if w != wantW || h != wantH {
		return nil, fmt.Errorf("MuMu display resized during capture")
	}
	return fromBottomUpRGBA(c.pixels, int(w), int(h)), nil
}

func fromBottomUpRGBA(src []byte, w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	stride := w * 4
	for y := 0; y < h; y++ {
		copy(img.Pix[y*stride:(y+1)*stride], src[(h-1-y)*stride:(h-y)*stride])
	}
	for i := 3; i < len(img.Pix); i += 4 {
		img.Pix[i] = 255
	}
	return img
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
	r, _, _ := c.display.Call(c.handle, uintptr(unsafe.Pointer(pkg)), 0)
	runtime.KeepAlive(pkg)
	return int32(r) >= 0
}
