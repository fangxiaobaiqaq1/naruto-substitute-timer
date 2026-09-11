//go:build windows && amd64

package mumu

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Instance is identified by Root plus Index. Index 0 in two different
// installations represents two separate MuMu targets.
type Instance struct {
	Root           string `json:"root"`
	Index          int    `json:"index"`
	Name           string `json:"name"`
	Running        bool   `json:"running"`
	ProcessStarted bool   `json:"process_started"`
	AndroidStarted bool   `json:"android_started"`
	PID            int    `json:"manager_pid,omitempty"`
}

func (i Instance) Label() string {
	state := "未启动"
	if i.AndroidStarted {
		state = "Android 已启动"
	} else if i.ProcessStarted {
		state = "进程已启动"
	}
	return fmt.Sprintf("%s · 实例 %d · %s · %s", i.Name, i.Index, state, i.Root)
}

type limitedBuffer struct{ bytes.Buffer }

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if b.Len()+len(p) > 2<<20 {
		return 0, errors.New("模拟器信息过大")
	}
	return b.Buffer.Write(p)
}
func listInstancesAtRoot(ctx context.Context, root string) ([]Instance, error) {
	root = canonicalRoot(root)
	if root == "" {
		return nil, errors.New("MuMu 安装目录为空")
	}
	if _, err := FindDLL(root); err != nil {
		return nil, err
	}
	manager := findManager(root)
	if manager == "" {
		return nil, errors.New("此 MuMu 版本未提供多开管理接口，请手动填写实例编号")
	}
	sub, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(sub, manager, "info", "-v", "all")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
	var stdout, stderr limitedBuffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if stderr.Len() > 0 {
			return nil, fmt.Errorf("MuMuManager 退出失败: %w；%s", err, strings.TrimSpace(stderr.String()))
		}
		return nil, err
	}
	items, err := parseInstances(stdout.Bytes(), root)
	if err != nil {
		return nil, err
	}
	return items, nil
}

// ListInstances keeps its established API. With an empty root it returns all
// instances from every running MuMu installation; otherwise it inspects only
// the requested root, which makes manual selection deterministic.
func ListInstances(ctx context.Context, root string) ([]Instance, error) {
	roots := []string{root}
	if strings.TrimSpace(root) == "" {
		var err error
		roots, err = DiscoverInstallations()
		if err != nil {
			return nil, err
		}
	}
	var out []Instance
	var errs []error
	for _, candidate := range roots {
		items, err := listInstancesAtRoot(ctx, candidate)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", candidate, err))
			continue
		}
		out = append(out, items...)
	}
	sort.Slice(out, func(i, j int) bool {
		if !strings.EqualFold(out[i].Root, out[j].Root) {
			return strings.ToLower(out[i].Root) < strings.ToLower(out[j].Root)
		}
		return out[i].Index < out[j].Index
	})
	if len(out) > 0 {
		return out, nil
	}
	if len(errs) > 0 {
		return nil, fmt.Errorf("无法枚举 MuMu 实例: %w", errors.Join(errs...))
	}
	return nil, errors.New("未找到 MuMu 实例，请先启动模拟器")
}

func parseInstances(data []byte, root string) ([]Instance, error) {
	var items map[string]struct {
		Name    string          `json:"name"`
		Index   json.RawMessage `json:"index"`
		Running bool            `json:"is_process_started"`
		Android bool            `json:"is_android_started"`
		PID     int             `json:"pid"`
	}
	if err := json.Unmarshal(data, &items); err != nil {
		return nil, err
	}
	var out []Instance
	for key, item := range items {
		id, e := strconv.Atoi(key)
		if e != nil {
			continue
		}
		if len(item.Index) > 0 {
			v := strings.Trim(string(item.Index), "\"")
			if parsed, e := strconv.Atoi(v); e == nil {
				id = parsed
			} else {
				continue
			}
		}
		if id < 0 {
			continue
		}
		name := strings.TrimSpace(item.Name)
		if name == "" {
			name = "MuMu"
		}
		out = append(out, Instance{
			Root:           canonicalRoot(root),
			Index:          id,
			Name:           name,
			Running:        item.Running || item.Android,
			ProcessStarted: item.Running,
			AndroidStarted: item.Android,
			PID:            item.PID,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Index < out[j].Index })
	return out, nil
}

// OpenAuto only accepts a single connected, running instance. MuMu returns
// display id 0 even when keep-alive is disabled, so it cannot prove which of
// several instances hosts a package. Multiple connected instances require an
// explicit installation-directory + instance-number selection.
func OpenAuto(o Options) (*Client, error) {
	items, err := ListInstances(context.Background(), o.InstallDir)
	if err != nil {
		return nil, err
	}
	var connected []*Client
	var failures []error
	running := 0
	for _, item := range items {
		if !item.Running {
			continue
		}
		running++
		candidate := o
		candidate.InstallDir = item.Root
		candidate.Instance = item.Index
		client, err := Open(candidate)
		if err != nil {
			failures = append(failures, fmt.Errorf("%s 实例 %d: %w", item.Root, item.Index, err))
			continue
		}
		// A non-negative display id does not prove a unique game instance when
		// MuMu keep-alive is disabled, but a negative result is still reliable
		// evidence that this package is not available in this instance.
		if !client.hasGame() {
			_ = client.Close()
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
		return nil, errors.New("检测到多个可连接的 MuMu 实例；请在「设置 → 模拟器」选择安装目录和实例编号")
	}
	if running == 0 {
		return nil, errors.New("请先启动 MuMu 实例及火影忍者游戏")
	}
	if len(failures) > 0 {
		return nil, fmt.Errorf("运行中的 MuMu 实例均无法建立截图连接: %w", errors.Join(failures...))
	}
	return nil, errors.New("MuMu 已运行，但未找到可连接的实例")
}

func (c *Client) Source() string {
	return fmt.Sprintf("MuMu 实例 %d · %s", c.opts.Instance, c.opts.InstallDir)
}
