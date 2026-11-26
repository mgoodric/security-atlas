package needsattention

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// #2783: one (tenant, source) pair warns at most once per WarnInterval;
// another source or another tenant still warns, and the pair warns again
// once the interval has passed.
func TestWarnLarge_RateLimited(t *testing.T) {
	t.Parallel()
	tenantA := uuid.MustParse(tenant2134)
	tenantB := uuid.New()
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	var buf bytes.Buffer
	h := New(nil, nil, nil).WithWarnRows(1).
		WithNow(func() time.Time { return now }).
		WithLogger(slog.New(slog.NewJSONHandler(&buf, nil)))
	warn := func(tenant uuid.UUID, source string) bool {
		t.Helper()
		buf.Reset()
		h.warnLarge(context.Background(), tenant, source, []string{BandOwnershipGap}, 2)
		return strings.Count(buf.String(), "\n") == 1
	}

	steps := []struct {
		name    string
		advance time.Duration
		tenant  uuid.UUID
		source  string
		want    bool
	}{
		{"first warning", 0, tenantA, "controls", true},
		{"repeat at once", 0, tenantA, "controls", false},
		{"repeat just inside the interval", WarnInterval - time.Second, tenantA, "controls", false},
		{"another source", 0, tenantA, "risks", true},
		{"another tenant", 0, tenantB, "controls", true},
		{"after the interval", time.Second, tenantA, "controls", true},
		{"repeat after rewarning", time.Minute, tenantA, "controls", false},
	}
	for _, s := range steps {
		now = now.Add(s.advance)
		if got := warn(s.tenant, s.source); got != s.want {
			t.Errorf("%s: warned = %v; want %v (log %q)", s.name, got, s.want, buf.String())
		}
	}
}

// #2783: a read at or under the threshold neither warns nor uses up the
// pair's warning, so the first read past it still warns.
func TestWarnLarge_UnderThresholdDoesNotArm(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	h := New(nil, nil, nil).WithWarnRows(5).
		WithLogger(slog.New(slog.NewJSONHandler(&buf, nil)))
	tenant := uuid.MustParse(tenant2134)
	h.warnLarge(context.Background(), tenant, "calendar", []string{BandOverdue}, 5)
	if buf.Len() != 0 {
		t.Fatalf("logged %q at the threshold; want nothing", buf.String())
	}
	h.warnLarge(context.Background(), tenant, "calendar", []string{BandOverdue}, 6)
	if buf.Len() == 0 {
		t.Fatal("no warning past the threshold")
	}
}
