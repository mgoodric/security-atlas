//go:build integration

// #2286 — an overlay edge's author reads as a name, not a credential id:
// in the list, the coverage wires and the export. A user reads as their
// display name, a key as "API key …<last4 of its id>", "system" as
// "System", and another tenant's user or key as "Unknown": the names come
// from the caller tenant's people under RLS.

package mappingoverlays_test

import (
	"context"
	"crypto/sha256"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api"
	"github.com/mgoodric/security-atlas/internal/api/testjwt"
	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/mappingoverlay"
)

func seedAuthorUser(t *testing.T, admin *pgxpool.Pool, tenant, displayName string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := admin.Exec(context.Background(),
		`INSERT INTO users (id, tenant_id, email, display_name, status) VALUES ($1, $2, $3, $4, 'active')`,
		id, tenant, id.String()+"@example.invalid", displayName); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	t.Cleanup(func() {
		if _, err := admin.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, id); err != nil {
			t.Logf("cleanup user: %v", err)
		}
	})
	return id
}

func seedAuthorKey(t *testing.T, admin *pgxpool.Pool, tenant string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	hash := sha256.Sum256([]byte(id.String()))
	if _, err := admin.Exec(context.Background(),
		`INSERT INTO api_keys (id, tenant_id, token_hash, last4) VALUES ($1, $2, $3, 'zz99')`,
		id, tenant, hash[:]); err != nil {
		t.Fatalf("seed api key: %v", err)
	}
	t.Cleanup(func() {
		if _, err := admin.Exec(context.Background(), `DELETE FROM api_keys WHERE id = $1`, id); err != nil {
			t.Logf("cleanup api key: %v", err)
		}
	})
	return id
}

func TestOverlayAuthor2286_NamedFromOwnTenantOnly(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	// The name starts with "=": the export must neutralise it.
	alice := seedAuthorUser(t, f.admin, f.tenantA.tenant, "=Alice 2286")
	keyA := seedAuthorKey(t, f.admin, f.tenantA.tenant)
	bob := seedAuthorUser(t, f.admin, f.tenantB.tenant, "Bob 2286 Other Tenant")
	keyB := seedAuthorKey(t, f.admin, f.tenantB.tenant)

	// Alice signs in: her session records her users(id), not "jwt:<jti>".
	srv := api.New(api.Config{})
	srv.AttachDB(dbtest.NewAppPool(t))
	claims := testjwt.AdminFor(uuid.MustParse(f.tenantA.tenant))
	claims.Subject = "user:" + alice.String()
	bearer := srv.IssueTestJWT(t, claims)
	ts := httptest.NewServer(srv.HTTPHandlerForTests())
	t.Cleanup(ts.Close)
	aliceEnv := env{url: ts.URL, tenant: f.tenantA.tenant, admin: bearer, viewer: bearer}

	status, body := call(t, aliceEnv, http.MethodPost, "/v1/mapping-overlays", bearer, f.body("C2", "IAC-06"))
	if status != http.StatusCreated {
		t.Fatalf("create as alice: %d %v", status, body)
	}
	created := edgeOf(t, body)
	if created["created_by"] != alice.String() || created["created_by_name"] != "=Alice 2286" {
		t.Fatalf("alice's edge created_by = %v, created_by_name = %v; want her id and name", created["created_by"], created["created_by_name"])
	}

	// Edges recorded under a tenant A key, tenant B's key and user, and the
	// legacy "system" actor.
	insert := func(code, anchor, actor string) {
		t.Helper()
		if _, err := f.admin.Exec(ctx, `INSERT INTO fw_to_scf_overlay_edges (
				tenant_id, framework_requirement_id, scf_anchor_id,
				relationship_type, strength, rationale, source_ref, created_by)
			VALUES ($1, $2, $3, 'intersects_with', 0.5, 'direct insert', 'test', $4)`,
			f.tenantA.tenant, f.requirement[code], f.anchorID(t, anchor), actor); err != nil {
			t.Fatalf("insert %s -> %s by %s: %v", code, anchor, actor, err)
		}
	}
	insert("C2", "CRY-03", "key_"+keyA.String())
	insert("C3", "CRY-03", "key_"+keyB.String())
	insert("C1", "CRY-03", bob.String())
	insert("C3", "IAC-06", "system")

	keyAName := "API key …" + keyA.String()[len(keyA.String())-4:]
	want := map[string]string{
		alice.String():         "=Alice 2286",
		"key_" + keyA.String(): keyAName,
		"key_" + keyB.String(): "Unknown",
		bob.String():           "Unknown",
		"system":               "System",
	}

	t.Run("list", func(t *testing.T) {
		got := map[string]string{}
		for _, e := range list(t, f.tenantA, "") {
			got[e["created_by"].(string)] = e["created_by_name"].(string)
		}
		for actor, name := range want {
			if got[actor] != name {
				t.Errorf("list: author %s reads %q; want %q", actor, got[actor], name)
			}
		}
	})

	t.Run("export", func(t *testing.T) {
		recs := exportCSV(t, f.tenantA, "")
		if !slices.Equal(recs[0], mappingoverlay.ExportColumns) {
			t.Fatalf("header = %v", recs[0])
		}
		nameCol := slices.Index(recs[0], "created_by")
		idCol := slices.Index(recs[0], "created_by_id")
		got := map[string]string{}
		for _, rec := range recs[1:] {
			got[rec[idCol]] = rec[nameCol]
			for _, cell := range rec {
				if strings.Contains(cell, "Bob 2286") {
					t.Errorf("tenant A's export names tenant B's user: %v", rec)
				}
			}
		}
		for actor, name := range want {
			if actor == alice.String() {
				// The formula-introducing name is neutralised.
				name = "'" + name
			}
			if got[actor] != name {
				t.Errorf("export: created_by_id %s has created_by %q; want %q", actor, got[actor], name)
			}
		}
	})

	t.Run("coverage", func(t *testing.T) {
		anchors := decode(t, rawGet(t, f.tenantA, "/v1/requirements/"+f.requirement["C2"].String()+"/coverage"))["anchors"]
		for anchor, name := range map[string]string{"IAC-06": "=Alice 2286", "CRY-03": keyAName} {
			a := pathTo(anchors, "scf_id", anchor)
			ov, _ := a["overlay"].(map[string]any)
			if ov["created_by_name"] != name {
				t.Errorf("C2 coverage via %s: overlay = %v; want created_by_name %q", anchor, ov, name)
			}
		}
		reqs := decode(t, rawGet(t, f.tenantA, "/v1/anchors/"+f.anchorID(t, "IAC-06").String()+"/requirements"))["requirements"]
		for code, name := range map[string]string{"C2": "=Alice 2286", "C3": "System"} {
			r := pathTo(reqs, "code", code)
			ov, _ := r["overlay"].(map[string]any)
			if ov["created_by_name"] != name {
				t.Errorf("IAC-06 traversal %s: overlay = %v; want created_by_name %q", code, ov, name)
			}
		}
		if b := string(rawGet(t, f.tenantA, "/v1/anchors/"+f.anchorID(t, "CRY-03").String()+"/requirements")); strings.Contains(b, "Bob 2286") {
			t.Errorf("tenant A's traversal names tenant B's user: %s", b)
		}
	})
}
