//go:build integration

// #2381 — a real-login viewer or auditor ("user:<uuid>" subject, role claim
// copied into OwnerRoles by jwtmw) is refused by the authz middleware on the
// evidence, artifact and walkthrough write routes. Before the fix BuildInput
// derived control_owner from the role claim and OPA let the request through
// to the handler. A control_owner session still passes the authz gate.
//
// The walkthrough handler has its own admin/grc_engineer check that also
// answers 403, so the test tells the two apart by the body: the authz
// middleware answers {"error":"forbidden"}.
//
// Requires DATABASE_URL_APP (atlas_app, RLS) and DATABASE_URL (migrate role).
package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api"
	"github.com/mgoodric/security-atlas/internal/api/testjwt"
	"github.com/mgoodric/security-atlas/internal/artifact"
	"github.com/mgoodric/security-atlas/internal/authz"
	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/evidence/ingest"
)

// unreachableS3 mounts the artifact routes without MinIO. The authz gate
// decides before the handler touches it.
type unreachableS3 struct{}

var errUnreachable2381 = errors.New("s3 not wired in this test")

func (unreachableS3) PutObject(context.Context, *s3.PutObjectInput, ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
	return nil, errUnreachable2381
}

func (unreachableS3) DeleteObject(context.Context, *s3.DeleteObjectInput, ...func(*s3.Options)) (*s3.DeleteObjectOutput, error) {
	return nil, errUnreachable2381
}

func (unreachableS3) PresignGetObject(context.Context, string, time.Duration) (string, error) {
	return "", errUnreachable2381
}

// noSchemas2381 satisfies ingest.SchemaValidator; no push reaches it.
type noSchemas2381 struct{}

func (noSchemas2381) ValidatePayload(context.Context, string, string, string, []byte) error {
	return nil
}
func (noSchemas2381) IsRegistered(string, string) bool { return false }

func TestJWTReadOnlyRoles2381_WritesRefused(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := dbtest.SeedTenant(t, admin)
	tid := uuid.MustParse(tenant)

	engine, err := authz.NewEngine(context.Background(), authz.NewDBRolesResolver(app))
	if err != nil {
		t.Fatalf("authz.NewEngine: %v", err)
	}
	store, err := artifact.NewStore(artifact.Config{Pool: app, S3: unreachableS3{}, Presigner: unreachableS3{}, Bucket: "unused-2381"})
	if err != nil {
		t.Fatalf("artifact.NewStore: %v", err)
	}
	srv := api.New(api.Config{ArtifactStore: store, IngestService: ingest.New(app, noSchemas2381{})})
	srv.AttachDB(app)
	srv.AttachAuthz(engine, authz.NewAuditWriter(app))

	bearer := func(role string) string {
		u := seedPeriodUser(t, admin, tenant, "active", role)
		c := testjwt.OwnerFor(tid, []string{role})
		c.Subject = "user:" + u.String()
		return srv.IssueTestJWT(t, c)
	}
	viewer, auditor, owner := bearer("viewer"), bearer("auditor"), bearer("control_owner")

	ts := httptest.NewServer(srv.HTTPHandlerForTests())
	t.Cleanup(ts.Close)

	// post reports the status and whether the authz middleware refused it.
	post := func(bearer, path string) (int, bool) {
		req, err := http.NewRequest(http.MethodPost, ts.URL+path, bytes.NewReader([]byte(`{}`)))
		if err != nil {
			t.Fatalf("new request: %v", err)
		}
		req.Header.Set("Authorization", "Bearer "+bearer)
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("POST %s: %v", path, err)
		}
		defer func() { _ = resp.Body.Close() }()
		var body struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&body)
		return resp.StatusCode, resp.StatusCode == http.StatusForbidden && body.Error == "forbidden"
	}

	for _, path := range []string{"/v1/evidence:push", "/v1/artifacts:upload", "/v1/walkthroughs"} {
		for name, b := range map[string]string{"viewer": viewer, "auditor": auditor} {
			if got, byAuthz := post(b, path); !byAuthz {
				t.Errorf("JWT %s POST %s: %d, not refused by authz; want 403 forbidden", name, path, got)
			}
		}
		if got, byAuthz := post(owner, path); byAuthz || got == http.StatusNotFound {
			t.Errorf("JWT control_owner POST %s: %d; want past the authz gate", path, got)
		}
	}
}
