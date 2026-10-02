//go:build windows && amd64

// Package leidian connects to LDPlayer/雷电 instances through the emulator's
// supported command-line manager and ADB. It intentionally does not depend on
// a private screenshot DLL: instance control is ldconsole, pixels are ADB.
package leidian

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"narutotimer/internal/win"
)

type Client struct {
	mu     sync.Mutex
	ctx    context.Context
	cancel context.CancelFunc
	opts   Options
	serial string
	adb    string

	methods []string
	// needsReconnect is set after a failed frame; the next Capture re-runs
	// adb connect once instead of on every frame.
	needsReconnect bool

	raw           *RawScreencap
	rawCalibrated bool

	hwnd             uintptr
	windowFailures   int
	windowRetryAfter time.Time
	lastList2        time.Time
}

const (
	windowMaxFailures = 3
	windowBackoff     = 10 * time.Second
	list2Interval     = 10 * time.Second
)

func defaultTimeout(value time.Duration) time.Duration {
	if value <= 0 {
		return 5 * time.Second
	}
	return value
}

func findFile(root string, names ...string) string {
	for _, name := range names {
		candidate := name
		if root != "" && !filepath.IsAbs(candidate) {
			candidate = filepath.Join(root, candidate)
		}
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
	}
	return ""
}

// DiscoverInstallDirs returns likely LDPlayer installation roots. It checks
// common program directories and one level under <drive>:\\leidian, which is
// where LDPlayer14 commonly installs without requiring a hard-coded drive.
func DiscoverInstallDirs() []string {
	seen := make(map[string]struct{})
	var result []string
	add := func(root string) {
		root = canonicalRoot(root)
		if root == "" || findFile(root, DefaultConsoleName, "dnconsole.exe") == "" {
			return
		}
		key := strings.ToLower(root)
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		result = append(result, root)
	}
	addRoots := func(root string) {
		if strings.TrimSpace(root) == "" {
			return
		}
		add(root)
		matches, _ := filepath.Glob(filepath.Join(root, "LDPlayer*"))
		for _, match := range matches {
			add(match)
		}
	}
	for _, base := range []string{
		os.Getenv("ProgramFiles"),
		os.Getenv("ProgramFiles(x86)"),
		os.Getenv("LOCALAPPDATA"),
		os.Getenv("ProgramData"),
	} {
		addRoots(filepath.Join(base, "leidian"))
		addRoots(filepath.Join(base, "LDPlayer"))
		addRoots(filepath.Join(base, "LDPlayer9"))
		addRoots(filepath.Join(base, "LDPlayer14"))
	}
	for drive := 'A'; drive <= 'Z'; drive++ {
		addRoots(fmt.Sprintf("%c:\\leidian", drive))
	}
	sort.Strings(result)
	return result
}

func DiscoverInstallDir() string {
	items := DiscoverInstallDirs()
	if len(items) == 0 {
		return ""
	}
	return items[0]
}

func resolvePaths(o Options) (Options, error) {
	o.InstallDir = canonicalRoot(o.InstallDir)
	if o.InstallDir == "" {
		if o.ConsolePath != "" {
			o.InstallDir = canonicalRoot(filepath.Dir(o.ConsolePath))
		} else if o.ADBPath != "" {
			o.InstallDir = canonicalRoot(filepath.Dir(o.ADBPath))
		} else {
			o.InstallDir = DiscoverInstallDir()
		}
	}
	if o.InstallDir != "" {
		if o.ConsolePath == "" {
			o.ConsolePath = findFile(o.InstallDir, DefaultConsoleName, "dnconsole.exe")
		}
		if o.ADBPath == "" {
			o.ADBPath = findFile(o.InstallDir, DefaultADBName)
		}
	}
	if o.ConsolePath == "" {
		o.ConsolePath = findFile("", DefaultConsoleName, "dnconsole.exe")
	}
	if o.ADBPath == "" {
		o.ADBPath = findFile("", DefaultADBName)
	}
	if o.ConsolePath == "" {
		return o, errors.New("未找到雷电 ldconsole.exe；请填写雷电安装目录或 consolePath")
	}
	if o.ADBPath == "" {
		return o, errors.New("未找到雷电 adb.exe；请填写雷电安装目录或 adbPath")
	}
	if o.Index < 0 {
		return o, errors.New("雷电实例编号必须为非负整数")
	}
	if o.Serial == "" {
		o.Serial = fmt.Sprintf("%s:%d", DefaultADBSerialHost, DefaultADBSerialBasePort+o.Index)
	}
	return o, nil
}

