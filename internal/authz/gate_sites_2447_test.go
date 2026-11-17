package authz_test

// #2447: drift guard for the #2428 route table. TestTenantGates2428_*
// checks the gates listed in routes2428, but that list is kept by hand, so
// a handler could move from one helper to the other, or gain a helper
// gate, without the table (and so the OPA agreement check) noticing.
//
// This test parses every non-test Go file under internal/api and records,
// per function, the authz.IsTenantAdmin / IsTenantApprover /
// IsTenantRiskReader (#2556) calls in
// it. That inventory must equal gateSites2447 exactly: each site names the
// gate it calls and the routes2428 rows it guards, or why it guards no
// route. A changed, added or removed call fails here until the site table
// and routes2428 are updated together. The scan reads source only, so it
// is deterministic.
//
// cred.IsAdmin reads are not inventoried: they are the install-wide and
// /v1/admin/* gates #2216 D2 keeps super-admin only, outside routes2428. A
// handler moved from a helper to the flag still fails here, because its
// helper call disappears.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"net/http"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
)

// gateSite2447 is one function under internal/api that calls a helper.
type gateSite2447 struct {
	// calls is the helper names called in the function, in source order.
	calls []string
	// routes are "METHOD path" keys of routes2428 rows this function's
	// gate guards. Empty when the call is not a route gate.
	routes []string
	// why explains a site with no routes.
	why string
}

func r2447(method, path string) string { return method + " " + path }

const (
	admin2447      = "IsTenantAdmin"
	approver2447   = "IsTenantApprover"
	riskReader2447 = "IsTenantRiskReader"
)

