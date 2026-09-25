package updates

import (
	"errors"
	"net/http"
	"net/url"
	"time"
)

// Source identifies the official release service used for update metadata and
// binaries. It is deliberately kept separate from project-navigation links.
type Source string

const (
	GitHub Source = "github"
	Gitee  Source = "gitee"

	GiteeRepository    = "xiaobaiqaq/naruto-substitute-timer"
	GiteeRepositoryURL = "https://gitee.com/" + GiteeRepository
	GiteeAPIBase       = "https://gitee.com/api/v5/repos/" + GiteeRepository
)

func (s Source) Valid() bool { return s == GitHub || s == Gitee }

func (s Source) Label() string {
	if s == Gitee {
		return "Gitee（国内）"
	}
	return "GitHub（海外）"
}

func (s Source) RepositoryURL() string {
	switch s {
	case GitHub:
		return RepositoryURL
	case Gitee:
		return GiteeRepositoryURL
	default:
		return ""
	}
}

func NewClientForSource(source Source) *Client {
	if source != Gitee {
		return NewClient()
	}
	return &Client{HTTP: &http.Client{Timeout: 20 * time.Second}, Base: GiteeAPIBase, Source: Gitee}
}

func trustedURL(raw string) (*url.URL, bool) {
	u, err := url.Parse(raw)
	return u, err == nil && u.Scheme == "https" && u.User == nil && u.Port() == "" && u.Fragment == ""
}

func assetRedirect(source Source) func(*http.Request, []*http.Request) error {
	return func(req *http.Request, via []*http.Request) error {
		u, ok := trustedURL(req.URL.String())
		if !ok || len(via) > 5 {
			return errors.New("更新下载跳转地址不安全")
		}
		switch source {
		case Gitee:
			// Public Gitee release attachments redirect from gitee.com to
			// foruda.gitee.com. Keep this an exact, audited host allowlist.
			if u.Host == "gitee.com" || u.Host == "foruda.gitee.com" {
				return nil
			}
		case GitHub:
			if u.Host == "github.com" || u.Host == "release-assets.githubusercontent.com" || u.Host == "objects.githubusercontent.com" {
				return nil
			}
		}
		return errors.New("更新下载跳转到所选源以外的地址")
	}
}
