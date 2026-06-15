package api

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	authapi "github.com/mgoodric/security-atlas/internal/api/auth"
	"github.com/mgoodric/security-atlas/internal/api/oauth"
	"github.com/mgoodric/security-atlas/internal/auth/apikeystore"
	"github.com/mgoodric/security-atlas/internal/auth/bearer"
	"github.com/mgoodric/security-atlas/internal/auth/keystore/fsstore"
	"github.com/mgoodric/security-atlas/internal/auth/sessions"
	"github.com/mgoodric/security-atlas/internal/auth/tokensign"
	"github.com/mgoodric/security-atlas/internal/auth/users"
	"github.com/mgoodric/security-atlas/internal/authz"
	"github.com/mgoodric/security-atlas/internal/evidence/ingest"
	"github.com/mgoodric/security-atlas/internal/oscal"
	"github.com/mgoodric/security-atlas/internal/scim"
)

// nopResetter is a no-op PlatformResetter used only to force the
// /v1/admin/install/reset-bootstrap route to mount in the route walk.
type nopResetter struct{}

func (nopResetter) ResetBootstrap(context.Context, bool) error { return nil }

// nopValidator satisfies ingest.SchemaValidator so ingest.New does not
// panic. It is never invoked — the route walk only registers routes.
type nopValidator struct{}

func (nopValidator) ValidatePayload(context.Context, string, string, string, []byte) error {
	return nil
}
func (nopValidator) IsRegistered(string, string) bool { return false }

// routeWalkServer builds a Server with EVERY optional dependency wired so
// the maximal set of conditionally-mounted routes is registered. The pool
// is a lazily-constructed *pgxpool.Pool that never dials (pgxpool.New does
// not connect until first use, and buildRouter() only REGISTERS routes —
// chi.Walk inspects the trie, it never invokes a handler). This makes the
// route walk a behavior-preservation oracle for slice 436: the same builder
// must yield the identical method+path set before and after the
// httpserver.go → register_*.go split.
//
// Wiring discipline: every gate inside buildRouter() that is reachable with
// a non-dialing pool + cheap constructors is forced ON here so the golden
// captures the widest route surface. The env-gated test-mode route
// (POST /v1/test/issue-jwt) is forced via t.Setenv in the caller.
func routeWalkServer(t testing.TB) *Server {
	t.Helper()

	// A syntactically-valid DSN that is never dialed. pgxpool.New parses
	// the DSN and returns a pool eagerly; no TCP connection is opened
	// until a query runs, and the route walk never runs one.
	pool, err := pgxpool.New(context.Background(), "postgres://atlas:atlas@127.0.0.1:1/atlas")
	if err != nil {
		t.Fatalf("routeWalkServer: pgxpool.New: %v", err)
	}
	t.Cleanup(pool.Close)

	srv := New(Config{
		VersionFieldsFn: func() VersionFields {
			return VersionFields{Version: "test"}
		},
	})
	srv.AttachDB(pool)

	// Force every dep-gated block ON. Constructors below tolerate a
	// non-dialing pool because none of them open a connection at
	// construction time.
	srv.ingestService = ingest.New(pool, nopValidator{})
	srv.AttachOscalExporter(oscal.NewExporter(pool, nil, nil))

	hasher, err := bearer.NewHasher([]byte("route-walk-test-hasher-key-0001-padding-to-32-bytes"))
	if err != nil {
		t.Fatalf("routeWalkServer: bearer.NewHasher: %v", err)
	}
	srv.AttachAPIKeyStore(apikeystore.NewStore(pool, pool, hasher, 0))
	srv.AttachSCIM(scim.NewCredentialStore(pool, pool, hasher))
	srv.AttachAuthPool(pool)

	// authz engine forces the authzmw + the authz-bundle reload route.
	engine, err := authz.NewEngine(context.Background(), authz.NoopRolesResolver{})
	if err != nil {
		t.Fatalf("routeWalkServer: authz.NewEngine: %v", err)
	}
	srv.AttachAuthz(engine, nil)

	// JWT validator forces the jwtBypass + requireCredential gates. The
	// fsstore-backed signer needs no live infra.
	ks, err := fsstore.Open(t.TempDir())
	if err != nil {
		t.Fatalf("routeWalkServer: fsstore.Open: %v", err)
	}
	srv.AttachJWTValidator(tokensign.New(ks), nil, testJWTIssuer, testJWTIssuer)

	// OAuth AS handler forces /.well-known/* + /oauth/* mounts.
	srv.AttachOAuthHandler(oauth.New(ks, oauth.Config{Issuer: testJWTIssuer}))

	// Auth handler forces the four /auth/* routes.
	srv.AttachAuthHandler(authapi.New(nil, users.NewStore(pool), sessions.NewStore(pool, 0), false, nil))

	// platformResetter forces /v1/admin/install/reset-bootstrap.
	srv.platformResetter = nopResetter{}

	// Metrics fallback handler forces GET /metrics.
	srv.AttachMetricsHandler(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))

	return srv
}

