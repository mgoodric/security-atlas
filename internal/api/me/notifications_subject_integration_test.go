//go:build integration

// #2183 — in-app notifications under the subject the real login path mints.
//
// Local login, PKCE and the device grant issue Subject = "user:<uuid>", and
// jwtmw bridges it verbatim into cred.UserID. Audit-note replies addressed
// their notification to that raw string while staleness, offboarding and the
// digest channels address the bare uuid, and /v1/me/notifications matched
// recipient_user_id against the raw cred.UserID. So a signed-in person missed
// every notification addressed to their bare uuid. Producers now write the
// bare uuid and both endpoints resolve the caller with credstore.CallerUserID.

package me_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api/auditnotes"
	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	meapi "github.com/mgoodric/security-atlas/internal/api/me"
	"github.com/mgoodric/security-atlas/internal/api/tenancymw"
	"github.com/mgoodric/security-atlas/internal/audit/notes"
	"github.com/mgoodric/security-atlas/internal/audit/notifications"
	"github.com/mgoodric/security-atlas/internal/dbtest"
)

// testUserHeader names the caller per request; the stub auth middleware
// turns it into the cred.UserID jwtmw would bridge from the JWT Subject.
const testUserHeader = "X-Test-Cred-User"

func notifSubjectServer(t *testing.T, app *pgxpool.Pool, tenant string) *httptest.Server {
	t.Helper()
	notifStore := notifications.NewStore(app)
	notesH := auditnotes.New(notes.NewStore(app), notifStore, nil)
	notifH := meapi.NewNotifications(notifStore)
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ctx := authctx.WithCredential(req.Context(), credstore.Credential{
				ID:       "jwt:" + req.Header.Get(testUserHeader),
				TenantID: tenant,
				UserID:   req.Header.Get(testUserHeader),
			})
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	})
	r.Use(tenancymw.Middleware)
	r.Post("/v1/audit-notes", notesH.Create)
	r.Get("/v1/me/notifications", notifH.List)
	r.Patch("/v1/me/notifications/{id}/read", notifH.MarkRead)
	ts := httptest.NewServer(r)
	t.Cleanup(ts.Close)
	return ts
}

func seedNotesPeriod(t *testing.T, admin *pgxpool.Pool, tenant string) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	fwID, versionID, periodID := uuid.New(), uuid.New(), uuid.New()
	if _, err := admin.Exec(ctx, `
		INSERT INTO frameworks (id, tenant_id, name, slug, issuer)
		VALUES ($1, NULL, '#2183 framework', $2, 'test')`,
		fwID, "fw-2183-"+uuid.NewString()[:8]); err != nil {
		t.Fatalf("seed framework: %v", err)
	}
	if _, err := admin.Exec(ctx, `
		INSERT INTO framework_versions (id, tenant_id, framework_id, version)
		VALUES ($1, NULL, $2, '1.0')`, versionID, fwID); err != nil {
		t.Fatalf("seed framework_version: %v", err)
	}
	if _, err := admin.Exec(ctx, `
		INSERT INTO audit_periods (
			id, tenant_id, name, framework_version_id, period_start, period_end,
			status, created_by, created_at, updated_at
		) VALUES ($1, $2, '#2183 period', $3, $4, $5, 'open', 'test-seed', now(), now())`,
		periodID, tenant, versionID,
		time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("seed audit_period: %v", err)
	}
	return periodID
}

