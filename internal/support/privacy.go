package support

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

// Support bundles are meant to be posted in the public feedback group, so by
// default they carry no account names and no Windows user name (GE-04):
//   - account names (own: ui.playerNames and player_name; opponents:
//     opponent_name) become stable per-bundle pseudonyms, also inside the
//     frame status text that embeds "我方 vs 对方";
//   - the user profile path becomes %USERPROFILE%, and the user name as a path
//     component (C:\Users\<name>\...) becomes %USERNAME%.
//
// IncludeAccountNames opts back into account names; paths are always redacted.

const (
	ownPseudonym      = "我方账号#%d"
	opponentPseudonym = "对手账号#%d"
)

// userIdentity returns the profile directory and user name to redact.
// Tests replace it.
var userIdentity = func() (profile, name string) {
	profile, _ = os.UserHomeDir()
	name = os.Getenv("USERNAME")
	if name == "" {
		name = os.Getenv("USER")
	}
	return profile, name
}

type nameKind int

const (
	ownAccount nameKind = iota
	opponentAccount
)

// Keys whose string values are account names.
var accountKeys = map[string]nameKind{
	"player_name": ownAccount, "playerName": ownAccount, "PlayerName": ownAccount, "playerNames": ownAccount,
	"opponent_name": opponentAccount, "OpponentName": opponentAccount, "OppName": opponentAccount, "oppName": opponentAccount,
}

type redactor struct {
	includeNames  bool
	names         map[string]string // account name (raw or normalized) → pseudonym
	own, opponent int
	paths         []func(string) string
}

func newRedactor(includeNames bool, ownNames []string) *redactor {
	r := &redactor{includeNames: includeNames, names: map[string]string{}}
	for _, name := range ownNames {
		r.pseudonym(name, ownAccount)
	}
	profile, user := userIdentity()
	r.paths = pathRules(profile, user)
	return r
}

