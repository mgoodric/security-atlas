//go:build integration

// security-atlas#2435: a key's scope_predicate is stored as JSONB and must
// be evaluated as the scope grammar (internal/scope) at push time. These
// tests issue keys through the real admin credentials API, resolve the
// bearer back out of api_keys, and push with that credential, so the
// predicate takes the same round trip it takes in production.

package ingest_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	evidencev1 "github.com/mgoodric/security-atlas/gen/proto/evidence/v1"
	"github.com/mgoodric/security-atlas/internal/api/admincreds"
	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/api/tenancymw"
	"github.com/mgoodric/security-atlas/internal/auth/apikeystore"
	"github.com/mgoodric/security-atlas/internal/auth/bearer"
	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/evidence/ingest"
)

// issueViaAdminAPI POSTs /v1/admin/credentials as a tenant admin with the
// given raw JSON body and returns the HTTP status and, on 201, the
// credential the minted bearer authenticates to.
func issueViaAdminAPI(t *testing.T, tenantID, body string) (int, credstore.Credential) {
	t.Helper()
	hasher, err := bearer.NewHasher([]byte("atlas-2435-test-hash-key-32bytes"))
	if err != nil {
		t.Fatalf("NewHasher: %v", err)
	}
	store := apikeystore.NewStore(dbtest.NewAppPool(t), dbtest.NewMigratePool(t), hasher, time.Hour)
	store.SetPrefix(bearer.PrefixTest)
	h := admincreds.New(store)

	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ctx := authctx.WithCredential(req.Context(), credstore.Credential{
				ID: "key_test_admin", TenantID: tenantID, IsAdmin: true,
			})
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	})
	r.Use(tenancymw.Middleware)
	r.Post("/v1/admin/credentials", h.Issue)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/admin/credentials", bytes.NewReader([]byte(body))))
	if w.Code != http.StatusCreated {
		return w.Code, credstore.Credential{}
	}
	var resp admincreds.IssueResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode issue response: %v", err)
	}
	cred, err := store.Authenticate(context.Background(), resp.BearerToken)
	if err != nil {
		t.Fatalf("Authenticate minted bearer: %v", err)
	}
	return w.Code, cred
}

func withScope(dims map[string][]string) recordOpt {
	return func(r *evidencev1.EvidenceRecord) {
		r.Scope = nil
		for k, vs := range dims {
			r.Scope = append(r.Scope, &evidencev1.ScopeDimension{Key: k, Values: vs})
		}
	}
}

// The admin form's default: no scope_predicate at all, stored as the
// column default '{}'. That key must push.
func TestScopePredicate_DefaultKeyPushes(t *testing.T) {
	svc, _, _ := boot(t)
	for name, body := range map[string]string{
		"omitted":      `{}`,
		"empty string": `{"scope_predicate":""}`,
		"empty object": `{"scope_predicate":"{}"}`,
	} {
		t.Run(name, func(t *testing.T) {
			code, cred := issueViaAdminAPI(t, tenantA, body)
			if code != http.StatusCreated {
				t.Fatalf("issue status = %d, want 201", code)
			}
			_, decision, err := svc.Process(context.Background(), record(t), cred)
			if err != nil || decision != ingest.DecisionAccepted {
				t.Fatalf("push with default-predicate key: decision=%s err=%v (stored predicate %q)", decision, err, cred.ScopePredicate)
			}
		})
	}
}

func cleanupKeys(t *testing.T, tenants ...string) {
	t.Helper()
	admin := dbtest.NewMigratePool(t)
	t.Cleanup(func() {
		for _, tn := range tenants {
			_, _ = admin.Exec(context.Background(), "DELETE FROM api_keys WHERE tenant_id = $1", tn)
		}
	})
}

