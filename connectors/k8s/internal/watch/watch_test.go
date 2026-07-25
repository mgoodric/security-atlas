package watch

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// fakeStream replays a scripted sequence of (Event, error) results. After the
// script is exhausted it returns the terminal error (default ErrStreamClosed).
type fakeStream struct {
	mu       sync.Mutex
	frames   []frameResult
	idx      int
	terminal error
	closed   bool
}

type frameResult struct {
	ev  Event
	err error
}

func (s *fakeStream) Recv() (Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.idx >= len(s.frames) {
		if s.terminal != nil {
			return Event{}, s.terminal
		}
		return Event{}, ErrStreamClosed
	}
	fr := s.frames[s.idx]
	s.idx++
	return fr.ev, fr.err
}

func (s *fakeStream) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	return nil
}

// fakeSource scripts a series of List results and a series of Watch streams. It
// records how many times List/Watch were called and the RVs Watch was opened
// from. After cancelFn is wired, the test cancels ctx once the watch has done
// enough work, ending Run.
type fakeSource struct {
	mu          sync.Mutex
	listResults []listResult
	listIdx     int
	streams     []*fakeStream
	streamIdx   int
	watchRVs    []string
	listCalls   int
	watchCalls  int
	onWatch     func(callIdx int) // hook to cancel ctx after a given watch opens
}

type listResult struct {
	objects []any
	rv      string
	err     error
}

func (s *fakeSource) List(_ context.Context) ([]any, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.listCalls++
	if s.listIdx >= len(s.listResults) {
		// Default: empty list with a stable RV.
		return nil, "rv-default", nil
	}
	lr := s.listResults[s.listIdx]
	s.listIdx++
	return lr.objects, lr.rv, lr.err
}

func (s *fakeSource) Watch(_ context.Context, rv string) (Stream, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.watchCalls++
	s.watchRVs = append(s.watchRVs, rv)
	callIdx := s.watchCalls - 1
	if s.onWatch != nil {
		s.onWatch(callIdx)
	}
	if s.streamIdx >= len(s.streams) {
		// Default: an immediately-closing stream.
		return &fakeStream{}, nil
	}
	st := s.streams[s.streamIdx]
	s.streamIdx++
	return st, nil
}

// recordingEmitter captures emitted (eventType, object, key) tuples and returns
// a deterministic key derived from the object pointer-ish identity (here, the
// object value formatted), so the coalescer can be exercised.
type recordingEmitter struct {
	mu       sync.Mutex
	calls    []emitCall
	keyFor   func(object any) string
	emitErr  error
	errAfter int // return emitErr starting at this call index (-1 = never)
}

type emitCall struct {
	et  EventType
	obj any
}

func (e *recordingEmitter) emit(_ context.Context, et EventType, obj any) (string, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	n := len(e.calls)
	e.calls = append(e.calls, emitCall{et: et, obj: obj})
	if e.emitErr != nil && e.errAfter >= 0 && n >= e.errAfter {
		return "", e.emitErr
	}
	if e.keyFor != nil {
		return e.keyFor(obj), nil
	}
	return "key", nil
}

func (e *recordingEmitter) count() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.calls)
}

func fixedNow() func() time.Time {
	t := time.Date(2026, 6, 9, 10, 30, 0, 0, time.UTC)
	return func() time.Time { return t }
}

func noLog(string, ...any) {}

// TestRun_NilGuards covers the constructor guards.
func TestRun_NilGuards(t *testing.T) {
	t.Parallel()
	if err := Run(context.Background(), nil, func(context.Context, EventType, any) (string, error) { return "", nil }, nil, Options{}); err == nil {
		t.Error("nil source should error")
	}
	if err := Run(context.Background(), &fakeSource{}, nil, nil, Options{}); err == nil {
		t.Error("nil emitter should error")
	}
}

