// Package watch implements the Kubernetes connector's event-driven (subscribe)
// profile: a long-lived consumer of the Kubernetes WATCH API that reacts to RBAC
// / workload changes as they happen, instead of waiting for the next scheduled
// pull pass.
//
// HONEST naming (CLAUDE.md anti-pattern: honest intervals). This is
// "event-driven via the Kubernetes watch API", NOT "continuous monitoring". The
// connector holds an open watch stream and emits evidence on each relevant
// change event; the pull profile remains the reconciliation backstop.
//
// Source-side only (invariant #3). The watch is how the connector retrieves data
// FROM the cluster (a `subscribe` profile value); the platform-side wire is
// always push — every event-built record goes out through the SAME
// IngestEvidence/Push path the pull profile uses, with no new inbound platform
// API and no new evidence kind. The watch reaches only the read-only API surfaces
// the pull path already reads (rolebindings/clusterrolebindings for RBAC; apps
// deployments/daemonsets/statefulsets for workloads), adding at most a `watch`
// verb alongside the existing get,list — never `secrets`, never a write verb,
// never a wildcard.
//
// THE WATCH LIFECYCLE (the load-bearing novelty). A Kubernetes watch is a
// long-lived stream that ENDS — the API server closes it periodically, or it
// errors. The standard reflector pattern, hand-rolled here against a fakeable
// seam (no client-go dependency, mirroring the connector's thin-HTTP style):
//
//  1. LIST the resource to obtain the starting resourceVersion (RV).
//  2. WATCH from that RV with allowWatchBookmarks=true so the server periodically
//     emits a Bookmark event carrying a fresh RV — advancing our resume point
//     cheaply without a re-LIST.
//  3. On each ADDED / MODIFIED / DELETED event, build the SAME existing evidence
//     kind via the existing rbac/workload record builder (UNCHANGED) and push.
//  4. On stream close / transient error, RE-WATCH from the last-seen RV.
//  5. On a 410 Gone ("resourceVersion too old" — the server has compacted past
//     our RV), the RV is stale: RE-LIST to obtain a fresh RV and RESUME the watch
//     from it. The re-LIST emits records for the current state (it IS fresh, and
//     the hour-window idempotency key makes re-emission safe — it collapses with
//     any pull-emitted or prior-watch-emitted record for the same resource in the
//     same hour).
//
// The whole loop is bound by the run context: cancelling ctx (graceful shutdown)
// ends the consumer.
//
// DoS bounding (threat-model D). A high-churn resource cannot grow memory
// unbounded: every event for a given (resource) maps — via the slice-487
// hour-window idempotency key — onto the same ledger row for the hour, so a burst
// of edits to one binding within the hour collapses to one record. A bounded
// per-resource coalescing map (capped) tracks the last key emitted this hour so
// the loop can skip re-pushing an identical key inside the process, shedding the
// burst before it reaches the platform. The hour-window key is the primary
// collapse; the in-process map is the cheap pre-filter.
package watch

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// EventType is the Kubernetes watch event type. Mirrors k8s.io/apimachinery
// watch.EventType without importing it (the connector's thin-HTTP style).
type EventType string

const (
	// EventAdded is a newly observed object.
	EventAdded EventType = "ADDED"
	// EventModified is a changed object.
	EventModified EventType = "MODIFIED"
	// EventDeleted is a removed object.
	EventDeleted EventType = "DELETED"
	// EventBookmark carries only a fresh resourceVersion (no object payload of
	// interest); it advances the resume point cheaply.
	EventBookmark EventType = "BOOKMARK"
	// EventError is a stream-level error frame (the server reports a status, e.g.
	// 410 Gone). The object is not a resource; ResourceExpired flags the 410.
	EventError EventType = "ERROR"
)

