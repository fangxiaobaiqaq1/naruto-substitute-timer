//go:build windows && amd64

// Package support builds a bounded, privacy-minimized diagnostic ZIP for
// user-initiated support requests.
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
const bundleSchema = "naruto-timer-support-v2"

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

// Manifest deliberately identifies the portable bundle format, not the local
// directory from which it was created.
type Manifest struct {
	SchemaVersion int       `json:"schema_version"`
	BundleSchema  string    `json:"bundle_schema"`
	GeneratedAt   time.Time `json:"generated_at"`
	Version       string    `json:"version"`
	Files         []string  `json:"files"`
	Images        bool      `json:"includes_images"`
}

type applicationExport struct {
	SchemaVersion   int       `json:"schema_version"`
	Version         string    `json:"version"`
	GeneratedAt     time.Time `json:"generated_at"`
	FeedbackQQGroup string    `json:"feedback_qq_group"`
}

type environmentExport struct {
	Platform string `json:"platform"`
}

type runtimeConfigExport struct {
	SchemaVersion    int      `json:"schema_version"`
	CaptureProvider  string   `json:"capture_provider"`
	CaptureSelection string   `json:"capture_selection"`
	PreferredMethods []string `json:"preferred_methods"`
	CaptureTimeoutMS int      `json:"capture_timeout_ms"`
	TrackingPollMS   int      `json:"tracking_poll_ms"`
	TrackingWindow   int      `json:"tracking_window_frames"`
	MinimumConfirm   int      `json:"minimum_confirm_frames"`
	EnterFightFrames int      `json:"enter_fight_frames"`
	LeaveFightFrames int      `json:"leave_fight_frames"`
	UnknownHoldMS    int      `json:"unknown_hold_ms"`
	StaleAfterMS     int      `json:"stale_after_ms"`
	ResetAfterMS     int      `json:"reset_after_ms"`
	ReferenceWidth   int      `json:"reference_width"`
	ReferenceHeight  int      `json:"reference_height"`
}

type savedConfigExport struct {
	SchemaVersion int  `json:"schema_version"`
	Available     bool `json:"available"`
}

type captureStateExport struct {
	RequestedRevision uint64    `json:"requested_revision"`
	AppliedRevision   uint64    `json:"applied_revision"`
	Provider          string    `json:"provider"`
	Method            string    `json:"method"`
	ErrorCategory     string    `json:"error_category,omitempty"`
	UpdatedAt         time.Time `json:"updated_at,omitempty"`
}

type inventoryExport struct {
	GeneratedAt       time.Time `json:"generated_at"`
	InstallationCount int       `json:"installation_count"`
	InstanceCount     int       `json:"instance_count"`
	RunningCount      int       `json:"running_count"`
	ProcessCount      int       `json:"process_count"`
	ErrorCategory     string    `json:"error_category,omitempty"`
}

type sessionExport struct {
	SchemaVersion     int       `json:"schema_version"`
	StartedAt         time.Time `json:"started_at,omitempty"`
	RecordFrames      bool      `json:"record_frames"`
	Replay            bool      `json:"replay"`
	HUDEvidence       bool      `json:"hud_evidence"`
	MaxRecordingBytes int64     `json:"max_recording_bytes"`
	MaxQueuedBytes    int64     `json:"max_queued_bytes"`
	MaxObservations   int       `json:"max_observations"`
}

type sessionReportExport struct {
	SchemaVersion  int       `json:"schema_version"`
	GeneratedAt    time.Time `json:"generated_at,omitempty"`
	StartedAt      time.Time `json:"started_at,omitempty"`
	EndedAt        time.Time `json:"ended_at,omitempty"`
	Attempts       uint64    `json:"attempts"`
	ValidFrames    uint64    `json:"valid_frames"`
	RecordedFrames uint64    `json:"recorded_frames"`
	DroppedFrames  uint64    `json:"dropped_frames"`
	DroppedLogs    uint64    `json:"dropped_logs"`
	Events         uint64    `json:"confirmed_events"`
}

type replayManifestExport struct {
	SchemaVersion     int       `json:"schema_version"`
	RequestedDuration string    `json:"requested_duration,omitempty"`
	Cutoff            time.Time `json:"cutoff,omitempty"`
	ActualStart       time.Time `json:"actual_start,omitempty"`
	ActualEnd         time.Time `json:"actual_end,omitempty"`
	MaxWidth          int       `json:"max_width"`
	Format            string    `json:"format,omitempty"`
	Quality           int       `json:"quality"`
	ByteCap           int64     `json:"byte_cap"`
	Saved             uint64    `json:"saved"`
	Dropped           uint64    `json:"dropped"`
	Evicted           uint64    `json:"evicted"`
	FrameCount        int       `json:"frame_count"`
}