// TestRun_ListBootstrapEmitsAndWatches proves the initial LIST emits a record per
// object and the watch opens from the list RV.
func TestRun_ListBootstrapEmitsAndWatches(t *testing.T) {
	t.Parallel()
	em := &recordingEmitter{}
	src := &fakeSource{
		listResults: []listResult{{objects: []any{"a", "b"}, rv: "rv-100"}},
	}
	ctx, cancel := context.WithCancel(context.Background())
	// Cancel as soon as the first watch opens (after the bootstrap LIST emitted).
	src.onWatch = func(int) { cancel() }

	err := Run(ctx, src, em.emit, noLog, Options{ResourceName: "rbac", now: fixedNow(), ReListBackoff: time.Millisecond})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run = %v; want context.Canceled", err)
	}
	if em.count() != 2 {
		t.Errorf("bootstrap emitted %d records; want 2", em.count())
	}
	if len(src.watchRVs) == 0 || src.watchRVs[0] != "rv-100" {
		t.Errorf("first watch RV = %v; want rv-100", src.watchRVs)
	}
}

// TestRun_EmitsOnChangeEvents proves ADDED/MODIFIED/DELETED each emit a push and
// advance the resume RV.
func TestRun_EmitsOnChangeEvents(t *testing.T) {
	t.Parallel()
	em := &recordingEmitter{keyFor: func(o any) string { return o.(string) }}
	stream := &fakeStream{frames: []frameResult{
		{ev: Event{Type: EventAdded, ResourceVersion: "rv-2", Object: "obj-add"}},
		{ev: Event{Type: EventModified, ResourceVersion: "rv-3", Object: "obj-mod"}},
		{ev: Event{Type: EventDeleted, ResourceVersion: "rv-4", Object: "obj-del"}},
	}}
	src := &fakeSource{
		listResults: []listResult{{rv: "rv-1"}}, // empty bootstrap
		streams:     []*fakeStream{stream},
	}
	ctx, cancel := context.WithCancel(context.Background())
	// Cancel when the SECOND watch opens (after the first stream drained + closed).
	src.onWatch = func(idx int) {
		if idx >= 1 {
			cancel()
		}
	}
	err := Run(ctx, src, em.emit, noLog, Options{now: fixedNow(), ReListBackoff: time.Millisecond})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run = %v; want context.Canceled", err)
	}
	// 3 change events emitted (bootstrap was empty).
	if em.count() != 3 {
		t.Errorf("emitted %d; want 3 change events", em.count())
	}
	// The re-watch after the first stream closed must resume from the last RV seen.
	if len(src.watchRVs) < 2 || src.watchRVs[1] != "rv-4" {
		t.Errorf("re-watch RV = %v; want second watch from rv-4", src.watchRVs)
	}
}

// TestRun_BookmarkAdvancesRVWithoutReList proves a Bookmark advances the resume
// RV cheaply (the re-watch uses the bookmark RV, no extra LIST).
func TestRun_BookmarkAdvancesRVWithoutReList(t *testing.T) {
	t.Parallel()
	em := &recordingEmitter{}
	stream := &fakeStream{frames: []frameResult{
		{ev: Event{Type: EventBookmark, ResourceVersion: "rv-900"}},
	}}
	src := &fakeSource{
		listResults: []listResult{{rv: "rv-1"}},
		streams:     []*fakeStream{stream},
	}
	ctx, cancel := context.WithCancel(context.Background())
	src.onWatch = func(idx int) {
		if idx >= 1 {
			cancel()
		}
	}
	err := Run(ctx, src, em.emit, noLog, Options{now: fixedNow(), ReListBackoff: time.Millisecond})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run = %v; want context.Canceled", err)
	}
	if em.count() != 0 {
		t.Errorf("bookmark must not emit; emitted %d", em.count())
	}
	// Exactly ONE list (bootstrap) — the bookmark avoided a re-LIST.
	if src.listCalls != 1 {
		t.Errorf("listCalls = %d; want 1 (bookmark avoids re-list)", src.listCalls)
	}
	if len(src.watchRVs) < 2 || src.watchRVs[1] != "rv-900" {
		t.Errorf("re-watch RV = %v; want rv-900 (bookmark-advanced)", src.watchRVs)
	}
}

