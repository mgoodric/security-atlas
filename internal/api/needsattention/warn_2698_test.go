package needsattention

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/google/uuid"
)

// #2698: a source read past the threshold logs one warning naming the
// tenant, the source, its bands and the row count; at or under it, none.
func TestWarnLarge(t *testing.T) {
	t.Parallel()
	tenant := uuid.MustParse(tenant2134)
	cases := map[string]struct {
		warnRows int
		rows     int
		want     bool
	}{
		"default, under":         {0, DefaultWarnRows - 1, false},
		"default, at":            {0, DefaultWarnRows, false},
		"default, over":          {0, DefaultWarnRows + 1, true},
		"configured, over":       {50, 51, true},
		"configured, at":         {50, 50, false},
		"negative keeps default": {-1, DefaultWarnRows, false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var buf bytes.Buffer
			h := New(nil, nil, nil).WithWarnRows(tc.warnRows).
				WithLogger(slog.New(slog.NewJSONHandler(&buf, nil)))
			h.warnLarge(context.Background(), tenant, "controls", []string{BandFailing, BandOwnershipGap}, tc.rows)
			if !tc.want {
				if buf.Len() != 0 {
					t.Fatalf("logged %q; want nothing", buf.String())
				}
				return
			}
			var rec map[string]any
			if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
				t.Fatalf("decode log %q: %v", buf.String(), err)
			}
			threshold := tc.warnRows
			if threshold <= 0 {
				threshold = DefaultWarnRows
			}
			want := map[string]any{
				"level": "WARN", "tenant_id": tenant2134, "source": "controls",
				"band": "failing,ownership_gap", "rows": float64(tc.rows), "threshold": float64(threshold),
			}
			for k, v := range want {
				if rec[k] != v {
					t.Errorf("%s = %v; want %v", k, rec[k], v)
				}
			}
		})
	}
}
