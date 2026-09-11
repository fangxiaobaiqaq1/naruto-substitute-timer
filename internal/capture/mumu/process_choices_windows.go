//go:build windows && amd64

package mumu

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"narutotimer/internal/win"
)

// ProcessChoice is a selectable target. Its identity is Instance.Root plus
// Instance.Index; PID is explanatory evidence and is never used as a saved
// target because it changes when MuMu restarts.
type ProcessChoice struct {
	Instance
	WindowTitle string `json:"window_title,omitempty"`
	ProcessName string `json:"process_name,omitempty"`
	IndexSource string `json:"index_source,omitempty"`
}

func sameRoot(left, right string) bool {
	return strings.EqualFold(filepath.Clean(left), filepath.Clean(right))
}

// ListProcessChoices merges each currently running MuMu installation with an
// optional manual directory hint. Two roots which each expose instance 0 return
// two independent choices instead of overwriting each other.
func ListProcessChoices(ctx context.Context, rootHint string) ([]ProcessChoice, error) {
	inventory := DiscoverInventory(ctx, rootHint)
	windows := win.FindMuMu()
	processByPID := map[uint32]ProcessEvidence{}
	for _, process := range inventory.Processes {
		processByPID[process.PID] = process
	}
	var choices []ProcessChoice
	var errs []error
	for _, installation := range inventory.Installations {
		if installation.Error != "" {
			errs = append(errs, fmt.Errorf("%s: %s", installation.Root, installation.Error))
			continue
		}
		for _, instance := range installation.Instances {
			choice := ProcessChoice{Instance: instance}
			// MuMuManager's PID is the best evidence available when Windows
			// blocks command-line inspection. Keep it as a display/evidence
			// value, but never use it as the persisted target identity.
			if instance.PID > 0 {
				choice.PID = instance.PID
				choice.IndexSource = "MuMuManager 实例列表"
			}
			// Prefer explicit process command-line evidence when it agrees
			// with this root and instance index.
			for _, process := range inventory.Processes {
				if !sameRoot(process.Root, instance.Root) {
					continue
				}
				if process.Index != nil && *process.Index == instance.Index {
					choice.PID = int(process.PID)
					choice.ProcessName = process.Name
					choice.IndexSource = process.IndexSource
					break
				}
			}
			if choice.PID > 0 && choice.IndexSource == "" {
				choice.IndexSource = "MuMuManager 实例列表"
			}
			for _, window := range windows {
				if !window.Visible || window.PID == 0 {
					continue
				}
				process, known := processByPID[window.PID]
				if !known || !sameRoot(process.Root, instance.Root) {
					continue
				}
				if choice.PID > 0 && uint32(choice.PID) == window.PID {
					choice.WindowTitle = window.Title
					if choice.ProcessName == "" {
						choice.ProcessName = window.ProcessName
					}
					break
				}
				if choice.PID == 0 && process.Index != nil && *process.Index == instance.Index {
					choice.PID = int(window.PID)
					choice.WindowTitle = window.Title
					choice.ProcessName = window.ProcessName
					choice.IndexSource = process.IndexSource
					break
				}
				// Names are only a display aid after the manager supplied the
				// instance number; they never create a guessed instance.
				if choice.PID == 0 && strings.EqualFold(strings.TrimSpace(window.Title), strings.TrimSpace(instance.Name)) {
					choice.PID = int(window.PID)
					choice.WindowTitle = window.Title
					choice.ProcessName = window.ProcessName
					choice.IndexSource = "MuMuManager 实例列表 + 窗口标题"
				}
			}
			choices = append(choices, choice)
		}
	}
	sort.SliceStable(choices, func(i, j int) bool {
		if !sameRoot(choices[i].Root, choices[j].Root) {
			return strings.ToLower(choices[i].Root) < strings.ToLower(choices[j].Root)
		}
		return choices[i].Index < choices[j].Index
	})
	if len(choices) > 0 {
		return choices, nil
	}
	for _, message := range inventory.Errors {
		errs = append(errs, errors.New(message))
	}
	if len(errs) > 0 {
		return nil, fmt.Errorf("没有可选 MuMu 实例: %w", errors.Join(errs...))
	}
	return nil, errors.New("没有可选 MuMu 实例，请先启动 MuMu 或手动选择安装目录")
}