// TestRun_410GoneReListsAndResumes proves a 410 Gone ERROR frame triggers a
// re-LIST (fresh RV) and the watch resumes from it.
func TestRun_410GoneReListsAndResumes(t *testing.T) {
	t.Parallel()
	em := &recordingEmitter{keyFor: func(o any) string { return o.(string) }}
	// First stream ends on a 410 Gone. Second stream is empty (then we cancel).
	expiredStream := &fakeStream{frames: []frameResult{
		{ev: Event{Type: EventError, ResourceExpired: true}},
	}}
	src := &fakeSource{
		listResults: []listResult{
			{objects: []any{"boot-1"}, rv: "rv-1"},       // bootstrap
			{objects: []any{"relisted-1"}, rv: "rv-500"}, // re-list after 410
		},
		streams: []*fakeStream{expiredStream},
	}
	ctx, cancel := context.WithCancel(context.Background())
	src.onWatch = func(idx int) {
		if idx >= 1 {
			cancel() // cancel once the post-410 re-watch opens
		}
	}
	err := Run(ctx, src, em.emit, noLog, Options{now: fixedNow(), ReListBackoff: time.Millisecond})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run = %v; want context.Canceled", err)
	}
	if src.listCalls != 2 {
		t.Errorf("listCalls = %d; want 2 (bootstrap + re-list on 410)", src.listCalls)
	}
	// The re-list emitted its object; bootstrap emitted its object → 2 emits.
	if em.count() != 2 {
		t.Errorf("emitted %d; want 2 (bootstrap + re-list)", em.count())
	}
	// The post-410 re-watch resumes from the fresh re-list RV.
	if len(src.watchRVs) < 2 || src.watchRVs[1] != "rv-500" {
		t.Errorf("post-410 watch RV = %v; want rv-500", src.watchRVs)
	}
}

// TestRun_BurstCoalesce proves a burst of identical-key events in one hour is
// shed by the in-process coalescer (the emitter is still CALLED per event — the
// platform hour-window key is the durable collapse — but the loop logs the
// coalesce, exercising the seen() path). This asserts the coalescer recognizes
// the duplicate within the hour.
func TestRun_BurstCoalesce(t *testing.T) {
	t.Parallel()
	coalesced := 0
	logf := func(format string, _ ...any) {
		if containsCoalesce(format) {
			coalesced++
		}
	}
	em := &recordingEmitter{keyFor: func(any) string { return "same-key" }}
	// 5 modifications to the same binding within the hour → same key each time.
	frames := make([]frameResult, 0, 5)
	for i := 0; i < 5; i++ {
		frames = append(frames, frameResult{ev: Event{Type: EventModified, ResourceVersion: "rv-x", Object: "binding-a"}})
	}
	src := &fakeSource{
		listResults: []listResult{{rv: "rv-1"}},
		streams:     []*fakeStream{{frames: frames}},
	}
	ctx, cancel := context.WithCancel(context.Background())
	src.onWatch = func(idx int) {
		if idx >= 1 {
			cancel()
		}
	}
	err := Run(ctx, src, em.emit, logf, Options{now: fixedNow(), ReListBackoff: time.Millisecond})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run = %v; want context.Canceled", err)
	}
	// 5 events emitted; 4 of them are duplicate keys this hour → 4 coalesce logs.
	if coalesced != 4 {
		t.Errorf("coalesced duplicates = %d; want 4 (first key fresh, next 4 dup)", coalesced)
	}
}

// TestRun_GracefulShutdownDuringStream proves cancelling ctx mid-stream ends Run.
func TestRun_GracefulShutdownDuringStream(t *testing.T) {
	t.Parallel()
	em := &recordingEmitter{}
	ctx, cancel := context.WithCancel(context.Background())
	// A stream whose Recv blocks until ctx cancellation would need a channel; here
	// we cancel before the first watch and rely on the ctx.Err() check.
	cancel()
	src := &fakeSource{listResults: []listResult{{rv: "rv-1"}}}
	err := Run(ctx, src, em.emit, noLog, Options{now: fixedNow()})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run = %v; want context.Canceled", err)
	}
}