// Event is one decoded watch frame. ResourceVersion is the object's (or
// bookmark's) resourceVersion, used to resume the watch. ResourceExpired is set
// on an ERROR frame that reports a 410 Gone (the watch RV is too old).
type Event struct {
	Type            EventType
	ResourceVersion string
	// Object is the decoded resource for ADDED/MODIFIED/DELETED. nil for
	// BOOKMARK/ERROR. The concrete type is the collector's Raw shape (e.g.
	// rbac.RawBinding) — kept as any so the watch loop stays resource-agnostic.
	Object any
	// ResourceExpired marks an ERROR frame as a 410 Gone (errors.IsResourceExpired
	// equivalent). The loop re-LISTs on this.
	ResourceExpired bool
}

// Stream is one open watch connection. Recv blocks until the next event, the
// stream closes (io.EOF-equivalent → ErrStreamClosed), or ctx is done. Close
// releases the connection.
type Stream interface {
	Recv() (Event, error)
	Close() error
}

// Source is the fakeable seam the watch loop depends on. A concrete
// implementation issues read-only Kubernetes API calls; tests pass a fake. It
// covers exactly the two operations the reflector loop needs against ONE resource
// surface: an initial LIST (returns the current objects + the RV to watch from)
// and a WATCH (opens a stream from a given RV).
//
// Read-only: List and Watch issue GET / GET-with-?watch only; no mutation, no
// new resource, no new verb beyond the `watch` verb added alongside get,list.
type Source interface {
	// List returns the current objects and the resourceVersion to start watching
	// from. The objects are the collector's Raw shape (rbac.RawBinding /
	// workload.RawWorkload), boxed as any.
	List(ctx context.Context) (objects []any, resourceVersion string, err error)
	// Watch opens a watch stream from resourceVersion with
	// allowWatchBookmarks=true. The caller owns Close.
	Watch(ctx context.Context, resourceVersion string) (Stream, error)
}

// ErrStreamClosed is returned by Stream.Recv when the server closed the watch
// normally (the routine re-watches from the last RV).
var ErrStreamClosed = errors.New("watch: stream closed by server")

// Emitter turns one watched object into a push. The cmd layer supplies this: it
// builds the existing evidence kind via the UNCHANGED rbac/workload record
// builder and pushes through the existing Push API. It returns the idempotency
// key the record carried so the loop can coalesce duplicate keys in-process
// (burst shedding) without re-pushing. A DELETED event still emits (the record
// reflects the last-observed config at the deletion instant); the emitter may
// choose to no-op — that is the emitter's call, not the loop's.
//
// eventType lets the emitter distinguish ADDED/MODIFIED/DELETED if it cares;
// most emitters build the same record regardless.
type Emitter func(ctx context.Context, eventType EventType, object any) (idempotencyKey string, err error)

// Logf is the loop's leveled log sink. The cmd layer passes a function that
// %q-quotes every user-tainted arg (resource name, namespace, resourceVersion,
// error strings) to pre-empt log injection. The loop NEVER formats a
// user-tainted value itself — it hands raw values to Logf, which owns the %q.
type Logf func(format string, args ...any)

// Options configure one Run loop.
type Options struct {
	// ResourceName labels log lines (e.g. "rbac" / "workload"). Operator-set, not
	// user-tainted, but still passed through Logf's %q.
	ResourceName string
	// CoalesceCap bounds the in-process per-hour key set. When the set exceeds the
	// cap it is reset (the hour-window idempotency key at the platform is the
	// durable collapse; the in-process set is a best-effort pre-filter, so a reset
	// only risks a redundant push the ledger then dedups). 0 → DefaultCoalesceCap.
	CoalesceCap int
	// ReListBackoff is the pause before a re-LIST after a 410 Gone or a List
	// error, to avoid hammering the API server. 0 → DefaultReListBackoff.
	ReListBackoff time.Duration
	// now is injectable for deterministic tests (nil → time.Now UTC). It governs
	// the hour bucket the in-process coalescer keys on.
	now func() time.Time
}

// DefaultCoalesceCap bounds the in-process coalescing key set so a high-churn
// cluster cannot grow it without limit (threat-model D). 100k distinct
// (resource,hour) keys is far beyond any real cluster's binding/workload count.
const DefaultCoalesceCap = 100_000

