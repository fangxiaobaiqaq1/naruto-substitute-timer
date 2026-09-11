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

func testRelease(data []byte) Release {
	h := sha256.Sum256(data)
	return Release{Tag: "v0.2.0", Name: "Version", URL: RepositoryURL + "/releases/tag/v0.2.0", Published: time.Date(2026, 9, 10, 1, 0, 0, 0, time.UTC), Assets: []Asset{{Name: "timer-app.exe", URL: RepositoryURL + "/releases/download/v0.2.0/timer-app.exe", Size: int64(len(data)), Digest: "sha256:" + hex.EncodeToString(h[:]), State: "uploaded"}}}
}
func TestVersionComparison(t *testing.T) {
	for _, tc := range []struct {
		remote, local string
		want          int
	}{{"v0.1", "0.1.0", 0}, {"v0.10.0", "v0.2.99", 1}, {"v1.0.0", "v2.0.0", -1}} {
		got, e := Compare(tc.remote, tc.local)
		if e != nil || got != tc.want {
			t.Fatal(tc, got, e)
		}
	}
	for _, v := range []string{"development", "v1.2.3-beta", "v1.2.3.4", "v1.-2.0"} {
		if _, e := Compare(v, "v0.1.0"); e == nil {
			t.Fatal(v)
		}
	}
}
func TestIsReleaseVersion(t *testing.T) {
	for _, tc := range []struct {
		version string
		want    bool
	}{
		{"v0.2.1", true}, {"0.2", true}, {"development", false}, {"v1.2.3-beta", false}, {"v1.2.3.4", false},
	} {
		if got := IsReleaseVersion(tc.version); got != tc.want {
			t.Fatalf("IsReleaseVersion(%q) = %v, want %v", tc.version, got, tc.want)
		}
	}
}

func TestFeedFiltersDraftAndPrereleaseAndPreservesDates(t *testing.T) {
	r := testRelease([]byte("test"))
	pre := r
	pre.Tag = "v0.3.0-beta"
	pre.Prerelease = true
	draft := r
	draft.Draft = true
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/releases/latest" {
			json.NewEncoder(w).Encode(r)
		} else {
			json.NewEncoder(w).Encode([]Release{pre, draft, r})
		}
	}))
	defer server.Close()
	c := &Client{HTTP: server.Client(), Base: server.URL}
	f, e := c.Check(context.Background())
	if e != nil || len(f.Releases) != 1 || !f.Releases[0].Published.Equal(r.Published) {
		t.Fatalf("%+v %v", f, e)
	}
	t.Setenv("LOCALAPPDATA", t.TempDir())
	if e = SaveFeed(f); e != nil {
		t.Fatal(e)
	}
	cached, e := CachedFeed()
	if e != nil || cached.Latest.Tag != r.Tag {
		t.Fatal(cached, e)
	}
}

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestDownloadVerifiesAndNeverPromotesPartialOrWrongHash(t *testing.T) {
	for _, kind := range []string{"good", "truncated", "tampered", "oversized", "cancelled"} {
		t.Run(kind, func(t *testing.T) {
			t.Setenv("LOCALAPPDATA", t.TempDir())
			data := []byte("downloaded official executable")
			r := testRelease(data)
			payload := data
			switch kind {
			case "truncated":
				payload = data[:4]
			case "tampered":
				payload = bytes.Repeat([]byte("x"), len(data))
			case "oversized":
				payload = append(append([]byte{}, data...), 1)
			}
			client := &Client{HTTP: &http.Client{Transport: transportFunc(func(req *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(payload)), Header: make(http.Header)}, nil
			})}}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if kind == "cancelled" {
				cancel()
			}
			path, e := client.Download(ctx, r, nil)
			if kind == "good" {
				if e != nil {
					t.Fatal(e)
				}
				b, _ := os.ReadFile(path)
				if !bytes.Equal(b, data) {
					t.Fatal("data changed")
				}
			} else if e == nil {
				t.Fatal("invalid download accepted", kind)
			}
			leftovers, _ := filepath.Glob(filepath.Join(Directory(), "*", "*.part"))
			if len(leftovers) > 0 {
				t.Fatal("partial files left", leftovers)
			}
		})
	}
}
func TestReleaseDoesNotInstallOtherRepositoriesOrMissingDigest(t *testing.T) {
	r := testRelease([]byte("data"))
	for _, u := range []string{"http://github.com/" + Repository + "/releases/download/v0.2.0/timer-app.exe", strings.Replace(r.Assets[0].URL, Repository, "someone/other", 1)} {
		bad := r
		bad.Assets = append([]Asset{}, r.Assets...)
		bad.Assets[0].URL = u
		if _, e := bad.Executable(); e == nil {
			t.Fatal("unsafe asset accepted", u)
		}
	}
	r.Assets[0].Digest = ""
	if _, e := r.Executable(); e == nil {
		t.Fatal("missing hash accepted")
	}
}
func TestRateLimitReportsUsefulError(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(403) }))
	defer s.Close()
	_, e := (&Client{HTTP: s.Client(), Base: s.URL}).Check(context.Background())
	if e == nil || !strings.Contains(e.Error(), "限流") {
		t.Fatal(e)
	}
}
