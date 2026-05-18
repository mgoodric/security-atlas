// Unit tests for the slice 126 external audit-log sink.
//
// These cover:
//   - AC-6: no-op when ATLAS_AUDIT_SINK_PATH unset
//   - D6: fail-fast when path is set but HMAC key is absent / too short
//   - AC-5: HMAC integrity proof — every line carries a verifiable _hmac
//   - AC-2: package-boundary type — Emit accepts unifiedlog.Entry
//   - D8: Emit signature is void (no error return) — caller can't mishandle
//   - Counters: Stats snapshot under happy + backpressure paths
//
// The 100-record + 10001-record end-to-end cases live in
// integration_test.go (build tag: integration) because they exercise the
// audit_sink_failures Postgres path.

package sink_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/audit/sink"
	"github.com/mgoodric/security-atlas/internal/audit/unifiedlog"
)

// safeBuffer is a goroutine-safe bytes.Buffer for the writer-side capture.
// The sink's writer goroutine writes concurrently with the test reader.
type safeBuffer struct {
	mu  sync.Mutex
	buf []byte
}

func (b *safeBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf = append(b.buf, p...)
	return len(p), nil
}

func (b *safeBuffer) Bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]byte, len(b.buf))
	copy(out, b.buf)
	return out
}

// makeEntry builds a synthetic unifiedlog.Entry for tests. The tenant id
// is deterministic so per-test assertions are stable.
func makeEntry(action string) unifiedlog.Entry {
	return unifiedlog.Entry{
		OccurredAt:  time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC),
		ActorID:     "test-actor-id-1",
		TenantID:    uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		Kind:        unifiedlog.KindMe,
		TargetType:  "user",
		TargetID:    "user-1",
		Action:      action,
		RowID:       uuid.MustParse("22222222-2222-2222-2222-222222222222"),
		PayloadJSON: json.RawMessage(`{"before":{},"after":{"k":"v"}}`),
	}
}