func commandContext(parent context.Context, timeout time.Duration, name string, args ...string) (*exec.Cmd, context.CancelFunc) {
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, defaultTimeout(timeout))
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
	return cmd, cancel
}

func runCommand(parent context.Context, timeout time.Duration, name string, args ...string) ([]byte, error) {
	cmd, cancel := commandContext(parent, timeout, name, args...)
	defer cancel()
	out, err := cmd.CombinedOutput()
	if err != nil {
		message := strings.TrimSpace(string(out))
		if message != "" {
			return nil, fmt.Errorf("%s %s: %w: %s", filepath.Base(name), strings.Join(args, " "), err, message)
		}
		return nil, fmt.Errorf("%s %s: %w", filepath.Base(name), strings.Join(args, " "), err)
	}
	return out, nil
}

func Open(o Options) (*Client, error) {
	resolved, err := resolvePaths(o)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	client := &Client{ctx: ctx, cancel: cancel, opts: resolved, serial: resolved.Serial, adb: resolved.ADBPath}
	client.methods = normalizeMethods(resolved.Methods)
	for _, method := range client.methods {
		if method == MethodADBRaw {
			client.raw = &RawScreencap{Dial: TCPDialer(resolved.ADBServerPort), Serial: resolved.Serial, Timeout: client.captureTimeout()}
		}
	}
	if resolved.Connect {
		if err := client.connect(); err != nil {
			cancel()
			return nil, err
		}
	}
	return client, nil
}

func normalizeMethods(methods []string) []string {
	var result []string
	seen := map[string]bool{}
	for _, method := range methods {
		method = strings.TrimSpace(method)
		if (method == MethodWindow || method == MethodADBRaw || method == MethodADB) && !seen[method] {
			seen[method] = true
			result = append(result, method)
		}
	}
	if len(result) == 0 {
		return DefaultMethods()
	}
	return result
}

func (c *Client) captureTimeout() time.Duration {
	if c.opts.CaptureTimeout > 0 {
		return c.opts.CaptureTimeout
	}
	return defaultTimeout(c.opts.CommandTimeout)
}

func (c *Client) connect() error {
	_, err := runCommand(c.ctx, c.opts.CommandTimeout, c.adb, "connect", c.serial)
	if err != nil && !strings.Contains(strings.ToLower(err.Error()), "already connected") {
		return fmt.Errorf("连接雷电 ADB %s: %w", c.serial, err)
	}
	state, err := runCommand(c.ctx, c.opts.CommandTimeout, c.adb, "-s", c.serial, "get-state")
	if err != nil {
		return fmt.Errorf("检查雷电 ADB %s: %w", c.serial, err)
	}
	if strings.TrimSpace(string(state)) != "device" {
		return fmt.Errorf("雷电 ADB %s 状态不是 device：%s", c.serial, strings.TrimSpace(string(state)))
	}
	return nil
}

// Capture walks the configured method chain and returns the first frame that
// succeeds. Window and raw failures silently fall through to the next method
// for this frame; only a failure of the whole chain is reported, after which
// the next call reconnects ADB once.
func (c *Client) Capture() (*image.RGBA, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cancel == nil {
		return nil, errors.New("雷电采集已关闭")
	}
	if c.needsReconnect {
		if err := c.connect(); err != nil {
			return nil, err
		}
		c.needsReconnect = false
	}
	var errs []error
	for _, method := range c.methods {
		var (
			img *image.RGBA
			err error
		)
		switch method {
		case MethodWindow:
			img, err = c.captureWindow()
		case MethodADBRaw:
			if c.raw == nil || !c.raw.Enabled() {
				continue
			}
			img, err = c.captureRaw()
		case MethodADB:
			img, err = c.capturePNG()
		default:
			continue
		}
		if err == nil {
			return img, nil
		}
		errs = append(errs, fmt.Errorf("%s: %w", method, err))
	}
	c.needsReconnect = true
	if len(errs) == 0 {
		return nil, errors.New("雷电截图失败：没有可用的采集方式")
	}
	return nil, fmt.Errorf("雷电截图失败: %w", errors.Join(errs...))
}

