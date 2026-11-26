package people

// Directory names an actor on the server (#2286), for a response or an
// export that must carry a readable author rather than a credential id.
// It is built from the same tenant-scoped read as GET /v1/people (list),
// so a page's <Person> and a server-side name come from one lookup.

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/identity"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// The names Directory.Name returns for actors that are not a named user.
const (
	NameSystem     = "System"
	NameUnknown    = "Unknown"
	NameFormerUser = "Former user"
	apiKeyPrefix   = "API key …"
)

// Directory is one tenant's users and API keys, keyed for Name.
type Directory struct {
	users map[uuid.UUID]User
	keys  map[uuid.UUID]bool
}

// NewDirectory indexes a people list.
func NewDirectory(r Response) *Directory {
	d := &Directory{users: map[uuid.UUID]User{}, keys: map[uuid.UUID]bool{}}
	for _, u := range r.Users {
		if id, ok := identity.UserID(u.ID); ok {
			d.users[id] = u
		}
	}
	for _, k := range r.APIKeys {
		if id, err := uuid.Parse(k.ID); err == nil {
			d.keys[id] = true
		}
	}
	return d
}

// Load reads the context tenant's directory on pool. The read runs under
// that tenant's RLS binding, so another tenant's users and keys are never
// in it and Name never resolves them.
func Load(ctx context.Context, pool *pgxpool.Pool) (*Directory, error) {
	t, err := tenancy.TenantFromContext(ctx)
	if err != nil {
		return nil, err
	}
	tenantID, err := uuid.Parse(t)
	if err != nil {
		return nil, fmt.Errorf("people: tenant %q: %w", t, err)
	}
	r, err := list(ctx, pool, tenantID)
	if err != nil {
		return nil, err
	}
	return NewDirectory(r), nil
}

// Name is the readable name of a recorded actor:
//
//   - a user of this tenant (a users(id) uuid or "user:<uuid>"): their
//     display name, or "Former user" once disabled;
//   - an API key of this tenant ("key_<uuid>"): "API key …" and the last
//     four characters of the key's id. The id is not secret; the token,
//     its prefix and its last four are never used;
//   - "system", or "system:<process>" (the SCF import's carry-forward
//     records "system:scf-import", #2554): "System";
//   - anything else, including another tenant's user or key, a per-token
//     "jwt:" id, and a nil Directory: "Unknown".
func (d *Directory) Name(actor string) string {
	actor = strings.TrimSpace(actor)
	if actor == "system" || strings.HasPrefix(actor, "system:") {
		return NameSystem
	}
	if d == nil {
		return NameUnknown
	}
	if id, ok := identity.UserID(actor); ok {
		u, found := d.users[id]
		switch {
		case !found:
			return NameUnknown
		case u.Status == "disabled":
			return NameFormerUser
		case strings.TrimSpace(u.DisplayName) != "":
			return strings.TrimSpace(u.DisplayName)
		}
		return NameUnknown
	}
	if raw, ok := strings.CutPrefix(actor, "key_"); ok {
		if id, err := uuid.Parse(raw); err == nil && id.String() == raw && d.keys[id] {
			return apiKeyPrefix + raw[len(raw)-4:]
		}
	}
	return NameUnknown
}
