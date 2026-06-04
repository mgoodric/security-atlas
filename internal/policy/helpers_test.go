// helpers_test.go — slice 426 pure-Go branch coverage for internal/policy.
//
// Per the slice-353 Q-2 fast-loop convention: no Postgres, no
// `//go:build integration` tag, fast `t.Parallel()` table tests. These
// exercise the package's pure-Go residue that the integration suite leaves
// uncovered:
//
//   - validateCreate (the six required-field guards + the
//     source_attribution enum guard) and validSourceAttribution
//   - mapCreateError (pg FK / CHECK SQLSTATE → wrapped error mapping)
//   - policyFromRow / policyFromChainRow nullable-column branches
//   - the pgtype <-> Go converters (uuidsToPg, uuidsFromPg, pgUUID, pgDate,
//     stringPtr, datePtr, pgts, pgUUIDPtr)
//   - Policy.IsOrphan
//   - DeriveAckToken determinism + day-bucketing
//   - rolesIntersect (admin wildcard, empty-required, empty-owner, hit/miss)
//   - the AckStore pre-tx input guards (Record + PendingForUser reject
//     missing/invalid identity BEFORE opening a transaction, so a nil pool
//     is never dereferenced — this is the security-relevant deny branch the
//     slice asks for, asserted without faking the DB)
//   - WithClock / now clock injection
//
// DB-dependent branches (the actual INSERT/transition/RLS paths) stay in
// integration_test.go.
package policy

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

func TestValidateCreate_RequiredFields(t *testing.T) {
	t.Parallel()
	base := CreateInput{
		Title:        "Access Control Policy",
		Version:      "1.0",
		BodyMd:       "# body",
		OwnerRole:    "grc_engineer",
		ApproverRole: "ciso",
		CreatedBy:    "user-1",
	}
	cases := []struct {
		name    string
		mutate  func(*CreateInput)
		wantErr error
	}{
		{"valid", func(*CreateInput) {}, nil},
		{"missing title", func(in *CreateInput) { in.Title = "" }, ErrTitleRequired},
		{"missing version", func(in *CreateInput) { in.Version = "" }, ErrVersionRequired},
		{"missing body", func(in *CreateInput) { in.BodyMd = "" }, ErrBodyRequired},
		{"missing owner role", func(in *CreateInput) { in.OwnerRole = "" }, ErrOwnerRoleRequired},
		{"missing approver role", func(in *CreateInput) { in.ApproverRole = "" }, ErrApproverRoleRequired},
		{"missing created_by", func(in *CreateInput) { in.CreatedBy = "" }, ErrCreatedByRequired},
		{"valid source attribution", func(in *CreateInput) { in.SourceAttribution = SourceTenantAuthored }, nil},
		{"empty source attribution allowed", func(in *CreateInput) { in.SourceAttribution = "" }, nil},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			in := base
			tc.mutate(&in)
			err := validateCreate(in)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("validateCreate(%s) = %v, want %v", tc.name, err, tc.wantErr)
			}
		})
	}
}

func TestValidateCreate_InvalidSourceAttribution(t *testing.T) {
	t.Parallel()
	in := CreateInput{
		Title: "T", Version: "1", BodyMd: "b", OwnerRole: "o",
		ApproverRole: "a", CreatedBy: "c",
		SourceAttribution: "not-a-real-source",
	}
	err := validateCreate(in)
	if err == nil {
		t.Fatal("expected error for invalid source_attribution, got nil")
	}
	// Must NOT be one of the required-field sentinels — it is a distinct
	// validation branch.
	for _, sentinel := range []error{ErrTitleRequired, ErrVersionRequired, ErrBodyRequired} {
		if errors.Is(err, sentinel) {
			t.Fatalf("invalid source_attribution mis-mapped to %v", sentinel)
		}
	}
}

