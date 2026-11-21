package authz_test

// #2447: drift guard for the #2428 route table. TestTenantGates2428_*
// checks the gates listed in routes2428, but that list is kept by hand, so
// a handler could move from one helper to the other, or gain a helper
// gate, without the table (and so the OPA agreement check) noticing.
//
// This test parses every non-test Go file under internal/api and records,
// per function, the authz.IsTenantAdmin / IsTenantApprover /
// IsTenantRiskReader (#2556) / HasAnyTenantRole (#2507) calls in
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
	"fmt"
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
	admin2447       = "IsTenantAdmin"
	approver2447    = "IsTenantApprover"
	riskReader2447  = "IsTenantRiskReader"
	auditReader2447 = "IsTenantAuditReader"
	anyRole2447     = "HasAnyTenantRole"
)

// gateSites2447 is keyed by "<dir under internal/api>/<file> <func>", with
// methods as "Recv.Name".
var gateSites2447 = func() map[string]gateSite2447 {
	const id = id2428
	adm := []string{admin2447}
	apr := []string{approver2447}
	rdr := []string{riskReader2447}
	aud := []string{auditReader2447}
	anyr := []string{anyRole2447}
	get, post, patch, put, del := http.MethodGet, http.MethodPost, http.MethodPatch, http.MethodPut, http.MethodDelete
	site := func(calls []string, routes ...string) gateSite2447 {
		return gateSite2447{calls: calls, routes: routes}
	}
	notRoute := func(calls []string, why string) gateSite2447 {
		return gateSite2447{calls: calls, why: why}
	}
	const preRoute = "tenant-admin gate that predates #2428 and is not in routes2428"
	return map[string]gateSite2447{
		// #2507: the last `len(OwnerRoles) > 0` read gates.
		"actionplans/authz.go requireProgramRead": site(apr,
			r2447(get, "/v1/action-plans"),
			r2447(get, "/v1/action-plans/"+id)),
		"actionplans/authz.go requireProgramWrite": site(adm,
			r2447(post, "/v1/action-plans"),
			r2447(patch, "/v1/action-plans/"+id),
			r2447(del, "/v1/action-plans/"+id),
			r2447(post, "/v1/action-plans/"+id+"/risks/"+id),
			r2447(del, "/v1/action-plans/"+id+"/risks/"+id),
			r2447(post, "/v1/action-plans/"+id+"/controls/"+id),
			r2447(del, "/v1/action-plans/"+id+"/controls/"+id)),
		"actionplans/handlers.go Handler.PlansForRisk":    site(rdr, r2447(get, "/v1/risks/"+id+"/action-plans")),
		"actionplans/handlers.go Handler.PlansForControl": site(anyr, r2447(get, "/v1/controls/"+id+"/action-plans")),
		"audit/export.go samplesHasProgramRead":           site(apr, r2447(get, "/v1/admin/samples/export")),
		"evidence/export.go evidenceHasProgramRead":       site(apr, r2447(get, "/v1/admin/evidence/export")),
		"exceptions/export.go exceptionsHasProgramRead":   site(apr, r2447(get, "/v1/admin/exceptions/export")),
		"policies/export.go policiesHasProgramRead":       site(apr, r2447(get, "/v1/admin/policies/export")),
		"oscalcomponents/handler.go hasOscalRead": site(apr,
			r2447(get, "/v1/oscal/component-definitions"),
			r2447(get, "/v1/oscal/component-definitions/"+id)),
		"oscalprovenance/handler.go hasOscalRead": site(apr,
			r2447(get, "/v1/oscal/imported-profiles/"+id+"/provenance")),
		"personnelsecurity/authz.go hasProgramRead": site(apr,
			r2447(get, "/v1/personnel-security/checklists"),
			r2447(get, "/v1/personnel-security/checklists/"+id),
			r2447(post, "/v1/personnel-security/checklists"),
			r2447(post, "/v1/personnel-security/checklist-items/"+id+"/complete")),
		"evidencesummary/portfoliohandler.go PortfolioHandler.PortfolioEvidenceSummary": site(apr,
			r2447(get, "/v1/evidence-summary/portfolio")),
		"evidencesummary/handler.go hasControlRead": site(anyr, r2447(get, "/v1/controls/"+id+"/evidence-summary")),
		"gapexplain/handler.go hasControlRead":      site(anyr, r2447(get, "/v1/controls/"+id+"/gap-explanation")),
		// Also GET /v1/evidence and /v1/evidence/{id}, where OPA is the
		// narrower layer: read_gates_2507_test.go pins those.
		"controldetail/authz.go hasControlRead": site(anyr,
			r2447(get, "/v1/controls/"+id+"/policies"),
			r2447(get, "/v1/controls/"+id+"/risks"),
			r2447(get, "/v1/controls/"+id+"/history"),
			r2447(get, "/v1/controls/"+id+"/anchor-history")),
		"controls/export.go controlsHasProgramRead": site(anyr,
			r2447(get, "/v1/controls/export"),
			r2447(get, "/v1/controls/history/export")),
		"dashboard/authz.go hasProgramRead": site(anyr,
			r2447(get, "/v1/activity"),
			r2447(get, "/v1/frameworks/posture")),
		"actionplans/handlers.go Handler.VerifyActionPlan": site(adm, r2447(post, "/v1/action-plans/"+id+"/verify")),
		"adminusers/moduleaccess.go Handler.moduleAccessTarget": site(adm,
			r2447(put, "/v1/admin/users/"+id+"/module-access")),
		// #2503: the tenant's own users. Assign and revoke also run it, for
		// the within-tenant path; their routes need a body this table
		// cannot send, so the two GETs and PATCH stand for the gate.
		// #2503: the tenant's SSO config and its preflight. PATCH shares the
		// gate; handlerNarrower2428 keys on path, so the GET row stands for it
		// (adminusers' 2503 integration test sends the PATCH).
		"adminsso/handler.go requireAdmin": site(adm,
			r2447(get, "/v1/admin/sso"),
			r2447(post, "/v1/admin/sso/preflight")),
		// #2503: the tenant's SCIM bearer. POST shares the gate with GET on
		// the same path, which handlerNarrower2428 keys on.
		"adminscim/http.go requireAdmin": site(adm,
			r2447(get, "/v1/admin/scim-credentials"),
			r2447(del, "/v1/admin/scim-credentials/"+id)),
		// #2503: the tenant's IdP group-to-role map. POST shares the gate with
		// GET on the same path, which handlerNarrower2428 keys on.
		"admingroupmappings/http.go requireAdmin": site(adm,
			r2447(get, "/v1/admin/group-role-mappings"),
			r2447(del, "/v1/admin/group-role-mappings/"+id)),
		// #2503: the tenant's module and feature switches.
		"features/handler.go requireAdmin": site(adm,
			r2447(get, "/v1/admin/features"),
			r2447(patch, "/v1/admin/features/policy.enabled")),
		// #2503: the tenant's API keys, within mintWithinCaller. POST /issue
		// shares the GET path, which handlerNarrower2428 keys on.
		"admincreds/http.go requireAdmin": site(adm,
			r2447(get, "/v1/admin/credentials"),
			r2447(post, "/v1/admin/credentials/"+id+"/revoke")),
		// #2503 D5: the tenant's feature-flag flip log.
		"adminauditlog/handler.go requireAdmin": site(adm, r2447(get, "/v1/admin/audit-log")),
		// #2503 D5: the unified read and its export admit IsTenantAdmin or a
		// live auditor/grc_engineer user_roles row (callerAllowedUnified, a
		// DB lookup this cred-only table cannot express). slice124_test.go,
		// slice135_test.go and adminusers' 2503 integration test pin them.
		"adminauditlog/unified.go Handler.UnifiedList": notRoute(adm,
			"unified audit-log read: IsTenantAdmin or a live auditor/grc_engineer row (callerAllowedUnified, #2503 D5)"),
		"adminauditlog/export.go Handler.ExportUnified": notRoute(adm,
			"unified audit-log export: IsTenantAdmin or a live auditor/grc_engineer row (callerAllowedUnified, #2503 D5)"),
		"adminauditlog/activity.go Handler.ActivityList": notRoute(adm,
			"activity ledger (#2503 D5): picks the privileged view for an admin; never a refusal"),
		"adminauditlog/activity_actors.go Handler.ActivityActors": notRoute(adm,
			"activity machine actors (#2414): the same privileged-view choice as ActivityList; never a refusal"),
		"tenants/handler.go Handler.callerCanRename": site(adm, r2447(patch, "/v1/tenants/"+id)),
		"adminusers/handler.go requireAdmin": site(adm,
			r2447(get, "/v1/admin/users/"+id),
			r2447(patch, "/v1/admin/users/"+id+"/roles")),
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
		"checklist/handler.go Handler.ExportMarkdown":    site(anyr, r2447(get, "/v1/controls/checklist/"+id+"/export.md")),
		"checklist/handler.go Handler.LoadGeneration":    site(anyr, r2447(get, "/v1/controls/checklist/"+id)),
		"controldetail/authz.go readsUnscopedEvidence": notRoute(apr,
			"per-control evidence list (#2470): narrows a period-scoped caller to control_id resolution; never a refusal"),
		"controlimports/settings.go requireTenantAdmin":     notRoute(adm, preRoute+" (control import LLM settings)"),
		"controls/handlers.go Handler.UploadBundle":         site(adm, r2447(post, "/v1/controls:upload-bundle")),
		"controls/heldback.go HeldBackHandler.List":         site(adm, r2447(get, "/v1/admin/controls/held-back")),
		"controlstate/evaluate.go EvaluateHandler.Evaluate": site(adm, r2447(post, "/v1/controls/"+id+"/evaluate")),
		"csfassessment/handler.go Handler.editContext": site(apr,
			r2447(put, "/v1/csf/tier"),
			r2447(put, "/v1/csf/profiles/target"),
			r2447(put, "/v1/csf/profiles/target/selections"),
			r2447(del, "/v1/csf/profiles/target/selections/"+id)),
		"dashboardexport/dashboardexport.go hasDashboardExportAccess": site(aud, r2447(get, "/v1/dashboard/export")),
		"evidencesummary/periodhandler.go hasAuditWorkspaceRead": site(apr,
			r2447(get, "/v1/audit-periods/"+id+"/controls/"+id+"/evidence-summary")),
		"exceptions/handlers.go Handler.Activate":     site(apr, r2447(patch, "/v1/exceptions/"+id+"/activate")),
		"exceptions/handlers.go Handler.Approve":      site(apr, r2447(patch, "/v1/exceptions/"+id+"/approve")),
		"exceptions/handlers.go Handler.Deny":         site(apr, r2447(patch, "/v1/exceptions/"+id+"/deny")),
		"exceptions/handlers.go Handler.Revoke":       site(apr, r2447(patch, "/v1/exceptions/"+id+"/revoke")),
		"frameworkscopes/handlers.go Handler.Approve": site(apr, r2447(patch, "/v1/framework-scopes/"+id+"/approve")),
		"frameworkscopes/handlers.go Handler.writerContext": site(apr,
			r2447(post, "/v1/framework-scopes"),
			r2447(patch, "/v1/framework-scopes/"+id),
			r2447(patch, "/v1/framework-scopes/"+id+"/submit"),
			r2447(patch, "/v1/framework-scopes/"+id+"/activate"),
			r2447(put, "/v1/framework-scopes/"+id+"/applicability/"+id),
			r2447(post, "/v1/framework-scopes/"+id+"/applicability/bulk"),
			r2447(post, "/v1/framework-scopes/"+id+"/applicability/import"),
			r2447(post, "/v1/framework-scopes/"+id+"/applicability:carry-over")),
		"exceptions/handlers.go Handler.CreateException":        site(apr, r2447(post, "/v1/exceptions")),
		"llmrouting/handler.go requireTenantAdmin":              notRoute(adm, preRoute+" (LLM routing)"),
		"mcpwriteproposals/handlers.go Handler.ConfirmProposal": site(adm, r2447(post, "/v1/mcp/write-proposals/"+id+"/confirm")),
		"mcpwriteproposals/handlers.go Handler.RejectProposal":  site(adm, r2447(post, "/v1/mcp/write-proposals/"+id+"/reject")),
		"me/profile.go ProfileHandler.buildProfile": notRoute([]string{admin2447, approver2447},
			"reports is_tenant_admin and is_approver on /v1/me; not a gate"),
		"me/profile.go profileWireFrom": notRoute([]string{admin2447, approver2447},
			"reports is_tenant_admin and is_approver on /v1/me; not a gate"),
		"metrics/handlers.go Handler.CreateInput":  site(adm, r2447(post, "/v1/metrics/"+id+"/inputs")),
		"metrics/handlers.go Handler.UpsertTarget": site(apr, r2447(put, "/v1/metrics/"+id+"/target")),
		"oscalcomponents/handler.go Handler.MapScfAnchor": site(adm,
			r2447(patch, "/v1/oscal/component-claims/"+id+"/scf-anchor")),
		"oscalcomponents/handler.go Handler.disposition": site(adm,
			r2447(post, "/v1/oscal/component-claims/"+id+":accept"),
			r2447(post, "/v1/oscal/component-claims/"+id+":reject"),
			r2447(post, "/v1/oscal/component-claims/"+id+":needs-info")),
		"policies/handlers.go Handler.CreatePolicy":  site(apr, r2447(post, "/v1/policies")),
		"policies/handlers.go Handler.Submit":        site(apr, r2447(patch, "/v1/policies/"+id+"/submit")),
		"policies/handlers.go Handler.Approve":       site(apr, r2447(patch, "/v1/policies/"+id+"/approve")),
		"policies/handlers.go Handler.Publish":       site(apr, r2447(post, "/v1/policies/"+id+"/publish")),
		"policies/handlers.go Handler.SetReviewDate": site(apr, r2447(patch, "/v1/policies/"+id+"/review-date")),
		"policies/handlers.go Handler.SetControls":   site(apr, r2447(patch, "/v1/policies/"+id+"/controls")),
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
		"risks/handlers.go Handler.CreateRisk": site(apr, r2447(post, "/v1/risks")),
		"risks/transitions.go Handler.runTransition": site(apr,
			r2447(patch, "/v1/risks/"+id+"/close"),
			r2447(patch, "/v1/risks/"+id+"/reopen"),
			r2447(patch, "/v1/risks/"+id+"/treatment"),
			r2447(patch, "/v1/risks/"+id+"/acceptance/propose"),
			r2447(patch, "/v1/risks/"+id+"/acceptance/approve"),
			r2447(patch, "/v1/risks/"+id+"/acceptance/reject"),
			r2447(patch, "/v1/risks/"+id+"/acceptance/revoke")),
		"risks/riskimports/handlers.go decidingPerson": site(apr,
			r2447(post, "/v1/risks/imports/"+id+"/approve"),
			r2447(post, "/v1/risks/imports/"+id+"/rows/"+id+"/approve"),
			r2447(post, "/v1/risks/imports/"+id+"/rows/"+id+"/reject")),
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

// authzImport2447 is the import path the helpers live in.
const authzImport2447 = "github.com/mgoodric/security-atlas/internal/authz"

// isHelper2447 reports whether name is one of the inventoried helpers.
func isHelper2447(name string) bool {
	switch name {
	case admin2447, approver2447, riskReader2447, auditReader2447, anyRole2447:
		return true
	}
	return false
}

// gateEscapes2447 (#2456) returns each way file f could reach a helper
// without the scan seeing an `authz.<helper>(...)` call: an aliased or dot
// import of internal/authz, or a helper used other than as the callee of a
// call (`f := authz.IsTenantApprover`). A wrapper in another package is
// still out of sight (#2447 D3).
func gateEscapes2447(fset *token.FileSet, f *ast.File) []string {
	var out []string
	for _, imp := range f.Imports {
		if strings.Trim(imp.Path.Value, "`\"") != authzImport2447 || imp.Name == nil || imp.Name.Name == "authz" {
			continue
		}
		out = append(out, fmt.Sprintf("%s: imports internal/authz as %q; import it unaliased so the gate scan sees its calls",
			fset.Position(imp.Pos()), imp.Name.Name))
	}
	callees := map[*ast.SelectorExpr]bool{}
	ast.Inspect(f, func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok {
			if sel, ok := ast.Unparen(call.Fun).(*ast.SelectorExpr); ok {
				callees[sel] = true
			}
		}
		return true
	})
	ast.Inspect(f, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok || callees[sel] || !isHelper2447(sel.Sel.Name) {
			return true
		}
		if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "authz" {
			out = append(out, fmt.Sprintf("%s: uses authz.%s as a value; call it directly so the gate scan sees it",
				fset.Position(sel.Pos()), sel.Sel.Name))
		}
		return true
	})
	return out
}

