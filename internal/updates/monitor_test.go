package updates

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type checkerFunc func(context.Context) (Feed, error)

func (f checkerFunc) Check(ctx context.Context) (Feed, error) { return f(ctx) }

func monitorRelease(tag string) Release {
	return Release{Tag: tag, URL: RepositoryURL + "/releases/tag/" + tag, Published: time.Date(2026, 9, 11, 1, 0, 0, 0, time.UTC)}
}

func TestMonitorNotifiesOncePerNewerVersionAndStaysQuietOnErrors(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var mu sync.Mutex
	var doneOnce sync.Once
	calls, notices, errorsSeen := 0, 0, 0
	done := make(chan struct{})
	monitor := Monitor{
		LocalVersion: "v0.2.0",
		InitialDelay: time.Millisecond,
		Interval:     time.Millisecond,
		Checker: checkerFunc(func(context.Context) (Feed, error) {
			mu.Lock()
			defer mu.Unlock()
			calls++
			if calls == 3 {
				return Feed{}, errors.New("temporary offline")
			}
			return Feed{Latest: monitorRelease("v0.2.1")}, nil
		}),
		OnResult: func(result MonitorResult) {
			mu.Lock()
			defer mu.Unlock()
			if result.Err != nil {
				errorsSeen++
			}
			if result.Notify {
				notices++
			}
			if calls >= 4 {
				doneOnce.Do(func() { close(done) })
			}
		},
	}
	go monitor.Run(ctx)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("monitor did not check repeatedly")
	}
	cancel()
	mu.Lock()
	defer mu.Unlock()
	if notices != 1 || errorsSeen != 1 {
		t.Fatalf("notices/errors = %d/%d, want 1/1", notices, errorsSeen)
	}
}

func TestMonitorDoesNotNotifyDevelopmentOrEqualVersions(t *testing.T) {
	for _, local := range []string{"development", "v0.2.1"} {
		t.Run(local, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			result := make(chan MonitorResult, 1)
			go Monitor{
				LocalVersion: local,
				InitialDelay: time.Millisecond,
				Interval:     time.Hour,
				Checker:      checkerFunc(func(context.Context) (Feed, error) { return Feed{Latest: monitorRelease("v0.2.1")}, nil }),
				OnResult:     func(r MonitorResult) { result <- r },
			}.Run(ctx)
			select {
			case got := <-result:
				if got.Notify {
					t.Fatal("unexpected update notification")
				}
			case <-time.After(time.Second):
				t.Fatal("monitor did not call checker")
			}
		})
	}
}

func TestFeedServiceCoalescesRequestsAndCloseCancelsWork(t *testing.T) {
	t.Setenv("LOCALAPPDATA", t.TempDir())
	started := make(chan struct{})
	release := make(chan struct{})
	var calls int
	service := newFeedService(checkerFunc(func(ctx context.Context) (Feed, error) {
		calls++
		close(started)
		select {
		case <-release:
			return Feed{Latest: monitorRelease("v0.2.1")}, nil
		case <-ctx.Done():
			return Feed{}, ctx.Err()
		}
	}), time.Second)
	first := make(chan error, 1)
	second := make(chan error, 1)
	go func() { _, err := service.Check(context.Background()); first <- err }()
	<-started
	go func() { _, err := service.Check(context.Background()); second <- err }()
	deadline := time.Now().Add(time.Second)
	for {
		service.mu.Lock()
		joined := service.call != nil && service.call.waiters == 2
		service.mu.Unlock()
		if joined {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("second caller did not join the shared request")
		}
		time.Sleep(time.Millisecond)
	}
	close(release)
	if err := <-first; err != nil {
		t.Fatal(err)
	}
	if err := <-second; err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("underlying calls = %d, want 1", calls)
	}

	blocking := newFeedService(checkerFunc(func(ctx context.Context) (Feed, error) {
		<-ctx.Done()
		return Feed{}, ctx.Err()
	}), time.Second)
	wait := make(chan error, 1)
	go func() { _, err := blocking.Check(context.Background()); wait <- err }()
	blocking.Close()
	select {
	case err := <-wait:
		if err == nil {
			t.Fatal("closed service returned success")
		}
	case <-time.After(time.Second):
		t.Fatal("Close did not cancel in-flight check")
	}
}
