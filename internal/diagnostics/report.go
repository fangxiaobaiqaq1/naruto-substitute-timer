package diagnostics

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Distribution uses null quantiles when no qualified samples exist. A real
// zero-duration sample remains distinguishable from unavailable measurements.
type Distribution struct {
	Count int      `json:"count"`
	P50MS *float64 `json:"p50_ms"`
	P95MS *float64 `json:"p95_ms"`
	P99MS *float64 `json:"p99_ms"`
	MinMS *float64 `json:"min_ms"`
	MaxMS *float64 `json:"max_ms"`
}

type Report struct {
	SchemaVersion        int                     `json:"schema_version"`
	GeneratedAt          time.Time               `json:"generated_at"`
	StartedAt            time.Time               `json:"started_at"`
	EndedAt              *time.Time              `json:"ended_at"`
	Status               Status                  `json:"status"`
	Events               uint64                  `json:"confirmed_events"`
	IncompleteFrames     uint64                  `json:"valid_frames_without_complete_draw_chain"`
	IncompleteEvents     uint64                  `json:"events_without_complete_visible_draw_chain"`
	RejectedObservations uint64                  `json:"observations_after_session_limit"`
	Exclusions           map[string]uint64       `json:"exclusions"`
	Metrics              map[string]Distribution `json:"metrics"`
	Notes                []string                `json:"notes"`
}

var metricNames = []string{
	"capture_ms", "analysis_wait_ms", "analysis_ms", "analysis_to_received_ms",
	"event_confirmation_ms", "received_to_ui_queue_ms", "ui_dispatch_ms", "ui_paint_wait_ms", "ui_paint_submit_ms",
	"frame_end_to_end_ms", "event_confirmed_to_ui_queue_ms", "event_confirmed_to_draw_ms", "event_end_to_end_ms",
}

func summarize(values []float64) Distribution {
	d := Distribution{Count: len(values)}
	if len(values) == 0 {
		return d
	}
	sort.Float64s(values)
	quantile := func(p float64) *float64 {
		index := int(math.Ceil(float64(len(values))*p)) - 1
		if index < 0 {
			index = 0
		}
		value := values[index]
		return &value
	}
	d.P50MS, d.P95MS, d.P99MS = quantile(.50), quantile(.95), quantile(.99)
	d.MinMS, d.MaxMS = quantile(0), quantile(1)
	return d
}

func (r *Recorder) report() Report {
	r.mu.Lock()
	report := Report{SchemaVersion: 1, GeneratedAt: time.Now(), StartedAt: r.started,
		Status: r.status, Events: r.events, IncompleteFrames: r.status.ValidFrames - r.status.CompleteFrames,
		IncompleteEvents: r.events - r.status.CompleteEvents, RejectedObservations: r.rejected,
		Exclusions: make(map[string]uint64), Metrics: make(map[string]Distribution),
		Notes: []string{
			"Durations use Go monotonic timestamps; JSON timestamps are wall-clock RFC3339.",
			"frame_end_to_end_ms = capture API request to actual paint/SwapBuffers completion for the same valid frame.",
			"event_end_to_end_ms = first evidence capture API request to a paint/SwapBuffers completion displaying that visible event serial.",
			"Source/game render time and physical display presentation are unavailable; neither is asserted by these measurements.",
			"All required timestamps must exist and be ordered. Missing/failed/held/duplicate frames, incomplete chains and hidden events never contribute end-to-end samples.",
			"A UI post, Refresh call, function cost or capture timeout is not a drawing endpoint. Without instrumented drawing, end-to-end quantiles stay null.",
			"Quantiles use nearest rank over qualified samples in this bounded session. Capture logs/recording drops are reported explicitly and are not latency samples.",
			"Limits: 4096-frame correlation history, at most 100000 observations per session, 4096 queued metadata jobs and a separate 256-job PNG/replay queue, configurable queued pixel bytes and PNG bytes, 64 MiB combined JSONL logs.",
			"Raw PNGs retain native pixel dimensions without overlays; capture.jsonl records recognition and raw save/drop status by frame_id. frames.jsonl is the replay manifest.",
			"Raw recording saves only current fighting scene frames without hold or errors. Non-fight and uncertain/error images are skipped before pixel copying; bounded metadata remains for diagnosis.",
			"When HUD evidence is enabled, 1/4 of the same PNG budget is reserved for native-pixel name/bean crops at most twice per second. HUD crops correlate by frame_id but are not full images or replay inputs. RecordingBytes includes both, HUDBytes identifies the crop share.",
			"capture.player_side is identity evidence supplied on that frame; empty can mean no new identity sample. It is not the UI's remembered/effective player side.",
		}}
	if !r.ended.IsZero() {
		ended := r.ended
		report.EndedAt = &ended
		report.Status.Finalized = true
	}
	for reason, count := range r.exclusions {
		report.Exclusions[reason] = count
	}
	copies := make(map[string][]float64, len(metricNames))
	for _, name := range metricNames {
		copies[name] = append([]float64(nil), r.samples[name]...)
	}
	r.mu.Unlock()
	for _, name := range metricNames {
		report.Metrics[name] = summarize(copies[name])
	}
	return report
}

