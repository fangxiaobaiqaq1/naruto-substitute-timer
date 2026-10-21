//go:build sessioncheck

// Go twin of SessionCheck.kt. run.sh injects this file into internal/ui with
// `go test -tags sessioncheck -overlay` (nothing is added to the package) and replays
// scenarios.seq through session.captureOnce, printing the same canonical line per report.
package ui

import (
	"bufio"
	"errors"
	"fmt"
	"image"
	"image/color"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	timerapp "narutotimer/internal/app"
	"narutotimer/internal/config"
	"narutotimer/internal/frame"
	"narutotimer/internal/ninja"
)

type scSpec struct {
	count                 int
	step                  int64
	left, right           int
	beads                 string
	leftSlots, rightSlots int
	meta                  *[2]int
	fight, hold           bool
	scene, profile        string
	width, height         int
	method                string
	fail                  *string
	ro                    bool
	ln, rn, lc, rc        string
	ps, pn, on            string
	dup                   bool
	ts                    int64
	shot                  *[2]int64
}

var scCount = regexp.MustCompile(`^x[0-9]+$`)

func scInt(t *testing.T, v string) int {
	n, err := strconv.Atoi(v)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func scInts(t *testing.T, v, sep string) (int, int) {
	a, b, _ := strings.Cut(v, sep)
	return scInt(t, a), scInt(t, b)
}

func scParseFrame(t *testing.T, tokens []string) scSpec {
	s := scSpec{count: 1, step: 50, left: 4, right: 4, leftSlots: 4, rightSlots: 4, fight: true,
		scene: "fight", profile: "duel", width: 1280, height: 576, method: "android-media-projection"}
	for _, tok := range tokens {
		if scCount.MatchString(tok) {
			s.count = scInt(t, tok[1:])
			continue
		}
		if strings.HasPrefix(tok, "+") {
			s.step = int64(scInt(t, tok[1:]))
			continue
		}
		k, v, _ := strings.Cut(tok, "=")
		switch k {
		case "beads":
			if v == "none" || v == "unknown" {
				s.beads = v
			} else {
				s.left, s.right = scInts(t, v, "/")
			}
		case "slots":
			s.leftSlots, s.rightSlots = scInts(t, v, "/")
		case "meta":
			l, r := scInts(t, v, "/")
			s.meta = &[2]int{l, r}
		case "fight":
			s.fight = v == "1"
		case "hold":
			s.hold = v == "1"
		case "scene":
			s.scene = v
		case "profile":
			s.profile = v
		case "size":
			s.width, s.height = scInts(t, v, "x")
		case "method":
			s.method = v
		case "fail":
			msg := v
			s.fail = &msg
		case "ro":
			s.ro = v == "1"
		case "ln":
			s.ln = v
		case "rn":
			s.rn = v
		case "lc":
			s.lc = v
		case "rc":
			s.rc = v
		case "ps":
			s.ps = v
		case "pn":
			s.pn = v
		case "on":
			s.on = v
		case "dup":
			s.dup = v == "1"
		case "ts":
			s.ts = int64(scInt(t, v))
		case "shot":
			a, b := scInts(t, v, "/")
			s.shot = &[2]int64{int64(a), int64(b)}
		default:
			t.Fatalf("unknown frame key %s", k)
		}
	}
	return s
}

func scBeads(s scSpec) []frame.Bead {
	if s.beads == "none" {
		return nil
	}
	unknown := s.beads == "unknown"
	var out []frame.Bead
	for i := 1; i <= s.leftSlots; i++ {
		lit := !unknown && i <= s.left
		out = append(out, frame.Bead{X: 100 + i*10, Y: 50, Label: fmt.Sprintf("L%d", i), Lit: lit, Dark: !lit && !unknown, Unknown: unknown, Conf: 1})
	}
	for i := 1; i <= s.rightSlots; i++ {
		lit := !unknown && i <= s.right
		out = append(out, frame.Bead{X: 1100 - i*10, Y: 50, Label: fmt.Sprintf("R%d", i), Lit: lit, Dark: !lit && !unknown, Unknown: unknown, Conf: 1})
	}
	return out
}

func scB(v bool) int {
	if v {
		return 1
	}
	return 0
}

func scDash(v string) string {
	if v == "" {
		return "-"
	}
	return v
}

func scColor(c color.NRGBA) string {
	switch c {
	case clockWarn:
		return "warn"
	case clockLive:
		return "live"
	}
	return "idle"
}

func scReport(s *session, name string, index int, ms int64, now time.Time) string {
	clock := func(c *timerapp.SideClock) string {
		var parts []string
		for _, v := range c.LatestRemaining(now) {
			parts = append(parts, strconv.FormatFloat(v, 'f', 3, 64))
		}
		return fmt.Sprintf("%d[%s]", c.EventCount(), strings.Join(parts, ","))
	}
	dual, altText, altColor := s.dualClockText(now)
	alt := "-(idle)"
	if dual {
		alt = fmt.Sprintf("%s(%s)", scDash(altText), scColor(altColor))
	}
	// The ninja name refreshOverlay puts in the tag (its suffixes are not compared).
	ninjaName := s.opponentNinja()
	if strings.TrimSpace(s.cfg.UI.NinjaQuery) == "" && (s.hold || s.captureLost || !s.fighting) {
		ninjaName = ""
	}
	recentName, _ := s.recentOpponentDisplay()
	candidate := ""
	switch s.side {
	case "left":
		candidate = s.rightNinjaCandidate
	case "right":
		candidate = s.leftNinjaCandidate
	}
	tag := "-"
	if manualSide(s.side) {
		for _, n := range []string{ninjaName, recentName, candidate} {
			if n != "" {
				tag = ninja.ShortLabel(n)
				break
			}
		}
	}
	return fmt.Sprintf("%s #%d @%dms: fight=%d side=%s L=%s R=%s dual=%d alt=%s tag=%s beads=%s/%s | "+
		"freeze=%d inherit=%d sync=%d%d leave=%d fightHits=%d profile=%s slots=%d/%d",
		name, index, ms, scB(s.fighting), scDash(s.side), clock(&s.left), clock(&s.right), scB(dual), alt, tag,
		s.visibleReady(true), s.visibleReady(false), scB(s.holdFreeze), scB(s.inheritOnReturn),
		scB(s.syncLeft), scB(s.syncRight), s.leaveHits, s.fightHits, scDash(s.layoutProfile), s.leftSlots, s.rightSlots)
}

func TestSessionCheck(t *testing.T) {
	in, err := os.Open(os.Getenv("SESSIONCHECK_SCENARIOS"))
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	// One hour ahead of now: zero-time (failure) frames use time.Now(), before every synthetic frame.
	base := time.Now().Add(time.Hour)
	at := func(ms int64) time.Time { return base.Add(time.Duration(ms) * time.Millisecond) }
	images := map[[2]int]*image.RGBA{}
	img := func(w, h int) *image.RGBA {
		if images[[2]int{w, h}] == nil {
			images[[2]int{w, h}] = image.NewRGBA(image.Rect(0, 0, w, h))
		}
		return images[[2]int{w, h}]
	}
	var out strings.Builder
	var s *session
	var next frame.Frame
	var name, side, query string
	var names []string
	var reports int
	var ms int64
	var seq uint64
	current := func() *session {
		if s == nil {
			s = &session{cfg: config.Default()}
			s.cfg.UI.RememberSide, s.remember = true, true
			s.cfg.UI.PlayerSide = side
			if manualSide(side) {
				s.side = side
			}
			s.cfg.UI.PlayerNames = names
			s.cfg.UI.NinjaQuery = query
			s.provider = func() frame.Frame { return next }
		}
		return s
	}
	scanner := bufio.NewScanner(in)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		tokens := strings.Fields(line)
		switch tokens[0] {
		case "==":
			name = strings.TrimSpace(strings.TrimPrefix(line, "=="))
			reports, ms, s = 0, 0, nil
			side, query, names = "left", "", nil
		case "set":
			for _, tok := range tokens[1:] {
				k, v, _ := strings.Cut(tok, "=")
				switch k {
				case "side":
					side = v
				case "names":
					names = strings.Split(v, "|")
				case "ninja":
					query = v
				default:
					t.Fatalf("unknown setting %s", k)
				}
			}
		case "f":
			spec := scParseFrame(t, tokens[1:])
			for i := 0; i < spec.count; i++ {
				seq++
				if spec.fail != nil {
					// Go capture failures: no image, no CapturedAt, the source's method.
					next = frame.Frame{Hold: true, Err: errors.New(*spec.fail), CaptureStarted: at(ms), CaptureMethod: spec.method, Sequence: seq}
				} else {
					captured := at(ms + spec.ts)
					if spec.shot != nil {
						captured = at(ms + spec.shot[0])
					}
					meta := [2]int{spec.leftSlots, spec.rightSlots}
					if spec.meta != nil {
						meta = *spec.meta
					} else if spec.beads == "none" {
						meta = [2]int{}
					}
					var pic *image.RGBA
					if spec.width > 0 && spec.height > 0 {
						pic = img(spec.width, spec.height)
					}
					next = frame.Frame{Img: pic, Beads: scBeads(spec), Fighting: spec.fight, Engine: "rgb-" + spec.profile + "+gate",
						LayoutProfile: spec.profile, RoundOpening: spec.ro, Hold: spec.hold, Scene: spec.scene,
						LeftNinja: spec.ln, RightNinja: spec.rn, LeftNinjaCandidate: spec.lc, RightNinjaCandidate: spec.rc,
						LeftSlots: meta[0], RightSlots: meta[1], PlayerSide: spec.ps, PlayerName: spec.pn, OppName: spec.on,
						CapturedAt: captured, CaptureStarted: captured, AnalysisStarted: captured, AnalyzedAt: captured,
						CaptureMethod: spec.method, Sequence: seq, Duplicate: spec.dup}
				}
				current().captureOnce()
				ms += spec.step
			}
		case "wait":
			ms += int64(scInt(t, tokens[1]))
		case "report":
			reports++
			out.WriteString(scReport(current(), name, reports, ms, at(ms)) + "\n")
		default:
			t.Fatalf("unknown line: %s", line)
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	// Go capture failures carry their source's method (internal/frame/source.go).
	out.WriteString("failure-frame methods: same as native\n")
	if err := os.WriteFile(os.Getenv("SESSIONCHECK_OUT"), []byte(out.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}
