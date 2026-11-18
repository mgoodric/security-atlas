// #2483: RolesFor reads $3 (the users(id) behind the deactivation guard)
// through identity.UserID, which accepts only the canonical uuid form. A
// uuid-shaped but non-canonical key left $3 NULL, so the guard never fired
// while user_id = $2 could still match a row stored in exactly that text.
// RolesFor now returns no roles for such a key without querying.

package authz_test

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/authz"
)

// nonCanonical2483 is every uuid-shaped spelling of u that is not its
// canonical form, bare and as a "user:" subject, plus the nil uuid, which
// parses but names no user either.
func nonCanonical2483(u uuid.UUID) []string {
	s := u.String()
	forms := []string{
		strings.ToUpper(s),
		"{" + s + "}",
		"urn:uuid:" + s,
		strings.ReplaceAll(s, "-", ""),
		uuid.Nil.String(),
	}
	for _, f := range forms[:len(forms)-1] {
		forms = append(forms, "user:"+f)
	}
	return forms
}

func TestDBRolesResolver2483_NonCanonicalKeyFailsClosed(t *testing.T) {
	t.Parallel()
	// pgxpool.New connects lazily; the address is unreachable, so any key
	// that reaches the query surfaces an error, and a key that short-circuits
	// returns (nil, nil) without touching it.
	pool, err := pgxpool.New(context.Background(), "postgres://nobody@127.0.0.1:1/none?connect_timeout=1")
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	t.Cleanup(pool.Close)
	r := authz.NewDBRolesResolver(pool)
	tenant := uuid.NewString()
	u := uuid.New()

	for _, key := range nonCanonical2483(u) {
		got, err := r.RolesFor(context.Background(), tenant, key)
		if err != nil || got != nil {
			t.Errorf("RolesFor(%q) = %v, %v; want nil, nil without a query", key, got, err)
		}
	}
	// The canonical form, and a non-uuid key, still go to the database.
	for _, key := range []string{u.String(), "user:" + u.String(), "key_x2483"} {
		if _, err := r.RolesFor(context.Background(), tenant, key); err == nil {
			t.Errorf("RolesFor(%q) returned no error; want it to reach the (unreachable) pool", key)
		}
	}
}
