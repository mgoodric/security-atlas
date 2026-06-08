// Pure-Go unit tests (no Postgres, no build tag) for componentimport's fast
// branches — the slice-353 Q-2 helpers_test convention. These cover the role
// gate + input validation that run BEFORE any bridge call or DB transaction,
// plus the bridge-error and validation-rejection branches via a fake bridge.

package componentimport

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	oscalv1 "github.com/mgoodric/security-atlas/gen/proto/oscal/v1"
	"github.com/mgoodric/security-atlas/internal/authz"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

func mustParse(t *testing.T, s string) uuid.UUID {
	t.Helper()
	id, err := uuid.Parse(s)
	if err != nil {
		t.Fatalf("uuid.Parse(%q): %v", s, err)
	}
	return id
}

// fakeBridge records its call and returns a canned response / error.
type fakeBridge struct {
	resp           *oscalv1.ImportComponentDefinitionResponse
	err            error
	called         bool
	gotJSON        []byte
	gotSourceLabel string
}

func (f *fakeBridge) ImportComponentDefinition(_ context.Context, oscalJSON []byte, sourceLabel string) (*oscalv1.ImportComponentDefinitionResponse, error) {
	f.called = true
	f.gotJSON = oscalJSON
	f.gotSourceLabel = sourceLabel
	if f.err != nil {
		return nil, f.err
	}
	return f.resp, nil
}

// nilBeginner is a txBeginner that always errors — proves the Go-side gates
// short-circuit BEFORE any transaction is begun.
type nilBeginner struct{ begun bool }

func (n *nilBeginner) Begin(_ context.Context) (pgx.Tx, error) {
	n.begun = true
	return nil, errors.New("Begin must not be called in these branches")
}

func tenantCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, err := tenancy.WithTenant(context.Background(), "11111111-1111-4111-8111-111111111111")
	if err != nil {
		t.Fatalf("WithTenant: %v", err)
	}
	return ctx
}

// okReq is a minimal authorized request used by the gate tests.
func okReq() Request {
	return Request{
		OscalJSON:  []byte(`{"component-definition":{}}`),
		ImportedBy: "tester",
		Role:       authz.RoleGRCEngineer,
	}
}

func TestImport_RejectsUnauthorizedRole(t *testing.T) {
	t.Parallel()
	for _, role := range []authz.Role{authz.RoleControlOwner, authz.RoleAuditor, authz.RoleViewer, authz.Role("nonsense"), authz.Role("")} {
		bridge := &fakeBridge{}
		beginner := &nilBeginner{}
		im := NewImporter(beginner, bridge)
		req := okReq()
		req.Role = role
		_, err := im.Import(tenantCtx(t), req)
		if !errors.Is(err, ErrUnauthorizedRole) {
			t.Errorf("role %q: expected ErrUnauthorizedRole, got %v", role, err)
		}
		if bridge.called {
			t.Errorf("role %q: bridge must not be called for an unauthorized role", role)
		}
		if beginner.begun {
			t.Errorf("role %q: no transaction must begin for an unauthorized role", role)
		}
	}
}

func TestImport_AuthorizedRoles(t *testing.T) {
	t.Parallel()
	for _, role := range []authz.Role{authz.RoleGRCEngineer, authz.RoleAdmin} {
		if !authorizedRole(role) {
			t.Errorf("role %q should be authorized to import component-definitions", role)
		}
	}
}

func TestImport_RejectsMissingImporter(t *testing.T) {
	t.Parallel()
	im := NewImporter(&nilBeginner{}, &fakeBridge{})
	req := okReq()
	req.ImportedBy = ""
	_, err := im.Import(tenantCtx(t), req)
	if !errors.Is(err, ErrMissingImporter) {
		t.Fatalf("expected ErrMissingImporter, got %v", err)
	}
}

func TestImport_RejectsEmptyDocument(t *testing.T) {
	t.Parallel()
	im := NewImporter(&nilBeginner{}, &fakeBridge{})
	req := okReq()
	req.OscalJSON = nil
	_, err := im.Import(tenantCtx(t), req)
	if !errors.Is(err, ErrEmptyDocument) {
		t.Fatalf("expected ErrEmptyDocument, got %v", err)
	}
}

