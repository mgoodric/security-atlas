package credstore

import (
	"context"
	"errors"
	"testing"
	"time"
)

// auditingFake is a fakePersister that also implements AuditedPersister,
// recording each audited call and who made it (#2652).
type auditingFake struct {
	*fakePersister
	calls    []string
	by       []Credential
	auditErr error
}

func (f *auditingFake) InsertAudited(_ context.Context, rec PersistedCredential, by Credential) error {
	if f.auditErr != nil {
		return f.auditErr
	}
	f.calls, f.by = append(f.calls, "issue"), append(f.by, by)
	return f.Insert(rec)
}

func (f *auditingFake) RotateAudited(_ context.Context, successor PersistedCredential, predecessor Credential, predecessorPersisted bool, retiresAt time.Time, by Credential) error {
	if f.auditErr != nil {
		return f.auditErr
	}
	f.calls, f.by = append(f.calls, "rotate"), append(f.by, by)
	if !predecessorPersisted {
		return f.Insert(successor)
	}
	return f.Rotate(successor, predecessor.ID, predecessor.TenantID, retiresAt)
}

func (f *auditingFake) RevokeAudited(_ context.Context, cred Credential, persisted bool, by Credential) error {
	if f.auditErr != nil {
		return f.auditErr
	}
	f.calls, f.by = append(f.calls, "revoke"), append(f.by, by)
	if !persisted {
		return nil
	}
	return f.Revoke(cred.ID, cred.TenantID)
}

const auditTenant = "00000000-0000-0000-0000-0000000000a1"

var auditCaller = Credential{ID: "key_caller", TenantID: auditTenant, UserID: "user:11111111-1111-1111-1111-111111111111", IsAdmin: true}

func TestAudited_PassesCallerToPersister(t *testing.T) {
	t.Parallel()
	s := New(time.Minute)
	p := &auditingFake{fakePersister: newFakePersister()}
	attach(t, s, p)
	ctx := context.Background()

	cred, _, err := s.IssueMintedAudited(ctx, auditCaller, auditTenant, "", nil, 0)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if cred.MintedBy != "11111111-1111-1111-1111-111111111111" {
		t.Errorf("MintedBy = %q, want the caller's user id", cred.MintedBy)
	}
	succ, _, _, err := s.RotateAudited(ctx, auditCaller, cred.ID)
	if err != nil {
		t.Fatalf("rotate: %v", err)
	}
	if err := s.RevokeAudited(ctx, auditCaller, succ.ID); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	// Revoking again changes nothing and reaches no persister.
	if err := s.RevokeAudited(ctx, auditCaller, succ.ID); err != nil {
		t.Fatalf("second revoke: %v", err)
	}
	want := []string{"issue", "rotate", "revoke"}
	if len(p.calls) != len(want) {
		t.Fatalf("audited calls = %v, want %v", p.calls, want)
	}
	for i := range want {
		if p.calls[i] != want[i] {
			t.Errorf("call %d = %s, want %s", i, p.calls[i], want[i])
		}
		if p.by[i].ID != auditCaller.ID {
			t.Errorf("call %d by = %q, want %q", i, p.by[i].ID, auditCaller.ID)
		}
	}
}

// A failed audit write leaves memory untouched: no issued key, no retiring
// predecessor, no revocation.
func TestAudited_FailedAuditChangesNothing(t *testing.T) {
	t.Parallel()
	s := New(time.Minute)
	p := &auditingFake{fakePersister: newFakePersister()}
	attach(t, s, p)
	ctx := context.Background()
	seed, seedTok, err := s.IssueMintedAudited(ctx, auditCaller, auditTenant, "", nil, 0)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	p.auditErr = errors.New("audit insert failed")

	if _, _, err := s.IssueMintedAudited(ctx, auditCaller, auditTenant, "", nil, 0); err == nil {
		t.Error("issue succeeded with a failing audit write")
	}
	if _, _, _, err := s.RotateAudited(ctx, auditCaller, seed.ID); err == nil {
		t.Error("rotate succeeded with a failing audit write")
	}
	if err := s.RevokeAudited(ctx, auditCaller, seed.ID); err == nil {
		t.Error("revoke succeeded with a failing audit write")
	}
	if got := s.List(auditTenant); len(got) != 1 || got[0].ID != seed.ID {
		t.Errorf("List = %+v, want only the seed", got)
	}
	if _, err := s.Authenticate(seedTok); err != nil {
		t.Errorf("seed no longer authenticates: %v", err)
	}
	// The seed is not retiring: a fresh rotate succeeds once audit works.
	p.auditErr = nil
	if _, _, _, err := s.RotateAudited(ctx, auditCaller, seed.ID); err != nil {
		t.Errorf("rotate after recovery: %v", err)
	}
}

// A Persister that cannot audit is refused rather than used unaudited.
func TestAudited_NonAuditingPersisterFailsClosed(t *testing.T) {
	t.Parallel()
	s := New(time.Minute)
	attach(t, s, newFakePersister())
	ctx := context.Background()
	if _, _, err := s.IssueMintedAudited(ctx, auditCaller, auditTenant, "", nil, 0); !errors.Is(err, ErrAuditUnavailable) {
		t.Errorf("issue err = %v, want ErrAuditUnavailable", err)
	}
	cred, _, err := s.Issue(auditTenant, "", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := s.RotateAudited(ctx, auditCaller, cred.ID); !errors.Is(err, ErrAuditUnavailable) {
		t.Errorf("rotate err = %v, want ErrAuditUnavailable", err)
	}
	if err := s.RevokeAudited(ctx, auditCaller, cred.ID); !errors.Is(err, ErrAuditUnavailable) {
		t.Errorf("revoke err = %v, want ErrAuditUnavailable", err)
	}
}

// With no Persister there is no database to audit into; the change stays
// memory-only, as every change on such a store does.
func TestAudited_NoPersisterIsMemoryOnly(t *testing.T) {
	t.Parallel()
	s := New(time.Minute)
	ctx := context.Background()
	cred, _, err := s.IssueMintedAudited(ctx, auditCaller, auditTenant, "", nil, 0)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	succ, _, _, err := s.RotateAudited(ctx, auditCaller, cred.ID)
	if err != nil {
		t.Fatalf("rotate: %v", err)
	}
	if err := s.RevokeAudited(ctx, auditCaller, succ.ID); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if err := s.RevokeAudited(ctx, auditCaller, "key_missing"); !errors.Is(err, ErrUnknownKey) {
		t.Errorf("revoke unknown err = %v, want ErrUnknownKey", err)
	}
}