// ExportReport takes a consistent snapshot without stopping capture. Repeated
// exports serialize and replace files using a same-directory temporary file.
func (r *Recorder) ExportReport() (string, error) {
	r.exportMu.Lock()
	defer r.exportMu.Unlock()
	report := r.report()
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return "", err
	}
	path := filepath.Join(report.Status.Directory, "report.json")
	if err = atomicWrite(path, data); err != nil {
		r.setError(err)
		return "", err
	}
	if err = atomicWrite(filepath.Join(report.Status.Directory, "report.md"), []byte(reportMarkdown(report))); err != nil {
		r.setError(err)
		return "", err
	}
	return path, nil
}

func atomicWrite(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".report-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if _, err = f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func reportMarkdown(r Report) string {
	var s strings.Builder
	fmt.Fprintf(&s, "# 诊断延迟报告\n\n采集尝试：%d；有效独立帧：%d；完整绘制帧：%d；完整可见事件：%d / %d。\n\n", r.Status.Attempts, r.Status.ValidFrames, r.Status.CompleteFrames, r.Status.CompleteEvents, r.Events)
	fmt.Fprintf(&s, "未完成绘制链路：%d 帧；未完成可见提示链路：%d 个事件。\n\n", r.IncompleteFrames, r.IncompleteEvents)
	fmt.Fprintf(&s, "原生录制：%d 帧；丢弃录制：%d 帧；日志丢弃：%d 条；已保存 PNG：%d 字节。\n\n", r.Status.RecordedFrames, r.Status.DroppedFrames, r.Status.DroppedLogs, r.Status.RecordingBytes)
	fmt.Fprintf(&s, "HUD 原像素证据：%d 张，%d 字节；HUD 丢弃：%d 张。启用时在同一录制额度内预留四分之一，每秒最多两张；与识别日志按帧号关联，不当作完整帧回放。\n\n", r.Status.HUDFrames, r.Status.HUDBytes, r.Status.DroppedHUD)
	fmt.Fprintf(&s, "仅保存已识别的对局原帧；非对局/未知/错误画面主动跳过：%d 帧（不计为录制丢帧）。\n\n", r.Status.SkippedFrames)
	if r.Status.LastError != "" {
		fmt.Fprintf(&s, "最近写入/限额异常：%s\n\n", r.Status.LastError)
	}
	s.WriteString("端到端起点是采集 API 请求，终点是实际绘制与缓冲提交完成；游戏源帧显示时间、显示器实际发光时间未知。未收到真实绘制回调时，端到端显示不可用。\n\n| 阶段（毫秒） | 样本 | P50 | P95 | P99 |\n|---|---:|---:|---:|---:|\n")
	format := func(v *float64) string {
		if v == nil {
			return "不可用"
		}
		return fmt.Sprintf("%.3f", *v)
	}
	for _, name := range metricNames {
		d := r.Metrics[name]
		fmt.Fprintf(&s, "| %s | %d | %s | %s | %s |\n", metricLabel(name), d.Count, format(d.P50MS), format(d.P95MS), format(d.P99MS))
	}
	s.WriteString("\n分位数采用最近秩法；缺帧、空帧、错误、hold、重复帧、时间缺失/倒序、未完整绘制和不可见事件均不作为端到端样本。录制队列/磁盘/日志限额丢弃另行计数。\n")
	if len(r.Exclusions) > 0 {
		s.WriteString("\n排除原因：\n\n")
		keys := make([]string, 0, len(r.Exclusions))
		for key := range r.Exclusions {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			fmt.Fprintf(&s, "- %s: %d\n", key, r.Exclusions[key])
		}
	}
	return s.String()
}

func metricLabel(name string) string {
	labels := map[string]string{
		"capture_ms":                     "采集",
		"analysis_wait_ms":               "图像校验/分析等待",
		"analysis_ms":                    "引擎分析",
		"analysis_to_received_ms":        "分析后交付",
		"event_confirmation_ms":          "首次证据到事件确认",
		"received_to_ui_queue_ms":        "结果接收到 UI 投递",
		"ui_dispatch_ms":                 "UI 投递到应用",
		"ui_paint_wait_ms":               "等待绘制",
		"ui_paint_submit_ms":             "绘制与缓冲提交",
		"frame_end_to_end_ms":            "帧应用内端到端",
		"event_confirmed_to_ui_queue_ms": "确认到可见事件 UI 投递",
		"event_confirmed_to_draw_ms":     "确认到提示提交",
		"event_end_to_end_ms":            "事件应用内端到端",
	}
	if label, ok := labels[name]; ok {
		return label
	}
	return name
}
