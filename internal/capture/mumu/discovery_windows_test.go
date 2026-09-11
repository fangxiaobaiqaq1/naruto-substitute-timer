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

func TestDeviceAndMainResolveSameInstallation(t *testing.T) {
	root := t.TempDir()
	dll := filepath.Join(root, "nx_device", "15.0", "shell", "sdk", "external_renderer_ipc.dll")
	if err := os.MkdirAll(filepath.Dir(dll), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dll, []byte("test"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, executable := range []string{filepath.Join(root, "nx_main", "MuMuNxMain.exe"), filepath.Join(root, "nx_device", "15.0", "shell", "MuMuNxDevice.exe")} {
		if got := installationFromExecutable(executable); got != root {
			t.Fatalf("%s resolved to %s, want %s", executable, got, root)
		}
	}
}

func TestFindDLLCandidatesUsesStablePreferenceOrder(t *testing.T) {
	root := t.TempDir()
	for _, relative := range []string{
		"nx_device/15.0/shell/sdk/external_renderer_ipc.dll",
		"nx_device/12.0/shell/sdk/external_renderer_ipc.dll",
		"nx_main/sdk/external_renderer_ipc.dll",
	} {
		path := filepath.Join(root, relative)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(relative), 0600); err != nil {
			t.Fatal(err)
		}
	}
	files, err := FindDLLCandidates(root)
	if err != nil || len(files) != 3 {
		t.Fatal(files, err)
	}
	if !files[0].Preferred || files[0].Path != filepath.Join(root, "nx_main", "sdk", "external_renderer_ipc.dll") {
		t.Fatalf("unexpected first SDK: %+v", files[0])
	}
	if files[1].Path >= files[2].Path {
		t.Fatalf("device SDK candidates not sorted: %+v", files)
	}
}