// Allow and deny per predicate shape, each key issued through the admin
// API and read back from api_keys before the push.
func TestScopePredicate_ShapesThroughAdminAPI(t *testing.T) {
	svc, _, _ := boot(t)
	cleanupKeys(t, tenantA)
	prod := map[string][]string{"environment": {"prod"}, "cloud_account": {"aws:1"}}
	cases := []struct {
		name      string
		predicate string
		dims      map[string][]string
		want      bool
	}{
		{"op true", `{"op":"true"}`, prod, true},
		{"eq match", `{"op":"eq","dim":"environment","value":"prod"}`, prod, true},
		{"eq miss", `{"op":"eq","dim":"environment","value":"staging"}`, prod, false},
		{"in match", `{"op":"in","dim":"cloud_account","values":["aws:1","aws:2"]}`, prod, true},
		{"and miss", `{"op":"and","args":[{"op":"eq","dim":"environment","value":"prod"},{"op":"eq","dim":"cloud_account","value":"aws:9"}]}`, prod, false},
		{"multi-value record, one value outside", `{"op":"eq","dim":"environment","value":"prod"}`, map[string][]string{"environment": {"prod", "staging"}}, false},
		{"legacy key=value is stored as the grammar", "environment=prod", prod, true},
		// Fail closed: a restricted key never pushes evidence that names
		// no scope dimensions. Validation refuses an unscoped record before
		// the predicate runs; scope.TestMatchRecordScope covers the
		// evaluator denying it on its own.
		{"restricted key, unscoped record", `{"op":"eq","dim":"environment","value":"prod"}`, nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body, _ := json.Marshal(map[string]string{"scope_predicate": tc.predicate})
			code, cred := issueViaAdminAPI(t, tenantA, string(body))
			if code != http.StatusCreated {
				t.Fatalf("issue status = %d, want 201", code)
			}
			if !strings.HasPrefix(strings.TrimSpace(cred.ScopePredicate), "{") {
				t.Fatalf("stored predicate %q is not a JSON expression", cred.ScopePredicate)
			}
			_, decision, err := svc.Process(context.Background(), record(t, withScope(tc.dims)), cred)
			if tc.want {
				if err != nil || decision != ingest.DecisionAccepted {
					t.Fatalf("want accepted, got decision=%s err=%v", decision, err)
				}
				return
			}
			if tc.dims == nil {
				if err == nil || decision == ingest.DecisionAccepted {
					t.Fatalf("unscoped record accepted under a restricted key: decision=%s", decision)
				}
				return
			}
			if decision != ingest.DecisionRejectedScopeViolation || !errors.Is(err, ingest.ErrScopeViolation) {
				t.Fatalf("want rejected_scope_violation, got decision=%s err=%v", decision, err)
			}
		})
	}
}

// A predicate that is not a valid expression is refused at issue with a
// 400, never stored and never treated as unrestricted.
func TestScopePredicate_IssueRejectsMalformed(t *testing.T) {
	cleanupKeys(t, tenantA)
	for _, p := range []string{
		`{"environment":"prod"}`,
		`{"op":"xor"}`,
		`{"op":"eq","dim":"environment"}`,
		`not a predicate`,
	} {
		body, _ := json.Marshal(map[string]string{"scope_predicate": p})
		if code, _ := issueViaAdminAPI(t, tenantA, string(body)); code != http.StatusBadRequest {
			t.Errorf("issue with predicate %q: status %d, want 400", p, code)
		}
	}
}

// A malformed predicate that reaches ingest anyway (an in-memory or proto
// caller) is denied with a clear error and an audit row.
func TestScopePredicate_MalformedDeniesAtPush(t *testing.T) {
	svc, pool, _ := boot(t)
	cred := mkCred(tenantA, nil, `{"environment":"prod"}`)
	_, decision, err := svc.Process(context.Background(), record(t), cred)
	if decision != ingest.DecisionRejectedScopeViolation || !errors.Is(err, ingest.ErrScopeViolation) ||
		!strings.Contains(err.Error(), "invalid predicate") {
		t.Fatalf("decision=%s err=%v, want rejected_scope_violation with invalid predicate", decision, err)
	}
	rows := listAuditEntries(t, pool, tenantA, cred.ID)
	if len(rows) != 1 || !strings.Contains(rows[0].ReasonCode, "is invalid") {
		t.Fatalf("audit rows = %+v, want one invalid-predicate row", rows)
	}
}

