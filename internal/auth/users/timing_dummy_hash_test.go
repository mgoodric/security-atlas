package users

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"testing"
)

// #1913: a failed dummy-hash build is logged at ERROR and yields "", which
// VerifyLocalLogin treats as fail-closed. Not parallel: it swaps the
// default slog logger.
func TestBuildTimingDummyHash_FailureLogsAndFailsClosed(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	got := buildTimingDummyHash(func(string) (string, error) { return "", errors.New("rand exhausted") })
	if got != "" {
		t.Fatalf("hash = %q, want empty", got)
	}
	out := buf.String()
	for _, want := range []string{"level=ERROR", "fails closed", "rand exhausted"} {
		if !strings.Contains(out, want) {
			t.Errorf("log %q missing %q", out, want)
		}
	}
}

func TestBuildTimingDummyHash_Success(t *testing.T) {
	t.Parallel()
	if got := buildTimingDummyHash(func(string) (string, error) { return "h", nil }); got != "h" {
		t.Fatalf("hash = %q, want h", got)
	}
}
