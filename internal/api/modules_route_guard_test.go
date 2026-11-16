package api

import (
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
