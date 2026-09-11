//go:build windows && amd64

// Package support builds a bounded diagnostic ZIP for user-initiated support
// requests. It only includes an allowlisted set of application files.
package support

import (
	"archive/zip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"narutotimer/internal/capture/mumu"
	"narutotimer/internal/config"
	"narutotimer/internal/frame"
)

const BugReportQQGroup = "1109044204"

type BundleOptions struct {
	Root          string
	SessionDir    string
	Config        config.Config
	ConfigPath    string
	CaptureState  frame.CaptureState
	Version       string
	Executable    string
	Inventory     mumu.Inventory
	Probe         *mumu.ProbeResult
	IncludeImages bool
}

type Manifest struct {
	SchemaVersion int       `json:"schema_version"`
	GeneratedAt   time.Time `json:"generated_at"`
	Version       string    `json:"version"`
	Files         []string  `json:"files"`
	SessionDir    string    `json:"session_dir,omitempty"`
	Images        bool      `json:"includes_images"`
}

type environment struct {
	GOOS       string `json:"goos"`
	GOARCH     string `json:"goarch"`
	Executable string `json:"executable,omitempty"`
	ConfigPath string `json:"config_path,omitempty"`
}

func bundleRoot(root string) string {
	if strings.TrimSpace(root) == "" {
		root = "debug"
	}
	return filepath.Join(root, "support-bundles")
}

func jsonBytes(v any) ([]byte, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

func configSummary(target config.MuMuCaptureConfig) string {
	if target.Selection == "auto" {
		return "自动检测"
	}
	if target.InstallDir == "" {
		return fmt.Sprintf("手动实例 %d（未填写安装目录）", target.Instance)
	}
	return fmt.Sprintf("%s · 实例 %d", target.InstallDir, target.Instance)
}

func summary(options BundleOptions) string {
	var b strings.Builder
	fmt.Fprintln(&b, "# 替身计时器支持诊断包")
	fmt.Fprintln(&b)
	fmt.Fprintf(&b, "版本：%s\n\n", options.Version)
	fmt.Fprintf(&b, "反馈 QQ 群：%s\n\n", BugReportQQGroup)
	fmt.Fprintf(&b, "已保存采集目标：%s\n\n", configSummary(options.Config.Capture.MuMu))
	if options.CaptureState.RequestedRevision != 0 || options.CaptureState.AppliedRevision != 0 || options.CaptureState.Source != "" || options.CaptureState.LastError != "" {
		fmt.Fprintf(&b, "采集器已请求目标：%s（修订 %d）\n\n", configSummary(options.CaptureState.Requested), options.CaptureState.RequestedRevision)
		fmt.Fprintf(&b, "采集器已应用目标：%s（修订 %d）\n\n", configSummary(options.CaptureState.Applied), options.CaptureState.AppliedRevision)
		if options.CaptureState.Source != "" {
			fmt.Fprintf(&b, "当前来源：%s\n\n", options.CaptureState.Source)
		}
		if options.CaptureState.LastError != "" {
			fmt.Fprintf(&b, "最近采集错误：%s\n\n", options.CaptureState.LastError)
		}
	}
	fmt.Fprintf(&b, "MuMu 清单：%s\n\n", options.Inventory.Summary())
	if options.Probe != nil {
		if options.Probe.Error == "" {
			fmt.Fprintf(&b, "SDK 自检：成功取得 %d × %d 图像。\n\n", options.Probe.Width, options.Probe.Height)
		} else {
			fmt.Fprintf(&b, "SDK 自检失败阶段：%s\n\n错误：%s\n\n", options.Probe.FailureStage, options.Probe.Error)
		}
	}
	fmt.Fprintln(&b, "压缩包默认不包含原帧 PNG；其中的 JSON 文件记录实际配置、MuMu 安装目录、实例编号、PID、SDK DLL 和分阶段自检结果。")
	return b.String()
}

func checkContext(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}

func writeZipBytes(ctx context.Context, zw *zip.Writer, name string, data []byte, files *[]string) error {
	if err := checkContext(ctx); err != nil {
		return err
	}
	writer, err := zw.Create(filepath.ToSlash(name))
	if err != nil {
		return err
	}
	if _, err = writer.Write(data); err != nil {
		return err
	}
	*files = append(*files, filepath.ToSlash(name))
	return nil
}

func copyZipFile(ctx context.Context, zw *zip.Writer, source, name string, files *[]string) error {
	if err := checkContext(ctx); err != nil {
		return err
	}
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	writer, err := zw.Create(filepath.ToSlash(name))
	if err != nil {
		return err
	}
	if _, err = io.Copy(writer, input); err != nil {
		return err
	}
	*files = append(*files, filepath.ToSlash(name))
	return nil
}

func allowedSessionFiles(dir string, includeImages bool) []string {
	allowed := []string{"session.json", "capture.jsonl", "frames.jsonl", "report.json", "report.md"}
	var out []string
	for _, name := range allowed {
		path := filepath.Join(dir, name)
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			out = append(out, name)
		}
	}
	if includeImages {
		entries, _ := os.ReadDir(dir)
		for _, entry := range entries {
			if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".png") {
				continue
			}
			out = append(out, entry.Name())
		}
	}
	sort.Strings(out)
	return out
}

