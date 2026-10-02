package ui

import (
	"sync"
	"testing"
	"time"
)

func TestLatestCoalescerSchedulesOnceAndKeepsNewest(t *testing.T) {
	var c latestCoalescer[int]
	one, two, three := 1, 2, 3
	if !c.Offer(&one) {
		t.Fatal("first offer must schedule a consumer")
	}
	if c.Offer(&two) || c.Offer(&three) {
		t.Fatal("offers while a consumer is pending must not schedule another")
	}
	if got := c.Take(); got == nil || *got != 3 {
		t.Fatalf("Take = %v, want newest value 3", got)
	}
	if got := c.Take(); got != nil {
		t.Fatalf("second Take = %v, want nil", *got)
	}
	if !c.Offer(&one) {
		t.Fatal("offer after Take must schedule again")
	}
}

func TestLatestCoalescerNeverLosesNewestUnderRace(t *testing.T) {
	var c latestCoalescer[int]
	scheduled := make(chan struct{}, 1024)
	var wg sync.WaitGroup
	const n = 2000
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 1; i <= n; i++ {
			v := i
			if c.Offer(&v) {
				scheduled <- struct{}{}
			}
		}
		close(scheduled)
	}()
	last := 0
	for range scheduled {
		if got := c.Take(); got != nil {
			if *got < last {
				t.Fatalf("Take went backwards: %d after %d", *got, last)
			}
			last = *got
		}
	}
	wg.Wait()
	if got := c.Take(); got != nil {
		last = *got
	}
	if last != n {
		t.Fatalf("newest applied value = %d, want %d", last, n)
	}
}

func TestSlowCaptureStreakReportsEveryTenConsecutiveFrames(t *testing.T) {
	var c slowCaptureStreak
	interval := 50 * time.Millisecond
	hits := 0
	for i := 0; i < 9; i++ {
		if c.Observe(80*time.Millisecond, interval) {
			hits++
		}
	}
	if hits != 0 {
		t.Fatal("reported before ten consecutive slow frames")
	}
	if c.Observe(interval, interval) {
		t.Fatal("an on-budget frame must not report")
	}
	for i := 0; i < 20; i++ {
		if c.Observe(80*time.Millisecond, interval) {
			hits++
		}
	}
	if hits != 2 {
		t.Fatalf("hits = %d, want 2 after twenty consecutive slow frames", hits)
	}
}
