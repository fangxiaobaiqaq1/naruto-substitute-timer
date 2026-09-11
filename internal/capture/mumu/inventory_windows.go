//go:build windows && amd64

package mumu

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// ProcessEvidence is operating-system metadata for one MuMu process. Index is
// nil when the process command line does not expose a verified instance number;
// nil must never be rendered as instance 0.
type ProcessEvidence struct {
	PID         uint32 `json:"pid"`
	Name        string `json:"name"`
	Executable  string `json:"executable,omitempty"`
	CommandLine string `json:"command_line,omitempty"`
	Root        string `json:"root,omitempty"`
	Index       *int   `json:"instance_index,omitempty"`
	IndexSource string `json:"index_source,omitempty"`
	Error       string `json:"error,omitempty"`
}

// Installation is a distinct MuMu product tree. Root plus an instance index is
// the connection identity: two installations may each have an instance 0.
type Installation struct {
	Root          string     `json:"root"`
	Sources       []string   `json:"sources,omitempty"`
	ManagerPath   string     `json:"manager_path,omitempty"`
	SDKCandidates []SDKFile  `json:"sdk_candidates,omitempty"`
	Instances     []Instance `json:"instances,omitempty"`
	Error         string     `json:"error,omitempty"`
}

// Inventory keeps every discovered installation, including ones that fail SDK
// or manager inspection, so support can see the complete machine state.
type Inventory struct {
	GeneratedAt   time.Time         `json:"generated_at"`
	Installations []Installation    `json:"installations"`
	Processes     []ProcessEvidence `json:"processes,omitempty"`
	Errors        []string          `json:"errors,omitempty"`
}

func canonicalRoot(root string) string {
	root = strings.TrimSpace(root)
	if root == "" {
		return ""
	}
	if absolute, err := filepath.Abs(root); err == nil {
		root = absolute
	}
	return filepath.Clean(root)
}

func rootIdentity(root string) string { return strings.ToLower(canonicalRoot(root)) }

func findManager(root string) string {
	for _, relative := range []string{
		filepath.Join("nx_main", "MuMuManager.exe"),
		filepath.Join("shell", "MuMuManager.exe"),
		"MuMuManager.exe",
	} {
		candidate := filepath.Join(root, relative)
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
	}
	return ""
}

// DiscoverInventory combines the configured/manual roots with all active MuMu
// installations. Each root is independently inspected; a failure in one tree
// does not hide valid instances in another.
func DiscoverInventory(ctx context.Context, hints ...string) Inventory {
	result := Inventory{GeneratedAt: time.Now()}
	roots := map[string]*Installation{}
	add := func(root, source string) {
		root = canonicalRoot(root)
		if root == "" {
			return
		}
		key := rootIdentity(root)
		installation := roots[key]
		if installation == nil {
			installation = &Installation{Root: root}
			roots[key] = installation
		}
		for _, old := range installation.Sources {
			if old == source {
				return
			}
		}
		installation.Sources = append(installation.Sources, source)
	}
	for _, hint := range hints {
		add(hint, "已保存或手动指定")
	}
	discovered, err := DiscoverInstallations()
	if err != nil {
		result.Errors = append(result.Errors, err.Error())
	}
	for _, root := range discovered {
		add(root, "运行中的 MuMu 进程")
	}

	keys := make([]string, 0, len(roots))
	for key := range roots {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		installation := roots[key]
		installation.ManagerPath = findManager(installation.Root)
		candidates, err := FindDLLCandidates(installation.Root)
		if err != nil {
			installation.Error = err.Error()
			result.Installations = append(result.Installations, *installation)
			continue
		}
		installation.SDKCandidates = candidates
		instances, err := listInstancesAtRoot(ctx, installation.Root)
		if err != nil {
			installation.Error = err.Error()
		} else {
			installation.Instances = instances
		}
		result.Installations = append(result.Installations, *installation)
	}
	processes, err := ListProcesses(ctx)
	if err != nil {
		result.Errors = append(result.Errors, err.Error())
	} else {
		result.Processes = processes
	}
	return result
}

func (i Inventory) Summary() string {
	instances, running := 0, 0
	for _, installation := range i.Installations {
		instances += len(installation.Instances)
		for _, instance := range installation.Instances {
			if instance.Running {
				running++
			}
		}
	}
	return fmt.Sprintf("发现 %d 个 MuMu 安装、%d 个实例（运行中 %d 个）", len(i.Installations), instances, running)
}

// instanceArgument parses MuMu's documented/observed device process index
// switches. No match means unknown, not instance zero.
var instanceArgument = regexp.MustCompile(`(?i)(?:^|\s)(?:-v|--vmindex|--index)(?:=|\s+)([0-9]+)(?:\s|$)`)

// ListProcesses reads only Windows process metadata for known MuMu processes.
func ListProcesses(ctx context.Context) ([]ProcessEvidence, error) {
	sub, cancel := context.WithTimeout(ctx, 2500*time.Millisecond)
	defer cancel()
	script := `@(Get-CimInstance Win32_Process -Filter "Name='MuMuNxDevice.exe' OR Name='MuMuPlayer.exe' OR Name='NemuPlayer.exe' OR Name='MuMuNxMain.exe' OR Name='MuMuNxService.exe'" | Select-Object ProcessId,Name,ExecutablePath,CommandLine) | ConvertTo-Json -Compress`
	cmd := exec.CommandContext(sub, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
	var stdout, stderr limitedBuffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if stderr.Len() > 0 {
			return nil, fmt.Errorf("读取 MuMu 进程信息失败: %w；%s", err, strings.TrimSpace(stderr.String()))
		}
		return nil, fmt.Errorf("读取 MuMu 进程信息失败: %w", err)
	}
	raw := strings.TrimSpace(stdout.String())
	if raw == "" || raw == "null" {
		return nil, nil
	}
	type row struct {
		ProcessID      uint32 `json:"ProcessId"`
		Name           string `json:"Name"`
		ExecutablePath string `json:"ExecutablePath"`
		CommandLine    string `json:"CommandLine"`
	}
	var rows []row
	if err := json.Unmarshal([]byte(raw), &rows); err != nil {
		var one row
		if oneErr := json.Unmarshal([]byte(raw), &one); oneErr != nil {
			return nil, fmt.Errorf("解析 MuMu 进程信息: %w", err)
		}
		if one.ProcessID != 0 {
			rows = []row{one}
		}
	}
	result := make([]ProcessEvidence, 0, len(rows))
	for _, item := range rows {
		process := ProcessEvidence{
			PID:         item.ProcessID,
			Name:        item.Name,
			Executable:  item.ExecutablePath,
			CommandLine: item.CommandLine,
		}
		if process.Executable != "" {
			process.Root = installationFromExecutable(process.Executable)
		}
		if match := instanceArgument.FindStringSubmatch(process.CommandLine); len(match) == 2 {
			if index, err := strconv.Atoi(match[1]); err == nil {
				process.Index = &index
				process.IndexSource = "设备进程启动参数"
			}
		}
		result = append(result, process)
	}
	sort.Slice(result, func(a, b int) bool { return result[a].PID < result[b].PID })
	return result, nil
}
