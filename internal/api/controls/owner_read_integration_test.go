//go:build integration

// #2128 — the control owner "assign owner" writes (slice 468) is read back:
// on the controls list (`GET /v1/controls?include=state`), on control detail
// (`GET /v1/controls/{id}/coverage`), and through the list's `?owner=`
// filter. Tenant B never sees, or filters by, tenant A's assignments.
//
// Real Postgres on the atlas_app (RLS) pool, routes behind credential
// injection + tenancymw, as in owner_assign_integration_test.go.

package controls_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	controlsapi "github.com/mgoodric/security-atlas/internal/api/controls"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/api/tenancymw"
	"github.com/mgoodric/security-atlas/internal/api/ucfcoverage"
	"github.com/mgoodric/security-atlas/internal/control"
	"github.com/mgoodric/security-atlas/internal/dbtest"
)

// ownerReadServer mounts assign-owner, the controls list and control
// detail for one tenant and acting user.
func ownerReadServer(t *testing.T, app *pgxpool.Pool, tenant, userID string) *httptest.Server {
	t.Helper()
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ctx := authctx.WithCredential(req.Context(), credstore.Credential{
				ID:       "test-owner-read",
				TenantID: tenant,
				IsAdmin:  true,
				UserID:   userID,
			})
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	})
	r.Use(tenancymw.Middleware)
	r.Post("/v1/controls/{id}/owner", controlsapi.NewOwnerAssignHandler(app).AssignOwner)
	r.Get("/v1/controls", controlsapi.NewListHandler(control.NewStore(app)).List)
	ucfcoverage.New(app).RegisterRoutes(r)
	ts := httptest.NewServer(r)
	t.Cleanup(ts.Close)
	return ts
}

type ownerListResp struct {
	Controls []struct {
		ID          string  `json:"id"`
		OwnerUserID *string `json:"owner_user_id"`
	} `json:"controls"`
	Count int   `json:"count"`
	Total int64 `json:"total"`
}

func getBody(t *testing.T, url string) (int, []byte) {
	t.Helper()
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, raw
}

func listControls(t *testing.T, ts *httptest.Server, query string) ownerListResp {
	t.Helper()
	status, raw := getBody(t, ts.URL+"/v1/controls?include=state"+query)
	if status != http.StatusOK {
		t.Fatalf("list%s: status %d body=%s", query, status, raw)
	}
	var out ownerListResp
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	return out
}