// TestRun_ListErrorBacksOffThenRecovers proves a live-ctx List error is treated
// as transient: the loop backs off and retries the LIST rather than tearing down.
func TestRun_ListErrorBacksOffThenRecovers(t *testing.T) {
	t.Parallel()
	em := &recordingEmitter{}
	src := &fakeSource{
		listResults: []listResult{
			{err: errors.New("api server 500")}, // first LIST fails
			{rv: "rv-2"},                        // retry succeeds
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	src.onWatch = func(int) { cancel() } // cancel once the watch finally opens
	err := Run(ctx, src, em.emit, noLog, Options{now: fixedNow(), ReListBackoff: time.Millisecond})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run = %v; want context.Canceled", err)
	}
	if src.listCalls != 2 {
		t.Errorf("listCalls = %d; want 2 (fail + retry)", src.listCalls)
	}
}

// TestRun_WatchOpenErrorBacksOff proves a Watch open error backs off and retries.
type watchErrSource struct {
	fakeSource
	failOpens int
	opens     int
}

func (s *watchErrSource) Watch(ctx context.Context, rv string) (Stream, error) {
	s.opens++
	if s.opens <= s.failOpens {
		s.watchRVs = append(s.watchRVs, rv)
		return nil, errors.New("dial failed")
	}
	return s.fakeSource.Watch(ctx, rv)
}

func TestRun_WatchOpenErrorBacksOff(t *testing.T) {
	t.Parallel()
	em := &recordingEmitter{}
	src := &watchErrSource{failOpens: 1}
	src.listResults = []listResult{{rv: "rv-1"}}
	ctx, cancel := context.WithCancel(context.Background())
	src.onWatch = func(int) { cancel() }
	err := Run(ctx, src, em.emit, noLog, Options{now: fixedNow(), ReListBackoff: time.Millisecond})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run = %v; want context.Canceled", err)
	}
	if src.opens < 2 {
		t.Errorf("watch opens = %d; want >=2 (fail + retry)", src.opens)
	}
}

// TestRun_EmitErrorEndsStreamToRetry proves a push error ends the stream so the
// loop re-watches (does NOT drop the consumer).
func TestRun_EmitErrorEndsStreamToRetry(t *testing.T) {
	t.Parallel()
	em := &recordingEmitter{emitErr: errors.New("push 503"), errAfter: 0, keyFor: func(any) string { return "k" }}
	stream := &fakeStream{frames: []frameResult{
		{ev: Event{Type: EventAdded, ResourceVersion: "rv-2", Object: "x"}},
	}}
	src := &fakeSource{listResults: []listResult{{rv: "rv-1"}}, streams: []*fakeStream{stream}}
	ctx, cancel := context.WithCancel(context.Background())
	src.onWatch = func(idx int) {
		if idx >= 1 {
			cancel() // second watch = the retry after the emit error
		}
	}
	err := Run(ctx, src, em.emit, noLog, Options{now: fixedNow(), ReListBackoff: time.Millisecond})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run = %v; want context.Canceled", err)
	}
	if src.watchCalls < 2 {
		t.Errorf("watchCalls = %d; want >=2 (re-watch after emit error)", src.watchCalls)
	}
}

// TestRun_NonExpiryErrorFrameReWatches proves a non-410 ERROR frame ends the
// stream and the loop re-watches from the last RV (no re-LIST).
func TestRun_NonExpiryErrorFrameReWatches(t *testing.T) {
	t.Parallel()
	em := &recordingEmitter{}
	stream := &fakeStream{frames: []frameResult{
		{ev: Event{Type: EventBookmark, ResourceVersion: "rv-50"}},
		{ev: Event{Type: EventError, ResourceExpired: false}},
	}}
	src := &fakeSource{listResults: []listResult{{rv: "rv-1"}}, streams: []*fakeStream{stream}}
	ctx, cancel := context.WithCancel(context.Background())
	src.onWatch = func(idx int) {
		if idx >= 1 {
			cancel()
		}
	}
	err := Run(ctx, src, em.emit, noLog, Options{now: fixedNow(), ReListBackoff: time.Millisecond})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run = %v; want context.Canceled", err)
	}
	if src.listCalls != 1 {
		t.Errorf("listCalls = %d; want 1 (non-410 error re-watches, no re-list)", src.listCalls)
	}
	if len(src.watchRVs) < 2 || src.watchRVs[1] != "rv-50" {
		t.Errorf("re-watch RV = %v; want rv-50", src.watchRVs)
	}
}

// TestRun_UnknownEventTypeIgnored proves an unknown event type is logged+ignored
// (no emit, no crash).
func TestRun_UnknownEventTypeIgnored(t *testing.T) {
	t.Parallel()
	em := &recordingEmitter{}
	stream := &fakeStream{frames: []frameResult{
		{ev: Event{Type: EventType("WEIRD"), ResourceVersion: "rv-2"}},
	}}
	src := &fakeSource{listResults: []listResult{{rv: "rv-1"}}, streams: []*fakeStream{stream}}
	ctx, cancel := context.WithCancel(context.Background())
	src.onWatch = func(idx int) {
		if idx >= 1 {
			cancel()
		}
	}
	err := Run(ctx, src, em.emit, noLog, Options{now: fixedNow(), ReListBackoff: time.Millisecond})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run = %v; want context.Canceled", err)
	}
	if em.count() != 0 {
		t.Errorf("unknown event must not emit; emitted %d", em.count())
	}
}

