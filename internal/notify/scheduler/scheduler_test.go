package scheduler

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// fakeDeliverer records every DeliverDigest call (the tenant on ctx, the
// userID, and the recipientUserID) so a test can assert WHO got driven,
// under WHICH tenant, and HOW many times. It can be told to skip or error
// per user, and it enforces idempotency the way the real sinks do: a second
// call for the same (tenant, user, UTC-day) returns Skipped instead of Sent.
type fakeDeliverer struct {
	mu sync.Mutex
	// calls is every (tenant, recipient) pair seen, in order.
	calls []call
	// skip is the set of userIDs that should report Skipped (opted-out is
	// handled by enumeration, but a sink may still skip on "no unread").
	skip map[uuid.UUID]bool
	// failOn maps a userID to an error the deliverer should return.
	failOn map[uuid.UUID]error
	// claimed tracks (tenant|user) already delivered today (idempotency).
	claimed map[string]bool
	// now is the UTC day used for the idempotency key.
	now string
}

type call struct {
	tenant    string
	userID    uuid.UUID
	recipient string
}

func newFakeDeliverer() *fakeDeliverer {
	return &fakeDeliverer{
		skip:    map[uuid.UUID]bool{},
		failOn:  map[uuid.UUID]error{},
		claimed: map[string]bool{},
		now:     "2026-06-07",
	}
}

func (f *fakeDeliverer) DeliverDigest(ctx context.Context, userID uuid.UUID, recipientUserID string) (Delivery, error) {
	tenant, err := tenancy.TenantFromContext(ctx)
	if err != nil {
		// The driver must always set the tenant before calling — a missing
		// tenant is a tenant-isolation bug and must surface as an error.
		return Delivery{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, call{tenant: tenant, userID: userID, recipient: recipientUserID})

	if e := f.failOn[userID]; e != nil {
		return Delivery{}, e
	}
	if f.skip[userID] {
		return Delivery{Skipped: true}, nil
	}
	key := tenant + "|" + userID.String() + "|" + f.now
	if f.claimed[key] {
		// claim-before-send already taken today: no double-send.
		return Delivery{Skipped: true}, nil
	}
	f.claimed[key] = true
	return Delivery{Sent: true}, nil
}

func (f *fakeDeliverer) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func (f *fakeDeliverer) snapshot() []call {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]call, len(f.calls))
	copy(out, f.calls)
	return out
}

// staticLister returns a fixed opt-in set, ignoring the queries handle (the
// driver passes the migrator-pool queries; a unit test does not need a DB).
func staticLister(optins []OptIn) OptInLister {
	return func(ctx context.Context, q *dbx.Queries) ([]OptIn, error) {
		return optins, nil
	}
}

func errLister(err error) OptInLister {
	return func(ctx context.Context, q *dbx.Queries) ([]OptIn, error) {
		return nil, err
	}
}

func mkOptIn(t string, u string) OptIn {
	return OptIn{TenantID: uuid.MustParse(t), UserID: uuid.MustParse(u)}
}

const (
	tenantA = "11111111-1111-1111-1111-111111111111"
	tenantB = "22222222-2222-2222-2222-222222222222"
	userA1  = "aaaaaaaa-0000-0000-0000-000000000001"
	userA2  = "aaaaaaaa-0000-0000-0000-000000000002"
	userB1  = "bbbbbbbb-0000-0000-0000-000000000001"
)

