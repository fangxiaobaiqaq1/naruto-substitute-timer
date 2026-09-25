package mumu

// Options identify a MuMu SDK capture target.
type Options struct {
	InstallDir string `json:"install_dir"`
	DLLPath    string `json:"dll_path,omitempty"`
	Instance   int    `json:"instance"`
	DisplayID  int    `json:"display_id"`
	Package    string `json:"package,omitempty"`
	// Manual preserves the configured target-selection mode for probe workers.
	// It is ignored by the native SDK itself.
	Manual bool `json:"manual,omitempty"`
}
