package api

import (
	"flag"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/mgoodric/security-atlas/internal/featureflag"
)

var routeParam = regexp.MustCompile(`\{[^}]*\}`)

// routeSample turns a chi route pattern into a concrete path the module
// registry can resolve: each "{param}" becomes "x", and a trailing "*"
// does too, so "/v1/walkthroughs/{id}:finalize" resolves as
// "/v1/walkthroughs/x:finalize".
func routeSample(route string) string {
	s := routeParam.ReplaceAllString(route, "x")
	if strings.HasSuffix(s, "*") {
		s = strings.TrimSuffix(s, "*") + "x"
	}
	return s
}

// TestEveryRouteBelongsToAModule is #2171's guard (#2166, isa/modules.md
// ISC-2): every route registered on the server belongs to exactly one
// module in the registry (featureflag.Modules), core or optional, so a new
// route cannot skip the module gate. A route no module owns, or one that
// two modules own equally, fails here: add its pattern to the module whose
// data it returns.
func TestEveryRouteBelongsToAModule(t *testing.T) {
	t.Setenv("ATLAS_TEST_MODE", "1")
	srv := routeWalkServer(t)
	routes := walkRoutes(t, srv)
	if len(routes) == 0 {
		t.Fatal("no routes walked")
	}
	for _, line := range routes {
		route := line[strings.IndexByte(line, ' ')+1:]
		if _, err := featureflag.Resolve(routeSample(route)); err != nil {
			t.Errorf("%s: %v", line, err)
		}
	}
}

// routeModulesGolden is the route-to-module golden: one "METHOD route
// owner" line per registered route, where owner is the module key, or
// "module/feature" when a feature's pattern is the most specific match.
const routeModulesGolden = "routes_modules.golden"

// routeModuleLines returns the sorted golden lines for every walked route.
func routeModuleLines(t *testing.T) []string {
	t.Helper()
	t.Setenv("ATLAS_TEST_MODE", "1")
	routes := walkRoutes(t, routeWalkServer(t))
	if len(routes) == 0 {
		t.Fatal("no routes walked")
	}
	lines := make([]string, 0, len(routes))
	for _, line := range routes {
		route := line[strings.IndexByte(line, ' ')+1:]
		o, err := featureflag.Resolve(routeSample(route))
		if err != nil {
			t.Fatalf("%s: %v", line, err)
		}
		owner := o.Module.Key
		if o.Feature != nil {
			owner += "/" + o.Feature.Key
		}
		lines = append(lines, line+" "+owner)
	}
	return lines
}

// TestRouteModules_MatchGolden is #2352's route-to-module golden.
// TestEveryRouteBelongsToAModule checks only that SOME module owns each
// route, and broad core prefixes ("/v1/controls", "/v1/admin", "/v1/me",
// "/v1/dashboard") claim any new route nested under them. A route such as
// "/v1/controls/{id}/exceptions" would then serve Exceptions data while
// Exceptions is switched off. Pinning every route's owner in
// testdata/routes_modules.golden makes each new route, and the module it
// lands in, a line in the PR diff for review.
//
// Regenerate after a deliberate route or registry change with
//
//	go test ./internal/api -run TestRouteModules_MatchGolden -update
//
// and check that each changed line names the module whose data the route
// returns.
func TestRouteModules_MatchGolden(t *testing.T) {
	got := routeModuleLines(t)
	path := filepath.Join("testdata", routeModulesGolden)
	if f := flag.Lookup("update"); f != nil && f.Value.String() == "true" {
		if err := os.WriteFile(path, []byte(strings.Join(got, "\n")+"\n"), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
		return
	}
	want := readGolden(t, routeModulesGolden)
	if d := diffStrings(want, got); d != "" {
		t.Errorf("route-to-module table diverged from %s; check each route's owner, then regenerate with -update:\n%s", path, d)
	}
}

func TestRouteSample(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		"/v1/walkthroughs/{id}:finalize":        "/v1/walkthroughs/x:finalize",
		"/v1/decisions/{id}/links/{kind}/{tid}": "/v1/decisions/x/links/x/x",
		"/scim/v2/*":                            "/scim/v2/x",
		"/v1/me":                                "/v1/me",
	} {
		if got := routeSample(in); got != want {
			t.Errorf("routeSample(%q) = %q; want %q", in, got, want)
		}
	}
}