func (c *Client) capturePNG() (*image.RGBA, error) {
	// exec-out PNG encode can take >1 s at 1080p on slow PCs; keep the
	// command timeout rather than the shortened per-frame budget.
	data, err := runCommand(c.ctx, defaultTimeout(c.opts.CommandTimeout), c.adb, "-s", c.serial, "exec-out", "screencap", "-p")
	if err != nil {
		return nil, fmt.Errorf("雷电 ADB 截图失败: %w", err)
	}
	decoded, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("解析雷电 ADB PNG: %w", err)
	}
	bounds := decoded.Bounds()
	if bounds.Dx() < 1 || bounds.Dy() < 1 || bounds.Dx() > 8192 || bounds.Dy() > 8192 || int64(bounds.Dx())*int64(bounds.Dy()) > 16777216 {
		return nil, fmt.Errorf("雷电截图尺寸无效 %dx%d", bounds.Dx(), bounds.Dy())
	}
	return toRGBA(decoded), nil
}

// captureRaw uses the adb host protocol. The first call captures through the
// PNG path instead and only enables raw (with alpha forced to 255) if that
// PNG frame is fully opaque, so raw frames match PNG frames pixel for pixel.
func (c *Client) captureRaw() (*image.RGBA, error) {
	if !c.rawCalibrated {
		img, err := c.capturePNG()
		if err != nil {
			return nil, err
		}
		c.rawCalibrated = true
		if img.Opaque() {
			c.raw.ForceOpaque = true
		} else {
			c.raw.Disable()
		}
		return img, nil
	}
	// Frames are handed to the scheduler as owned images, so never reuse dst.
	return c.raw.Capture(c.ctx, nil)
}

// captureWindow PrintWindows the LDPlayer render window. Blank, busy or
// failed frames return an error so the chain falls back to ADB; after
// repeated failures the mode backs off and the HWND is re-resolved.
func (c *Client) captureWindow() (*image.RGBA, error) {
	if time.Now().Before(c.windowRetryAfter) {
		return nil, errors.New("窗口采集退避中")
	}
	if c.hwnd == 0 || !win.IsWindow(c.hwnd) {
		c.hwnd = 0
		hwnd, err := c.resolveRenderWindow()
		if err != nil {
			c.windowFailed()
			return nil, err
		}
		c.hwnd = hwnd
	}
	img, err := win.CaptureClient(c.hwnd)
	if err != nil {
		if !win.IsWindow(c.hwnd) {
			c.hwnd = 0
		}
		c.windowFailed()
		return nil, err
	}
	c.windowFailures = 0
	return img, nil
}

func (c *Client) windowFailed() {
	c.windowFailures++
	if c.windowFailures >= windowMaxFailures {
		c.windowFailures = 0
		c.windowRetryAfter = time.Now().Add(windowBackoff)
		c.hwnd = 0
	}
}

func (c *Client) resolveRenderWindow() (uintptr, error) {
	hwnd, err := win.FindLeidianRender(c.opts.TopHWND, c.opts.BindHWND)
	if err == nil {
		return hwnd, nil
	}
	if c.opts.ConsolePath == "" || time.Since(c.lastList2) < list2Interval {
		return 0, err
	}
	c.lastList2 = time.Now()
	data, listErr := runCommand(c.ctx, c.opts.CommandTimeout, c.opts.ConsolePath, "list2")
	if listErr != nil {
		return 0, err
	}
	items, listErr := parseList2(data, c.opts.InstallDir)
	if listErr != nil {
		return 0, err
	}
	for _, item := range items {
		if item.Index == c.opts.Index {
			c.opts.TopHWND, c.opts.BindHWND = item.TopHWND, item.BindHWND
			return win.FindLeidianRender(item.TopHWND, item.BindHWND)
		}
	}
	return 0, err
}

func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cancel != nil {
		c.cancel()
		c.cancel = nil
	}
	return nil
}

func (c *Client) Source() string {
	return fmt.Sprintf("雷电 ADB · %s · 实例 %d", c.serial, c.opts.Index)
}

func (c *Client) Serial() string { return c.serial }

