package leidian

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func makeLDRoot(t *testing.T, root, console string) string {
	t.Helper()
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, console), []byte("MZ"), 0o755); err != nil {
		t.Fatal(err)
	}
	return root
}

// UR-06 PoC: a folder any local user can create (C:\ProgramData\leidian,
// C:\leidian\...) sorted ahead of the real installation on D: and its
// ldconsole.exe was executed. Ranking now follows the discovery source, and
// drive-root finds are subject to an ownership check.
func TestRankInstallCandidatesPrefersTrustedSources(t *testing.T) {
	base := t.TempDir()
	real := makeLDRoot(t, filepath.Join(base, "D", "leidian", "LDPlayer9"), DefaultConsoleName)
	planted := makeLDRoot(t, filepath.Join(base, "C", "leidian", "LDPlayer9"), DefaultConsoleName)
	ownedDriveRoot := makeLDRoot(t, filepath.Join(base, "E", "leidian", "LDPlayer14"), "dnconsole.exe")

	found := expandInstallCandidates([]installCandidate{
		{Root: filepath.Join(base, "C", "leidian"), Source: sourceDriveRoot},
		{Root: filepath.Join(base, "E", "leidian"), Source: sourceDriveRoot},
		{Root: real, Source: sourceRegistry},
	})
	trusted := func(c installCandidate) bool { return c.Root != planted }
	got := rankInstallCandidates(found, trusted)
	want := []string{real, ownedDriveRoot}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ranked roots = %q, want %q", got, want)
	}
	// Without the ownership filter, source order still puts the registry find
	// first although the planted path sorts lexicographically ahead of it.
	if got := rankInstallCandidates(found, nil); len(got) != 3 || got[0] != real {
		t.Fatalf("unfiltered order = %q", got)
	}
}

func TestExpandInstallCandidatesChecksConsoleAndSkipsRelative(t *testing.T) {
	base := t.TempDir()
	root := makeLDRoot(t, filepath.Join(base, "leidian"), DefaultConsoleName)
	child := makeLDRoot(t, filepath.Join(root, "LDPlayer9"), "dnconsole.exe")
	if err := os.MkdirAll(filepath.Join(root, "LDPlayerEmpty"), 0o755); err != nil {
		t.Fatal(err)
	}
	got := expandInstallCandidates([]installCandidate{
		{Root: root, Source: sourceUserLocal},
		{Root: "leidian", Source: sourceProgramFiles}, // empty %ProgramFiles% must not mean the CWD
		{Root: "", Source: sourceProgramFiles},
		{Root: filepath.Join(base, "missing"), Source: sourceRegistry},
	})
	want := []installCandidate{{Root: root, Source: sourceUserLocal}, {Root: child, Source: sourceUserLocal}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("expanded = %+v, want %+v", got, want)
	}
	// Case-insensitive duplicates collapse to the first (most trusted) source.
	dup := append(got, installCandidate{Root: root, Source: sourceDriveRoot})
	if ranked := rankInstallCandidates(dup, nil); len(ranked) != 2 {
		t.Fatalf("duplicates kept: %q", ranked)
	}
}

func TestRegistryPathDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "LDPlayer9")
	exe := filepath.Join(dir, "dnplayer.exe")
	for _, tc := range []struct{ value, data, want string }{
		{"InstallLocation", `"` + dir + `"`, dir},
		{"InstallLocation", dir + string(filepath.Separator), dir},
		{"DisplayIcon", exe + ",0", dir},
		{"DisplayIcon", `"` + exe + `",-101`, dir},
		{"DisplayIcon", exe, dir},
		{"UninstallString", `"` + filepath.Join(dir, "dnuninst.exe") + `" /S`, dir},
		{"UninstallString", filepath.Join(dir, "dnuninst.exe") + " /uninstall", dir},
		{"InstallLocation", "LDPlayer9", ""},
		{"UninstallString", `"unterminated`, ""},
		{"DisplayIcon", "", ""},
	} {
		if got := registryPathDir(tc.value, tc.data); got != tc.want {
			t.Errorf("registryPathDir(%s, %q) = %q, want %q", tc.value, tc.data, got, tc.want)
		}
	}
}

func TestIsLeidianUninstallEntry(t *testing.T) {
	for _, tc := range []struct {
		key, name string
		want      bool
	}{
		{"LDPlayer9", "", true},
		{"{1234}", "雷电模拟器9", true},
		{"leidian", "", true},
		{"MuMuPlayer-12.0", "MuMu模拟器12", false},
		{"{abcd}", "Some Game", false},
	} {
		if got := isLeidianUninstallEntry(tc.key, tc.name); got != tc.want {
			t.Errorf("isLeidianUninstallEntry(%q, %q) = %v", tc.key, tc.name, got)
		}
	}
}
