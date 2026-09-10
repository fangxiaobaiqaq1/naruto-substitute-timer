//go:build windows && amd64

package mumu

import (
	"context"
	"encoding/json"
	"golang.org/x/sys/windows"
	"narutotimer/internal/win"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type ProcessChoice struct {
	Instance
	WindowTitle string
	ProcessName string
}

func ListProcessChoices(ctx context.Context, root string) ([]ProcessChoice, error) {
	instances, err := ListInstances(ctx, root)
	if err != nil {
		return nil, err
	}
	windowsList := win.FindMuMu()
	indexes := processInstanceIndexes(ctx)
	var out []ProcessChoice
	for _, item := range instances {
		choice := ProcessChoice{Instance: item}
		var candidates []win.Window
		for _, w := range windowsList {
			if !w.Visible || w.PID == 0 {
				continue
			}
			r := processInstallation(w.PID)
			if !strings.EqualFold(filepath.Clean(r), filepath.Clean(item.Root)) {
				continue
			}
			if index, known := indexes[w.PID]; known {
				if index == item.Index {
					candidates = append(candidates, w)
				}
				continue
			}
			if strings.EqualFold(strings.TrimSpace(w.Title), strings.TrimSpace(item.Name)) {
				candidates = append(candidates, w)
			}
		}
		if len(candidates) == 1 {
			choice.PID = int(candidates[0].PID)
			choice.WindowTitle = candidates[0].Title
			choice.ProcessName = candidates[0].ProcessName
		}
		// Some emulator builds append the game title. A sole running instance/window
		// within this installation has an unambiguous mapping even then.
		if choice.PID == 0 && item.Running {
			same := 0
			for _, v := range instances {
				if v.Running && strings.EqualFold(v.Root, item.Root) {
					same++
				}
			}
			if same == 1 {
				var unique []win.Window
				for _, w := range windowsList {
					if w.Visible && strings.EqualFold(processInstallation(w.PID), item.Root) && !strings.Contains(strings.ToLower(w.ProcessName), "main") {
						unique = append(unique, w)
					}
				}
				if len(unique) == 1 {
					choice.PID = int(unique[0].PID)
					choice.WindowTitle = unique[0].Title
					choice.ProcessName = unique[0].ProcessName
				}
			}
		}
		out = append(out, choice)
	}
	return out, nil
}
func processInstallation(pid uint32) string {
	h, e := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if e != nil {
		return ""
	}
	defer windows.CloseHandle(h)
	b := make([]uint16, 32768)
	n := uint32(len(b))
	if windows.QueryFullProcessImageName(h, 0, &b[0], &n) != nil {
		return ""
	}
	return installationFromExecutable(windows.UTF16ToString(b[:n]))
}

var instanceArgument = regexp.MustCompile(`(?:^|\s)(?:-v|--vmindex|--index)(?:=|\s+)([0-9]+)(?:\s|$)`)

func processInstanceIndexes(ctx context.Context) map[uint32]int {
	out := map[uint32]int{}
	sub, cancel := context.WithTimeout(ctx, 2500*time.Millisecond)
	defer cancel()
	// Read OS process metadata only for emulator UI processes, never game memory.
	script := `@(Get-CimInstance Win32_Process -Filter "Name='MuMuNxDevice.exe' OR Name='MuMuPlayer.exe' OR Name='NemuPlayer.exe'" | Select-Object ProcessId,CommandLine) | ConvertTo-Json -Compress`
	cmd := exec.CommandContext(sub, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
	var data limitedBuffer
	cmd.Stdout = &data
	cmd.Stderr = &limitedBuffer{}
	if cmd.Run() != nil {
		return out
	}
	var rows []struct {
		ProcessID   uint32 `json:"ProcessId"`
		CommandLine string `json:"CommandLine"`
	}
	if json.Unmarshal(data.Bytes(), &rows) != nil {
		return out
	}
	for _, row := range rows {
		m := instanceArgument.FindStringSubmatch(row.CommandLine)
		if len(m) == 2 {
			if n, e := strconv.Atoi(m[1]); e == nil {
				out[row.ProcessID] = n
			}
		}
	}
	return out
}
