package updates

import (
	"context"
	"errors"
	"sync"
	"time"
)

// Checker is implemented by Client and permits a single checked feed to be
// shared by the automatic monitor and the manual update page.
type Checker interface {
	Check(context.Context) (Feed, error)
}

// FeedService coalesces simultaneous refreshes. A cancelled caller only stops
// waiting; the bounded shared GitHub request continues for other callers.
type FeedService struct {
	checker Checker
	timeout time.Duration

	ctx    context.Context
	cancel context.CancelFunc

	mu     sync.Mutex
	closed bool
	call   *feedCall
}

type feedCall struct {
	done    chan struct{}
	feed    Feed
	err     error
	waiters int // requesters joined to this shared refresh; useful for safe diagnostics/tests.
}

func NewFeedService(checker Checker) *FeedService {
	return newFeedService(checker, 25*time.Second)
}

func newFeedService(checker Checker, timeout time.Duration) *FeedService {
	ctx, cancel := context.WithCancel(context.Background())
	if timeout <= 0 {
		timeout = 25 * time.Second
	}
	return &FeedService{checker: checker, timeout: timeout, ctx: ctx, cancel: cancel}
}

// Check gets a fresh release feed. Successful responses are cached for the
// update page, but a cache-write failure never changes a verified response.
func (s *FeedService) Check(ctx context.Context) (Feed, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if s == nil || s.checker == nil {
		return Feed{}, errors.New("更新检查服务不可用")
	}

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return Feed{}, errors.New("更新检查服务已关闭")
	}
	call := s.call
	if call == nil {
		call = &feedCall{done: make(chan struct{})}
		s.call = call
		go s.run(call)
	}
	call.waiters++
	done := call.done
	s.mu.Unlock()

	select {
	case <-ctx.Done():
		return Feed{}, ctx.Err()
	case <-done:
		s.mu.Lock()
		feed, err := call.feed, call.err
		s.mu.Unlock()
		return feed, err
	}
}

func (s *FeedService) run(call *feedCall) {
	request, cancel := context.WithTimeout(s.ctx, s.timeout)
	feed, err := s.checker.Check(request)
	cancel()
	if err == nil {
		// The in-memory response remains valid if the cache directory is
		// unavailable (for example, a restrictive portable location).
		_ = SaveFeed(feed)
	}

	s.mu.Lock()
	call.feed, call.err = feed, err
	if s.call == call {
		s.call = nil
	}
	close(call.done)
	s.mu.Unlock()
}

// Close cancels in-flight requests during application shutdown. It is safe to
// call more than once.
func (s *FeedService) Close() {
	if s == nil {
		return
	}
	s.mu.Lock()
	if !s.closed {
		s.closed = true
		s.cancel()
	}
	s.mu.Unlock()
}