// gateSites2447 is keyed by "<dir under internal/api>/<file> <func>", with
// methods as "Recv.Name".
var gateSites2447 = func() map[string]gateSite2447 {
	const id = id2428
	adm := []string{admin2447}
	apr := []string{approver2447}
	rdr := []string{riskReader2447}
	get, post, patch, put, del := http.MethodGet, http.MethodPost, http.MethodPatch, http.MethodPut, http.MethodDelete
	site := func(calls []string, routes ...string) gateSite2447 {
		return gateSite2447{calls: calls, routes: routes}
	}
	notRoute := func(calls []string, why string) gateSite2447 {
		return gateSite2447{calls: calls, why: why}
	}
	const preRoute = "tenant-admin gate that predates #2428 and is not in routes2428"
	return map[string]gateSite2447{
		"actionplans/handlers.go Handler.VerifyActionPlan": site(adm, r2447(post, "/v1/action-plans/"+id+"/verify")),
		"adminusers/moduleaccess.go Handler.moduleAccessTarget": site(adm,
			r2447(put, "/v1/admin/users/"+id+"/module-access")),
		"approvals/handler.go Handler.Pending":           notRoute(apr, "approvals list (#2119, and the needs-attention approvals band, #2134): filters what the caller can approve; a non-approver gets an empty 200, never a refusal"),
		"needsattention/handler.go Handler.List":         site(apr, r2447(get, "/v1/needs-attention?for=all")),
		"auditperiods/auditors.go AuditorsHandler.admit": notRoute(adm, preRoute+" (period auditors, #2118)"),
		"auditperiods/handlers.go canReadControlState":   site(apr, r2447(get, "/v1/audit-periods/"+id+"/control-state")),
		"auditperiods/handlers.go canWrite": site(adm,
			r2447(post, "/v1/audit-periods"),
			r2447(post, "/v1/audit-periods/"+id+"/freeze"),
			r2447(post, "/v1/audit-periods/"+id+"/populations/"+id)),
		"boardnarrative/handlers.go Handler.Approve":     site(adm, r2447(post, "/v1/board/narrative/approve")),
		"boardnarrative/handlers.go Handler.Generate":    site(adm, r2447(post, "/v1/board/narrative/generate")),
		"boardnarrative/handlers.go Handler.GenerateAll": site(adm, r2447(post, "/v1/board/narrative/generate-all")),
		"checklist/handler.go Handler.ApproveSection":    site(apr, r2447(post, "/v1/controls/checklist/sections/"+id+":approve")),
		"checklist/handler.go Handler.Generate":          site(apr, r2447(post, "/v1/controls/checklist:generate")),
		"checklist/handler.go Handler.ExportMarkdown":    notRoute(apr, "checklist read: an approver or any owner-role credential"),
		"checklist/handler.go Handler.LoadGeneration":    notRoute(apr, "checklist read: an approver or any owner-role credential"),
		"controldetail/authz.go readsUnscopedEvidence": notRoute(apr,
			"per-control evidence list (#2470): narrows a period-scoped caller to control_id resolution; never a refusal"),
		"controlimports/settings.go requireTenantAdmin":     notRoute(adm, preRoute+" (control import LLM settings)"),
		"controls/handlers.go Handler.UploadBundle":         site(adm, r2447(post, "/v1/controls:upload-bundle")),
		"controlstate/evaluate.go EvaluateHandler.Evaluate": site(adm, r2447(post, "/v1/controls/"+id+"/evaluate")),
		"csfassessment/handler.go Handler.editContext": site(apr,
			r2447(put, "/v1/csf/tier"),
			r2447(put, "/v1/csf/profiles/target"),
			r2447(put, "/v1/csf/profiles/target/selections"),
			r2447(del, "/v1/csf/profiles/target/selections/"+id)),
		"dashboardexport/dashboardexport.go hasDashboardExportAccess": site(apr, r2447(get, "/v1/dashboard/export")),
		"evidencesummary/periodhandler.go hasAuditWorkspaceRead": site(apr,
			r2447(get, "/v1/audit-periods/"+id+"/controls/"+id+"/evidence-summary")),
		"exceptions/handlers.go Handler.Activate":               site(apr, r2447(patch, "/v1/exceptions/"+id+"/activate")),
		"exceptions/handlers.go Handler.Approve":                site(apr, r2447(patch, "/v1/exceptions/"+id+"/approve")),
		"exceptions/handlers.go Handler.Deny":                   site(apr, r2447(patch, "/v1/exceptions/"+id+"/deny")),
		"exceptions/handlers.go Handler.Revoke":                 site(apr, r2447(patch, "/v1/exceptions/"+id+"/revoke")),
		"frameworkscopes/handlers.go Handler.Approve":           site(apr, r2447(patch, "/v1/framework-scopes/"+id+"/approve")),
		"llmrouting/handler.go requireTenantAdmin":              notRoute(adm, preRoute+" (LLM routing)"),
		"mcpwriteproposals/handlers.go Handler.ConfirmProposal": site(adm, r2447(post, "/v1/mcp/write-proposals/"+id+"/confirm")),
		"mcpwriteproposals/handlers.go Handler.RejectProposal":  site(adm, r2447(post, "/v1/mcp/write-proposals/"+id+"/reject")),
		"me/profile.go ProfileHandler.buildProfile": notRoute([]string{admin2447, approver2447},
			"reports is_tenant_admin and is_approver on /v1/me; not a gate"),
		"me/profile.go profileWireFrom": notRoute([]string{admin2447, approver2447},
			"reports is_tenant_admin and is_approver on /v1/me; not a gate"),
		"metrics/handlers.go Handler.CreateInput":  site(adm, r2447(post, "/v1/metrics/"+id+"/inputs")),
		"metrics/handlers.go Handler.UpsertTarget": site(adm, r2447(put, "/v1/metrics/"+id+"/target")),
		"oscalcomponents/handler.go Handler.MapScfAnchor": site(adm,
			r2447(patch, "/v1/oscal/component-claims/"+id+"/scf-anchor")),
		"oscalcomponents/handler.go Handler.disposition": site(adm,
			r2447(post, "/v1/oscal/component-claims/"+id+":accept"),
			r2447(post, "/v1/oscal/component-claims/"+id+":reject"),
			r2447(post, "/v1/oscal/component-claims/"+id+":needs-info")),
		"policies/handlers.go Handler.Approve":       site(apr, r2447(patch, "/v1/policies/"+id+"/approve")),
		"policies/handlers.go Handler.Publish":       site(apr, r2447(post, "/v1/policies/"+id+"/publish")),
		"policies/handlers.go Handler.SetReviewDate": site(apr, r2447(patch, "/v1/policies/"+id+"/review-date")),
		"posturetargets/handler.go Handler.Set": site(apr,
			r2447(put, "/v1/framework-scopes/posture-targets/"+id)),
		"questionnaires/handlers.go Handler.AIApprove": site(adm,
			r2447(post, "/v1/questionnaires/"+id+"/answers/q1/ai-approve")),
		"questionnaires/handlers.go Handler.AIReject": site(adm,
			r2447(post, "/v1/questionnaires/"+id+"/answers/q1/ai-reject")),
		"questionnaires/handlers.go Handler.AISuggest": site(adm,
			r2447(post, "/v1/questionnaires/"+id+"/answers/q1/ai-suggest")),
		"questionnaires/handlers.go Handler.CancelAnswerRun": site(adm,
			r2447(post, "/v1/questionnaires/"+id+"/answer-runs/r1/cancel")),
		"questionnaires/handlers.go Handler.MappingApprove": site(adm,
			r2447(post, "/v1/questionnaires/"+id+"/questions/q1/scf-mapping/p1/approve")),
		"questionnaires/handlers.go Handler.MappingReject": site(adm,
			r2447(post, "/v1/questionnaires/"+id+"/questions/q1/scf-mapping/p1/reject")),
		"questionnaires/handlers.go Handler.MappingSuggest": site(adm,
			r2447(post, "/v1/questionnaires/"+id+"/questions/q1/scf-mapping/ai-suggest")),
		"questionnaires/handlers.go Handler.StartAnswerRun": site(adm,
			r2447(post, "/v1/questionnaires/"+id+"/answer-runs")),
		"risks/authz.go requireTaxonomyAdmin": site(apr,
			r2447(post, "/v1/risks/categories"),
			r2447(patch, "/v1/risks/categories/c1"),
			r2447(del, "/v1/risks/categories/c1"),
			r2447(post, "/v1/risks/categories/c1/archive"),
			r2447(post, "/v1/risks/categories/c1/unarchive"),
			r2447(post, "/v1/risks/categories/c1/set-default"),
			r2447(post, "/v1/themes"),
			r2447(patch, "/v1/themes/t1"),
			r2447(del, "/v1/themes/t1")),
		// #2556: the risk-module read gates. risks' hasProgramRead guards
		// the list and theme heatmap (requireProgramRead) and the export.
		"risks/authz.go hasProgramRead": site(rdr,
			r2447(get, "/v1/risks"),
			r2447(get, "/v1/risks/theme-heatmap"),
			r2447(get, "/v1/risks/export")),
		"decisions/authz.go hasProgramRead":    site(rdr, r2447(get, "/v1/decisions")),
		"orgunits/authz.go hasProgramRead":     site(rdr, r2447(get, "/v1/org_units")),
		"risks/handlers.go Handler.DeleteRisk": site(adm, r2447(del, "/v1/risks/"+id)),
		"risks/transitions.go Handler.runTransition": site(apr,
			r2447(patch, "/v1/risks/"+id+"/acceptance/approve"),
			r2447(patch, "/v1/risks/"+id+"/acceptance/reject"),
			r2447(patch, "/v1/risks/"+id+"/acceptance/revoke")),
		"walkthroughs/handlers.go canWrite": site(apr,
			r2447(post, "/v1/walkthroughs"),
			r2447(post, "/v1/walkthroughs/"+id+"/attachments"),
			r2447(post, "/v1/walkthroughs/"+id+":finalize")),
		"mappingoverlays/handlers.go requireOverlayWrite": site(apr,
			r2447(post, "/v1/mapping-overlays"),
			r2447(post, "/v1/mapping-overlays/"+id+"/retire"),
			r2447(post, "/v1/mapping-overlays/import"),
			r2447(post, "/v1/mapping-overlays/remap-proposals/"+id+"/approve"),
			r2447(post, "/v1/mapping-overlays/remap-proposals/"+id+"/reject"),
			r2447(post, "/v1/mapping-overlays/remap-proposals/"+id+"/keep-legacy")),
	}
}()

