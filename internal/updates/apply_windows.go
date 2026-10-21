//go:build windows

package updates

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
	"io"
	"narutotimer/internal/buildinfo"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

const ApplyArgument = "--timer-apply-update"

var errElevatedUpdate = errors.New("以管理员身份运行时不支持自动更新：请关闭计时器，以普通方式重新打开后再更新，或下载安装包手动更新")

// TOKEN_ELEVATION_TYPE values (winnt.h).
const tokenElevationTypeFull = 2

// elevatedSplitToken reports whether the process runs with the elevated half of
// a split UAC token ("Run as administrator" while UAC is on). Unelevated
// programs of the same user can then write the update cache and the per-user
// install directory, so the helper's verify, rename and start steps could be
// raced into elevated code execution. Without a split token (UAC off, or a
// standard user) there is no lower-integrity peer to defend against.
func elevatedSplitToken() bool {
	var elevationType, n uint32
	token := windows.GetCurrentProcessToken()
	if err := windows.GetTokenInformation(token, windows.TokenElevationType, (*byte)(unsafe.Pointer(&elevationType)), uint32(unsafe.Sizeof(elevationType)), &n); err != nil {
		return token.IsElevated()
	}
	return elevationType == tokenElevationTypeFull
}

func PrepareRestart(incoming string, r Release) error {
	if elevatedSplitToken() {
		return errElevatedUpdate
	}
	cmp, e := Compare(r.Tag, buildinfo.Version)
	if e != nil {
		return e
	}
	if cmp <= 0 {
		return errors.New("仅支持更新到更新的正式版本")
	}
	a, e := r.Executable()
	if e != nil {
		return e
	}
	hash, _ := assetHash(a)
	if e = verifyFile(incoming, hash, a.Size); e != nil {
		return e
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	check := exec.CommandContext(ctx, incoming, "--version")
	check.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
	out, e := check.Output()
	if e != nil || strings.TrimSpace(string(out)) != r.Tag {
		return errors.New("新版程序启动验证失败，请改用安装包")
	}
	target, e := os.Executable()
	if e != nil {
		return e
	}
	target, e = filepath.Abs(target)
	if e != nil {
		return e
	}
	if strings.EqualFold(target, incoming) {
		return errors.New("更新文件不能覆盖自身缓存")
	}
	// Check directory write access before closing the application.
	probe, e := os.CreateTemp(filepath.Dir(target), ".timer-update-write-*")
	if e != nil {
		return errors.New("程序目录不可写，请下载安装版或将便携版移到可写目录")
	}
	probe.Close()
	os.Remove(probe.Name())
	work, e := os.MkdirTemp(Directory(), "apply-*")
	if e != nil {
		return e
	}
	helper := filepath.Join(work, "updater.exe")
	if e = copyFile(target, helper); e != nil {
		return e
	}
	p := Plan{uint32(os.Getpid()), target, incoming, hash, a.Size, r.Tag}
	data, e := json.Marshal(p)
	if e != nil {
		return e
	}
	path := filepath.Join(work, "plan.json")
	if e = os.WriteFile(path, data, 0600); e != nil {
		return e
	}
	cmd := exec.Command(helper, ApplyArgument, path)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
	if e = cmd.Start(); e != nil {
		return e
	}
	return cmd.Process.Release()
}
func ApplyFile(path string) error {
	d := Directory()
	abs, e := filepath.Abs(path)
	if e != nil {
		return e
	}
	rel, e := filepath.Rel(d, abs)
	if e != nil || !filepath.IsLocal(rel) {
		return errors.New("更新计划不在本程序缓存中")
	}
	data, e := os.ReadFile(abs)
	if e != nil {
		return e
	}
	if len(data) > 16384 {
		return errors.New("更新计划无效")
	}
	var p Plan
	if e = json.Unmarshal(data, &p); e != nil {
		return e
	}
	// The helper is a copy of the program being replaced; bind the plan to it.
	helper, e := os.Executable()
	if e != nil {
		return e
	}
	err := apply(p, helper, true)
	report := struct {
		At      time.Time `json:"at"`
		Version string    `json:"version"`
		Error   string    `json:"error,omitempty"`
	}{At: time.Now(), Version: p.Version}
	if err != nil {
		report.Error = err.Error()
	}
	b, _ := json.MarshalIndent(report, "", "  ")
	_ = os.WriteFile(filepath.Join(d, "last-update.json"), b, 0600)
	return err
}

// apply installs a verified plan. helper is the helper's own executable (see
// checkPlan); tests that call apply directly pass "".
func apply(p Plan, helper string, restart bool) error {
	if elevatedSplitToken() {
		return errElevatedUpdate
	}
	if e := checkPlan(p, helper); e != nil {
		return e
	}
	h, e := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_QUERY_LIMITED_INFORMATION, false, p.Parent)
	if e == nil {
		defer windows.CloseHandle(h)
		buf := make([]uint16, 32768)
		n := uint32(len(buf))
		if e = windows.QueryFullProcessImageName(h, 0, &buf[0], &n); e != nil {
			return e
		}
		if !strings.EqualFold(filepath.Clean(windows.UTF16ToString(buf[:n])), filepath.Clean(p.Target)) {
			return errors.New("等待的进程与目标程序不一致")
		}
		result, e := windows.WaitForSingleObject(h, 60000)
		if e != nil || result != windows.WAIT_OBJECT_0 {
			return errors.New("旧程序未退出，更新已取消；请关闭后重试")
		}
	} else if !errors.Is(e, windows.ERROR_INVALID_PARAMETER) {
		return e
	}
	sibling, e := os.CreateTemp(filepath.Dir(p.Target), ".timer-update-*.exe")
	if e != nil {
		return e
	}
	staged := sibling.Name()
	sibling.Close()
	defer os.Remove(staged)
	if e = copyFile(p.Incoming, staged); e != nil {
		return e
	}
	if e = verifyFile(staged, p.Hash, p.Size); e != nil {
		return e
	}
	backup := p.Target + ".previous"
	if _, e = os.Stat(backup); e == nil {
		if e = os.Remove(backup); e != nil {
			return e
		}
	} else if !os.IsNotExist(e) {
		return e
	}
	// Keep exactly one recoverable binary. Never touch the user's settings/logs.
	if e = os.Rename(p.Target, backup); e != nil {
		return fmt.Errorf("无法替换旧程序: %w", e)
	}
	if e = os.Rename(staged, p.Target); e != nil {
		_ = os.Rename(backup, p.Target)
		return e
	}
	if restart {
		cmd := exec.Command(p.Target)
		cmd.Dir = filepath.Dir(p.Target)
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
		if e = cmd.Start(); e != nil {
			_ = os.Remove(p.Target)
			_ = os.Rename(backup, p.Target)
			return fmt.Errorf("已恢复旧版，新版启动失败: %w", e)
		}
		_ = cmd.Process.Release()
	}
	updateInstalledVersion(p)
	return nil
}
func copyFile(from, to string) error {
	src, e := os.Open(from)
	if e != nil {
		return e
	}
	defer src.Close()
	dst, e := os.OpenFile(to, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0700)
	if e != nil {
		return e
	}
	_, e = io.Copy(dst, src)
	if e == nil {
		e = dst.Sync()
	}
	closeErr := dst.Close()
	if e != nil {
		return e
	}
	return closeErr
}

func updateInstalledVersion(p Plan) {
	if _, e := os.Stat(filepath.Join(filepath.Dir(p.Target), "installed.marker")); e != nil {
		return
	}
	key, e := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Uninstall\{0A7B5ED9-57C5-4DE2-A1C0-25C8D671E5F3}_is1`, registry.QUERY_VALUE|registry.SET_VALUE)
	if e != nil {
		return
	}
	defer key.Close()
	location, _, e := key.GetStringValue("InstallLocation")
	if e == nil && strings.EqualFold(filepath.Clean(location), filepath.Dir(p.Target)) {
		_ = key.SetStringValue("DisplayVersion", p.Version)
	}
}
