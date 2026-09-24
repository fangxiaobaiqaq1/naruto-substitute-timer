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
	"unicode/utf8"

	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/transform"
	"sync"
	"syscall"
	"time"
)

const (
	DefaultADBSerialBasePort = 5555
	DefaultADBSerialHost     = "127.0.0.1"
	DefaultADBName           = "adb.exe"
	DefaultConsoleName       = "ldconsole.exe"
)

type Options struct {
	InstallDir     string        `json:"install_dir"`
	ConsolePath    string        `json:"console_path,omitempty"`
	ADBPath        string        `json:"adb_path,omitempty"`
	Index          int           `json:"index"`
	Serial         string        `json:"serial,omitempty"`
	Package        string        `json:"package,omitempty"`
	Connect        bool          `json:"connect_on_start"`
	CommandTimeout time.Duration `json:"command_timeout"`
}

type Client struct {
	mu     sync.Mutex
	ctx    context.Context
	cancel context.CancelFunc
	opts   Options
	serial string
	adb    string
}

func defaultTimeout(value time.Duration) time.Duration {
	if value <= 0 {
		return 5 * time.Second
	}
	return value
}

func canonicalRoot(root string) string {
	root = strings.TrimSpace(root)
	if root == "" {
		return ""
	}
	absolute, err := filepath.Abs(root)
	if err == nil {
		root = absolute
	}
	return filepath.Clean(root)
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
	if resolved.Connect {
		if err := client.connect(); err != nil {
			cancel()
			return nil, err
		}
	}
	return client, nil
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

func (c *Client) Capture() (*image.RGBA, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cancel == nil {
		return nil, errors.New("雷电采集已关闭")
	}
	if err := c.connect(); err != nil {
		return nil, err
	}
	data, err := runCommand(c.ctx, c.opts.CommandTimeout, c.adb, "-s", c.serial, "exec-out", "screencap", "-p")
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
	result := image.NewRGBA(image.Rect(0, 0, bounds.Dx(), bounds.Dy()))
	for y := 0; y < bounds.Dy(); y++ {
		for x := 0; x < bounds.Dx(); x++ {
			result.Set(x, y, decoded.At(bounds.Min.X+x, bounds.Min.Y+y))
		}
	}
	return result, nil
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

// Instance is identified by installation root plus LDPlayer index. The ADB
// serial is derived evidence and can be overridden for non-default mappings.
type Instance struct {
	Root           string `json:"root"`
	Index          int    `json:"index"`
	Name           string `json:"name"`
	Running        bool   `json:"running"`
	ProcessStarted bool   `json:"process_started"`
	AndroidStarted bool   `json:"android_started"`
	PID            int    `json:"pid,omitempty"`
	Serial         string `json:"serial"`
	Resolution     string `json:"resolution,omitempty"`
}

func (i Instance) Label() string {
	state := "未启动"
	if i.AndroidStarted {
		state = "Android 已启动"
	} else if i.ProcessStarted || i.Running {
		state = "进程已启动"
	}
	return fmt.Sprintf("%s · 实例 %d · %s · %s", i.Name, i.Index, state, i.Serial)
}

type list2Row struct {
	Index   int
	Name    string
	Running bool
	PID     int
	VBoxPID int
	Width   int
	Height  int
	DPI     int
	Raw     string
}

func decodeConsoleText(data []byte) string {
	if utf8.Valid(data) {
		return string(data)
	}
	decoded, _, err := transform.Bytes(simplifiedchinese.GBK.NewDecoder(), data)
	if err == nil {
		return string(decoded)
	}
	return string(data)
}

func parseList2(data []byte, root string) ([]Instance, error) {
	var result []Instance
	for _, line := range strings.Split(strings.ReplaceAll(decodeConsoleText(data), "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Split(line, ",")
		if len(fields) < 2 {
			continue
		}
		index, err := strconv.Atoi(strings.TrimSpace(fields[0]))
		if err != nil || index < 0 {
			continue
		}
		row := list2Row{Index: index, Name: strings.TrimSpace(fields[1]), Raw: line}
		if len(fields) > 2 {
			row.Running = strings.TrimSpace(fields[2]) == "1"
		}
		if len(fields) > 5 {
			row.PID, _ = strconv.Atoi(strings.TrimSpace(fields[5]))
		}
		if len(fields) > 6 {
			row.VBoxPID, _ = strconv.Atoi(strings.TrimSpace(fields[6]))
		}
		if len(fields) > 7 {
			row.Width, _ = strconv.Atoi(strings.TrimSpace(fields[7]))
		}
		if len(fields) > 8 {
			row.Height, _ = strconv.Atoi(strings.TrimSpace(fields[8]))
		}
		if len(fields) > 9 {
			row.DPI, _ = strconv.Atoi(strings.TrimSpace(fields[9]))
		}
		if row.Name == "" {
			row.Name = "雷电"
		}
		result = append(result, Instance{
			Root: canonicalRoot(root), Index: row.Index, Name: row.Name,
			Running:        row.Running || row.PID > 0 || row.VBoxPID > 0,
			ProcessStarted: row.Running || row.PID > 0,
			AndroidStarted: row.Running,
			PID:            row.PID, Serial: fmt.Sprintf("%s:%d", DefaultADBSerialHost, DefaultADBSerialBasePort+row.Index),
			Resolution: func() string {
				if row.Width > 0 && row.Height > 0 {
					return fmt.Sprintf("%dx%d", row.Width, row.Height)
				}
				return ""
			}(),
		})
	}
	if len(result) == 0 {
		return nil, errors.New("雷电 list2 未返回实例")
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Index < result[j].Index })
	return result, nil
}

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

// Probe executes the same ADB path as live capture and returns structured
// evidence suitable for a cloud agent or support bundle.
type ProbeResult struct {
	StartedAt      time.Time   `json:"started_at"`
	EndedAt        time.Time   `json:"ended_at"`
	Requested      Options     `json:"requested"`
	Resolved       Options     `json:"resolved"`
	Instance       *Instance   `json:"instance,omitempty"`
	State          string      `json:"state,omitempty"`
	Width          int         `json:"width,omitempty"`
	Height         int         `json:"height,omitempty"`
	ImageAvailable bool        `json:"image_available"`
	PackageFound   bool        `json:"package_found,omitempty"`
	Error          string      `json:"error,omitempty"`
	Steps          []ProbeStep `json:"steps"`
}

type ProbeStep struct {
	Stage      string  `json:"stage"`
	OK         bool    `json:"ok"`
	Detail     string  `json:"detail,omitempty"`
	Error      string  `json:"error,omitempty"`
	DurationMS float64 `json:"duration_ms"`
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
	client, err := Open(Options{InstallDir: resolved.InstallDir, ConsolePath: resolved.ConsolePath, ADBPath: resolved.ADBPath, Index: resolved.Index, Serial: resolved.Serial, Package: resolved.Package, Connect: true, CommandTimeout: resolved.CommandTimeout})
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