// scanGateSites2447 returns the helper calls per function under
// internal/api, test files excluded. It fails the test on any file
// gateEscapes2447 flags.
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
		for _, e := range gateEscapes2447(fset, f) {
			t.Error(e)
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
				sel, ok := ast.Unparen(call.Fun).(*ast.SelectorExpr)
				if !ok {
					return true
				}
				pkg, ok := sel.X.(*ast.Ident)
				if ok && pkg.Name == "authz" && isHelper2447(sel.Sel.Name) {
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

// TestGateSites2447_EscapesCaught (#2456): gateEscapes2447 flags each way a
// helper could be reached out of the scan's sight, and passes a plain call.
func TestGateSites2447_EscapesCaught(t *testing.T) {
	t.Parallel()
	src := func(name, body string) string {
		return "package p\n\nimport " + name + " \"" + authzImport2447 + "\"\n\n" + body + "\n"
	}
	cases := []struct {
		name, src string
		escapes   int
	}{
		{"plain call", src("", "func f() { _ = authz.IsTenantAdmin(nil) }"), 0},
		{"parenthesised call", src("", "func f() { _ = (authz.IsTenantAdmin)(nil) }"), 0},
		{"explicit authz name", src("authz", "func f() { _ = authz.IsTenantAdmin(nil) }"), 0},
		{"alias", src("az", "func f() { _ = az.IsTenantAdmin(nil) }"), 1},
		{"dot import", src(".", "func f() { _ = IsTenantAdmin(nil) }"), 1},
		{"function value", src("", "func f() { g := authz.IsTenantApprover; _ = g(nil) }"), 1},
		{"function argument", src("", "func f() { run(authz.HasAnyTenantRole) }"), 1},
		{"package-level value", src("", "var g = authz.IsTenantRiskReader"), 1},
		{"non-helper value", src("", "var g = authz.Other"), 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fset := token.NewFileSet()
			f, err := parser.ParseFile(fset, "x.go", tc.src, parser.SkipObjectResolution)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if got := gateEscapes2447(fset, f); len(got) != tc.escapes {
				t.Errorf("escapes = %v, want %d", got, tc.escapes)
			}
		})
	}
}
