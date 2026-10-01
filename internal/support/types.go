// Shared support-bundle types; the ZIP writer and SDK probe are Windows-only.
package support

import (
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