// The tick drives DeliverDigest for every ENUMERATED (opted-in) user, under
// that user's OWN tenant context, with recipient_user_id == userID string.
func TestSweepOnce_DrivesOptedInUsers(t *testing.T) {
	t.Parallel()
	del := newFakeDeliverer()
	ch := Channel{
		Name:      "email",
		List:      staticLister([]OptIn{mkOptIn(tenantA, userA1), mkOptIn(tenantA, userA2)}),
		Deliverer: del,
	}
	s := New(nil, []Channel{ch}, nil)

	rep, err := s.SweepOnce(context.Background())
	if err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}
	if rep.UsersEnumerated != 2 || rep.Sent != 2 || rep.Skipped != 0 || rep.Failures != 0 {
		t.Fatalf("unexpected report: %+v", rep)
	}
	calls := del.snapshot()
	if len(calls) != 2 {
		t.Fatalf("expected 2 deliveries, got %d", len(calls))
	}
	for _, c := range calls {
		if c.tenant != tenantA {
			t.Fatalf("delivery ran under wrong tenant: %q", c.tenant)
		}
		// slice-029 recipient_user_id is the user's UUID string.
		if c.recipient != c.userID.String() {
			t.Fatalf("recipient %q != userID string %q", c.recipient, c.userID)
		}
	}
}

// Enumeration is the opt-in gate: an opted-OUT user simply never appears in
// the lister's result, so the driver never calls DeliverDigest for them.
func TestSweepOnce_SkipsOptedOutViaEnumeration(t *testing.T) {
	t.Parallel()
	del := newFakeDeliverer()
	// Only userA1 is enumerated (opted in); userA2 is absent (opted out).
	ch := Channel{
		Name:      "slack",
		List:      staticLister([]OptIn{mkOptIn(tenantA, userA1)}),
		Deliverer: del,
	}
	s := New(nil, []Channel{ch}, nil)

	rep, err := s.SweepOnce(context.Background())
	if err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}
	if rep.UsersEnumerated != 1 || rep.Sent != 1 {
		t.Fatalf("unexpected report: %+v", rep)
	}
	for _, c := range del.snapshot() {
		if c.userID == uuid.MustParse(userA2) {
			t.Fatalf("opted-out user A2 should never be driven")
		}
	}
}

// Re-running the same sweep the same UTC day is a no-op: the sink's
// claim-before-send returns Skipped, so no user is double-sent.
func TestSweepOnce_IdempotentNoDoubleSend(t *testing.T) {
	t.Parallel()
	del := newFakeDeliverer()
	ch := Channel{
		Name:      "webhook",
		List:      staticLister([]OptIn{mkOptIn(tenantA, userA1)}),
		Deliverer: del,
	}
	s := New(nil, []Channel{ch}, nil)

	first, _ := s.SweepOnce(context.Background())
	second, _ := s.SweepOnce(context.Background())

	if first.Sent != 1 {
		t.Fatalf("first sweep: expected 1 sent, got %+v", first)
	}
	if second.Sent != 0 || second.Skipped != 1 {
		t.Fatalf("second sweep should be all-skipped (idempotent), got %+v", second)
	}
	// DeliverDigest was still CALLED both times (the claim lives inside the
	// sink); the guarantee is no second SEND, not no second call.
	if del.callCount() != 2 {
		t.Fatalf("expected 2 calls across 2 sweeps, got %d", del.callCount())
	}
}

// Each user's delivery runs under that user's own tenant; the walk never
// crosses tenants even when two tenants share one sweep.
func TestSweepOnce_TenantIsolation(t *testing.T) {
	t.Parallel()
	del := newFakeDeliverer()
	ch := Channel{
		Name: "email",
		List: staticLister([]OptIn{
			mkOptIn(tenantA, userA1),
			mkOptIn(tenantB, userB1),
		}),
		Deliverer: del,
	}
	s := New(nil, []Channel{ch}, nil)

	if _, err := s.SweepOnce(context.Background()); err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}
	byUser := map[uuid.UUID]string{}
	for _, c := range del.snapshot() {
		byUser[c.userID] = c.tenant
	}
	if byUser[uuid.MustParse(userA1)] != tenantA {
		t.Fatalf("user A1 delivered under wrong tenant: %q", byUser[uuid.MustParse(userA1)])
	}
	if byUser[uuid.MustParse(userB1)] != tenantB {
		t.Fatalf("user B1 delivered under wrong tenant: %q", byUser[uuid.MustParse(userB1)])
	}
}

