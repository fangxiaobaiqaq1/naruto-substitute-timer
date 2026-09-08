//go:build windows && amd64

package mumu

import (
	"fmt"
	"golang.org/x/sys/windows"
	"path/filepath"
	"strings"
	"unsafe"
)

// DiscoverInstallation only considers known running MuMu executables and
// validates their installation layout before selecting an SDK.
func DiscoverInstallation() (string, error) {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return "", fmt.Errorf("enumerate MuMu processes: %w", err)
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
		return "", fmt.Errorf("enumerate MuMu processes: %w", err)
	}
	if len(roots) > 1 {
		return "", fmt.Errorf("检测到多个 MuMu 安装目录，请在 config.json 的 capture.mumu.installDir 中指定")
	}
	for _, root := range roots {
		return root, nil
	}
	return "", fmt.Errorf("未找到运行中的 MuMu 截图 SDK，请启动 MuMu，或在 config.json 的 capture.mumu.installDir 中填写安装目录")
}
func installationFromExecutable(executable string) string {
	var installation string
	for dir, n := filepath.Dir(executable), 0; n < 6; n++ {
		if _, err := FindDLL(dir); err == nil {
			installation = dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return installation
}