// ExportBundle writes a temporary ZIP beside its final path then renames it
// after Close succeeds. The session directory is allowlisted; no arbitrary user
// folders or emulator installation files are recursively archived.
func ExportBundle(ctx context.Context, options BundleOptions) (string, error) {
	destination := bundleRoot(options.Root)
	if err := os.MkdirAll(destination, 0o700); err != nil {
		return "", err
	}
	// Include milliseconds and still check for an existing path: users may
	// export twice from the same diagnostic window within one second, and a
	// support bundle must never silently overwrite an earlier report.
	stamp := time.Now()
	base := "naruto-timer-diagnostic-" + stamp.Format("20060102-150405.000")
	finalPath := filepath.Join(destination, base+".zip")
	for suffix := 1; ; suffix++ {
		_, err := os.Stat(finalPath)
		if os.IsNotExist(err) {
			break
		}
		if err != nil {
			return "", fmt.Errorf("检查支持诊断包目标路径: %w", err)
		}
		finalPath = filepath.Join(destination, fmt.Sprintf("%s-%d.zip", base, suffix))
	}
	temp, err := os.CreateTemp(destination, ".diagnostic-*.zip")
	if err != nil {
		return "", err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	zw := zip.NewWriter(temp)
	var files []string
	write := func(name string, value any) error {
		data, err := jsonBytes(value)
		if err != nil {
			return err
		}
		return writeZipBytes(ctx, zw, name, data, &files)
	}
	closeWithError := func(cause error) (string, error) {
		_ = zw.Close()
		_ = temp.Close()
		return "", cause
	}

	if err := write("application.json", map[string]any{
		"version": options.Version, "generated_at": time.Now(), "feedback_qq_group": BugReportQQGroup,
	}); err != nil {
		return closeWithError(err)
	}
	if err := write("environment.json", environment{GOOS: runtime.GOOS, GOARCH: runtime.GOARCH, Executable: options.Executable, ConfigPath: options.ConfigPath}); err != nil {
		return closeWithError(err)
	}
	if err := write("config/runtime.json", options.Config); err != nil {
		return closeWithError(err)
	}
	if options.ConfigPath != "" {
		if info, err := os.Stat(options.ConfigPath); err == nil && !info.IsDir() {
			if err := copyZipFile(ctx, zw, options.ConfigPath, "config/saved.json", &files); err != nil {
				return closeWithError(err)
			}
		}
	}
	if err := write("capture/state.json", options.CaptureState); err != nil {
		return closeWithError(err)
	}
	if err := write("mumu/inventory.json", options.Inventory); err != nil {
		return closeWithError(err)
	}
	if options.Probe != nil {
		if err := write("mumu/sdk-probe.json", options.Probe); err != nil {
			return closeWithError(err)
		}
	}
	if err := writeZipBytes(ctx, zw, "summary.md", []byte(summary(options)), &files); err != nil {
		return closeWithError(err)
	}
	if options.SessionDir != "" {
		for _, file := range allowedSessionFiles(options.SessionDir, options.IncludeImages) {
			if err := copyZipFile(ctx, zw, filepath.Join(options.SessionDir, file), filepath.Join("session", file), &files); err != nil {
				return closeWithError(err)
			}
		}
	}
	manifestFiles := append(append([]string(nil), files...), "manifest.json")
	manifest := Manifest{SchemaVersion: 1, GeneratedAt: time.Now(), Version: options.Version, Files: manifestFiles, SessionDir: options.SessionDir, Images: options.IncludeImages}
	if err := write("manifest.json", manifest); err != nil {
		return closeWithError(err)
	}
	if err := zw.Close(); err != nil {
		_ = temp.Close()
		return "", err
	}
	if err := temp.Close(); err != nil {
		return "", err
	}
	if err := checkContext(ctx); err != nil {
		return "", err
	}
	if err := os.Rename(tempPath, finalPath); err != nil {
		return "", err
	}
	return finalPath, nil
}
