package adminauditlog

import (
	"bufio"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/mgoodric/security-atlas/internal/audit/unifiedlog"
	"github.com/mgoodric/security-atlas/internal/authz"
	"github.com/mgoodric/security-atlas/internal/featureflag"
)

// An Activity row the module registry cannot place is shown, by design
// (#2804, the #2173 entrySeen behaviour): the guards below catch a new kind
// or route left unregistered at build time rather than hiding rows at read
// time. Routes themselves are held to a module by
// internal/api TestEveryRouteBelongsToAModule (#2171) and
// TestRouteModules_MatchGolden (#2352).

// coreKinds are the unified-log kinds with no kindModuleRoute entry: they
// belong to a core module, so their rows are always shown. A new kind must
// go in kindModuleRoute (its module is optional) or here (it is core).
var coreKinds = map[unifiedlog.Kind]string{
	// Placed per row by its request path (decisionRoute), not by kind.
	unifiedlog.KindDecision:       "decision rows are placed by route",
	unifiedlog.KindEvidence:       "evidence",
	unifiedlog.KindSample:         "audit_periods",
	unifiedlog.KindAuditPeriod:    "audit_periods",
	unifiedlog.KindWalkthrough:    "audit_periods",
	unifiedlog.KindFeatureFlag:    "settings_admin",
	unifiedlog.KindMe:             "settings_admin",
	unifiedlog.KindModuleAccess:   "settings_admin",
	unifiedlog.KindFrameworkScope: "scopes",
	unifiedlog.KindMappingOverlay: "frameworks",
	unifiedlog.KindPostureTarget:  "frameworks",
	unifiedlog.KindControlImport:  "controls",
}

// #2804: every canonical kind is classified exactly once, as an optional
// module's kind (kindModuleRoute) or a core one (coreKinds).
func TestEveryKindHasAModuleOrIsCore(t *testing.T) {
	t.Parallel()
	for _, kind := range unifiedlog.AllKinds {
		_, optional := kindModuleRoute[kind]
		_, core := coreKinds[kind]
		switch {
		case optional && core:
			t.Errorf("kind %q is in both kindModuleRoute and coreKinds", kind)
		case !optional && !core:
			t.Errorf("kind %q has no module: add it to kindModuleRoute (optional module) or coreKinds (core)", kind)
		}
	}
	for kind := range coreKinds {
		if !unifiedlog.IsCanonical(kind) {
			t.Errorf("coreKinds lists %q, which is not a canonical kind", kind)
		}
	}
}

// unplacedTargetTypes are the authorization target types whose
// decisionRoute fallback ("/v1/"+target_type) no module owns, so a decision
// row with that type and no recorded request path is shown. Only legacy
// rows (before #2477 recorded the path) take the fallback.
var unplacedTargetTypes = map[string]string{
	// /health, /ready, /metrics: no type. settings_admin (core) owns them,
	// so shown is the answer the registry would give.
	"": "settings_admin",
	// Non-/v1 routes, all owned by settings_admin (core): shown is right.
	"auth":        "settings_admin",
	"oauth":       "settings_admin",
	"scim":        "settings_admin",
	".well-known": "settings_admin",
	// /v1/oscal/* is split: imported-profiles is frameworks (core),
	// component-definitions and component-claims are third_party_risk's
	// vendor_claims feature. A legacy "oscal" row cannot say which, so it
	// is shown even while vendor claims are off.
	"oscal": "split: frameworks / third_party_risk",
}

var routeParamRE = regexp.MustCompile(`\{[^}]*\}`)

// #2804: for every registered route, the decision target type authz
// derives from it falls back to a route some module owns, or is listed in
// unplacedTargetTypes. The route table is internal/api's
// routes_modules.golden, which TestRouteModules_MatchGolden keeps equal to
// the live router.
func TestEveryDecisionTargetTypeHasAModule(t *testing.T) {
	t.Parallel()
	path := filepath.Join("..", "testdata", "routes_modules.golden")
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer f.Close()
	unplacedSeen := map[string]bool{}
	routes := 0
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 2 {
			continue
		}
		routes++
		method, route := fields[0], fields[1]
		sample := routeParamRE.ReplaceAllString(route, "x")
		in := authz.BuildInput(httptest.NewRequest(method, sample, nil), nil)
		targetType := in.Resource.Type
		fallback := decisionRoute(unifiedlog.Entry{Kind: unifiedlog.KindDecision, TargetType: targetType})
		if fallback != "" {
			if _, err := featureflag.Resolve(fallback); err == nil {
				continue
			}
		}
		if _, ok := unplacedTargetTypes[targetType]; ok {
			unplacedSeen[targetType] = true
			continue
		}
		t.Errorf("%s %s: decision target type %q falls back to %q, which no module owns; add the route to featureflag.Modules or the type to unplacedTargetTypes", method, route, targetType, fallback)
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if routes == 0 {
		t.Fatalf("%s has no routes", path)
	}
	for tt := range unplacedTargetTypes {
		if !unplacedSeen[tt] {
			t.Errorf("unplacedTargetTypes lists %q, but no route yields an unplaced fallback for it; remove it", tt)
		}
	}
}
