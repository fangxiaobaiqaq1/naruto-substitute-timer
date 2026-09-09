package hudtext

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestBackendFailurePreservesBoundedCause(t *testing.T) {
	e := &Engine{}
	e.backendFailed(time.Unix(1, 0), errors.New("OCR runtime cache checksum mismatch\n"+strings.Repeat("损坏", 400)))
	if e.status != "unavailable" || !strings.Contains(e.lastError, "checksum mismatch") || strings.Contains(e.lastError, "\n") || len([]rune(e.lastError)) > 370 {
		t.Fatalf("unhelpful or unbounded error: %q", e.lastError)
	}
}
