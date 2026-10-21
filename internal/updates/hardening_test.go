package updates

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// UR-07: metadata carries the trusted digest, so a redirect may neither leave
// the API host nor drop to plain HTTP.
func TestMetadataRedirectStaysOnAPIHostOverHTTPS(t *testing.T) {
	r := testRelease([]byte("x"))
	var plainHits int
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		plainHits++
		if strings.HasSuffix(req.URL.Path, "/latest") {
			json.NewEncoder(w).Encode(r)
		} else {
			json.NewEncoder(w).Encode([]Release{r})
		}
	}))
	defer plain.Close()
	api := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		http.Redirect(w, req, plain.URL+req.URL.RequestURI(), http.StatusFound)
	}))
	defer api.Close()
	c := &Client{HTTP: api.Client(), Base: api.URL}
	if _, err := c.Check(context.Background()); err == nil {
		t.Fatal("feed accepted through a cross-host plain-HTTP redirect")
	}
	if plainHits != 0 {
		t.Fatalf("plain-HTTP host was contacted %d times", plainHits)
	}

	// A redirect that stays on the same HTTPS API host is still followed.
	var same *httptest.Server
	same = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch req.URL.Path {
		case "/releases/latest":
			http.Redirect(w, req, same.URL+"/moved/latest", http.StatusMovedPermanently)
		case "/moved/latest":
			json.NewEncoder(w).Encode(r)
		default:
			json.NewEncoder(w).Encode([]Release{r})
		}
	}))
	defer same.Close()
	c = &Client{HTTP: same.Client(), Base: same.URL}
	if f, err := c.Check(context.Background()); err != nil || f.Latest.Tag != r.Tag {
		t.Fatalf("same-host HTTPS redirect: %+v %v", f.Latest, err)
	}
}

// UR-07: the release list only feeds the changelog picker; losing it must not
// hide a verified latest release.
func TestHistoryFailureStillOffersLatest(t *testing.T) {
	r := testRelease([]byte("x"))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/releases/latest" {
			json.NewEncoder(w).Encode(r)
			return
		}
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer server.Close()
	f, err := (&Client{HTTP: server.Client(), Base: server.URL}).Check(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if f.Latest.Tag != r.Tag || len(f.Releases) != 1 || f.Releases[0].Tag != r.Tag {
		t.Fatalf("feed=%+v", f)
	}
}

// UR-07: GitHub downloads use the same audited redirect policy as Gitee
// (no userinfo, no explicit port), not a hostname-only check.
func TestGitHubDownloadRejectsRedirectWithUserinfoOrPort(t *testing.T) {
	for _, location := range []string{
		"https://user:secret@objects.githubusercontent.com/asset",
		"https://objects.githubusercontent.com:8443/asset",
		"http://objects.githubusercontent.com/asset",
	} {
		t.Run(location, func(t *testing.T) {
			t.Setenv("LOCALAPPDATA", t.TempDir())
			data := []byte("official executable")
			r := testRelease(data)
			client := &Client{HTTP: &http.Client{Transport: transportFunc(func(req *http.Request) (*http.Response, error) {
				if req.URL.Host == "github.com" {
					h := make(http.Header)
					h.Set("Location", location)
					return &http.Response{StatusCode: http.StatusFound, Header: h, Body: io.NopCloser(strings.NewReader(""))}, nil
				}
				return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(data))}, nil
			})}}
			if _, err := client.Download(context.Background(), r, nil); err == nil {
				t.Fatalf("download followed unsafe redirect to %s", location)
			}
		})
	}
}

// UR-08: Gitee is the default source, so its gitee-<sha256> download
// directories must age out like the GitHub ones.
func TestPruneCacheRemovesOldGiteeDownloads(t *testing.T) {
	t.Setenv("LOCALAPPDATA", t.TempDir())
	root := Directory()
	old := filepath.Join(root, "gitee-"+strings.Repeat("b", 64))
	fresh := filepath.Join(root, "gitee-"+strings.Repeat("c", 64))
	lookalike := filepath.Join(root, "gitee-not-a-hash")
	for _, dir := range []string{old, fresh, lookalike} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "timer-app.exe"), []byte("exe"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(old, "notes.txt"), []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-30 * 24 * time.Hour)
	for _, path := range []string{filepath.Join(old, "timer-app.exe"), filepath.Join(old, "notes.txt"), old, filepath.Join(lookalike, "timer-app.exe"), lookalike} {
		if err := os.Chtimes(path, past, past); err != nil {
			t.Fatal(err)
		}
	}
	PruneCache(time.Now())
	if _, err := os.Stat(filepath.Join(old, "timer-app.exe")); !os.IsNotExist(err) {
		t.Fatalf("30-day-old Gitee download survived: %v", err)
	}
	for _, keep := range []string{filepath.Join(old, "notes.txt"), filepath.Join(fresh, "timer-app.exe"), filepath.Join(lookalike, "timer-app.exe")} {
		if _, err := os.Stat(keep); err != nil {
			t.Fatalf("%s removed: %v", keep, err)
		}
	}
}

// UR-09: a plan may only replace the program the helper was copied from.
func TestCheckPlanBindsTargetToHelper(t *testing.T) {
	t.Setenv("LOCALAPPDATA", t.TempDir())
	installed := t.TempDir()
	target := filepath.Join(installed, "timer-app.exe")
	other := filepath.Join(installed, "other.exe")
	current := []byte("currently installed timer")
	if err := os.WriteFile(target, current, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(other, []byte("some other program"), 0o700); err != nil {
		t.Fatal(err)
	}
	work := filepath.Join(Directory(), "apply-1")
	helper := filepath.Join(work, "updater.exe")
	incoming := filepath.Join(Directory(), strings.Repeat("a", 64), "timer-app.exe")
	for _, dir := range []string{work, filepath.Dir(incoming)} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(helper, current, 0o700); err != nil {
		t.Fatal(err)
	}
	next := []byte("verified new timer")
	if err := os.WriteFile(incoming, next, 0o700); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(next)
	plan := Plan{Parent: 0xfffffffc, Target: target, Incoming: incoming, Hash: hex.EncodeToString(sum[:]), Size: int64(len(next)), Version: "v9.9.9"}
	if err := checkPlan(plan, helper); err != nil {
		t.Fatalf("plan for the helper's own program rejected: %v", err)
	}
	planted := plan
	planted.Target = other
	if err := checkPlan(planted, helper); err == nil {
		t.Fatal("plan naming a different program as target was accepted")
	}
	if err := os.WriteFile(helper, []byte("currently installed timeR"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := checkPlan(plan, helper); err == nil {
		t.Fatal("same-size but different helper accepted")
	}
	tampered := plan
	tampered.Hash = strings.Repeat("0", 64)
	if err := checkPlan(tampered, ""); err == nil {
		t.Fatal("incoming file with the wrong hash accepted")
	}
}
