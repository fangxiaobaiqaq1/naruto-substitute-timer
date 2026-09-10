//go:build windows && amd64

package mumu

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type Instance struct {
	Root    string
	Index   int
	Name    string
	Running bool
	PID     int
}

func (i Instance) Label() string {
	s := "未启动"
	if i.Running {
		s = "运行中"
	}
	return fmt.Sprintf("%s · 实例 %d · %s · %s", i.Name, i.Index, s, i.Root)
}

type limitedBuffer struct{ bytes.Buffer }

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if b.Len()+len(p) > 2<<20 {
		return 0, errors.New("模拟器信息过大")
	}
	return b.Buffer.Write(p)
}
func ListInstances(ctx context.Context, root string) ([]Instance, error) {
	roots := []string{root}
	if root == "" {
		var e error
		roots, e = DiscoverInstallations()
		if e != nil {
			return nil, e
		}
	}
	var out []Instance
	var errs []error
	for _, r := range roots {
		if _, e := FindDLL(r); e != nil {
			errs = append(errs, e)
			continue
		}
		manager := filepath.Join(r, "nx_main", "MuMuManager.exe")
		if _, e := os.Stat(manager); e != nil {
			errs = append(errs, fmt.Errorf("此 MuMu 版本未提供多开管理接口，请手动填写实例编号"))
			continue
		}
		sub, cancel := context.WithTimeout(ctx, 5*time.Second)
		cmd := exec.CommandContext(sub, manager, "info", "-v", "all")
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
		var data limitedBuffer
		cmd.Stdout = &data
		cmd.Stderr = &limitedBuffer{}
		e := cmd.Run()
		cancel()
		if e != nil {
			errs = append(errs, e)
			continue
		}
		items, e := parseInstances(data.Bytes(), r)
		if e != nil {
			errs = append(errs, e)
			continue
		}
		out = append(out, items...)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Root != out[j].Root {
			return out[i].Root < out[j].Root
		}
		return out[i].Index < out[j].Index
	})
	if len(out) == 0 {
		if len(errs) > 0 {
			return nil, fmt.Errorf("无法枚举 MuMu 实例: %w", errors.Join(errs...))
		}
		return nil, errors.New("未找到 MuMu 实例，请先启动模拟器")
	}
	return out, nil
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
		out = append(out, Instance{root, id, name, item.Running || item.Android, item.PID})
	}
	return out, nil
}

// Auto selection only picks a unique running game, never an arbitrary PID.
func OpenAuto(o Options) (*Client, error) {
	items, e := ListInstances(context.Background(), o.InstallDir)
	if e != nil {
		return nil, e
	}
	var chosen *Client
	matches := 0
	running := 0
	for _, item := range items {
		if !item.Running {
			continue
		}
		running++
		candidate := o
		candidate.InstallDir = item.Root
		candidate.Instance = item.Index
		c, e := Open(candidate)
		if e != nil {
			continue
		}
		if !c.hasGame() {
			c.Close()
			continue
		}
		matches++
		if chosen == nil {
			chosen = c
		} else {
			c.Close()
		}
	}
	if matches == 1 {
		return chosen, nil
	}
	if chosen != nil {
		chosen.Close()
	}
	if matches > 1 {
		return nil, errors.New("多个 MuMu 实例都在运行游戏，请在「设置 → 模拟器」选择要计时的实例")
	}
	if running == 0 {
		return nil, errors.New("请先启动 MuMu 实例及火影忍者游戏")
	}
	return nil, errors.New("MuMu 已运行，请打开火影忍者；或在「设置 → 模拟器」选择实例")
}
func (c *Client) Source() string {
	return fmt.Sprintf("MuMu 实例 %d · %s", c.opts.Instance, c.opts.InstallDir)
}
