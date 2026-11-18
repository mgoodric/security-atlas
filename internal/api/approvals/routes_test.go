package approvals

import (
	"bufio"
	"os"
	"strings"
	"testing"

	"github.com/mgoodric/security-atlas/internal/featureflag"
)

// routeModulesGolden is the registered-route to module map the API's
// every-route guard test holds every route to (#2352).
const routeModulesGolden = "../testdata/routes_modules.golden"

// TestApproveRoutes_ResolveToOwningModule pins each approveRoutes entry to
// the module that owns the real approve route (#2457). RouteOn reads a
// path no module owns as on, so a renamed approve route that left its
// approveRoutes entry behind would list that type with its module
// switched off. Every entry must name a registered PATCH route and
// resolve, with no error, to that route's owner in the golden.
func TestApproveRoutes_ResolveToOwningModule(t *testing.T) {
	t.Parallel()
	golden := readRouteOwners(t)
	if len(approveRoutes) != len(Types) {
		t.Fatalf("approveRoutes has %d entries, Types has %d", len(approveRoutes), len(Types))
	}
	for _, typ := range Types {
		path, ok := approveRoutes[typ]
		if !ok {
			t.Errorf("%s: no approveRoutes entry", typ)
			continue
		}
		route := "PATCH " + strings.ReplaceAll(path, "/_/", "/{id}/")
		want, ok := golden[route]
		if !ok {
			t.Errorf("%s: %q is no registered route in %s", typ, route, routeModulesGolden)
			continue
		}
		o, err := featureflag.Resolve(path)
		if err != nil {
			t.Errorf("%s: Resolve(%q): %v", typ, path, err)
			continue
		}
		got := o.Module.Key
		if o.Feature != nil {
			got += "/" + o.Feature.Key
		}
		if got != want {
			t.Errorf("%s: Resolve(%q) = %s, the registered route is owned by %s", typ, path, got, want)
		}
	}
}

// readRouteOwners parses the golden's "METHOD /path owner" lines.
func readRouteOwners(t *testing.T) map[string]string {
	t.Helper()
	f, err := os.Open(routeModulesGolden)
	if err != nil {
		t.Fatalf("open %s: %v", routeModulesGolden, err)
	}
	defer func() { _ = f.Close() }()
	owners := map[string]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) != 3 {
			continue
		}
		owners[fields[0]+" "+fields[1]] = fields[2]
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("read %s: %v", routeModulesGolden, err)
	}
	if len(owners) == 0 {
		t.Fatalf("%s lists no routes", routeModulesGolden)
	}
	return owners
}