// Restart: keys minted into the in-memory credstore persist to api_keys
// and a fresh credstore loads them back with the stored predicate. The
// unrestricted key still pushes, and the restricted one still restricts.
func TestScopePredicate_SurvivesRestart(t *testing.T) {
	svc, _, _ := boot(t)
	cleanupKeys(t, tenantA)
	hasher, err := bearer.NewHasher([]byte("atlas-2435-test-hash-key-32bytes"))
	if err != nil {
		t.Fatalf("NewHasher: %v", err)
	}
	app, migrate := dbtest.NewAppPool(t), dbtest.NewMigratePool(t)

	before := credstore.New(time.Hour)
	if _, err := before.AttachPersistence(apikeystore.NewPersister(app, migrate, hasher)); err != nil {
		t.Fatalf("attach: %v", err)
	}
	_, openTok, err := before.IssueMinted(tenantA, "", nil, 0, "")
	if err != nil {
		t.Fatalf("issue unrestricted: %v", err)
	}
	_, prodTok, err := before.IssueMinted(tenantA, `{"op":"eq","dim":"environment","value":"prod"}`, nil, 0, "")
	if err != nil {
		t.Fatalf("issue restricted: %v", err)
	}

	after := credstore.New(time.Hour)
	if _, err := after.AttachPersistence(apikeystore.NewPersister(app, migrate, hasher)); err != nil {
		t.Fatalf("reload: %v", err)
	}
	open, err := after.Authenticate(openTok)
	if err != nil {
		t.Fatalf("authenticate reloaded unrestricted key: %v", err)
	}
	if open.ScopePredicate != "{}" {
		t.Fatalf("reloaded predicate = %q, want the stored default {}", open.ScopePredicate)
	}
	if _, d, err := svc.Process(context.Background(), record(t), open); err != nil || d != ingest.DecisionAccepted {
		t.Fatalf("reloaded unrestricted key: decision=%s err=%v", d, err)
	}
	prod, err := after.Authenticate(prodTok)
	if err != nil {
		t.Fatalf("authenticate reloaded restricted key: %v", err)
	}
	staging := record(t, withScope(map[string][]string{"environment": {"staging"}}))
	if _, d, _ := svc.Process(context.Background(), staging, prod); d != ingest.DecisionRejectedScopeViolation {
		t.Fatalf("reloaded restricted key pushed staging: decision=%s", d)
	}
}

// The predicate is evaluated against the pushed record only, and the record
// lands in the key's own tenant: a tenant-B key scoped to B's account
// cannot push A's account, and what it does push never shows up in A.
func TestScopePredicate_CrossTenant(t *testing.T) {
	svc, pool, _ := boot(t)
	cleanupKeys(t, tenantA, tenantB)
	body, _ := json.Marshal(map[string]string{
		"scope_predicate": `{"op":"eq","dim":"cloud_account","value":"aws:B"}`,
	})
	code, credB := issueViaAdminAPI(t, tenantB, string(body))
	if code != http.StatusCreated {
		t.Fatalf("issue status = %d", code)
	}
	if credB.TenantID != tenantB {
		t.Fatalf("key tenant = %s, want %s", credB.TenantID, tenantB)
	}

	recA := record(t, withScope(map[string][]string{"cloud_account": {"aws:A"}}))
	if _, d, _ := svc.Process(context.Background(), recA, credB); d != ingest.DecisionRejectedScopeViolation {
		t.Fatalf("B key pushed A's account: decision=%s", d)
	}
	recB := record(t, withScope(map[string][]string{"cloud_account": {"aws:B"}}))
	if _, d, err := svc.Process(context.Background(), recB, credB); err != nil || d != ingest.DecisionAccepted {
		t.Fatalf("B key pushing B's account: decision=%s err=%v", d, err)
	}
	if n := countEvidenceRecords(t, pool, tenantA); n != 0 {
		t.Fatalf("tenant A rows = %d, want 0", n)
	}
	if n := countEvidenceRecords(t, pool, tenantB); n != 1 {
		t.Fatalf("tenant B rows = %d, want 1", n)
	}
}
