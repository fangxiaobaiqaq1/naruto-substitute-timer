//go:build windows && amd64

package leidian

import (
	"strings"
	"testing"
)

func TestParseList2KeepsIndexAndRuntimeEvidence(t *testing.T) {
	items, err := parseList2([]byte("0,主号,1,0,0,1234,5678,1920,1080,280\n1,训练,0,0,0,-1,-1,1280,720,240\n"), `E:\\leidian\\LDPlayer14`)
	if err != nil || len(items) != 2 {
		t.Fatalf("items=%+v err=%v", items, err)
	}
	if items[0].Index != 0 || !items[0].Running || items[0].PID != 1234 || items[0].Serial != "127.0.0.1:5555" {
		t.Fatalf("unexpected running instance: %+v", items[0])
	}
	if items[1].Running || items[1].Serial != "127.0.0.1:5556" {
		t.Fatalf("unexpected stopped instance: %+v", items[1])
	}
}

func TestDiscoverInstallDirFindsLocalLDPlayer(t *testing.T) {
	got := DiscoverInstallDir()
	if got == "" {
		t.Skip("本机没有可发现的雷电安装目录")
	}
	if !strings.Contains(strings.ToLower(got), "leidian") {
		t.Fatalf("unexpected discovered root: %q", got)
	}
}

func TestResolvePathsUsesBundledTools(t *testing.T) {
	o, err := resolvePaths(Options{InstallDir: `E:\\leidian\\LDPlayer14`, Index: 0})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(strings.ToLower(o.ConsolePath), `ldconsole.exe`) || !strings.HasSuffix(strings.ToLower(o.ADBPath), `adb.exe`) {
		t.Fatalf("unexpected tools: %+v", o)
	}
}
