package updates

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Gitee v5 release metadata has no digest for attachments. The shipped public
// updater therefore requires the source-native attachment API plus a matching
// SHA256SUMS.txt uploaded with timer-app.exe.
type giteeRelease struct {
	ID         int64     `json:"id"`
	Tag        string    `json:"tag_name"`
	Name       string    `json:"name"`
	Body       string    `json:"body"`
	Created    time.Time `json:"created_at"`
	Draft      bool      `json:"draft"`
	Prerelease bool      `json:"prerelease"`
}

func giteeDownloadURL(releaseID, assetID int64) string {
	if releaseID <= 0 || assetID <= 0 {
		return ""
	}
	return fmt.Sprintf("%s/releases/%d/attach_files/%d/download", GiteeAPIBase, releaseID, assetID)
}

func (c *Client) checkGitee(ctx context.Context) (Feed, error) {
	var raw []giteeRelease
	var f Feed
	if err := c.get(ctx, "/releases?per_page=100&direction=desc", &raw); err != nil {
		return f, err
	}
	for _, item := range raw {
		r := Release{Source: Gitee, ID: item.ID, Tag: item.Tag, Name: item.Name, Body: item.Body, Published: item.Created, Draft: item.Draft, Prerelease: item.Prerelease, URL: GiteeRepositoryURL + "/releases/tag/" + item.Tag}
		if !validRelease(r) {
			continue
		}
		f.Releases = append(f.Releases, r)
		if f.Latest.Tag == "" || mustCompare(r.Tag, f.Latest.Tag) > 0 {
			f.Latest = r
		}
	}
	if f.Latest.Tag == "" {
		return f, errors.New("Gitee 尚无可用的正式发行版")
	}
	var attachments []Asset
	if err := c.get(ctx, fmt.Sprintf("/releases/%d/attach_files?per_page=100", f.Latest.ID), &attachments); err != nil {
		return f, err
	}
	var exe, sums Asset
	for _, a := range attachments {
		if a.Name != "timer-app.exe" && a.Name != "SHA256SUMS.txt" {
			continue
		}
		if a.ID <= 0 || a.Size <= 0 {
			return f, errors.New("Gitee 附件信息无效")
		}
		a.URL, a.State = giteeDownloadURL(f.Latest.ID, a.ID), "uploaded"
		if a.Name == "timer-app.exe" {
			if exe.ID != 0 {
				return f, errors.New("Gitee 存在重复 EXE 附件")
			}
			exe = a
		} else {
			if sums.ID != 0 {
				return f, errors.New("Gitee 存在重复校验附件")
			}
			sums = a
		}
	}
	if exe.ID == 0 || exe.Size > MaxDownload || sums.ID == 0 || sums.Size > 64<<10 {
		return f, errors.New("Gitee 最新版缺少完整且可校验的更新附件")
	}
	checksum, err := c.giteeChecksum(ctx, sums)
	if err != nil {
		return f, err
	}
	digest, err := checksumForExecutable(checksum)
	if err != nil {
		return f, err
	}
	exe.Digest = "sha256:" + digest
	f.Latest.Assets = []Asset{exe}
	for i := range f.Releases {
		if f.Releases[i].ID == f.Latest.ID {
			f.Releases[i] = f.Latest
		}
	}
	sort.SliceStable(f.Releases, func(i, j int) bool { return f.Releases[i].Published.After(f.Releases[j].Published) })
	f.Checked = time.Now()
	return f, nil
}

func mustCompare(a, b string) int { n, _ := Compare(a, b); return n }

func (c *Client) giteeChecksum(ctx context.Context, a Asset) ([]byte, error) {
	client := *c.HTTP
	client.CheckRedirect = assetRedirect(Gitee)
	req, err := http.NewRequestWithContext(ctx, "GET", a.URL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Naruto-Substitute-Timer")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("Gitee 校验文件下载失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Gitee 校验文件返回 HTTP %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, (64<<10)+1))
	if err != nil {
		return nil, err
	}
	if len(b) > 64<<10 || int64(len(b)) != a.Size {
		return nil, errors.New("Gitee 校验文件大小不符")
	}
	return b, nil
}

func checksumForExecutable(b []byte) (string, error) {
	digest := ""
	for _, line := range strings.Split(strings.TrimPrefix(string(b), "\ufeff"), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 || strings.TrimPrefix(fields[1], "*") != "timer-app.exe" {
			continue
		}
		if digest != "" || len(fields[0]) != 64 {
			return "", errors.New("EXE 校验值重复或格式错误")
		}
		if _, err := hex.DecodeString(fields[0]); err != nil {
			return "", errors.New("EXE SHA256 格式错误")
		}
		digest = strings.ToLower(fields[0])
	}
	if digest == "" {
		return "", errors.New("校验文件缺少 timer-app.exe 的 SHA256")
	}
	return digest, nil
}

func (r Release) giteeExecutable() (Asset, error) {
	if !validRelease(r) {
		return Asset{}, errors.New("无效 Gitee 发布信息")
	}
	for _, a := range r.Assets {
		if a.Name == "timer-app.exe" && a.State == "uploaded" && a.Size > 0 && a.Size <= MaxDownload && a.URL == giteeDownloadURL(r.ID, a.ID) {
			if _, err := assetHash(a); err == nil {
				return a, nil
			}
		}
	}
	return Asset{}, errors.New("此 Gitee 版本尚未提供可校验的 Windows EXE")
}

func (c *Client) downloadGitee(ctx context.Context, r Release, progress func(int64, int64)) (string, error) {
	if r.UpdateSource() != Gitee {
		return "", errors.New("发布信息与所选 Gitee 更新源不匹配")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	a, err := r.Executable()
	if err != nil {
		return "", err
	}
	hash, _ := assetHash(a)
	root := Directory()
	if root == "" {
		return "", errors.New("无法获取更新目录")
	}
	d := filepath.Join(root, "gitee-"+hash)
	if err := os.MkdirAll(d, 0700); err != nil {
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
	client.Timeout, client.CheckRedirect = 15*time.Minute, assetRedirect(Gitee)
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
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("下载返回 HTTP %d", resp.StatusCode)
	}
	h := sha256.New()
	buf := make([]byte, 128<<10)
	var n int64
	last := time.Time{}
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		nr, readErr := resp.Body.Read(buf)
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
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return "", readErr
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
