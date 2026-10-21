package frame

import (
	"sync"
	"testing"
	"time"

	"narutotimer/internal/config"
)

// GE-07: the capture worker applied a new selection while the settings window
// and the support bundle read CaptureState. AppliedRevision was written without
// the lock state() reads it under (a data race under -race), and it could be
// reported with the applied config of a different moment. Each revision here
// selects instance == revision, so a consistent snapshot has them equal.
func TestCaptureStateAppliedRevisionMatchesAppliedConfig(t *testing.T) {
	cfg := config.Default()
	cfg.Capture.Provider = ""
	cfg.Capture.PreferredMethods = []string{"unsupported-test-method"} // fails fast, no native client
	provider, closeFn, selectConfig, state := NewSelectableSnapshotterConfigStateful(&analysisProbe{}, cfg)
	defer closeFn()
	var wg sync.WaitGroup
	stop := make(chan struct{})
	wg.Add(2)
	go func() { // settings window: 保存并应用
		defer wg.Done()
		next := cfg.Capture
		for {
			select {
			case <-stop:
				return
			default:
			}
			next.MuMu.Instance++
			if revision := selectConfig(next); revision != uint64(next.MuMu.Instance) {
				t.Errorf("revision %d selected instance %d", revision, next.MuMu.Instance)
				return
			}
			time.Sleep(100 * time.Microsecond)
		}
	}()
	inconsistent := 0
	go func() { // settings/support bundle: CaptureState snapshot
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			s := state()
			if s.AppliedRevision != 0 && s.AppliedRevision != uint64(s.AppliedCapture.MuMu.Instance) {
				inconsistent++
			}
		}
	}()
	for deadline := time.Now().Add(300 * time.Millisecond); time.Now().Before(deadline); {
		_ = provider() // capture loop
	}
	close(stop)
	wg.Wait()
	if inconsistent != 0 {
		t.Fatalf("%d snapshots paired an applied revision with another revision's config", inconsistent)
	}
	if s := state(); s.AppliedRevision == 0 {
		t.Fatal("no selection was applied")
	}
}
