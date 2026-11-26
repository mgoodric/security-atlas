package ingest

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

// #1742: only a Service built ForHistoricalImport accepts an observed_at
// past the 24h window, and only in the past; every other Service keeps the
// symmetric check.
func TestObservedAtSkewAllowed(t *testing.T) {
	t.Parallel()
	live := &Service{path: "push"}
	imp := live.ForHistoricalImport(ImportBatch{ID: uuid.New(), Source: "FY25", Importer: "key_a"})
	cases := []struct {
		skew       time.Duration
		live, imp_ bool
	}{
		{0, true, true},
		{-MaxObservedAtSkew, true, true},
		{MaxObservedAtSkew, true, true},
		{-MaxObservedAtSkew - time.Second, false, true},
		{-400 * 24 * time.Hour, false, true},
		{MaxObservedAtSkew + time.Second, false, false},
		{30 * 24 * time.Hour, false, false},
	}
	for _, c := range cases {
		if got := live.observedAtSkewAllowed(c.skew); got != c.live {
			t.Errorf("live skew %s: %v, want %v", c.skew, got, c.live)
		}
		if got := imp.observedAtSkewAllowed(c.skew); got != c.imp_ {
			t.Errorf("import skew %s: %v, want %v", c.skew, got, c.imp_)
		}
	}
}

func TestForHistoricalImport_CopiesAndTags(t *testing.T) {
	t.Parallel()
	live := &Service{path: "push"}
	id := uuid.New()
	imp := live.ForHistoricalImport(ImportBatch{ID: id})
	if imp.path != ImportPath {
		t.Fatalf("path = %q, want %q", imp.path, ImportPath)
	}
	if live.path != "push" || live.importBatch != nil || live.importBatchID().Valid {
		t.Fatal("ForHistoricalImport mutated the receiver")
	}
	if b := imp.importBatchID(); !b.Valid || uuid.UUID(b.Bytes) != id {
		t.Fatalf("importBatchID = %v, want %s", b, id)
	}
	// WithPath on an import copy drops the batch, and with it the lifted
	// past skew limit.
	if back := imp.WithPath("push"); back.importBatch != nil || back.observedAtSkewAllowed(-400*24*time.Hour) {
		t.Fatal("WithPath kept the import batch")
	}
	if (*Service)(nil).importBatchID().Valid {
		t.Fatal("nil Service has a batch id")
	}
}