// TestRun_RecvErrorReWatches proves a transient Recv error (not ErrStreamClosed)
// ends the stream and the loop re-watches.
func TestRun_RecvErrorReWatches(t *testing.T) {
	t.Parallel()
	em := &recordingEmitter{}
	stream := &fakeStream{frames: []frameResult{
		{ev: Event{Type: EventBookmark, ResourceVersion: "rv-77"}},
		{err: errors.New("connection reset")},
	}}
	src := &fakeSource{listResults: []listResult{{rv: "rv-1"}}, streams: []*fakeStream{stream}}
	ctx, cancel := context.WithCancel(context.Background())
	src.onWatch = func(idx int) {
		if idx >= 1 {
			cancel()
		}
	}
	err := Run(ctx, src, em.emit, noLog, Options{now: fixedNow(), ReListBackoff: time.Millisecond})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run = %v; want context.Canceled", err)
	}
	if len(src.watchRVs) < 2 || src.watchRVs[1] != "rv-77" {
		t.Errorf("re-watch RV = %v; want rv-77 (resumed after recv error)", src.watchRVs)
	}
}

// TestRun_ListEmptyRVErrors proves a LIST that returns an empty RV is a hard
// error (treated as a transient List failure → backoff/retry).
func TestRun_ListEmptyRVErrors(t *testing.T) {
	t.Parallel()
	em := &recordingEmitter{}
	src := &fakeSource{listResults: []listResult{
		{rv: ""},     // empty RV → relist returns an error
		{rv: "rv-9"}, // retry succeeds
	}}
	ctx, cancel := context.WithCancel(context.Background())
	src.onWatch = func(int) { cancel() }
	err := Run(ctx, src, em.emit, noLog, Options{now: fixedNow(), ReListBackoff: time.Millisecond})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run = %v; want context.Canceled", err)
	}
	if src.listCalls != 2 {
		t.Errorf("listCalls = %d; want 2 (empty-RV fail + retry)", src.listCalls)
	}
}

// TestRun_ListEmitErrorContinues proves a per-object emit error during the
// bootstrap LIST does not abort the bootstrap.
func TestRun_ListEmitErrorContinues(t *testing.T) {
	t.Parallel()
	em := &recordingEmitter{emitErr: errors.New("push fail"), errAfter: 0, keyFor: func(any) string { return "k" }}
	src := &fakeSource{listResults: []listResult{{objects: []any{"a", "b"}, rv: "rv-1"}}}
	ctx, cancel := context.WithCancel(context.Background())
	src.onWatch = func(int) { cancel() }
	err := Run(ctx, src, em.emit, noLog, Options{now: fixedNow(), ReListBackoff: time.Millisecond})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run = %v; want context.Canceled", err)
	}
	// Both objects attempted despite the emit error on each.
	if em.count() != 2 {
		t.Errorf("emit attempts = %d; want 2 (continue past per-object error)", em.count())
	}
}

// TestCoalescer_CapResets proves the in-process key set resets at the cap so it
// cannot grow unbounded (threat-model D).
func TestCoalescer_CapResets(t *testing.T) {
	t.Parallel()
	c := &coalescer{cap: 2, now: fixedNow()}
	c.seen("a")
	c.seen("b")
	// At cap: the next seen() resets the set, so "a" is no longer remembered.
	c.seen("c")
	if c.seen("a") {
		t.Error("after cap reset, 'a' should be treated as fresh")
	}
}

// TestCoalescer_HourRollover proves keys reset across the hour boundary.
func TestCoalescer_HourRollover(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 6, 9, 10, 59, 0, 0, time.UTC)
	c := &coalescer{cap: 10, now: func() time.Time { return now }}
	c.seen("k")
	if !c.seen("k") {
		t.Error("same key same hour should be seen")
	}
	now = now.Add(2 * time.Minute) // cross into 11:00
	if c.seen("k") {
		t.Error("key should be fresh in the new hour")
	}
}

func containsCoalesce(s string) bool {
	return len(s) > 0 && (indexOf(s, "coalesced") >= 0)
}

func indexOf(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}
