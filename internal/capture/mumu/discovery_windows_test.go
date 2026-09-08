//go:build windows && amd64

package mumu

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDiscoverInstallationFromKnownExecutableLayout(t *testing.T) {
	for _, rel := range []string{"nx_main/sdk/external_renderer_ipc.dll", "nx_device/12.0/shell/sdk/external_renderer_ipc.dll"} {
		t.Run(rel, func(t *testing.T) {
			root := t.TempDir()
			dll := filepath.Join(root, rel)
			if err := os.MkdirAll(filepath.Dir(dll), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(dll, []byte("test"), 0600); err != nil {
				t.Fatal(err)
			}
			if got := installationFromExecutable(filepath.Join(root, "nx_main", "MuMuNxMain.exe")); got != root {
				t.Fatalf("got %q, want %q", got, root)
			}
		})
	}
}
func TestDiscoverRejectsUnrelatedDirectory(t *testing.T) {
	if got := installationFromExecutable(filepath.Join(t.TempDir(), "MuMuNxMain.exe")); got != "" {
		t.Fatalf("unexpected installation %s", got)
	}
}
