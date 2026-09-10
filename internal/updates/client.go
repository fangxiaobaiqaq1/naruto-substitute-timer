package updates

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const Repository = "fangxiaobaiqaq1/naruto-substitute-timer"
const RepositoryURL = "https://github.com/" + Repository
const APIBase = "https://api.github.com/repos/" + Repository
const MaxDownload = 256 << 20

type Asset struct {
	Name   string `json:"name"`
	URL    string `json:"browser_download_url"`
	Size   int64  `json:"size"`
	Digest string `json:"digest"`
	State  string `json:"state"`
}
type Release struct {
	Tag        string    `json:"tag_name"`
	Name       string    `json:"name"`
	Body       string    `json:"body"`
	Published  time.Time `json:"published_at"`
	URL        string    `json:"html_url"`
	Draft      bool      `json:"draft"`
	Prerelease bool      `json:"prerelease"`
	Assets     []Asset   `json:"assets"`
}
type Feed struct {
	Latest   Release   `json:"latest"`
	Releases []Release `json:"releases"`
	Checked  time.Time `json:"checked"`
}
type Client struct {
	HTTP *http.Client
	Base string
}

func NewClient() *Client {
	return &Client{HTTP: &http.Client{Timeout: 20 * time.Second}, Base: APIBase}
}
func (c *Client) get(ctx context.Context, path string, dst any) error {
	req, err := http.NewRequestWithContext(ctx, "GET", c.Base+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "Naruto-Substitute-Timer")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("无法连接 GitHub，请检查网络后重试: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == 403 || resp.StatusCode == 429 {
		return errors.New("GitHub 请求限流，请稍后重试")
	}
	if resp.StatusCode != 200 {
		return fmt.Errorf("GitHub 返回 HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, (2<<20)+1))
	if err != nil {
		return err
	}
	if len(data) > 2<<20 {
		return errors.New("更新信息超过大小限制")
	}
	return json.Unmarshal(data, dst)
}
func (c *Client) Check(ctx context.Context) (Feed, error) {
	var f Feed
	if err := c.get(ctx, "/releases/latest", &f.Latest); err != nil {
		return f, err
	}
	if !validRelease(f.Latest) {
		return f, errors.New("GitHub 最新正式版信息无效")
	}
	if err := c.get(ctx, "/releases?per_page=100", &f.Releases); err != nil {
		return f, err
	}
	var kept []Release
	for _, r := range f.Releases {
		if validRelease(r) {
			kept = append(kept, r)
		}
	}
	sort.SliceStable(kept, func(i, j int) bool { return kept[i].Published.After(kept[j].Published) })
	f.Releases = kept
	f.Checked = time.Now()
	return f, nil
}
func validRelease(r Release) bool {
	_, ok := version(r.Tag)
	u, e := url.Parse(r.URL)
	return ok && !r.Draft && !r.Prerelease && !r.Published.IsZero() && e == nil && u.Scheme == "https" && u.Host == "github.com" && strings.HasPrefix(u.Path, "/"+Repository+"/releases/tag/")
}
func version(v string) ([3]uint64, bool) {
	var out [3]uint64
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	p := strings.Split(v, ".")
	if len(p) < 2 || len(p) > 3 {
		return out, false
	}
	for i, s := range p {
		if s == "" {
			return out, false
		}
		for _, ch := range s {
			if ch < '0' || ch > '9' {
				return out, false
			}
		}
		n, e := strconv.ParseUint(s, 10, 64)
		if e != nil {
			return out, false
		}
		out[i] = n
	}
	return out, true
}
func Compare(remote, local string) (int, error) {
	r, ok := version(remote)
	if !ok {
		return 0, errors.New("远端版本号无效")
	}
	l, ok := version(local)
	if !ok {
		return 0, errors.New("开发构建不支持自动覆盖，请使用正式版")
	}
	for i := range r {
		if r[i] > l[i] {
			return 1, nil
		}
		if r[i] < l[i] {
			return -1, nil
		}
	}
	return 0, nil
}
func (r Release) Executable() (Asset, error) {
	for _, a := range r.Assets {
		if a.Name == "timer-app.exe" && a.State == "uploaded" && a.Size > 0 && a.Size <= MaxDownload {
			u, e := url.Parse(a.URL)
			if e == nil && u.Scheme == "https" && u.Host == "github.com" && u.Path == "/"+Repository+"/releases/download/"+r.Tag+"/timer-app.exe" {
				if _, e := assetHash(a); e == nil {
					return a, nil
				}
			}
		}
	}
	return Asset{}, errors.New("此版本尚未提供可校验的 Windows EXE，请前往发布页")
}
func assetHash(a Asset) (string, error) {
	s := strings.TrimPrefix(a.Digest, "sha256:")
	if !strings.HasPrefix(a.Digest, "sha256:") || len(s) != 64 {
		return "", errors.New("缺少 SHA256 校验值")
	}
	if _, e := hex.DecodeString(s); e != nil {
		return "", e
	}
	return strings.ToLower(s), nil
}
func Directory() string {
	d, e := os.UserCacheDir()
	if e != nil {
		return ""
	}
	return filepath.Join(d, "naruto-timer", "updates")
}
func SaveFeed(f Feed) error {
	d := Directory()
	if d == "" {
		return errors.New("无法获取更新缓存目录")
	}
	if err := os.MkdirAll(d, 0700); err != nil {
		return err
	}
	b, e := json.Marshal(f)
	if e != nil {
		return e
	}
	return os.WriteFile(filepath.Join(d, "releases.json"), b, 0600)
}
func CachedFeed() (Feed, error) {
	var f Feed
	b, e := os.ReadFile(filepath.Join(Directory(), "releases.json"))
	if e != nil {
		return f, e
	}
	if len(b) > 2<<20 {
		return f, errors.New("缓存过大")
	}
	e = json.Unmarshal(b, &f)
	if e == nil && !validRelease(f.Latest) {
		e = errors.New("缓存无效")
	}
	return f, e
}

// Download only the canonical EXE from the release selected by the GitHub API.
// TLS redirects must remain on GitHub's asset infrastructure. Partial downloads
// never become installable; every byte is verified again by the updater helper.
func (c *Client) Download(ctx context.Context, r Release, progress func(int64, int64)) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	a, err := r.Executable()
	if err != nil {
		return "", err
	}
	hash, _ := assetHash(a)
	if !validRelease(r) {
		return "", errors.New("无效发布信息")
	}
	d := Directory()
	if d == "" {
		return "", errors.New("无法获取更新目录")
	}
	d = filepath.Join(d, hash)
	if err = os.MkdirAll(d, 0700); err != nil {
		return "", err
	}
	target := filepath.Join(d, "timer-app.exe")
	if verifyFile(target, hash, a.Size) == nil {
		return target, nil
	}
	file, err := os.CreateTemp(d, "download-*.part")
	if err != nil {
		return "", err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	client := *c.HTTP
	client.Timeout = 15 * time.Minute
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		h := req.URL.Hostname()
		if len(via) > 5 || req.URL.Scheme != "https" || (h != "github.com" && h != "release-assets.githubusercontent.com" && h != "objects.githubusercontent.com") {
			return errors.New("更新下载跳转到非 GitHub 地址")
		}
		return nil
	}
	req, err := http.NewRequestWithContext(ctx, "GET", a.URL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "Naruto-Substitute-Timer")
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("下载返回 HTTP %d", resp.StatusCode)
	}
	h := sha256.New()
	var n int64
	buf := make([]byte, 128<<10)
	last := time.Time{}
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		nr, e := resp.Body.Read(buf)
		if nr > 0 {
			n += int64(nr)
			if n > a.Size || n > MaxDownload {
				return "", errors.New("更新文件大小不符")
			}
			if _, err = file.Write(buf[:nr]); err != nil {
				return "", err
			}
			h.Write(buf[:nr])
			if progress != nil && time.Since(last) > 100*time.Millisecond {
				progress(n, a.Size)
				last = time.Now()
			}
		}
		if e == io.EOF {
			break
		}
		if e != nil {
			return "", e
		}
	}
	if n != a.Size || hex.EncodeToString(h.Sum(nil)) != hash {
		return "", errors.New("更新文件校验失败，请重新下载")
	}
	if err = file.Sync(); err != nil {
		return "", err
	}
	if err = file.Close(); err != nil {
		return "", err
	}
	if err = os.Rename(file.Name(), target); err != nil {
		return "", err
	}
	if progress != nil {
		progress(n, a.Size)
	}
	return target, nil
}
func verifyFile(path, hash string, size int64) error {
	f, e := os.Open(path)
	if e != nil {
		return e
	}
	defer f.Close()
	h := sha256.New()
	n, e := io.Copy(h, io.LimitReader(f, MaxDownload+1))
	if e != nil {
		return e
	}
	if n != size || hex.EncodeToString(h.Sum(nil)) != hash {
		return errors.New("EXE SHA256 或大小校验失败")
	}
	return nil
}
