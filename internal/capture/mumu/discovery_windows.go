//go:build windows && amd64

package mumu

import (
	"fmt"
	"golang.org/x/sys/windows"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unsafe"
)

// DiscoverInstallation only considers known running MuMu executables and
// validates their installation layout before selecting an SDK.
func DiscoverInstallations() ([]string, error) {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, fmt.Errorf("enumerate MuMu processes: %w", err)
	}
	defer windows.CloseHandle(snapshot)
	entry := windows.ProcessEntry32{Size: uint32(unsafe.Sizeof(windows.ProcessEntry32{}))}
	roots := map[string]string{}
	for err = windows.Process32First(snapshot, &entry); err == nil; err = windows.Process32Next(snapshot, &entry) {
		switch strings.ToLower(windows.UTF16ToString(entry.ExeFile[:])) {
		case "mumunxmain.exe", "mumunxdevice.exe", "mumuplayer.exe", "nemuplayer.exe", "mumunxservice.exe":
		default:
			continue
		}
		process, e := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, entry.ProcessID)
		if e != nil {
			continue
		}
		buf := make([]uint16, 32768)
		size := uint32(len(buf))
		e = windows.QueryFullProcessImageName(process, 0, &buf[0], &size)
		windows.CloseHandle(process)
		if e != nil {
			continue
		}
		if root := installationFromExecutable(windows.UTF16ToString(buf[:size])); root != "" {
			roots[strings.ToLower(root)] = root
		}
	}
	if err != windows.ERROR_NO_MORE_FILES {
		return nil, fmt.Errorf("enumerate MuMu processes: %w", err)
	}
	var out []string
	for _, root := range roots {
		out = append(out, root)
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i]) < strings.ToLower(out[j]) })
	if len(out) == 0 {
		return nil, fmt.Errorf("请先启动 MuMu，或在设置中选择安装目录")
	}
	return out, nil
}
func DiscoverInstallation() (string, error) {
	roots, err := DiscoverInstallations()
	if err != nil {
		return "", err
	}
	if len(roots) != 1 {
		return "", fmt.Errorf("发现多个 MuMu 安装，请在设置中选择")
	}
	return roots[0], nil
}
func installationFromExecutable(executable string) string {
	for dir, n := filepath.Dir(executable), 0; n < 6; n++ {
		mainDir := filepath.Join(dir, "nx_main")
		deviceDir := filepath.Join(dir, "nx_device")
		if info, err := os.Stat(mainDir); err == nil && info.IsDir() {
			return dir
		}
		if info, err := os.Stat(deviceDir); err == nil && info.IsDir() {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return ""
}