// DefaultReListBackoff paces re-LISTs after a 410 / List error.
const DefaultReListBackoff = 2 * time.Second

// Run consumes the watch stream until ctx is cancelled, emitting an evidence push
// (via emit) for every relevant change event. It implements the reflector
// lifecycle documented on the package: LIST → watch-from-RV → bookmark-advance →
// re-watch-on-close → re-LIST-on-410. It returns ctx.Err() on graceful shutdown
// and never returns on a transient watch error (it re-watches); it returns a
// non-context error only when the seam reports a fatal, non-recoverable List
// error AND ctx is still live but the caller asked to surface it (see the List
// error path).
func Run(ctx context.Context, src Source, emit Emitter, log Logf, opts Options) error {
	if src == nil {
		return errors.New("watch: source is nil")
	}
	if emit == nil {
		return errors.New("watch: emitter is nil")
	}
	if log == nil {
		log = func(string, ...any) {}
	}
	cap := opts.CoalesceCap
	if cap <= 0 {
		cap = DefaultCoalesceCap
	}
	backoff := opts.ReListBackoff
	if backoff <= 0 {
		backoff = DefaultReListBackoff
	}
	now := opts.now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}

	c := &coalescer{cap: cap, now: now}

	// resourceVersion is the resume point. Empty forces an initial LIST.
	resourceVersion := ""

	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		// (Re)LIST when we have no RV (first pass, or after a 410 Gone reset). The
		// LIST emits records for the current state — it IS fresh, and the
		// hour-window idempotency key makes the emission safe (it collapses with
		// any pull/prior-watch record for the same resource in the same hour).
		if resourceVersion == "" {
			rv, err := c.relist(ctx, src, emit, log, opts.ResourceName)
			if err != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				// A live-ctx List error is transient (API server hiccup); back off
				// and retry rather than tearing the consumer down.
				log("watch[%q]: list failed, backing off: %q", opts.ResourceName, err.Error())
				if waitErr := sleepCtx(ctx, backoff); waitErr != nil {
					return waitErr
				}
				continue
			}
			resourceVersion = rv
		}

		// Open the watch from the current RV.
		stream, err := src.Watch(ctx, resourceVersion)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			log("watch[%q]: open failed, backing off: %q", opts.ResourceName, err.Error())
			if waitErr := sleepCtx(ctx, backoff); waitErr != nil {
				return waitErr
			}
			continue
		}

		nextRV, expired, err := consume(ctx, stream, emit, log, c, opts.ResourceName)
		_ = stream.Close()

		switch {
		case ctx.Err() != nil:
			return ctx.Err()
		case expired:
			// 410 Gone: our RV is too old. Drop it so the next loop iteration
			// re-LISTs for a fresh RV (the standard reflector recovery).
			log("watch[%q]: resourceVersion expired (410 Gone), re-listing", opts.ResourceName)
			resourceVersion = ""
			if waitErr := sleepCtx(ctx, backoff); waitErr != nil {
				return waitErr
			}
		case err != nil && !errors.Is(err, ErrStreamClosed):
			// Transient stream error: re-watch from the last good RV.
			log("watch[%q]: stream error, re-watching from rv=%q: %q",
				opts.ResourceName, nextRV, err.Error())
			if nextRV != "" {
				resourceVersion = nextRV
			}
		default:
			// Normal close (ErrStreamClosed) or clean EOF: re-watch from the most
			// advanced RV we saw (bookmarks keep this cheap — no re-LIST).
			if nextRV != "" {
				resourceVersion = nextRV
			}
		}
	}
}

