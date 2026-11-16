//go:build integration

package writeproposals_test

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/evidence/ingest"
	"github.com/mgoodric/security-atlas/internal/mcp/writeproposals"
)

// #2435: an approver key loaded from api_keys carries scope_predicate "{}"
// (the column default). Confirming MCP evidence under it must ingest; a
// restricted approver key is still refused, as the applier documents.
func TestConfirm_ScopePredicateFromDB(t *testing.T) {
	for _, tc := range []struct {
		name      string
		predicate string
		wantErr   error
	}{
		{"column default", "{}", nil},
		{"restricted", `{"op":"eq","dim":"cloud_account","value":"aws:1"}`, ingest.ErrScopeViolation},
	} {
		t.Run(tc.name, func(t *testing.T) {
			admin, _, store, tenant := ingestHarness(t)
			ctx, cred := approverCtx(t, tenant)
			cred.ScopePredicate = tc.predicate
			ctx = authctx.WithCredential(ctx, cred)
			controlID := seedControl(t, admin, tenant)

			in, _ := json.Marshal(map[string]any{
				"control_id": controlID.String(),
				"state":      "pass",
				"rationale":  "reviewed",
			})
			create := validCreate()
			create.ToolName = writeproposals.ToolUpdateControlState
			create.ToolInput = in
			p, err := store.Create(ctx, create)
			if err != nil {
				t.Fatalf("Create: %v", err)
			}
			_, err = store.Confirm(ctx, p.ID, cred.ID)
			if tc.wantErr == nil {
				if err != nil {
					t.Fatalf("Confirm under predicate %q: %v", tc.predicate, err)
				}
				if n := countLedger(t, admin, tenant); n != 1 {
					t.Fatalf("ledger rows = %d, want 1", n)
				}
				return
			}
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("Confirm err = %v, want %v", err, tc.wantErr)
			}
			if n := countLedger(t, admin, tenant); n != 0 {
				t.Fatalf("ledger rows = %d, want 0", n)
			}
		})
	}
}