// scanGateSites2447 returns the helper calls per function under
// internal/api, test files excluded.
func scanGateSites2447(t *testing.T) map[string][]string {
	t.Helper()
	root := filepath.Join("..", "api")
	fset := token.NewFileSet()
	out := map[string][]string{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			var calls []string
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				pkg, ok := sel.X.(*ast.Ident)
				if ok && pkg.Name == "authz" && (sel.Sel.Name == admin2447 || sel.Sel.Name == approver2447 || sel.Sel.Name == riskReader2447) {
					calls = append(calls, sel.Sel.Name)
				}
				return true
			})
			if len(calls) > 0 {
				out[filepath.ToSlash(rel)+" "+funcName2447(fn)] = calls
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("scan internal/api: %v", err)
	}
	return out
}

func funcName2447(fn *ast.FuncDecl) string {
	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		return fn.Name.Name
	}
	typ := fn.Recv.List[0].Type
	if star, ok := typ.(*ast.StarExpr); ok {
		typ = star.X
	}
	if id, ok := typ.(*ast.Ident); ok {
		return id.Name + "." + fn.Name.Name
	}
	return fn.Name.Name
}

func TestGateSites2447_TableMatchesSource(t *testing.T) {
	t.Parallel()
	scanned := scanGateSites2447(t)

	var keys []string
	for k := range scanned {
		keys = append(keys, k)
	}
	for k := range gateSites2447 {
		if _, ok := scanned[k]; !ok {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	for _, k := range keys {
		got, inSource := scanned[k]
		site, inTable := gateSites2447[k]
		switch {
		case !inTable:
			t.Errorf("%s calls %v but is not in gateSites2447: add it, and its routes to routes2428", k, got)
		case !inSource:
			t.Errorf("%s is in gateSites2447 but calls no helper any more: remove it, and its routes from routes2428", k)
		case !slices.Equal(got, site.calls):
			t.Errorf("%s calls %v, gateSites2447 says %v: update the site and the gate of its routes2428 rows", k, got, site.calls)
		case len(site.routes) == 0 && site.why == "":
			t.Errorf("%s guards no route and gives no reason", k)
		}
	}
}

// TestGateSites2447_RoutesAgree: every routes2428 row is guarded by a site
// calling exactly its gate, and every route a site names is a routes2428
// row.
func TestGateSites2447_RoutesAgree(t *testing.T) {
	t.Parallel()
	gateOf := map[string]string{}
	for _, rt := range routes2428 {
		gateOf[r2447(rt.method, rt.path)] = rt.gate.name
	}
	covered := map[string]bool{}
	for k, site := range gateSites2447 {
		for _, route := range site.routes {
			g, ok := gateOf[route]
			if !ok {
				t.Errorf("%s names %q, which is not a routes2428 row", k, route)
				continue
			}
			if !slices.Contains(site.calls, g) {
				t.Errorf("%s guards %q with %v, routes2428 says %s", k, route, site.calls, g)
			}
			covered[route] = true
		}
	}
	for route := range gateOf {
		if !covered[route] {
			t.Errorf("routes2428 row %q is guarded by no gateSites2447 site", route)
		}
	}
}