// normalizeAccount mirrors identity.NormalizeName (template file names and
// identity readouts use the normalized form of the configured names).
func normalizeAccount(s string) string {
	var b strings.Builder
	for _, r := range strings.TrimSpace(s) {
		if unicode.IsSpace(r) || r == '/' || r == '\\' || r == ':' {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func (r *redactor) pseudonym(name string, kind nameKind) string {
	if strings.TrimSpace(name) == "" {
		return name
	}
	if p, ok := r.names[name]; ok {
		return p
	}
	if p, ok := r.names[normalizeAccount(name)]; ok {
		r.names[name] = p
		return p
	}
	var p string
	if kind == ownAccount {
		r.own++
		p = fmt.Sprintf(ownPseudonym, r.own)
	} else {
		r.opponent++
		p = fmt.Sprintf(opponentPseudonym, r.opponent)
	}
	r.names[name] = p
	if n := normalizeAccount(name); n != "" {
		r.names[n] = p
	}
	return p
}

// pathRules redacts the current profile directory, the user name as a path
// component and any other C:\Users\<name> profile. Separators may be single
// or JSON-escaped backslashes or slashes.
func pathRules(profile, user string) []func(string) string {
	var rules []func(string) string
	sep := `[\\/]+`
	if profile = strings.TrimRight(profile, `\/`); len(profile) > 1 {
		var parts []string
		for _, part := range regexp.MustCompile(`[\\/]+`).Split(profile, -1) {
			parts = append(parts, regexp.QuoteMeta(part))
		}
		re := regexp.MustCompile(`(?i)` + strings.Join(parts, sep) + `([\\/"]|$)`)
		rules = append(rules, func(s string) string { return re.ReplaceAllString(s, `%USERPROFILE%$1`) })
	}
	// Other profiles: C:\Users\<name> followed by a separator, quote or the end.
	users := regexp.MustCompile(`(?i)([\\/]+Users[\\/]+)([^\\/:*?"<>|\s%]+)([\\/"]|$)`)
	rules = append(rules, func(s string) string {
		return users.ReplaceAllStringFunc(s, func(m string) string {
			sub := users.FindStringSubmatch(m)
			switch strings.ToLower(sub[2]) {
			case "public", "default":
				return m
			}
			return sub[1] + "%USERNAME%" + sub[3]
		})
	})
	if user = strings.TrimSpace(user); user != "" {
		re := regexp.MustCompile(`(?i)(^|[\\/])` + regexp.QuoteMeta(user) + `([\\/"]|$)`)
		rules = append(rules, func(s string) string { return re.ReplaceAllString(s, `${1}%USERNAME%${2}`) })
	}
	return rules
}

func (r *redactor) redactPaths(s string) string {
	for _, rule := range r.paths {
		s = rule(s)
	}
	return s
}

// replaceNames substitutes the given names, longest first, inside s.
func replaceNames(s string, names map[string]string) string {
	keys := make([]string, 0, len(names))
	for name := range names {
		if name != "" {
			keys = append(keys, name)
		}
	}
	sort.Slice(keys, func(i, j int) bool { return len(keys[i]) > len(keys[j]) })
	for _, name := range keys {
		s = strings.ReplaceAll(s, name, names[name])
	}
	return s
}

// value redacts one decoded JSON value. key is the member name it belongs to
// (array elements inherit their array's key); local maps this object's own
// account names to pseudonyms for its "status" text.
func (r *redactor) value(v any, key string, local map[string]string) any {
	switch t := v.(type) {
	case string:
		if !r.includeNames {
			if kind, ok := accountKeys[key]; ok {
				return r.pseudonym(t, kind)
			}
			if key == "status" && len(local) > 0 {
				t = replaceNames(t, local)
			}
		}
		return r.redactPaths(t)
	case []any:
		for i := range t {
			t[i] = r.value(t[i], key, nil)
		}
		return t
	case map[string]any:
		var names map[string]string
		if !r.includeNames {
			names = map[string]string{}
			for k, member := range t {
				if s, ok := member.(string); ok && s != "" {
					if kind, ok := accountKeys[k]; ok {
						names[s] = r.pseudonym(s, kind)
					}
				}
			}
		}
		for k, member := range t {
			t[k] = r.value(member, k, names)
		}
		return t
	default:
		return v
	}
}

// json redacts one JSON document. Key order is normalized by the encoder;
// numbers are preserved exactly.
func (r *redactor) json(data []byte, indent bool) ([]byte, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	if dec.More() {
		return nil, fmt.Errorf("trailing data after JSON document")
	}
	v = r.value(v, "", nil)
	var out bytes.Buffer
	enc := json.NewEncoder(&out)
	enc.SetEscapeHTML(false)
	if indent {
		enc.SetIndent("", "  ")
	}
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// text redacts free text (Markdown, unparsable JSON): paths always, and
// without the opt-in every account name known so far.
func (r *redactor) text(s string) string {
	if !r.includeNames {
		s = replaceNames(s, r.names)
	}
	return r.redactPaths(s)
}

// jsonDocument redacts a JSON file, falling back to text redaction when it
// does not parse (for example a hand-edited config.json).
func (r *redactor) jsonDocument(data []byte) []byte {
	if out, err := r.json(data, true); err == nil {
		return out
	}
	return []byte(r.text(string(data)))
}

// jsonLines redacts JSONL line by line. Without the opt-in, a line that does
// not parse (an interrupted last write) is replaced by a marker: its names are
// unknown, so it cannot be redacted reliably.
func (r *redactor) jsonLines(data []byte) []byte {
	var out bytes.Buffer
	for len(data) > 0 {
		line := data
		if i := bytes.IndexByte(data, '\n'); i >= 0 {
			line, data = data[:i], data[i+1:]
		} else {
			data = nil
		}
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		if redacted, err := r.json(line, false); err == nil {
			out.Write(redacted)
			continue
		}
		if r.includeNames {
			out.WriteString(r.redactPaths(string(line)))
			out.WriteByte('\n')
		} else {
			fmt.Fprintf(&out, "{\"type\":\"redacted_unparsable_line\",\"bytes\":%d}\n", len(line))
		}
	}
	return out.Bytes()
}

// BundleContents mirrors the BundleOptions that decide what a support ZIP
// contains.
type BundleContents struct {
	HasSession          bool
	IncludeImages       bool
	IncludeAccountNames bool
}

// DescribeBundle lists, for the user, exactly what a support ZIP with these
// options contains. The export window shows it before exporting and
// summary.md repeats it inside the ZIP.
func DescribeBundle(c BundleContents) []string {
	names := "账号名（我方与对手）已替换为“我方账号#1”“对手账号#1”这类代号"
	if c.IncludeAccountNames {
		names = "按你的选择包含账号名原文（我方与对手）"
	}
	lines := []string{
		"版本、系统类型、程序与配置文件路径（application.json、environment.json）",
		"当前配置和 config.json：采集方式、MuMu/雷电安装目录与实例、识别与界面设置",
		"采集器状态、MuMu 安装清单（安装目录、实例、进程 PID、SDK DLL）和 SDK 自检结果",
	}
	if c.HasSession {
		lines = append(lines, "最近一次诊断会话的逐帧识别日志与延迟报告：场景、忍者、豆位、计时文字、错误（session.json、capture.jsonl、frames.jsonl、report.json/md、replay/manifest.json）")
	} else {
		lines = append(lines, "没有诊断会话：不含逐帧识别日志")
	}
	if c.IncludeImages {
		lines = append(lines, "原帧 PNG、HUD 证据和诊断回放 JPEG：图像里能看到双方账号名和游戏画面")
	} else {
		lines = append(lines, "不含任何图像（原帧 PNG、HUD 证据、回放 JPEG）")
	}
	return append(lines, names, "路径里的 Windows 用户名已替换为 %USERNAME%，用户目录写作 %USERPROFILE%")
}