// walkRoutes returns the sorted "METHOD path" set registered on the
// server's assembled chi router.
func walkRoutes(t testing.TB, srv *Server) []string {
	t.Helper()
	router := srv.buildRouter()
	if router == nil {
		t.Fatal("walkRoutes: nil chi router")
	}
	var routes []string
	walkFn := func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		routes = append(routes, method+" "+route)
		return nil
	}
	if err := chi.Walk(router, walkFn); err != nil {
		t.Fatalf("walkRoutes: chi.Walk: %v", err)
	}
	sort.Strings(routes)
	return routes
}

// walkRoutesWithMW returns the sorted "METHOD path <mwcount>" set. The
// middleware count is the depth of the per-route inline middleware chain
// chi records — the GLOBAL root.Use() chain plus any group-level Use().
// The base depth (currently 8) is every shared middleware; routes inside a
// featureflag.Gate group (oscal.export, board.reporting) or the SCIM auth
// subrouter carry depth+1. This is the AC-6 gating fingerprint: a route
// that silently moves OFF its feature-gate / SCIM auth group would change
// its count here.
func walkRoutesWithMW(t testing.TB, srv *Server) []string {
	t.Helper()
	router := srv.buildRouter()
	if router == nil {
		t.Fatal("walkRoutesWithMW: nil chi router")
	}
	var lines []string
	walkFn := func(method, route string, _ http.Handler, mws ...func(http.Handler) http.Handler) error {
		lines = append(lines, fmt.Sprintf("%s %s %d", method, route, len(mws)))
		return nil
	}
	if err := chi.Walk(router, walkFn); err != nil {
		t.Fatalf("walkRoutesWithMW: chi.Walk: %v", err)
	}
	sort.Strings(lines)
	return lines
}

// readGolden loads a newline-delimited golden file under testdata/.
func readGolden(t testing.TB, name string) []string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("readGolden(%s): %v", name, err)
	}
	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	return lines
}

// diffStrings returns the symmetric difference between want and got as a
// human-readable report (entries present in only one side).
func diffStrings(want, got []string) string {
	in := func(set []string, x string) bool {
		i := sort.SearchStrings(set, x)
		return i < len(set) && set[i] == x
	}
	w := append([]string(nil), want...)
	g := append([]string(nil), got...)
	sort.Strings(w)
	sort.Strings(g)
	var b strings.Builder
	for _, x := range w {
		if !in(g, x) {
			fmt.Fprintf(&b, "  - MISSING (dropped by split): %q\n", x)
		}
	}
	for _, x := range g {
		if !in(w, x) {
			fmt.Fprintf(&b, "  + ADDED (new since golden):   %q\n", x)
		}
	}
	return b.String()
}

// TestRouteTable_MatchesGolden is the slice-436 AC-5 behavior-preservation
// oracle: the complete method+path set the assembled router registers MUST
// equal the golden captured from clean main BEFORE the httpserver.go →
// register_*.go split. A dropped Mount (silent 404 of a whole subtree) or a
// duplicated route shows up as a diff here. The test runs in the plain unit
// tier (no build tag) so it gates the merge via the package coverage floor
// and runs on every PR — exactly where a chi double-Mount hazard must be
// caught.
//
// Updating the golden is a deliberate act: when a future slice legitimately
// adds or removes a route, regenerate testdata/routes.golden in the SAME PR
// and review the diff. The golden is NOT auto-rewritten.
func TestRouteTable_MatchesGolden(t *testing.T) {
	t.Setenv("ATLAS_TEST_MODE", "1")
	srv := routeWalkServer(t)
	got := walkRoutes(t, srv)
	want := readGolden(t, "routes.golden")

	if len(got) != len(want) {
		t.Errorf("route COUNT changed: golden=%d, got=%d", len(want), len(got))
	}
	if d := diffStrings(want, got); d != "" {
		t.Errorf("route table diverged from golden (slice 436 AC-5 — a route was dropped, added, or relocated):\n%s", d)
	}
}

// TestRouteMiddleware_GatingUnchanged is the slice-436 AC-6 EoP guard: the
// per-route middleware DEPTH must equal the golden. The depth encodes which
// routes sit inside a featureflag.Gate group (oscal.export / board.reporting)
// or the SCIM auth subrouter — the only group-level middleware in the tree.
// Every other route carries exactly the shared global chain. If the split
// moved a feature-gated or SCIM-gated route OUT from under its group (the
// classic "admin route onto the unauthenticated mux" regression, expressed
// here as a feature-gate / auth-subrouter escape), its count drops and this
// fails. Because the global auth/tenancy/authz chain is applied via
// root.Use() to the SINGLE root router that every registrar receives, no
// route can lose the global chain without losing ALL of it — which would
// collapse every count and fail loudly.
func TestRouteMiddleware_GatingUnchanged(t *testing.T) {
	t.Setenv("ATLAS_TEST_MODE", "1")
	srv := routeWalkServer(t)
	got := walkRoutesWithMW(t, srv)
	want := readGolden(t, "routes_mw.golden")

	if len(got) != len(want) {
		t.Errorf("route-mw COUNT changed: golden=%d, got=%d", len(want), len(got))
	}
	if d := diffStrings(want, got); d != "" {
		t.Errorf("per-route middleware gating diverged from golden (slice 436 AC-6 — a route changed its auth/feature-gate group):\n%s", d)
	}
}
