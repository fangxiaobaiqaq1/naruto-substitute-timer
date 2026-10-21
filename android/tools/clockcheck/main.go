//go:build clockcheck

// clockcheck is the Go reference for android/tools/clockcheck/ClockCheck.kt:
// it replays scenarios.in through internal/app.SideClock and prints one state
// line per step. run.sh runs both and compares the outputs byte for byte.
// Excluded from normal builds by the clockcheck tag. From the repo root:
//
//	go run -tags clockcheck ./android/tools/clockcheck android/tools/clockcheck/scenarios.in
package main

import (
	"bufio"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
	"time"

	"narutotimer/internal/app"
)

// base keeps every scenario time non-zero (zero means "now" to SideClock).
const baseNanos = int64(1_700_000_000_000) * int64(time.Millisecond)

func at(ms int64) time.Time { return time.Unix(0, baseNanos+ms*int64(time.Millisecond)) }

func rel(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return strconv.FormatInt(t.UnixNano()-baseNanos, 10)
}

func micros(secs []float64) string {
	parts := make([]string, len(secs))
	for i, s := range secs {
		parts[i] = strconv.FormatInt(int64(math.Round(s*1e6)), 10)
	}
	return "[" + strings.Join(parts, " ") + "]"
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: clockcheck scenarios.in")
		os.Exit(2)
	}
	file, err := os.Open(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer file.Close()
	out := bufio.NewWriter(os.Stdout)
	defer out.Flush()
	var c *app.SideClock
	name := ""
	now := int64(0)
	step := 0
	state := func(op string) {
		step++
		t := at(now)
		eventAt, serial := c.LastEvent()
		fmt.Fprintf(out, "%s #%d %s ready=%d events=%d serial=%d last=%s remaining=%s latest=%s key=%d active=%t cd=%s\n",
			name, step, op, c.LastReady(), c.EventCount(), serial, rel(eventAt),
			micros(c.Remaining(t)), micros(c.LatestRemaining(t)), c.DisplayKey(t), c.Active(), app.FormatCD(c.LatestRemaining(t)))
	}
	scan := bufio.NewScanner(file)
	line := 0
	for scan.Scan() {
		line++
		fields := strings.Fields(scan.Text())
		if len(fields) == 0 || strings.HasPrefix(fields[0], "#") {
			continue
		}
		n := func(i int, fallback int64) int64 {
			if i >= len(fields) {
				return fallback
			}
			v, err := strconv.ParseInt(fields[i], 10, 64)
			if err != nil {
				fmt.Fprintf(os.Stderr, "line %d: %v\n", line, err)
				os.Exit(1)
			}
			return v
		}
		if fields[0] == "scenario" {
			c, name, now, step = &app.SideClock{}, fields[1], 0, 0
			continue
		}
		if c == nil {
			fmt.Fprintf(os.Stderr, "line %d: no scenario\n", line)
			os.Exit(1)
		}
		switch fields[0] {
		case "gap":
			c.SetObservationGap(time.Duration(n(1, 0)) * time.Millisecond)
			state("gap")
		case "obs":
			now = n(2, 0)
			c.Observe(int(n(1, 0)), n(5, 1) != 0, at(now), time.Duration(n(3, 15000))*time.Millisecond, int(n(4, 2)))
			state("obs " + fields[1])
		case "frames":
			ready, from, every, count, need := n(1, 0), n(2, 0), n(3, 0), n(4, 0), n(5, 2)
			for i := int64(0); i < count; i++ {
				now = from + i*every
				c.Observe(int(ready), true, at(now), 15*time.Second, int(need))
				state("obs " + fields[1])
			}
		case "inv":
			now = n(1, 0)
			c.InvalidateObservation(at(now))
			state("inv")
		case "hold":
			now = n(2, 0)
			ok := c.HoldObservation(int(n(1, 0)), at(now))
			state(fmt.Sprintf("hold %s %t", fields[1], ok))
		case "resume":
			now = n(1, 0)
			ok := c.ResumeInheritedObservation(at(now))
			state(fmt.Sprintf("resume %t", ok))
		case "sync":
			c.SyncReady(int(n(1, 0)))
			state("sync " + fields[1])
		case "resync":
			c.ResyncObservation()
			state("resync")
		case "round":
			c.ResetRound()
			state("round")
		case "reset":
			c.Reset()
			state("reset")
		default:
			fmt.Fprintf(os.Stderr, "line %d: unknown op %q\n", line, fields[0])
			os.Exit(1)
		}
	}
	if err := scan.Err(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
