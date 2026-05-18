package sink_test

import (
	"context"
	"testing"

	"github.com/mgoodric/security-atlas/internal/audit/sink"
)

// TestEmitDefault_NoOpWhenUnset asserts the call sites can safely call
// EmitDefault before SetDefault has been called (e.g., in tests). The
// fast-discard branch fires.
func TestEmitDefault_NoOpWhenUnset(t *testing.T) {
	// Clear any prior singleton from another test.
	sink.SetDefault(nil)
	t.Cleanup(func() { sink.SetDefault(nil) })

	// Must not panic.
	sink.EmitDefault(context.Background(), makeEntry("test"))
}

// TestSetDefault_ForwardsToInstalledSink asserts the SetDefault →
// EmitDefault → Sink.Emit wiring works end-to-end.
func TestSetDefault_ForwardsToInstalledSink(t *testing.T) {
	t.Setenv(sink.EnvPath, "")
	t.Setenv(sink.EnvHMACKey, "")

	buf := &safeBuffer{}
	s, err := sink.New(sink.Options{
		Writer:  buf,
		HMACKey: []byte("test-hmac-key-must-be-32-bytes-min!!"),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	sink.SetDefault(s)
	t.Cleanup(func() {
		sink.SetDefault(nil)
		_ = s.Shutdown(context.Background())
	})

	sink.EmitDefault(context.Background(), makeEntry("preferences.update"))
	if err := s.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if len(buf.Bytes()) == 0 {
		t.Fatal("EmitDefault did not forward to the installed sink")
	}
}
