package updates

import (
	"context"
	"time"
)

const (
	DefaultMonitorInitialDelay = 10 * time.Second
	DefaultMonitorInterval     = 6 * time.Hour
)

// MonitorResult is delivered after an automatic check. Errors are included so
// callers can record diagnostics, but normal UI code should stay quiet on a
// transient network failure.
type MonitorResult struct {
	Feed   Feed
	Err    error
	Notify bool
}

// Monitor checks the official release feed after startup and then at a bounded
// interval. It never downloads, installs, restarts, or takes focus.
type Monitor struct {
	Checker      Checker
	LocalVersion string
	InitialDelay time.Duration
	Interval     time.Duration
	OnResult     func(MonitorResult)
}

func (m Monitor) Run(ctx context.Context) {
	if ctx == nil {
		ctx = context.Background()
	}
	if m.Checker == nil {
		return
	}
	initial := m.InitialDelay
	if initial <= 0 {
		initial = DefaultMonitorInitialDelay
	}
	interval := m.Interval
	if interval <= 0 {
		interval = DefaultMonitorInterval
	}
	if !waitContext(ctx, initial) {
		return
	}

	lastNotified := ""
	for {
		feed, err := m.Checker.Check(ctx)
		result := MonitorResult{Feed: feed, Err: err}
		if err == nil {
			if cmp, compareErr := Compare(feed.Latest.Tag, m.LocalVersion); compareErr == nil && cmp > 0 && feed.Latest.Tag != lastNotified {
				result.Notify = true
				lastNotified = feed.Latest.Tag
			}
		}
		if m.OnResult != nil && ctx.Err() == nil {
			m.OnResult(result)
		}
		if !waitContext(ctx, interval) {
			return
		}
	}
}

func waitContext(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
