package api

import (
	"context"
	"strings"
	"testing"
)

// #2555: without the OPA engine, calendar.ReadableTypes and the
// needs-attention list admit every item type, so a served binary refuses
// to start unwired. Unit servers that build the handler directly keep
// working without one.
func TestRunHTTP_RefusesWithoutAuthzEngine(t *testing.T) {
	srv := New(Config{})
	srv.AttachDB(lazyPool(t))

	err := srv.RunHTTP(context.Background(), "127.0.0.1:0")
	if err == nil || !strings.Contains(err.Error(), "AttachAuthz") {
		t.Fatalf("RunHTTP err = %v; want the missing authz engine error", err)
	}
}