type probeStepExport struct {
	Stage         string    `json:"stage"`
	StartedAt     time.Time `json:"started_at,omitempty"`
	DurationMS    float64   `json:"duration_ms"`
	OK            bool      `json:"ok"`
	ErrorCategory string    `json:"error_category,omitempty"`
}

type probeExport struct {
	StartedAt         time.Time         `json:"started_at,omitempty"`
	EndedAt           time.Time         `json:"ended_at,omitempty"`
	SDKCandidateCount int               `json:"sdk_candidate_count"`
	SelectedSDKFound  bool              `json:"selected_sdk_found"`
	Width             int               `json:"width"`
	Height            int               `json:"height"`
	ImageAvailable    bool              `json:"image_available"`
	FailureStage      string            `json:"failure_stage,omitempty"`
	ErrorCategory     string            `json:"error_category,omitempty"`
	Steps             []probeStepExport `json:"steps"`
}

type captureLogExport struct {
	Type                 string    `json:"type"`
	FrameID              uint64    `json:"frame_id"`
	SourceSequence       uint64    `json:"source_sequence"`
	SourceRevision       uint64    `json:"source_revision"`
	CaptureStarted       time.Time `json:"capture_started,omitempty"`
	CapturedAt           time.Time `json:"captured_at,omitempty"`
	ReceivedAt           time.Time `json:"received_at,omitempty"`
	HelperRequestStarted time.Time `json:"helper_request_started,omitempty"`
	HelperDeadline       time.Time `json:"helper_deadline,omitempty"`
	CaptureMethod        string    `json:"capture_method"`
	Width                int       `json:"width"`
	Height               int       `json:"height"`
	ValidForLatency      bool      `json:"valid_for_latency"`
	RawState             string    `json:"raw_state"`
	Duplicate            bool      `json:"duplicate"`
	Hold                 bool      `json:"hold"`
	ErrorCategory        string    `json:"error_category,omitempty"`
	HelperOutcome        string    `json:"helper_outcome,omitempty"`
	HelperReap           string    `json:"helper_reap,omitempty"`
	HelperOriginOutcome  string    `json:"helper_origin_outcome,omitempty"`
	HelperTerminal       string    `json:"helper_terminal,omitempty"`
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

func exportVersion(value string) string {
	value = strings.TrimSpace(value)
	if value == "development" {
		return value
	}
	if len(value) < 2 || len(value) > 64 || value[0] != 'v' {
		return "unknown"
	}
	for i, char := range value[1:] {
		if (char < '0' || char > '9') && char != '.' && char != '-' && char != '+' {
			return "unknown"
		}
		if i == 0 && (char == '.' || char == '-' || char == '+') {
			return "unknown"
		}
	}
	return value
}

func captureProvider(value string) string {
	switch value {
	case "":
		return "mumu-sdk" // Legacy empty provider selects MuMu SDK.
	case "auto", "mumu-sdk", "leidian-adb", "printwindow-fullcontent":
		return value
	default:
		return "other"
	}
}

func captureSelection(value config.CaptureConfig) string {
	selection := value.MuMu.Selection
	if value.Provider == "leidian-adb" {
		selection = value.Leidian.Selection
	}
	switch selection {
	case "auto", "manual":
		return selection
	default:
		return "other"
	}
}

func captureMethod(value string) string {
	switch value {
	case "mumu-sdk", "leidian-adb", "printwindow-fullcontent":
		return value
	default:
		return "other"
	}
}

func captureErrorCategory(value string) string {
	switch value {
	case "":
		return ""
	case "timeout", "busy", "helper_launch", "helper_protocol", "sdk_worker", "success", "source_switch", "no_image", "capture_error", "capture_or_analysis_error":
		return value
	default:
		return "capture_failed"
	}
}

func helperOutcome(value string) string {
	switch value {
	case "timeout", "busy", "helper_launch", "helper_protocol", "sdk_worker", "success":
		return value
	default:
		return ""
	}
}

func helperReap(value string) string {
	switch value {
	case "not_needed", "completed", "killed_reaped", "pending":
		return value
	default:
		return ""
	}
}

func captureLogType(value string) string {
	if value == "capture" {
		return value
	}
	return "other"
}

func captureRawState(value string) string {
	if value == "disabled" {
		return value
	}
	return "other"
}

func runtimeConfigSummary(value config.Config) runtimeConfigExport {
	methods := make([]string, 0, len(value.Capture.PreferredMethods))
	for _, method := range value.Capture.PreferredMethods {
		methods = append(methods, captureMethod(method))
	}
	return runtimeConfigExport{
		SchemaVersion: value.SchemaVersion, CaptureProvider: captureProvider(value.Capture.Provider),
		CaptureSelection: captureSelection(value.Capture), PreferredMethods: methods,
		CaptureTimeoutMS: value.Capture.TimeoutMS, TrackingPollMS: value.Tracking.PollIntervalMS,
		TrackingWindow: value.Tracking.WindowFrames, MinimumConfirm: value.Tracking.MinimumConfirmFrames,
		EnterFightFrames: value.Tracking.EnterFightFrames, LeaveFightFrames: value.Tracking.LeaveFightFrames,
		UnknownHoldMS: value.Tracking.UnknownHoldMS, StaleAfterMS: value.Tracking.StaleAfterMS,
		ResetAfterMS: value.Tracking.ResetAfterMS, ReferenceWidth: value.Layout.ReferenceWidth,
		ReferenceHeight: value.Layout.ReferenceHeight,
	}
}

func captureStateSummary(value frame.CaptureState) captureStateExport {
	provider := value.AppliedCapture.Provider
	if provider == "" {
		provider = value.RequestedCapture.Provider
	}
	return captureStateExport{RequestedRevision: value.RequestedRevision, AppliedRevision: value.AppliedRevision,
		Provider: captureProvider(provider), Method: captureMethod(value.Method),
		ErrorCategory: captureErrorCategory(value.LastError), UpdatedAt: value.UpdatedAt}
}

func inventorySummary(value mumu.Inventory) inventoryExport {
	out := inventoryExport{GeneratedAt: value.GeneratedAt, InstallationCount: len(value.Installations), ProcessCount: len(value.Processes)}
	for _, installation := range value.Installations {
		out.InstanceCount += len(installation.Instances)
		if installation.Error != "" {
			out.ErrorCategory = "inventory_failed"
		}
		for _, instance := range installation.Instances {
			if instance.Running {
				out.RunningCount++
			}
		}
	}
	if len(value.Errors) != 0 {
		out.ErrorCategory = "inventory_failed"
	}
	return out
}

func probeStage(value string) string {
	switch value {
	case "":
		return ""
	case mumu.ProbeStageResolveRoot, mumu.ProbeStageFindSDK, mumu.ProbeStageLoadSDK, mumu.ProbeStageExports,
		mumu.ProbeStageConnect, mumu.ProbeStageDisplay, mumu.ProbeStageDimensions, mumu.ProbeStagePixels, mumu.ProbeStageCapture:
		return value
	default:
		return "other"
	}
}

func probeErrorCategory(value string) string {
	if strings.TrimSpace(value) == "" {
		return ""
	}
	return "probe_failed"
}

func probeSummary(value mumu.ProbeResult) probeExport {
	steps := make([]probeStepExport, 0, len(value.Steps))
	for _, step := range value.Steps {
		steps = append(steps, probeStepExport{
			Stage:         probeStage(step.Stage),
			StartedAt:     step.StartedAt,
			DurationMS:    step.DurationMS,
			OK:            step.OK,
			ErrorCategory: probeErrorCategory(step.Error),
		})
	}
	return probeExport{
		StartedAt:         value.StartedAt,
		EndedAt:           value.EndedAt,
		SDKCandidateCount: len(value.SDKCandidates),
		SelectedSDKFound:  value.SelectedSDK.Path != "",
		Width:             value.Width,
		Height:            value.Height,
		ImageAvailable:    value.ImageAvailable,
		FailureStage:      probeStage(value.FailureStage),
		ErrorCategory:     probeErrorCategory(value.Error),
		Steps:             steps,
	}
}

func summary(app applicationExport, runtime runtimeConfigExport, saved savedConfigExport, capture captureStateExport, inventory inventoryExport, probe *probeExport, includeImages bool) string {
	var b strings.Builder
	fmt.Fprintln(&b, "# 替身计时器支持诊断包")
	fmt.Fprintln(&b)
	fmt.Fprintf(&b, "版本：%s\n\n", app.Version)
	fmt.Fprintf(&b, "反馈 QQ 群：%s\n\n", app.FeedbackQQGroup)
	fmt.Fprintf(&b, "采集提供方：%s；选择方式：%s；超时：%d ms。\n\n", runtime.CaptureProvider, runtime.CaptureSelection, runtime.CaptureTimeoutMS)
	fmt.Fprintf(&b, "已保存配置：%t；配置架构版本：%d。\n\n", saved.Available, saved.SchemaVersion)
	fmt.Fprintf(&b, "采集器修订：请求 %d；已应用 %d；方法：%s。\n\n", capture.RequestedRevision, capture.AppliedRevision, capture.Method)
	if capture.ErrorCategory != "" {
		fmt.Fprintf(&b, "采集器状态：%s。\n\n", capture.ErrorCategory)
	}
	fmt.Fprintf(&b, "MuMu 清单：安装 %d；实例 %d；运行中 %d；进程 %d。\n\n", inventory.InstallationCount, inventory.InstanceCount, inventory.RunningCount, inventory.ProcessCount)
	if inventory.ErrorCategory != "" {
		fmt.Fprintf(&b, "MuMu 清单状态：%s。\n\n", inventory.ErrorCategory)
	}
	if probe != nil {
		if probe.ErrorCategory == "" {
			fmt.Fprintf(&b, "SDK 自检：成功；图像尺寸 %d × %d。\n\n", probe.Width, probe.Height)
		} else {
			fmt.Fprintf(&b, "SDK 自检：%s；失败阶段：%s。\n\n", probe.ErrorCategory, probe.FailureStage)
		}
	}
	if includeImages {
		fmt.Fprintln(&b, "已按用户明确选择包含原帧 PNG、HUD 证据和诊断回放 JPEG；请在发送前确认图像隐私。")
	} else {
		fmt.Fprintln(&b, "压缩包默认不包含原帧 PNG、HUD 证据或诊断回放 JPEG；JSON 仅包含经允许的稳定诊断摘要。")
	}
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

func imageZipName(source string, counters map[string]int) string {
	rel := filepath.ToSlash(source)
	category := "other"
	switch {
	case strings.HasPrefix(rel, "frames/"):
		category = "frame"
	case strings.HasPrefix(rel, "hud/"):
		category = "hud"
	case strings.HasPrefix(rel, "replay/annotated/"):
		category = "replay-annotated"
	case strings.HasPrefix(rel, "replay/"):
		category = "replay"
	}
	ext := strings.ToLower(filepath.Ext(rel))
	if ext != ".png" && ext != ".jpg" && ext != ".jpeg" {
		ext = ".png"
	}
	counters[category]++
	return fmt.Sprintf("images/%s-%06d%s", category, counters[category], ext)
}

func allowedSessionFiles(dir string, includeImages bool) []string {
	allowed := []string{"session.json", "capture.jsonl", "frames.jsonl", "report.json", "report.md", "replay/manifest.json"}
	var out []string
	for _, name := range allowed {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			out = append(out, filepath.ToSlash(name))
		}
	}
	if includeImages {
		for _, subdir := range []string{"frames", "hud", "replay", "replay/annotated"} {
			entries, _ := os.ReadDir(filepath.Join(dir, subdir))
			for _, entry := range entries {
				if entry.IsDir() || entry.Name()[0] == '.' || strings.HasSuffix(entry.Name(), ".partial") {
					continue
				}
				ext := strings.ToLower(filepath.Ext(entry.Name()))
				if ext != ".png" && ext != ".jpg" && ext != ".jpeg" {
					continue
				}
				out = append(out, filepath.ToSlash(filepath.Join(subdir, entry.Name())))
			}
		}
	}
	sort.Strings(out)
	return out
}

func readSessionObject(path string) map[string]any {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var value map[string]any
	if json.Unmarshal(data, &value) != nil {
		return nil
	}
	return value
}

func objectString(value map[string]any, key string) string {
	text, _ := value[key].(string)
	return text
}
func objectBool(value map[string]any, key string) bool { result, _ := value[key].(bool); return result }
func objectInt(value map[string]any, key string) int {
	result, _ := value[key].(float64)
	return int(result)
}
func objectInt64(value map[string]any, key string) int64 {
	result, _ := value[key].(float64)
	return int64(result)
}
func objectUint64(value map[string]any, key string) uint64 {
	result, _ := value[key].(float64)
	return uint64(result)
}
func objectTime(value map[string]any, key string) time.Time {
	parsed, _ := time.Parse(time.RFC3339Nano, objectString(value, key))
	return parsed
}

func sanitizeSession(value map[string]any) sessionExport {
	return sessionExport{SchemaVersion: objectInt(value, "schema_version"), StartedAt: objectTime(value, "started_at"),
		RecordFrames: objectBool(value, "record_frames"), Replay: objectBool(value, "replay"), HUDEvidence: objectBool(value, "hud_evidence"),
		MaxRecordingBytes: objectInt64(value, "max_recording_bytes"), MaxQueuedBytes: objectInt64(value, "max_queued_bytes"), MaxObservations: objectInt(value, "max_observations")}
}

func sanitizeSessionReport(value map[string]any) sessionReportExport {
	status, _ := value["status"].(map[string]any)
	return sessionReportExport{SchemaVersion: objectInt(value, "schema_version"), GeneratedAt: objectTime(value, "generated_at"),
		StartedAt: objectTime(value, "started_at"), EndedAt: objectTime(value, "ended_at"), Attempts: objectUint64(status, "attempts"),
		ValidFrames: objectUint64(status, "valid_frames"), RecordedFrames: objectUint64(status, "recorded_frames"),
		DroppedFrames: objectUint64(status, "dropped_frames"), DroppedLogs: objectUint64(status, "dropped_logs"), Events: objectUint64(value, "confirmed_events")}
}

func replayDuration(value string) string {
	parsed, err := time.ParseDuration(value)
	if err != nil || parsed < 0 || parsed > 24*time.Hour {
		return ""
	}
	return parsed.String()
}

func replayFormat(value string) string {
	if strings.EqualFold(value, "jpeg") {
		return "JPEG"
	}
	return "other"
}

func sanitizeReplayManifest(value map[string]any) replayManifestExport {
	frames, _ := value["frames"].([]any)
	return replayManifestExport{SchemaVersion: objectInt(value, "schema_version"), RequestedDuration: replayDuration(objectString(value, "requested_duration")),
		Cutoff: objectTime(value, "cutoff"), ActualStart: objectTime(value, "actual_start"), ActualEnd: objectTime(value, "actual_end"),
		MaxWidth: objectInt(value, "max_width"), Format: replayFormat(objectString(value, "format")), Quality: objectInt(value, "quality"),
		ByteCap: objectInt64(value, "byte_cap"), Saved: objectUint64(value, "saved"), Dropped: objectUint64(value, "dropped"),
		Evicted: objectUint64(value, "evicted"), FrameCount: len(frames)}
}

func sanitizeCaptureLog(data []byte) []byte {
	var out strings.Builder
	for _, line := range strings.Split(string(data), "\n") {
		var entry map[string]any
		if json.Unmarshal([]byte(line), &entry) != nil {
			continue
		}
		export := captureLogExport{
			Type:                 captureLogType(objectString(entry, "type")),
			FrameID:              objectUint64(entry, "frame_id"),
			SourceSequence:       objectUint64(entry, "source_sequence"),
			SourceRevision:       objectUint64(entry, "source_revision"),
			CaptureStarted:       objectTime(entry, "capture_started"),
			CapturedAt:           objectTime(entry, "captured_at"),
			ReceivedAt:           objectTime(entry, "received_at"),
			HelperRequestStarted: objectTime(entry, "helper_request_started"),
			HelperDeadline:       objectTime(entry, "helper_deadline"),
			CaptureMethod:        captureMethod(objectString(entry, "capture_method")),
			Width:                objectInt(entry, "width"),
			Height:               objectInt(entry, "height"),
			ValidForLatency:      objectBool(entry, "valid_for_latency"),
			RawState:             captureRawState(objectString(entry, "raw_state")),
			Duplicate:            objectBool(entry, "duplicate"),
			Hold:                 objectBool(entry, "hold"),
			ErrorCategory:        captureErrorCategory(objectString(entry, "error_category")),
			HelperOutcome:        helperOutcome(objectString(entry, "helper_outcome")),
			HelperReap:           helperReap(objectString(entry, "helper_reap")),
			HelperOriginOutcome:  helperOutcome(objectString(entry, "helper_origin_outcome")),
			HelperTerminal:       helperReap(objectString(entry, "helper_terminal")),
		}
		if export.ErrorCategory == "" {
			export.ErrorCategory = captureErrorCategory(objectString(entry, "error"))
		}
		encoded, err := json.Marshal(export)
		if err != nil {
			continue
		}
		out.Write(encoded)
		out.WriteByte('\n')
	}
	return []byte(out.String())
}

func sessionReportMarkdown(value sessionReportExport) string {
	return fmt.Sprintf("# 诊断会话摘要\n\n采集尝试：%d；有效帧：%d；已保存帧：%d；丢弃帧：%d；丢弃日志：%d；确认事件：%d。\n", value.Attempts, value.ValidFrames, value.RecordedFrames, value.DroppedFrames, value.DroppedLogs, value.Events)
}

func writeSessionFile(ctx context.Context, zw *zip.Writer, source, name string, files *[]string) error {
	data, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	var safe []byte
	switch name {
	case "session.json":
		safe, err = jsonBytes(sanitizeSession(readSessionObject(source)))
	case "capture.jsonl", "frames.jsonl":
		safe = sanitizeCaptureLog(data)
	case "report.json":
		safe, err = jsonBytes(sanitizeSessionReport(readSessionObject(source)))
	case "report.md":
		value := sanitizeSessionReport(readSessionObject(filepath.Join(filepath.Dir(source), "report.json")))
		safe = []byte(sessionReportMarkdown(value))
	case "replay/manifest.json":
		safe, err = jsonBytes(sanitizeReplayManifest(readSessionObject(source)))
	default:
		return fmt.Errorf("unsupported session export member %q", name)
	}
	if err != nil {
		return err
	}
	return writeZipBytes(ctx, zw, filepath.Join("session", name), safe, files)
}

// ExportBundle writes a temporary ZIP beside its final path then renames it
// after Close succeeds. Configurations and session metadata are transformed to
// fixed allowlisted schemas; only explicitly opted-in image bytes are copied.
func ExportBundle(ctx context.Context, options BundleOptions) (string, error) {
	destination := bundleRoot(options.Root)
	if err := os.MkdirAll(destination, 0o700); err != nil {
		return "", err
	}
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

	version := exportVersion(options.Version)
	app := applicationExport{SchemaVersion: 1, Version: version, GeneratedAt: time.Now(), FeedbackQQGroup: BugReportQQGroup}
	runtimeConfig := runtimeConfigSummary(options.Config)
	configInfo, configStatErr := os.Stat(options.ConfigPath)
	savedConfig := savedConfigExport{SchemaVersion: options.Config.SchemaVersion, Available: options.ConfigPath != "" && configStatErr == nil && !configInfo.IsDir()}
	capture := captureStateSummary(options.CaptureState)
	inventory := inventorySummary(options.Inventory)
	var probe *probeExport
	if options.Probe != nil {
		value := probeSummary(*options.Probe)
		probe = &value
	}
	if err := write("application.json", app); err != nil {
		return closeWithError(err)
	}
	if err := write("environment.json", environmentExport{Platform: runtime.GOOS + "/" + runtime.GOARCH}); err != nil {
		return closeWithError(err)
	}
	if err := write("config/runtime.json", runtimeConfig); err != nil {
		return closeWithError(err)
	}
	if err := write("config/saved.json", savedConfig); err != nil {
		return closeWithError(err)
	}
	if err := write("capture/state.json", capture); err != nil {
		return closeWithError(err)
	}
	if err := write("mumu/inventory.json", inventory); err != nil {
		return closeWithError(err)
	}
	if probe != nil {
		if err := write("mumu/sdk-probe.json", probe); err != nil {
			return closeWithError(err)
		}
	}
	if err := writeZipBytes(ctx, zw, "summary.md", []byte(summary(app, runtimeConfig, savedConfig, capture, inventory, probe, options.IncludeImages)), &files); err != nil {
		return closeWithError(err)
	}
	if options.SessionDir != "" {
		imageCounters := make(map[string]int)
		for _, file := range allowedSessionFiles(options.SessionDir, options.IncludeImages) {
			source := filepath.Join(options.SessionDir, file)
			if strings.HasSuffix(strings.ToLower(filepath.Ext(file)), ".png") || strings.HasSuffix(strings.ToLower(filepath.Ext(file)), ".jpg") || strings.HasSuffix(strings.ToLower(filepath.Ext(file)), ".jpeg") {
				if err := copyZipFile(ctx, zw, source, imageZipName(file, imageCounters), &files); err != nil {
					return closeWithError(err)
				}
				continue
			}
			if err := writeSessionFile(ctx, zw, source, file, &files); err != nil {
				return closeWithError(err)
			}
		}
	}
	manifestFiles := append(append([]string(nil), files...), "manifest.json")
	manifest := Manifest{SchemaVersion: 2, BundleSchema: bundleSchema, GeneratedAt: time.Now(), Version: version, Files: manifestFiles, Images: options.IncludeImages}
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
