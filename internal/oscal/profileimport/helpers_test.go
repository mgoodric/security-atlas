// Pure-Go unit tests (no Postgres, no build tag) for profileimport's fast
// branches — the slice-353 Q-2 helpers_test convention. These cover the
// role gate + input validation that run BEFORE any bridge call or DB
// transaction, plus the bridge-error and resolution-rejection branches via
// a fake bridge.

package profileimport

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
	resp           *oscalv1.ImportProfileResponse
	err            error
	called         bool
	gotProfile     []byte
	gotCatalogs    [][]byte
	gotProfiles    [][]byte
	gotSourceLabel string
}

func (f *fakeBridge) ImportProfile(_ context.Context, profileJSON []byte, catalogs [][]byte, profiles [][]byte, sourceLabel string) (*oscalv1.ImportProfileResponse, error) {
	f.called = true
	f.gotProfile = profileJSON
	f.gotCatalogs = catalogs
	f.gotProfiles = profiles
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

// okReq is a minimal authorized request used by the gate tests. The profile
// imports the single supplied catalog by uuid, so it PASSES the slice-578
// Go-side chain validation (which now runs before the bridge call); the gate
// tests that drive the bridge-error / rejection branches need a chain-valid
// request so they reach the bridge.
func okReq() Request {
	return Request{
		ProfileJSON: []byte(`{"profile":{"uuid":"p-entry","imports":[{"href":"#cat-1"}]}}`),
		Catalogs:    [][]byte{[]byte(`{"catalog":{"uuid":"cat-1"}}`)},
		ImportedBy:  "tester",
		Role:        authz.RoleGRCEngineer,
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
			t.Errorf("role %q should be authorized to import profiles", role)
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

func TestImport_RejectsEmptyProfile(t *testing.T) {
	t.Parallel()
	im := NewImporter(&nilBeginner{}, &fakeBridge{})
	req := okReq()
	req.ProfileJSON = nil
	_, err := im.Import(tenantCtx(t), req)
	if !errors.Is(err, ErrEmptyDocument) {
		t.Fatalf("expected ErrEmptyDocument, got %v", err)
	}
}

func TestImport_RejectsOversizeProfile(t *testing.T) {
	t.Parallel()
	im := NewImporter(&nilBeginner{}, &fakeBridge{})
	req := okReq()
	req.ProfileJSON = make([]byte, MaxProfileBytes+1)
	_, err := im.Import(tenantCtx(t), req)
	if !errors.Is(err, ErrDocumentTooLarge) {
		t.Fatalf("expected ErrDocumentTooLarge, got %v", err)
	}
}

func TestImport_RejectsNoCatalogs(t *testing.T) {
	t.Parallel()
	im := NewImporter(&nilBeginner{}, &fakeBridge{})
	req := okReq()
	req.Catalogs = nil
	_, err := im.Import(tenantCtx(t), req)
	if !errors.Is(err, ErrNoCatalogs) {
		t.Fatalf("expected ErrNoCatalogs, got %v", err)
	}
}

func TestImport_RejectsEmptyCatalog(t *testing.T) {
	t.Parallel()
	im := NewImporter(&nilBeginner{}, &fakeBridge{})
	req := okReq()
	req.Catalogs = [][]byte{[]byte(`{"catalog":{}}`), nil}
	_, err := im.Import(tenantCtx(t), req)
	if !errors.Is(err, ErrNoCatalogs) {
		t.Fatalf("expected ErrNoCatalogs for an empty supplied catalog, got %v", err)
	}
}

func TestImport_RejectsTooManyCatalogs(t *testing.T) {
	t.Parallel()
	im := NewImporter(&nilBeginner{}, &fakeBridge{})
	req := okReq()
	req.Catalogs = make([][]byte, MaxSuppliedCatalogs+1)
	for i := range req.Catalogs {
		req.Catalogs[i] = []byte(`{"catalog":{}}`)
	}
	_, err := im.Import(tenantCtx(t), req)
	if !errors.Is(err, ErrTooManyCatalogs) {
		t.Fatalf("expected ErrTooManyCatalogs, got %v", err)
	}
}

func TestImport_RejectsOversizeCatalog(t *testing.T) {
	t.Parallel()
	im := NewImporter(&nilBeginner{}, &fakeBridge{})
	req := okReq()
	req.Catalogs = [][]byte{make([]byte, MaxCatalogBytes+1)}
	_, err := im.Import(tenantCtx(t), req)
	if !errors.Is(err, ErrDocumentTooLarge) {
		t.Fatalf("expected ErrDocumentTooLarge for an oversize catalog, got %v", err)
	}
}

func TestImport_ResolutionRejectedSurfacesAndAttemptsAudit(t *testing.T) {
	t.Parallel()
	// A bridge that reports the profile invalid (e.g. an external import.href
	// — P0-511-1) drives the rejection branch: Import surfaces
	// ErrResolutionFailed and attempts a best-effort rejection audit (whose
	// tx Begin fails here — swallowed by design, so the resolution error
	// still surfaces).
	bridge := &fakeBridge{resp: &oscalv1.ImportProfileResponse{
		Valid:  false,
		Errors: []string{"import #0 href 'https://attacker.example/c.json' is an external reference; external resources are never dereferenced"},
	}}
	beginner := &nilBeginner{}
	im := NewImporter(beginner, bridge)
	_, err := im.Import(tenantCtx(t), okReq())
	if !errors.Is(err, ErrResolutionFailed) {
		t.Fatalf("expected ErrResolutionFailed, got %v", err)
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
	if errors.Is(err, ErrResolutionFailed) {
		t.Fatalf("a transport error must not be reported as a resolution failure")
	}
}

func TestImport_PassesProfileAndCatalogsToBridge(t *testing.T) {
	t.Parallel()
	// Prove the importer forwards the profile + every supplied catalog +
	// source label to the bridge unchanged (the bridge is the only resolver;
	// the Go side never re-parses or re-orders the inputs).
	bridge := &fakeBridge{resp: &oscalv1.ImportProfileResponse{Valid: false, Errors: []string{"stop before persist"}}}
	im := NewImporter(&nilBeginner{}, bridge)
	req := okReq()
	// A two-catalog request whose entry profile imports the first catalog by
	// uuid — chain-valid so it reaches the bridge.
	req.ProfileJSON = []byte(`{"profile":{"uuid":"p-entry","imports":[{"href":"#cat-a"}]}}`)
	req.Catalogs = [][]byte{[]byte(`{"catalog":{"uuid":"cat-a"}}`), []byte(`{"catalog":{"uuid":"cat-b"}}`)}
	req.SourceLabel = "FedRAMP Moderate"
	_, _ = im.Import(tenantCtx(t), req)
	if string(bridge.gotProfile) != string(req.ProfileJSON) {
		t.Errorf("bridge got profile %q, want %q", bridge.gotProfile, req.ProfileJSON)
	}
	if len(bridge.gotCatalogs) != 2 {
		t.Fatalf("bridge got %d catalogs, want 2", len(bridge.gotCatalogs))
	}
	if bridge.gotSourceLabel != "FedRAMP Moderate" {
		t.Errorf("bridge got source label %q, want FedRAMP Moderate", bridge.gotSourceLabel)
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