func ListInstances(ctx context.Context, root string) ([]Instance, error) {
	o, err := resolvePaths(Options{InstallDir: root})
	if err != nil {
		return nil, err
	}
	data, err := runCommand(ctx, o.CommandTimeout, o.ConsolePath, "list2")
	if err != nil {
		return nil, fmt.Errorf("枚举雷电实例: %w", err)
	}
	items, err := parseList2(data, o.InstallDir)
	if err != nil {
		return nil, err
	}
	for index := range items {
		status, statusErr := runCommand(ctx, o.CommandTimeout, o.ConsolePath, "isrunning", "--index", strconv.Itoa(items[index].Index))
		if statusErr == nil {
			items[index].Running = strings.EqualFold(strings.TrimSpace(string(status)), "running")
			items[index].ProcessStarted = items[index].Running
			items[index].AndroidStarted = items[index].Running
		}
	}
	return items, nil
}

func OpenAuto(o Options) (*Client, error) {
	items, err := ListInstances(context.Background(), o.InstallDir)
	if err != nil {
		return nil, err
	}
	var connected []*Client
	var failures []error
	for _, item := range items {
		if !item.Running {
			continue
		}
		candidate := o
		candidate.Index = item.Index
		candidate.Serial = item.Serial
		candidate.Connect = true
		candidate.TopHWND = item.TopHWND
		candidate.BindHWND = item.BindHWND
		client, openErr := Open(candidate)
		if openErr != nil {
			failures = append(failures, fmt.Errorf("实例 %d: %w", item.Index, openErr))
			continue
		}
		connected = append(connected, client)
	}
	if len(connected) == 1 {
		return connected[0], nil
	}
	for _, client := range connected {
		_ = client.Close()
	}
	if len(connected) > 1 {
		return nil, errors.New("检测到多个可连接的雷电实例；请在设置中选择安装目录和实例编号")
	}
	if len(failures) > 0 {
		return nil, fmt.Errorf("运行中的雷电实例均无法建立 ADB 连接: %w", errors.Join(failures...))
	}
	return nil, errors.New("请先启动雷电实例及 Android 游戏")
}

func probeStep(result *ProbeResult, stage string, started time.Time, detail string, err error) {
	step := ProbeStep{Stage: stage, OK: err == nil, Detail: detail, DurationMS: float64(time.Since(started).Microseconds()) / 1000}
	if err != nil {
		step.Error = err.Error()
		result.Error = err.Error()
	}
	result.Steps = append(result.Steps, step)
}

func ProbeContext(ctx context.Context, options Options) (result ProbeResult) {
	result.StartedAt = time.Now()
	result.Requested = options
	defer func() { result.EndedAt = time.Now() }()
	resolved, err := resolvePaths(options)
	if err != nil {
		probeStep(&result, "resolve", result.StartedAt, "", err)
		return result
	}
	result.Resolved = resolved
	probeStep(&result, "resolve", result.StartedAt, fmt.Sprintf("console=%s; adb=%s; serial=%s", resolved.ConsolePath, resolved.ADBPath, resolved.Serial), nil)
	started := time.Now()
	items, err := ListInstances(ctx, resolved.InstallDir)
	if err != nil {
		probeStep(&result, "instances", started, "", err)
		return result
	}
	var selected *Instance
	for index := range items {
		if items[index].Index == resolved.Index {
			selected = &items[index]
			break
		}
	}
	if selected == nil {
		probeStep(&result, "instances", started, "", fmt.Errorf("未找到雷电实例 %d", resolved.Index))
		return result
	}
	result.Instance = selected
	probeStep(&result, "instances", started, selected.Label(), nil)
	client, err := Open(Options{InstallDir: resolved.InstallDir, ConsolePath: resolved.ConsolePath, ADBPath: resolved.ADBPath, Index: resolved.Index, Serial: resolved.Serial, Package: resolved.Package, Connect: true, CommandTimeout: resolved.CommandTimeout, CaptureTimeout: resolved.CaptureTimeout, Methods: resolved.Methods, ADBServerPort: resolved.ADBServerPort, TopHWND: selected.TopHWND, BindHWND: selected.BindHWND})
	if err != nil {
		probeStep(&result, "connect", time.Now(), "", err)
		return result
	}
	defer client.Close()
	probeStep(&result, "connect", time.Now(), client.Source(), nil)
	started = time.Now()
	img, err := client.Capture()
	if err != nil {
		probeStep(&result, "screencap", started, "", err)
		return result
	}
	result.Width, result.Height = img.Bounds().Dx(), img.Bounds().Dy()
	result.ImageAvailable = true
	probeStep(&result, "screencap", started, fmt.Sprintf("%d x %d", result.Width, result.Height), nil)
	return result
}

func Probe(options Options) ProbeResult {
	return ProbeContext(context.Background(), options)
}