func detailOwner(t *testing.T, ts *httptest.Server, cid uuid.UUID) (int, string, string) {
	t.Helper()
	status, raw := getBody(t, ts.URL+"/v1/controls/"+cid.String()+"/coverage")
	if status != http.StatusOK {
		return status, "", ""
	}
	var out struct {
		Control struct {
			OwnerUserID string `json:"owner_user_id"`
			OwnerRole   string `json:"owner_role"`
		} `json:"control"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode detail: %v", err)
	}
	return status, out.Control.OwnerUserID, out.Control.OwnerRole
}

func ownerOf(resp ownerListResp, cid uuid.UUID) (string, bool) {
	for _, c := range resp.Controls {
		if c.ID == cid.String() {
			if c.OwnerUserID == nil {
				return "", true
			}
			return *c.OwnerUserID, true
		}
	}
	return "", false
}

func TestControlOwner_ReadableOnListAndDetail_AndFilterable(t *testing.T) {
	migrate := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := dbtest.SeedTenant(t, migrate, ownerAssignCleanupTables()...)

	owner := seedUser(t, migrate, tenant)
	actor := seedUser(t, migrate, tenant)
	mine := seedControl(t, migrate, tenant)
	other := seedControl(t, migrate, tenant)
	ts := ownerReadServer(t, app, tenant, actor.String())

	// Before assigning: no owner on list or detail; manifest role intact.
	if got, ok := ownerOf(listControls(t, ts, ""), mine); !ok || got != "" {
		t.Fatalf("before assign: list owner = %q (found %v); want none", got, ok)
	}
	if _, got, role := detailOwner(t, ts, mine); got != "" || role != "control_owner" {
		t.Fatalf("before assign: detail owner = %q role = %q", got, role)
	}

	status, raw := postJSON(t, ts.URL+"/v1/controls/"+mine.String()+"/owner",
		fmt.Sprintf(`{"owner_user_id":%q}`, owner))
	if status != http.StatusOK {
		t.Fatalf("assign: status %d body=%s", status, raw)
	}

	list := listControls(t, ts, "")
	if got, _ := ownerOf(list, mine); got != owner.String() {
		t.Fatalf("list owner = %q; want %s", got, owner)
	}
	if got, _ := ownerOf(list, other); got != "" {
		t.Fatalf("unassigned control has owner %q", got)
	}
	if st, got, role := detailOwner(t, ts, mine); st != http.StatusOK || got != owner.String() || role != "control_owner" {
		t.Fatalf("detail: status %d owner %q role %q; want 200 %s control_owner", st, got, role, owner)
	}

	filtered := listControls(t, ts, "&owner="+owner.String())
	if filtered.Total != 1 || filtered.Count != 1 || filtered.Controls[0].ID != mine.String() {
		t.Fatalf("owner filter: total %d count %d rows %+v; want only %s", filtered.Total, filtered.Count, filtered.Controls, mine)
	}
	if none := listControls(t, ts, "&owner="+actor.String()); none.Total != 0 || none.Count != 0 {
		t.Fatalf("owner filter for a person who owns nothing: total %d count %d", none.Total, none.Count)
	}

	if st, raw := getBody(t, ts.URL+"/v1/controls?include=state&owner=not-a-uuid"); st != http.StatusBadRequest {
		t.Fatalf("malformed owner: status %d body=%s; want 400", st, raw)
	}
}

func TestControlOwner_TenantBCannotSeeOrFilterTenantA(t *testing.T) {
	migrate := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenantA := dbtest.SeedTenant(t, migrate, ownerAssignCleanupTables()...)
	tenantB := dbtest.SeedTenant(t, migrate, ownerAssignCleanupTables()...)

	ownerA := seedUser(t, migrate, tenantA)
	controlA := seedControl(t, migrate, tenantA)
	actorB := seedUser(t, migrate, tenantB)
	controlB := seedControl(t, migrate, tenantB)

	tsA := ownerReadServer(t, app, tenantA, ownerA.String())
	if st, raw := postJSON(t, tsA.URL+"/v1/controls/"+controlA.String()+"/owner",
		fmt.Sprintf(`{"owner_user_id":%q}`, ownerA)); st != http.StatusOK {
		t.Fatalf("tenant A assign: status %d body=%s", st, raw)
	}

	tsB := ownerReadServer(t, app, tenantB, actorB.String())

	// B's list carries none of A's controls or assignments.
	listB := listControls(t, tsB, "")
	if _, ok := ownerOf(listB, controlA); ok {
		t.Fatalf("tenant B list includes tenant A's control")
	}
	if got, _ := ownerOf(listB, controlB); got != "" {
		t.Fatalf("tenant B control shows owner %q; want none", got)
	}
	// Filtering by A's owner from B matches nothing.
	if f := listControls(t, tsB, "&owner="+ownerA.String()); f.Total != 0 || f.Count != 0 {
		t.Fatalf("tenant B filter by tenant A owner: total %d count %d; want 0", f.Total, f.Count)
	}
	// A's control detail is invisible to B.
	if st, _, _ := detailOwner(t, tsB, controlA); st != http.StatusNotFound {
		t.Fatalf("tenant B detail of tenant A control: status %d; want 404", st)
	}
	// B cannot make A's user the owner of B's control.
	if st, raw := postJSON(t, tsB.URL+"/v1/controls/"+controlB.String()+"/owner",
		fmt.Sprintf(`{"owner_user_id":%q}`, ownerA)); st != http.StatusUnprocessableEntity {
		t.Fatalf("tenant B assigns tenant A user: status %d body=%s; want 422", st, raw)
	}
}

func TestControlOwner_AssignRejectsDisabledUser(t *testing.T) {
	migrate := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := dbtest.SeedTenant(t, migrate, ownerAssignCleanupTables()...)

	actor := seedUser(t, migrate, tenant)
	disabled := seedUser(t, migrate, tenant)
	if _, err := migrate.Exec(context.Background(),
		`UPDATE users SET status = 'disabled' WHERE id = $1`, disabled); err != nil {
		t.Fatalf("disable user: %v", err)
	}
	cid := seedControl(t, migrate, tenant)
	ts := ownerReadServer(t, app, tenant, actor.String())

	if st, raw := postJSON(t, ts.URL+"/v1/controls/"+cid.String()+"/owner",
		fmt.Sprintf(`{"owner_user_id":%q}`, disabled)); st != http.StatusUnprocessableEntity {
		t.Fatalf("assign disabled user: status %d body=%s; want 422", st, raw)
	}
}