// One failing delivery does NOT abort the sweep for the rest
// (try/log/continue). The failure is tallied; the other user still sends.
func TestSweepOnce_PerUserFailureDoesNotAbort(t *testing.T) {
	t.Parallel()
	del := newFakeDeliverer()
	del.failOn[uuid.MustParse(userA1)] = errors.New("smtp boom")
	ch := Channel{
		Name: "email",
		List: staticLister([]OptIn{
			mkOptIn(tenantA, userA1),
			mkOptIn(tenantA, userA2),
		}),
		Deliverer: del,
	}
	s := New(nil, []Channel{ch}, nil)

	rep, err := s.SweepOnce(context.Background())
	if err != nil {
		t.Fatalf("SweepOnce should not return an error on per-user failure: %v", err)
	}
	if rep.Failures != 1 || rep.Sent != 1 {
		t.Fatalf("expected 1 failure + 1 sent, got %+v", rep)
	}
}

// A channel-level enumeration error does NOT abort the sweep for OTHER
// channels (channel try/log/continue).
func TestSweepOnce_ChannelEnumerationErrorDoesNotAbortOthers(t *testing.T) {
	t.Parallel()
	good := newFakeDeliverer()
	chBad := Channel{Name: "slack", List: errLister(errors.New("db down")), Deliverer: newFakeDeliverer()}
	chGood := Channel{Name: "email", List: staticLister([]OptIn{mkOptIn(tenantA, userA1)}), Deliverer: good}
	s := New(nil, []Channel{chBad, chGood}, nil)

	rep, err := s.SweepOnce(context.Background())
	if err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}
	if rep.Sent != 1 {
		t.Fatalf("good channel should still deliver despite bad channel, got %+v", rep)
	}
	if good.callCount() != 1 {
		t.Fatalf("expected the good channel to be driven once, got %d", good.callCount())
	}
}

// Run fires an immediate inline sweep on start (fresh-deploy first signal),
// then ticks; ctx cancel stops it cleanly.
func TestRun_InlineSweepThenStop(t *testing.T) {
	t.Parallel()
	del := newFakeDeliverer()
	ch := Channel{Name: "email", List: staticLister([]OptIn{mkOptIn(tenantA, userA1)}), Deliverer: del}
	s := New(nil, []Channel{ch}, nil)

	done := make(chan struct{})
	var gotRep SweepReport
	var gotErr error
	s.setInlineSweepHook(func(rep SweepReport, err error) {
		gotRep = rep
		gotErr = err
		close(done)
	})

	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() { runDone <- s.Run(ctx, time.Hour) }()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		cancel()
		t.Fatal("inline sweep never fired")
	}
	cancel()

	if gotErr != nil {
		t.Fatalf("inline sweep error: %v", gotErr)
	}
	if gotRep.Sent != 1 {
		t.Fatalf("inline sweep should have sent 1, got %+v", gotRep)
	}
	select {
	case err := <-runDone:
		if err != nil {
			t.Fatalf("Run returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not stop on ctx cancel")
	}
}

// DefaultInterval is daily — the honest digest-period name keyed to the
// per-UTC-day digest_key. A guard against an accidental sub-day default that
// would mislabel the cadence.
func TestDefaultInterval_IsDaily(t *testing.T) {
	t.Parallel()
	if DefaultInterval != 24*time.Hour {
		t.Fatalf("DefaultInterval should be daily, got %s", DefaultInterval)
	}
}

// Run normalizes a non-positive interval to DefaultInterval rather than
// busy-looping; we assert it accepts the cancel path with interval=0.
func TestRun_ZeroIntervalNormalizes(t *testing.T) {
	t.Parallel()
	del := newFakeDeliverer()
	ch := Channel{Name: "email", List: staticLister(nil), Deliverer: del}
	s := New(nil, []Channel{ch}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() { runDone <- s.Run(ctx, 0) }()
	cancel()
	select {
	case err := <-runDone:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not stop")
	}
}