func TestValidSourceAttribution(t *testing.T) {
	t.Parallel()
	cases := map[string]bool{
		SourceCommunityDraft: true,
		SourceTenantAuthored: true,
		SourceVendorProvided: true,
		"":                   false,
		"bogus":              false,
		"Community_Draft":    false, // case sensitive
	}
	for in, want := range cases {
		if got := validSourceAttribution(in); got != want {
			t.Fatalf("validSourceAttribution(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestMapCreateError(t *testing.T) {
	t.Parallel()
	t.Run("foreign key violation maps to predecessor-not-in-tenant", func(t *testing.T) {
		t.Parallel()
		in := &pgconn.PgError{Code: pgErrForeignKeyViolation}
		got := mapCreateError(in)
		if got == nil || !errors.Is(got, in) {
			t.Fatalf("mapCreateError did not wrap the FK error: %v", got)
		}
		if msg := got.Error(); !strings.Contains(msg, "predecessor not in tenant") {
			t.Fatalf("FK error message = %q, want predecessor-not-in-tenant text", msg)
		}
	})
	t.Run("check violation names the constraint", func(t *testing.T) {
		t.Parallel()
		in := &pgconn.PgError{Code: pgErrCheckViolation, ConstraintName: "policies_status_check"}
		got := mapCreateError(in)
		if msg := got.Error(); !strings.Contains(msg, "policies_status_check") {
			t.Fatalf("check error message = %q, want it to name the constraint", msg)
		}
	})
	t.Run("non-pg error falls through to generic wrap", func(t *testing.T) {
		t.Parallel()
		in := errors.New("connection reset")
		got := mapCreateError(in)
		if !errors.Is(got, in) {
			t.Fatalf("mapCreateError lost the wrapped non-pg error: %v", got)
		}
		if msg := got.Error(); !strings.Contains(msg, "create policy") {
			t.Fatalf("generic error message = %q, want create-policy prefix", msg)
		}
	})
}

func TestPolicy_IsOrphan(t *testing.T) {
	t.Parallel()
	if !(Policy{}).IsOrphan() {
		t.Fatal("policy with no linked controls should be orphan")
	}
	linked := Policy{LinkedControlIDs: []uuid.UUID{uuid.New()}}
	if linked.IsOrphan() {
		t.Fatal("policy with linked controls should not be orphan")
	}
}

func TestPolicyFromRow_NullableColumns(t *testing.T) {
	t.Parallel()
	id := uuid.New()
	tenant := uuid.New()
	pred := uuid.New()
	submitter := "user-sub"
	approver := "user-app"
	publisher := "user-pub"
	now := time.Now().UTC().Truncate(time.Second)

	t.Run("all nullable set", func(t *testing.T) {
		t.Parallel()
		row := dbx.Policy{
			ID:                          pgUUID(id),
			TenantID:                    pgUUID(tenant),
			PredecessorID:               pgUUID(pred),
			Title:                       "T",
			Version:                     "1.0",
			BodyMd:                      "body",
			OwnerRole:                   "owner",
			ApproverRole:                "approver",
			LinkedControlIds:            uuidsToPg([]uuid.UUID{uuid.New()}),
			AcknowledgmentRequiredRoles: []string{"all_staff"},
			Status:                      StatePublished,
			SourceAttribution:           SourceTenantAuthored,
			CreatedBy:                   "creator",
			EffectiveDate:               pgDate(now),
			SubmittedAt:                 pgts(now),
			SubmittedBy:                 &submitter,
			ApprovedAt:                  pgts(now),
			ApprovedBy:                  &approver,
			PublishedAt:                 pgts(now),
			PublishedBy:                 &publisher,
			SupersededAt:                pgts(now),
			CreatedAt:                   pgts(now),
			UpdatedAt:                   pgts(now),
		}
		p := policyFromRow(row)
		if p.PredecessorID == nil || *p.PredecessorID != pred {
			t.Fatalf("PredecessorID not carried through: %v", p.PredecessorID)
		}
		if p.EffectiveDate == nil || p.SubmittedAt == nil || p.ApprovedAt == nil ||
			p.PublishedAt == nil || p.SupersededAt == nil {
			t.Fatal("expected all timestamp pointers populated")
		}
		if p.SubmittedBy == nil || *p.SubmittedBy != submitter {
			t.Fatalf("SubmittedBy = %v, want %q", p.SubmittedBy, submitter)
		}
		if p.Status != StatePublished {
			t.Fatalf("Status = %q", p.Status)
		}
	})

	t.Run("all nullable absent", func(t *testing.T) {
		t.Parallel()
		row := dbx.Policy{
			ID:       pgUUID(id),
			TenantID: pgUUID(tenant),
			Title:    "T",
			Status:   StateDraft,
		}
		p := policyFromRow(row)
		if p.PredecessorID != nil {
			t.Fatal("PredecessorID should be nil when not Valid")
		}
		if p.EffectiveDate != nil || p.SubmittedAt != nil || p.ApprovedAt != nil ||
			p.PublishedAt != nil || p.SupersededAt != nil {
			t.Fatal("nil-able timestamps should be nil when not Valid")
		}
		if p.SubmittedBy != nil || p.ApprovedBy != nil || p.PublishedBy != nil {
			t.Fatal("nil-able *string columns should be nil")
		}
	})
}

func TestPolicyFromChainRow_DelegatesToFromRow(t *testing.T) {
	t.Parallel()
	id := uuid.New()
	row := dbx.ListPolicyVersionChainRow(dbx.Policy{
		ID:     pgUUID(id),
		Title:  "Chain head",
		Status: StateSuperseded,
	})
	p := policyFromChainRow(row)
	if p.ID != id || p.Title != "Chain head" || p.Status != StateSuperseded {
		t.Fatalf("policyFromChainRow lost fields: %+v", p)
	}
}

func TestPgConverters(t *testing.T) {
	t.Parallel()
	t.Run("pgUUID nil → invalid", func(t *testing.T) {
		t.Parallel()
		if pgUUID(uuid.Nil).Valid {
			t.Fatal("pgUUID(uuid.Nil) should be invalid")
		}
		u := uuid.New()
		got := pgUUID(u)
		if !got.Valid || uuid.UUID(got.Bytes) != u {
			t.Fatalf("pgUUID(%v) round-trip failed: %+v", u, got)
		}
	})
	t.Run("uuids round-trip", func(t *testing.T) {
		t.Parallel()
		in := []uuid.UUID{uuid.New(), uuid.New()}
		out := uuidsFromPg(uuidsToPg(in))
		if len(out) != len(in) {
			t.Fatalf("round-trip length mismatch: %d vs %d", len(out), len(in))
		}
		for i := range in {
			if out[i] != in[i] {
				t.Fatalf("round-trip[%d] = %v, want %v", i, out[i], in[i])
			}
		}
		// empty input
		if len(uuidsToPg(nil)) != 0 {
			t.Fatal("uuidsToPg(nil) should be empty")
		}
	})
	t.Run("pgDate", func(t *testing.T) {
		t.Parallel()
		ts := time.Date(2026, 6, 4, 0, 0, 0, 0, time.UTC)
		d := pgDate(ts)
		if !d.Valid || !d.Time.Equal(ts) {
			t.Fatalf("pgDate = %+v, want valid %v", d, ts)
		}
	})
	t.Run("stringPtr", func(t *testing.T) {
		t.Parallel()
		if stringPtr("") != nil {
			t.Fatal("stringPtr(\"\") should be nil")
		}
		if got := stringPtr("x"); got == nil || *got != "x" {
			t.Fatalf("stringPtr(\"x\") = %v", got)
		}
	})
	t.Run("datePtr", func(t *testing.T) {
		t.Parallel()
		if datePtr(pgtype.Date{}) != nil {
			t.Fatal("datePtr(invalid) should be nil")
		}
		ts := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
		if got := datePtr(pgtype.Date{Time: ts, Valid: true}); got == nil || !got.Equal(ts) {
			t.Fatalf("datePtr(valid) = %v, want %v", got, ts)
		}
	})
	t.Run("pgts", func(t *testing.T) {
		t.Parallel()
		ts := time.Now().UTC()
		got := pgts(ts)
		if !got.Valid || !got.Time.Equal(ts) {
			t.Fatalf("pgts = %+v", got)
		}
	})
	t.Run("pgUUIDPtr", func(t *testing.T) {
		t.Parallel()
		if pgUUIDPtr(nil).Valid {
			t.Fatal("pgUUIDPtr(nil) should be invalid")
		}
		u := uuid.New()
		got := pgUUIDPtr(&u)
		if !got.Valid || uuid.UUID(got.Bytes) != u {
			t.Fatalf("pgUUIDPtr(&u) = %+v", got)
		}
	})
}

func TestDeriveAckToken(t *testing.T) {
	t.Parallel()
	u := "user-42"
	pv := "policy-version-7"
	day := time.Date(2026, 6, 4, 9, 30, 0, 0, time.UTC)

	tok := DeriveAckToken(u, pv, day)
	if len(tok) != len("ack-")+32 {
		t.Fatalf("token length = %d, want %d (ack- + 32 hex)", len(tok), len("ack-")+32)
	}
	if tok[:4] != "ack-" {
		t.Fatalf("token = %q, want ack- prefix", tok)
	}
	// Determinism: same inputs → same token, even across a time-of-day change
	// within the same UTC day (day-bucketing).
	sameDayLater := time.Date(2026, 6, 4, 23, 59, 59, 0, time.UTC)
	if DeriveAckToken(u, pv, sameDayLater) != tok {
		t.Fatal("token must be stable within the same UTC day")
	}
	// Next day → different token.
	nextDay := time.Date(2026, 6, 5, 0, 0, 1, 0, time.UTC)
	if DeriveAckToken(u, pv, nextDay) == tok {
		t.Fatal("token must change across UTC day boundary")
	}
	// Different user → different token.
	if DeriveAckToken("user-99", pv, day) == tok {
		t.Fatal("token must depend on user id")
	}
	// Different policy version → different token.
	if DeriveAckToken(u, "other-version", day) == tok {
		t.Fatal("token must depend on policy version id")
	}
}

func TestRolesIntersect(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		caller   AckCaller
		required []string
		want     bool
	}{
		{"admin wildcard", AckCaller{IsAdmin: true}, []string{"x"}, true},
		{"admin with no required still true", AckCaller{IsAdmin: true}, nil, true},
		{"empty required → no match", AckCaller{OwnerRoles: []string{"a"}}, nil, false},
		{"empty owner → no match", AckCaller{}, []string{"a"}, false},
		{"hit", AckCaller{OwnerRoles: []string{"b", "a"}}, []string{"a"}, true},
		{"miss", AckCaller{OwnerRoles: []string{"c"}}, []string{"a", "b"}, false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := rolesIntersect(tc.caller, tc.required); got != tc.want {
				t.Fatalf("rolesIntersect(%+v, %v) = %v, want %v", tc.caller, tc.required, got, tc.want)
			}
		})
	}
}

// TestAckStore_PreTxGuards asserts the AckStore rejects malformed identity
// BEFORE opening a transaction. A nil pool proves no DB call is made: if any
// of these reached s.inTx → pool.Begin it would panic on the nil pool. This
// is the security-relevant deny path (missing/invalid caller identity) the
// slice asks to assert without faking the DB (P0-426-4).
func TestAckStore_PreTxGuards(t *testing.T) {
	t.Parallel()
	s := NewAckStore(nil) // nil pool: any tx attempt panics — none should occur
	ctx := context.Background()

	t.Run("Record missing policy id", func(t *testing.T) {
		t.Parallel()
		_, err := s.Record(ctx, RecordInput{Caller: AckCaller{UserID: uuid.NewString()}})
		if !errors.Is(err, ErrAckMissingPolicyID) {
			t.Fatalf("Record(nil policy) = %v, want ErrAckMissingPolicyID", err)
		}
	})
	t.Run("Record missing user id", func(t *testing.T) {
		t.Parallel()
		_, err := s.Record(ctx, RecordInput{PolicyID: uuid.New()})
		if !errors.Is(err, ErrAckMissingUser) {
			t.Fatalf("Record(no user) = %v, want ErrAckMissingUser", err)
		}
	})
	t.Run("Record non-UUID user id", func(t *testing.T) {
		t.Parallel()
		_, err := s.Record(ctx, RecordInput{PolicyID: uuid.New(), Caller: AckCaller{UserID: "not-a-uuid"}})
		if err == nil || errors.Is(err, ErrAckMissingUser) {
			t.Fatalf("Record(bad uuid) = %v, want a parse error", err)
		}
	})
	t.Run("PendingForUser missing user id", func(t *testing.T) {
		t.Parallel()
		_, err := s.PendingForUser(ctx, AckCaller{})
		if !errors.Is(err, ErrAckMissingUser) {
			t.Fatalf("PendingForUser(no user) = %v, want ErrAckMissingUser", err)
		}
	})
	t.Run("PendingForUser non-UUID user id", func(t *testing.T) {
		t.Parallel()
		_, err := s.PendingForUser(ctx, AckCaller{UserID: "nope"})
		if err == nil || errors.Is(err, ErrAckMissingUser) {
			t.Fatalf("PendingForUser(bad uuid) = %v, want a parse error", err)
		}
	})
}

// TestAckStore_inTx_TenantGuards asserts inTx rejects a missing/invalid
// tenant context before touching the (nil) pool.
func TestAckStore_inTx_TenantGuards(t *testing.T) {
	t.Parallel()
	s := NewAckStore(nil)
	t.Run("no tenant in context", func(t *testing.T) {
		t.Parallel()
		err := s.inTx(context.Background(), func(context.Context, *dbx.Queries, uuid.UUID) error { return nil })
		if err == nil {
			t.Fatal("inTx with no tenant context should error before begin")
		}
	})
	t.Run("non-uuid tenant in context", func(t *testing.T) {
		t.Parallel()
		ctx, err := tenancy.WithTenant(context.Background(), "not-a-uuid")
		if err != nil {
			// tenancy.WithTenant itself may reject the bad id; that is also a
			// valid guard — either way no nil-pool deref happens.
			return
		}
		if e := s.inTx(ctx, func(context.Context, *dbx.Queries, uuid.UUID) error { return nil }); e == nil {
			t.Fatal("inTx with non-uuid tenant should error before begin")
		}
	})
}

func TestAckStore_WithClock(t *testing.T) {
	t.Parallel()
	fixed := time.Date(2026, 6, 4, 12, 0, 0, 0, time.UTC)
	s := NewAckStore(nil).WithClock(func() time.Time { return fixed })
	if got := s.now(); !got.Equal(fixed) {
		t.Fatalf("now() with injected clock = %v, want %v", got, fixed)
	}
	// Default clock (no injection) returns a UTC time.
	def := NewAckStore(nil)
	if loc := def.now().Location(); loc != time.UTC {
		t.Fatalf("default now() location = %v, want UTC", loc)
	}
}

func TestAcknowledgmentFreshness_PinnedToOneYear(t *testing.T) {
	t.Parallel()
	if AcknowledgmentFreshness != 365*24*time.Hour {
		t.Fatalf("AcknowledgmentFreshness = %v, want 365d", AcknowledgmentFreshness)
	}
}