// consume drains one open stream until it ends. It returns the most advanced
// resourceVersion observed (for resume), whether the stream ended on a 410 Gone,
// and the terminating error.
func consume(ctx context.Context, s Stream, emit Emitter, log Logf, c *coalescer, resource string) (rv string, expired bool, err error) {
	for {
		if cerr := ctx.Err(); cerr != nil {
			return rv, false, cerr
		}
		ev, recvErr := s.Recv()
		if recvErr != nil {
			return rv, false, recvErr
		}
		switch ev.Type {
		case EventError:
			if ev.ResourceExpired {
				return rv, true, nil
			}
			// Non-expiry error frame: treat as a transient stream end.
			log("watch[%q]: error frame, ending stream", resource)
			return rv, false, ErrStreamClosed
		case EventBookmark:
			// Advance the resume point cheaply; no emission.
			if ev.ResourceVersion != "" {
				rv = ev.ResourceVersion
			}
		case EventAdded, EventModified, EventDeleted:
			if ev.ResourceVersion != "" {
				rv = ev.ResourceVersion
			}
			if err := emitOne(ctx, emit, c, ev, log, resource); err != nil {
				if ctx.Err() != nil {
					return rv, false, ctx.Err()
				}
				// A push error is transient (platform hiccup); end the stream so the
				// loop re-watches from rv and retries. We do NOT drop the consumer.
				log("watch[%q]: emit failed, ending stream to retry: %q", resource, err.Error())
				return rv, false, ErrStreamClosed
			}
		default:
			log("watch[%q]: unknown event type %q, ignoring", resource, string(ev.Type))
		}
	}
}

// emitOne pushes one event's record, coalescing in-process by idempotency key so
// a burst of identical-key events (same resource, same hour) sheds before
// reaching the platform.
func emitOne(ctx context.Context, emit Emitter, c *coalescer, ev Event, log Logf, resource string) error {
	key, err := emit(ctx, ev.Type, ev.Object)
	if err != nil {
		return err
	}
	if key == "" {
		// Emitter chose not to push (e.g. an unbuildable object). Nothing to
		// coalesce.
		return nil
	}
	if c.seen(key) {
		log("watch[%q]: coalesced duplicate key this hour (burst shedding)", resource)
	}
	return nil
}

// relist performs an initial / recovery LIST and emits a record per object. It
// returns the resourceVersion to watch from.
func (c *coalescer) relist(ctx context.Context, src Source, emit Emitter, log Logf, resource string) (string, error) {
	objects, rv, err := src.List(ctx)
	if err != nil {
		return "", err
	}
	for _, obj := range objects {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		key, eerr := emit(ctx, EventAdded, obj)
		if eerr != nil {
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			// A push failure during the re-LIST is transient; log and continue so
			// one bad record does not abort the whole bootstrap. The watch will
			// pick up subsequent changes; the next re-LIST re-attempts.
			log("watch[%q]: list emit failed for one object, continuing: %q", resource, eerr.Error())
			continue
		}
		if key != "" && c.seen(key) {
			log("watch[%q]: list coalesced duplicate key this hour", resource)
		}
	}
	if rv == "" {
		return "", fmt.Errorf("watch[%s]: list returned empty resourceVersion", resource)
	}
	return rv, nil
}

// coalescer is the bounded in-process per-hour key set. It is the cheap
// pre-filter for burst shedding; the hour-window idempotency key at the platform
// is the durable collapse, so a reset (cap exceeded, or hour rollover) only risks
// a redundant push the ledger dedups.
type coalescer struct {
	cap  int
	now  func() time.Time
	hour time.Time
	keys map[string]struct{}
}

// seen records key for the current hour and reports whether it was already
// present this hour. It resets on hour rollover and when the cap is exceeded.
func (c *coalescer) seen(key string) bool {
	h := c.now().UTC().Truncate(time.Hour)
	if c.keys == nil || !h.Equal(c.hour) {
		c.hour = h
		c.keys = make(map[string]struct{})
	}
	if len(c.keys) >= c.cap {
		// Bounded: shed the set rather than grow unbounded. The platform key still
		// dedups, so correctness is preserved; we only lose the in-process filter.
		c.keys = make(map[string]struct{})
	}
	_, ok := c.keys[key]
	c.keys[key] = struct{}{}
	return ok
}

// sleepCtx waits d or until ctx is done, returning ctx.Err() if cancelled first.
func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
