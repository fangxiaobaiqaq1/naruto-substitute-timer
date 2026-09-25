package updates

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

func TestGiteeFeedUsesPublicCanonicalAttachmentAPI(t *testing.T) {
	t.Setenv("LOCALAPPDATA", t.TempDir())
	data := []byte("official executable")
	digest := strings.TrimPrefix(testRelease(data).Assets[0].Digest, "sha256:")
	sums := digest + "  timer-app.exe\n"
	client := NewClientForSource(Gitee)
	client.HTTP.Transport = transportFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Host != "gitee.com" || !strings.HasPrefix(req.URL.Path, "/api/v5/repos/"+GiteeRepository+"/") {
			t.Fatalf("unexpected URL %s", req.URL)
		}
		if req.URL.Query().Get("access_token") != "" || req.Header.Get("Authorization") != "" {
			t.Fatal("public Gitee request used credentials")
		}
		var body []byte
		switch {
		case strings.HasSuffix(req.URL.Path, "/releases"):
			body, _ = json.Marshal([]giteeRelease{{ID: 30, Tag: "v0.3.0", Name: "Release", Created: time.Now()}})
		case strings.HasSuffix(req.URL.Path, "/releases/30/attach_files"):
			body, _ = json.Marshal([]Asset{{ID: 1, Name: "timer-app.exe", Size: int64(len(data)), URL: "https://evil.invalid/ignored"}, {ID: 2, Name: "SHA256SUMS.txt", Size: int64(len(sums))}})
		case strings.HasSuffix(req.URL.Path, "/releases/30/attach_files/2/download"):
			body = []byte(sums)
		case strings.HasSuffix(req.URL.Path, "/releases/30/attach_files/1/download"):
			body = data
		default:
			t.Fatalf("unexpected endpoint %s", req.URL)
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(body))}, nil
	})
	feed, err := client.Check(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if feed.Latest.UpdateSource() != Gitee || feed.Latest.Assets[0].URL != giteeDownloadURL(30, 1) {
		t.Fatalf("untrusted Gitee feed: %+v", feed.Latest)
	}
	if err := SaveFeed(feed); err != nil {
		t.Fatal(err)
	}
	if _, err := CachedFeedForSource(GitHub); err == nil {
		t.Fatal("Gitee cache leaked into GitHub cache")
	}
	if cached, err := CachedFeedForSource(Gitee); err != nil || cached.Latest.Tag != feed.Latest.Tag {
		t.Fatalf("Gitee cache = %+v, %v", cached, err)
	}
	path, err := client.Download(context.Background(), feed.Latest, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(path); err != nil || !bytes.Equal(got, data) {
		t.Fatalf("download = %q, %v", got, err)
	}
	if _, err := NewClient().Download(context.Background(), feed.Latest, nil); err == nil {
		t.Fatal("GitHub client accepted Gitee release")
	}
}

func TestGiteeAttachmentRedirectAllowlist(t *testing.T) {
	check := assetRedirect(Gitee)
	for _, raw := range []string{
		"https://gitee.com/xiaobaiqaq/naruto-substitute-timer/releases/1/attach_files/1/download",
		"https://foruda.gitee.com/attachment/1",
	} {
		req, err := http.NewRequest(http.MethodGet, raw, nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := check(req, nil); err != nil {
			t.Fatalf("redirect to %q rejected: %v", raw, err)
		}
	}
	for _, raw := range []string{
		"https://foruda.gitee.com.evil.invalid/attachment/1",
		"https://evil.invalid/foruda.gitee.com/attachment/1",
	} {
		req, err := http.NewRequest(http.MethodGet, raw, nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := check(req, nil); err == nil {
			t.Fatalf("redirect to lookalike %q accepted", raw)
		}
	}
}

func TestGiteeFailureDoesNotFallBackToGitHub(t *testing.T) {
	client := NewClientForSource(Gitee)
	client.HTTP.Transport = transportFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Host != "gitee.com" {
			t.Fatalf("Gitee check attempted fallback request: %s", req.URL)
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`[]`))}, nil
	})
	if _, err := client.Check(context.Background()); err == nil || !strings.Contains(err.Error(), "Gitee") {
		t.Fatalf("Gitee failure was not reported clearly: %v", err)
	}
}

func TestGiteeRejectsMissingOrDuplicateChecksumAssets(t *testing.T) {
	for _, assets := range [][]Asset{
		{{ID: 1, Name: "timer-app.exe", Size: 1}},
		{{ID: 1, Name: "timer-app.exe", Size: 1}, {ID: 2, Name: "timer-app.exe", Size: 1}, {ID: 3, Name: "SHA256SUMS.txt", Size: 1}},
		{{ID: 1, Name: "timer-app.exe", Size: 1}, {ID: 2, Name: "SHA256SUMS.txt", Size: 1}, {ID: 3, Name: "SHA256SUMS.txt", Size: 1}},
	} {
		client := NewClientForSource(Gitee)
		client.HTTP.Transport = transportFunc(func(req *http.Request) (*http.Response, error) {
			var body []byte
			if strings.HasSuffix(req.URL.Path, "/releases") {
				body, _ = json.Marshal([]giteeRelease{{ID: 1, Tag: "v1.0.0", Created: time.Now()}})
			} else {
				body, _ = json.Marshal(assets)
			}
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(body))}, nil
		})
		if _, err := client.Check(context.Background()); err == nil {
			t.Fatal("accepted incomplete or duplicate Gitee assets")
		}
	}
}

func TestChecksumForExecutableStrict(t *testing.T) {
	h := strings.Repeat("a", 64)
	if got, err := checksumForExecutable([]byte(h + " *timer-app.exe\n")); err != nil || got != h {
		t.Fatalf("checksum = %q, %v", got, err)
	}
	for _, b := range []string{"", h + " other.exe", h + " timer-app.exe\n" + h + " timer-app.exe"} {
		if _, err := checksumForExecutable([]byte(b)); err == nil {
			t.Fatalf("accepted %q", b)
		}
	}
}
