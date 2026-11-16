//go:build integration

// #2183 — a local-login / PKCE / device-grant JWT carries Subject
// "user:<uuid>". CreateInput used to parse cred.UserID raw, so a signed-in
// admin got a 400 on every manual input. It now resolves the user through
// credstore.CallerUserID; a machine token still has no user to record.

package metrics_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api"
	"github.com/mgoodric/security-atlas/internal/api/testjwt"
	"github.com/mgoodric/security-atlas/internal/dbtest"
)

func TestIntegration_CreateInput_UserPrefixedSubject(t *testing.T) {
	app := dbtest.NewAppPool(t)
	admin := dbtest.NewMigratePool(t)
	tenant := freshTenant(t, admin)

	boardID := "ms.2183.board." + uuid.NewString()[:8]
	programID := "ms.2183.program." + uuid.NewString()[:8]
	seedCatalog(t, admin, boardID, programID)

	srv := api.New(api.Config{})
	srv.AttachDB(app)
	userID := uuid.New()
	userClaims := testjwt.AdminFor(tenant)
	userClaims.Subject = "user:" + userID.String()
	userBearer := srv.IssueTestJWT(t, userClaims)
	machineClaims := testjwt.AdminFor(tenant)
	machineClaims.Subject = "oauth_client:ci-runner"
	machineBearer := srv.IssueTestJWT(t, machineClaims)
	ts := httptest.NewServer(srv.HTTPHandlerForTests())
	t.Cleanup(ts.Close)

	body := fmt.Sprintf(`{"numeric_value": 0.71, "observed_at": %q}`, time.Now().UTC().Format(time.RFC3339))
	url := ts.URL + "/v1/metrics/" + boardID + "/inputs"

	resp := do(t, http.MethodPost, url, userBearer, "application/json", body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("user: subject: status %d (body=%v); want 201", resp.StatusCode, decodeMap(t, resp))
	}
	inputID, _ := decodeMap(t, resp)["id"].(string)
	var enteredBy uuid.UUID
	if err := admin.QueryRow(context.Background(),
		`SELECT entered_by_user_id FROM metric_inputs WHERE tenant_id = $1 AND metric_id = $2 AND ($3 = '' OR id::text = $3)`,
		tenant, boardID, inputID).Scan(&enteredBy); err != nil {
		t.Fatalf("read metric_inputs: %v", err)
	}
	if enteredBy != userID {
		t.Errorf("entered_by_user_id = %s; want the subject's user %s", enteredBy, userID)
	}

	resp = do(t, http.MethodPost, url, machineBearer, "application/json", body)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("oauth_client subject: status %d; want 400 (no user to record)", resp.StatusCode)
	}
	_ = resp.Body.Close()
}