// TestNew_NoOpWhenPathUnset asserts AC-6: with no ATLAS_AUDIT_SINK_PATH
// and no opts.Writer override, the sink is disabled and Emit calls are
// instantly discarded.
func TestNew_NoOpWhenPathUnset(t *testing.T) {
	t.Setenv(sink.EnvPath, "")
	t.Setenv(sink.EnvHMACKey, "")

	s, err := sink.New(sink.Options{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if s.Enabled() {
		t.Fatal("Enabled() = true; want false when ATLAS_AUDIT_SINK_PATH unset")
	}
	// Emit on a disabled sink is a fast discard — should not panic.
	s.Emit(context.Background(), makeEntry("test"))
	// Shutdown on a disabled sink is a no-op.
	if err := s.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
}

// TestNew_RejectsMissingHMACKeyWhenPathSet asserts D6: when the operator
// configures the path but forgets the HMAC key, the binary fail-fasts at
// boot rather than ship a silent integrity gap.
func TestNew_RejectsMissingHMACKeyWhenPathSet(t *testing.T) {
	t.Setenv(sink.EnvPath, "/tmp/should-not-be-opened.jsonl")
	t.Setenv(sink.EnvHMACKey, "")

	// We bypass the env-var path by supplying an in-memory writer (so
	// the test doesn't actually try to open the file) but leave the
	// HMACKey absent so the key-validation branch fires.
	buf := &safeBuffer{}
	_, err := sink.New(sink.Options{Writer: buf})
	if err == nil {
		t.Fatal("New: expected error for missing HMAC key when sink is active")
	}
	if !strings.Contains(err.Error(), sink.EnvHMACKey) {
		t.Errorf("error must mention %s; got: %v", sink.EnvHMACKey, err)
	}
}

// TestNew_RejectsShortHMACKey asserts D6: keys shorter than the SHA-256
// block size (32 bytes) are rejected at boot.
func TestNew_RejectsShortHMACKey(t *testing.T) {
	t.Setenv(sink.EnvPath, "")
	t.Setenv(sink.EnvHMACKey, "")

	buf := &safeBuffer{}
	_, err := sink.New(sink.Options{
		Writer:  buf,
		HMACKey: []byte("short"),
	})
	if err == nil {
		t.Fatal("New: expected error for short HMAC key")
	}
	if !strings.Contains(err.Error(), "at least") {
		t.Errorf("error must mention minimum length; got: %v", err)
	}
}

// TestEmit_WritesValidHMACSignedJSONL asserts AC-5: every successfully-
// emitted record carries a _hmac field that re-verifies against the same
// key + the canonical entry payload.
func TestEmit_WritesValidHMACSignedJSONL(t *testing.T) {
	t.Setenv(sink.EnvPath, "")
	t.Setenv(sink.EnvHMACKey, "")

	key := []byte("test-hmac-key-must-be-32-bytes-min!!")
	buf := &safeBuffer{}
	s, err := sink.New(sink.Options{Writer: buf, HMACKey: key})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if !s.Enabled() {
		t.Fatal("Enabled() = false; want true with writer override")
	}

	entry := makeEntry("preferences.update")
	s.Emit(context.Background(), entry)

	// Shutdown drains the channel + flushes.
	if err := s.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	out := buf.Bytes()
	if len(out) == 0 {
		t.Fatal("writer received no bytes")
	}
	if out[len(out)-1] != '\n' {
		t.Fatal("output must end with newline (JSONL framing)")
	}
	line := strings.TrimRight(string(out), "\n")

	// Parse: the line must be valid JSON containing all the canonical
	// fields + _hmac.
	var got map[string]json.RawMessage
	if err := json.Unmarshal([]byte(line), &got); err != nil {
		t.Fatalf("emitted line is not valid JSON: %v; line=%q", err, line)
	}
	mac, ok := got["_hmac"]
	if !ok {
		t.Fatal("emitted line missing _hmac field")
	}

	// Strip _hmac, re-marshal the canonical entry, recompute HMAC, compare.
	canonical, err := json.Marshal(entry)
	if err != nil {
		t.Fatalf("re-canonicalize: %v", err)
	}
	hm := hmac.New(sha256.New, key)
	hm.Write(canonical)
	want := hex.EncodeToString(hm.Sum(nil))

	var gotTag string
	if err := json.Unmarshal(mac, &gotTag); err != nil {
		t.Fatalf("_hmac is not a string: %v", err)
	}
	if gotTag != want {
		t.Errorf("HMAC mismatch:\n  got:  %s\n  want: %s", gotTag, want)
	}

	// Counters: 1 emitted, 0 dropped, 0 errors.
	emitted, dropped, writeErrors, _ := s.StatsSnapshot()
	if emitted != 1 {
		t.Errorf("Emitted = %d; want 1", emitted)
	}
	if dropped != 0 {
		t.Errorf("Dropped = %d; want 0", dropped)
	}
	if writeErrors != 0 {
		t.Errorf("WriteErrors = %d; want 0", writeErrors)
	}
}

// TestEmit_DoesNotReturnError pins D8: the Emit signature is void. A
// caller cannot accidentally `if err := sink.Emit(...); err != nil` —
// the type system forbids it. Test by assigning the method value to an
// explicitly-typed function variable (the type annotation is the pin —
// inferring the type from the right-hand side would defeat the test).
func TestEmit_DoesNotReturnError(t *testing.T) {
	t.Setenv(sink.EnvPath, "")
	t.Setenv(sink.EnvHMACKey, "")
	s, err := sink.New(sink.Options{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// The explicit type annotation is INTENTIONAL — D8 pin. Removing it
	// would defeat the purpose of the test. Suppress staticcheck ST1023
	// via a //nolint directive on the next line.
	//nolint:staticcheck // ST1023: explicit type is the test's load-bearing assertion (D8)
	var emit func(context.Context, unifiedlog.Entry) = s.Emit
	emit(context.Background(), makeEntry("test"))
}

// TestEmit_HighThroughputManyEntries asserts that the channel + writer
// goroutine can handle a burst of 1000 entries without dropping any
// (well under the 10000 default buffer size). All 1000 must end up in
// the writer.
func TestEmit_HighThroughputManyEntries(t *testing.T) {
	t.Setenv(sink.EnvPath, "")
	t.Setenv(sink.EnvHMACKey, "")

	key := []byte("test-hmac-key-must-be-32-bytes-min!!")
	buf := &safeBuffer{}
	s, err := sink.New(sink.Options{Writer: buf, HMACKey: key})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	const n = 1000
	for i := 0; i < n; i++ {
		s.Emit(context.Background(), makeEntry("p"))
	}
	if err := s.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	// Each line ends with '\n', so the line count == newline count.
	out := buf.Bytes()
	lines := 0
	for _, b := range out {
		if b == '\n' {
			lines++
		}
	}
	if lines != n {
		t.Errorf("lines emitted = %d; want %d", lines, n)
	}
	emitted, dropped, _, _ := s.StatsSnapshot()
	if emitted != n {
		t.Errorf("Emitted = %d; want %d", emitted, n)
	}
	if dropped != 0 {
		t.Errorf("Dropped = %d; want 0", dropped)
	}
}

// TestEmit_BufferOverflowCountsDropped asserts the backpressure-counter
// surface: when the channel is full, the dropped counter increments and
// the Emit call returns instantly. The audit_sink_failures-table write
// path is covered by the integration test (it needs Postgres).
func TestEmit_BufferOverflowCountsDropped(t *testing.T) {
	t.Setenv(sink.EnvPath, "")
	t.Setenv(sink.EnvHMACKey, "")

	key := []byte("test-hmac-key-must-be-32-bytes-min!!")
	// Use a small buffer + a slow writer to force overflow within a
	// bounded test run. The slow writer blocks the goroutine reading
	// the channel, so successive Emits backfill + then overflow.
	slow := &slowWriter{delay: 50 * time.Millisecond}
	s, err := sink.New(sink.Options{
		Writer:     slow,
		HMACKey:    key,
		BufferSize: 4,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = s.Shutdown(context.Background()) })

	// Pump 32 entries as fast as possible. The first ~5 (1 being read +
	// 4 buffered) get accepted; the remaining 27 overflow.
	for i := 0; i < 32; i++ {
		s.Emit(context.Background(), makeEntry("p"))
	}

	_, dropped, _, _ := s.StatsSnapshot()
	if dropped == 0 {
		t.Error("expected at least one dropped entry; got 0")
	}
}

// slowWriter is a delaying io.Writer for the backpressure test.
type slowWriter struct {
	delay time.Duration
}

func (w *slowWriter) Write(p []byte) (int, error) {
	time.Sleep(w.delay)
	return len(p), nil
}

// TestEnvBufferSize asserts the ATLAS_AUDIT_SINK_BUFFER_SIZE override
// path; defaults to 10000 when unset/garbage.
func TestEnvBufferSize(t *testing.T) {
	cases := []struct {
		name   string
		envVal string
		opts   sink.Options
		// We can't directly read bufferSize (unexported), but we can
		// rely on the fact that Enabled-sinks are constructable.
		wantErr bool
	}{
		{"empty-defaults-to-10000", "", sink.Options{}, false},
		{"valid-override-100", "100", sink.Options{}, false},
		{"garbage-falls-back", "not-a-number", sink.Options{}, false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(sink.EnvPath, "")
			t.Setenv(sink.EnvHMACKey, "")
			t.Setenv(sink.EnvBufferSize, tc.envVal)
			buf := &safeBuffer{}
			tc.opts.Writer = buf
			tc.opts.HMACKey = []byte("test-hmac-key-must-be-32-bytes-min!!")
			s, err := sink.New(tc.opts)
			if tc.wantErr && err == nil {
				t.Fatal("expected error; got nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if s != nil {
				_ = s.Shutdown(context.Background())
			}
		})
	}
}

// TestShutdown_Idempotent asserts the Shutdown can be called multiple
// times without panic / error. Important for graceful-shutdown paths
// that may call it from multiple defers (e.g., signal handler + main).
func TestShutdown_Idempotent(t *testing.T) {
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
	if err := s.Shutdown(context.Background()); err != nil {
		t.Fatalf("first Shutdown: %v", err)
	}
	if err := s.Shutdown(context.Background()); err != nil {
		t.Fatalf("second Shutdown: %v", err)
	}
}

// TestCanonicalize_DeterministicAcrossRuns asserts the JSON
// canonicalization is stable run-to-run. Two emissions of the same
// entry MUST produce byte-identical canonical bytes (and thus the same
// _hmac). This is the property the receiver-side verifier relies on.
func TestCanonicalize_DeterministicAcrossRuns(t *testing.T) {
	t.Setenv(sink.EnvPath, "")
	t.Setenv(sink.EnvHMACKey, "")

	key := []byte("test-hmac-key-must-be-32-bytes-min!!")
	buf1, buf2 := &safeBuffer{}, &safeBuffer{}
	s1, err := sink.New(sink.Options{Writer: buf1, HMACKey: key})
	if err != nil {
		t.Fatalf("New 1: %v", err)
	}
	s2, err := sink.New(sink.Options{Writer: buf2, HMACKey: key})
	if err != nil {
		t.Fatalf("New 2: %v", err)
	}
	entry := makeEntry("preferences.update")
	s1.Emit(context.Background(), entry)
	s2.Emit(context.Background(), entry)
	_ = s1.Shutdown(context.Background())
	_ = s2.Shutdown(context.Background())

	if string(buf1.Bytes()) != string(buf2.Bytes()) {
		t.Errorf("non-deterministic canonical bytes:\n  s1: %s\n  s2: %s",
			buf1.Bytes(), buf2.Bytes())
	}
}

// TestCanonicalize_RejectsOversizedEntry asserts the MaxCanonicalSize cap:
// an entry whose marshaled canonical bytes exceed the 1 MiB cap is rejected
// (no line written) and the WriteErrors counter increments. Closes the
// allocation-overflow path that CodeQL flagged on the original PR. The
// fallback-row write is exercised by the integration test; here we just
// assert the producer-side bound.
func TestCanonicalize_RejectsOversizedEntry(t *testing.T) {
	t.Setenv(sink.EnvPath, "")
	t.Setenv(sink.EnvHMACKey, "")

	key := []byte("test-hmac-key-must-be-32-bytes-min!!")
	buf := &safeBuffer{}
	s, err := sink.New(sink.Options{Writer: buf, HMACKey: key})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Build a payload_json larger than the cap. The raw payload alone is
	// already past MaxCanonicalSize, so the marshaled entry comfortably
	// exceeds the bound.
	big := make([]byte, sink.MaxCanonicalSize+1024)
	for i := range big {
		big[i] = 'a'
	}
	huge := json.RawMessage(`{"blob":"` + string(big) + `"}`)

	entry := makeEntry("preferences.update")
	entry.PayloadJSON = huge
	s.Emit(context.Background(), entry)
	if err := s.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	emitted, _, writeErrors, _ := s.StatsSnapshot()
	if emitted != 0 {
		t.Errorf("expected 0 emitted, got %d", emitted)
	}
	if writeErrors != 1 {
		t.Errorf("expected 1 WriteErrors increment from oversized entry, got %d", writeErrors)
	}
	if len(buf.Bytes()) != 0 {
		t.Errorf("expected no bytes written to sink, got %d bytes", len(buf.Bytes()))
	}
}