func TestImport_RejectsOversizeDocument(t *testing.T) {
	t.Parallel()
	im := NewImporter(&nilBeginner{}, &fakeBridge{})
	req := okReq()
	req.OscalJSON = make([]byte, MaxComponentDefBytes+1)
	_, err := im.Import(tenantCtx(t), req)
	if !errors.Is(err, ErrDocumentTooLarge) {
		t.Fatalf("expected ErrDocumentTooLarge, got %v", err)
	}
}

func TestImport_ValidationRejectedSurfacesAndAttemptsAudit(t *testing.T) {
	t.Parallel()
	// A bridge that reports the document invalid drives the rejection branch:
	// Import surfaces ErrValidationFailed and attempts a best-effort rejection
	// audit (whose tx Begin fails here — swallowed by design, so the
	// validation error still surfaces).
	bridge := &fakeBridge{resp: &oscalv1.ImportComponentDefinitionResponse{
		Valid:  false,
		Errors: []string{"component-definition failed OSCAL v1.1.x validation: ..."},
	}}
	beginner := &nilBeginner{}
	im := NewImporter(beginner, bridge)
	_, err := im.Import(tenantCtx(t), okReq())
	if !errors.Is(err, ErrValidationFailed) {
		t.Fatalf("expected ErrValidationFailed, got %v", err)
	}
	if !bridge.called {
		t.Error("bridge should have been called before rejection")
	}
	if !beginner.begun {
		t.Error("rejection path should attempt to write an audit row (Begin)")
	}
}

func TestImport_BridgeTransportErrorSurfaces(t *testing.T) {
	t.Parallel()
	bridge := &fakeBridge{err: errors.New("bridge down")}
	im := NewImporter(&nilBeginner{}, bridge)
	_, err := im.Import(tenantCtx(t), okReq())
	if err == nil || !bridge.called {
		t.Fatalf("expected a surfaced bridge error, got %v (called=%v)", err, bridge.called)
	}
	if errors.Is(err, ErrValidationFailed) {
		t.Fatalf("a transport error must not be reported as a validation failure")
	}
}

func TestImport_PassesDocumentAndLabelToBridge(t *testing.T) {
	t.Parallel()
	// Prove the importer forwards the document + the source label to the
	// bridge unchanged (the bridge is the only parser; the Go side never
	// re-parses the input).
	bridge := &fakeBridge{resp: &oscalv1.ImportComponentDefinitionResponse{Valid: false, Errors: []string{"stop before persist"}}}
	im := NewImporter(&nilBeginner{}, bridge)
	req := okReq()
	req.OscalJSON = []byte(`{"component-definition":{"x":1}}`)
	req.SourceLabel = "Acme Cloud"
	_, _ = im.Import(tenantCtx(t), req)
	if string(bridge.gotJSON) != string(req.OscalJSON) {
		t.Errorf("bridge got document %q, want %q", bridge.gotJSON, req.OscalJSON)
	}
	if bridge.gotSourceLabel != "Acme Cloud" {
		t.Errorf("bridge got source label %q, want Acme Cloud", bridge.gotSourceLabel)
	}
}

func TestTenantUUID(t *testing.T) {
	t.Parallel()
	ctx, _ := tenancy.WithTenant(context.Background(), "22222222-2222-4222-8222-222222222222")
	id, err := tenantUUID(ctx)
	if err != nil || id.String() != "22222222-2222-4222-8222-222222222222" {
		t.Fatalf("tenantUUID = %v, %v", id, err)
	}
	if _, err := tenantUUID(context.Background()); err == nil {
		t.Error("tenantUUID with no tenant in context should error")
	}
}

func TestPgUUID(t *testing.T) {
	t.Parallel()
	id := mustParse(t, "33333333-3333-4333-8333-333333333333")
	pg := pgUUID(id)
	if !pg.Valid || pg.Bytes != id {
		t.Errorf("pgUUID round-trip failed: %+v", pg)
	}
}