func callAs(t *testing.T, method, url, credUser string, body any) (int, map[string]any) {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatalf("encode body: %v", err)
		}
	}
	req, err := http.NewRequestWithContext(context.Background(), method, url, &buf)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(testUserHeader, credUser)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	out := map[string]any{}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func TestNotifications_UserPrefixedSubject_ListAndMarkRead(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := dbtest.SeedTenant(t, admin, "notifications", "audit_notes", "audit_periods")
	periodID := seedNotesPeriod(t, admin, tenant)
	ts := notifSubjectServer(t, app, tenant)

	alice, bob := uuid.New(), uuid.New()
	aliceCred, bobCred := "user:"+alice.String(), "user:"+bob.String()

	// Alice opens a shared thread; Bob replies, which notifies Alice.
	status, body := callAs(t, http.MethodPost, ts.URL+"/v1/audit-notes", aliceCred, map[string]any{
		"audit_period_id": periodID.String(), "scope_type": "finding", "scope_id": "fnd-2183",
		"body": "Root note", "visibility": "shared",
	})
	if status != http.StatusCreated {
		t.Fatalf("alice create: status %d body=%v", status, body)
	}
	rootID, _ := body["audit_note"].(map[string]any)["id"].(string)
	status, body = callAs(t, http.MethodPost, ts.URL+"/v1/audit-notes", bobCred, map[string]any{
		"audit_period_id": periodID.String(), "scope_type": "finding", "scope_id": "fnd-2183",
		"body": "Reply", "visibility": "shared", "parent_note_id": rootID,
	})
	if status != http.StatusCreated {
		t.Fatalf("bob reply: status %d body=%v", status, body)
	}

	// The producer addressed Alice's bare users(id), the form the digest
	// channels read.
	var recipient string
	if err := admin.QueryRow(context.Background(),
		`SELECT recipient_user_id FROM notifications WHERE tenant_id = $1`, tenant).Scan(&recipient); err != nil {
		t.Fatalf("read notification row: %v", err)
	}
	if recipient != alice.String() {
		t.Fatalf("recipient_user_id = %q; want bare uuid %q", recipient, alice)
	}

	// Alice, signed in as "user:<uuid>", sees it.
	status, body = callAs(t, http.MethodGet, ts.URL+"/v1/me/notifications", aliceCred, nil)
	if status != http.StatusOK {
		t.Fatalf("alice list: status %d body=%v", status, body)
	}
	list, _ := body["notifications"].([]any)
	if len(list) != 1 || body["unread_count"] != float64(1) {
		t.Fatalf("alice list = %d notifications, unread %v; want 1 and 1 (body=%v)", len(list), body["unread_count"], body)
	}
	notifID, _ := list[0].(map[string]any)["id"].(string)

	// Bob was the author of the reply, so he has nothing.
	if _, body = callAs(t, http.MethodGet, ts.URL+"/v1/me/notifications", bobCred, nil); body["count"] != float64(0) {
		t.Fatalf("bob list count = %v; want 0", body["count"])
	}
	// Bob cannot mark Alice's notification read.
	if status, _ = callAs(t, http.MethodPatch, ts.URL+"/v1/me/notifications/"+notifID+"/read", bobCred, nil); status != http.StatusNotFound {
		t.Fatalf("bob mark-read alice's notification: status %d; want 404", status)
	}

	// Alice marks it read.
	status, body = callAs(t, http.MethodPatch, ts.URL+"/v1/me/notifications/"+notifID+"/read", aliceCred, nil)
	if status != http.StatusOK {
		t.Fatalf("alice mark-read: status %d body=%v", status, body)
	}
	if readAt := body["notification"].(map[string]any)["read_at"]; readAt == nil {
		t.Fatalf("mark-read returned no read_at: %v", body)
	}
	if _, body = callAs(t, http.MethodGet, ts.URL+"/v1/me/notifications", aliceCred, nil); body["unread_count"] != float64(0) {
		t.Fatalf("alice unread after mark-read = %v; want 0", body["unread_count"])
	}
}

// A notification addressed by an existing producer (staleness, offboarding)
// to the bare uuid reaches the "user:<uuid>" caller, and a caller that is no
// user gets an empty list rather than someone's inbox.
func TestNotifications_BareUUIDProducerReachesUserSubject(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := dbtest.SeedTenant(t, admin, "notifications")
	ts := notifSubjectServer(t, app, tenant)
	user := uuid.New()

	if _, err := admin.Exec(context.Background(), `
		INSERT INTO notifications (id, tenant_id, recipient_user_id, type, payload)
		VALUES (gen_random_uuid(), $1, $2, 'staleness_alert', '{}'::jsonb)`, tenant, user.String()); err != nil {
		t.Fatalf("seed notification: %v", err)
	}

	_, body := callAs(t, http.MethodGet, ts.URL+"/v1/me/notifications", "user:"+user.String(), nil)
	if body["count"] != float64(1) {
		t.Fatalf("user: subject list count = %v; want 1 (body=%v)", body["count"], body)
	}
	for _, nonUser := range []string{"bootstrap-admin", "oauth_client:ci-runner"} {
		status, body := callAs(t, http.MethodGet, ts.URL+"/v1/me/notifications", nonUser, nil)
		if status != http.StatusOK || body["count"] != float64(0) {
			t.Errorf("%s list: status %d count %v; want 200 and 0", nonUser, status, body["count"])
		}
	}
}
