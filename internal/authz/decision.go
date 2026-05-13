package authz

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"path"
	"strings"

	"github.com/open-policy-agent/opa/v1/ast"
	"github.com/open-policy-agent/opa/v1/rego"
	"github.com/open-policy-agent/opa/v1/storage/inmem"
)

// Decision is the result of an authz.Decide call. Reason captures the
// human-readable explanation for the audit log (e.g. "default-deny —
// no role rule matched"); PolicyHits captures which .rego files'
// `allow` rule fired.
type Decision struct {
	Allow      bool
	Reason     string
	PolicyHits []string
}

// Engine wraps a prepared OPA query. It is goroutine-safe and intended
// to be constructed once at startup and reused for every request.
type Engine struct {
	query    rego.PreparedEvalQuery
	resolver RolesResolver
	// attrsResolver hydrates per-user ABAC attributes (slice 025). nil
	// is treated as NoopAttrsResolver; production callers wire a
	// DB-backed implementation via WithAttrsResolver after NewEngine.
	attrsResolver AttrsResolver
}

// NewEngine constructs an Engine from the embedded policies/authz/*.rego
// bundle. resolver is the optional DB-backed role lookup; pass
// NoopRolesResolver{} when DB lookup is not required.
//
// The attribute resolver (slice 025) is set separately via
// WithAttrsResolver so this signature stays stable for existing
// callers. NewEngine leaves attrsResolver at its zero value, which
// Engine.Decide treats as NoopAttrsResolver.
func NewEngine(ctx context.Context, resolver RolesResolver) (*Engine, error) {
	if resolver == nil {
		resolver = NoopRolesResolver{}
	}
	modules, err := embeddedPolicies()
	if err != nil {
		return nil, fmt.Errorf("authz: load embedded policies: %w", err)
	}
	opts := []func(*rego.Rego){
		rego.Query("data.authz.allow"),
		rego.Store(inmem.New()),
	}
	for _, m := range modules {
		opts = append(opts, rego.ParsedModule(m))
	}
	q, err := rego.New(opts...).PrepareForEval(ctx)
	if err != nil {
		return nil, fmt.Errorf("authz: prepare query: %w", err)
	}
	return &Engine{query: q, resolver: resolver}, nil
}

// WithAttrsResolver wires the slice-025 attribute resolver. Returns the
// receiver so callers can chain. Safe to call before Decide is ever
// invoked; not safe to call concurrently with Decide.
func (e *Engine) WithAttrsResolver(r AttrsResolver) *Engine {
	e.attrsResolver = r
	return e
}

// Decide evaluates the input against the loaded policies. Default-deny
// applies: if no rule fires `allow := true`, the Decision has
// Allow=false with a default-deny reason.
//
// The resolver is consulted to merge user_roles table rows into the
// derived-from-flags role set. When the resolver returns roles, they
// REPLACE the derived set; the caller can pre-populate input.User.Roles
// to skip the resolver (slice-014 admincreds use this path).
func (e *Engine) Decide(ctx context.Context, in Input) (Decision, error) {
	// Resolver hook: enrich roles from user_roles table when the input
	// arrived without any (or with only credential-derived roles).
	if in.TenantID != "" && in.User.ID != "" {
		dbRoles, err := e.resolver.RolesFor(ctx, in.TenantID, in.User.ID)
		if err != nil {
			return Decision{}, fmt.Errorf("authz: resolve roles: %w", err)
		}
		if len(dbRoles) > 0 {
			in.User.Roles = mergeRoles(in.User.Roles, dbRoles)
		}
	}

	// Slice 025: hydrate auditor ABAC attributes (audit_period_ids)
	// when the request is from an auditor AND the attrs map doesn't
	// already carry them (tests can pre-populate to skip this hop).
	if e.attrsResolver != nil &&
		in.TenantID != "" && in.User.ID != "" &&
		hasAuditorRole(in.User.Roles) &&
		!hasAuditPeriodIDsAttr(in.User.Attrs) {
		extra, err := e.attrsResolver.AttrsFor(ctx, in.TenantID, in.User.ID, in.User.Roles)
		if err != nil {
			return Decision{}, fmt.Errorf("authz: resolve attrs: %w", err)
		}
		if len(extra) > 0 {
			if in.User.Attrs == nil {
				in.User.Attrs = map[string]interface{}{}
			}
			for k, v := range extra {
				if _, exists := in.User.Attrs[k]; !exists {
					in.User.Attrs[k] = v
				}
			}
		}
	}

	results, err := e.query.Eval(ctx, rego.EvalInput(toRegoInput(in)))
	if err != nil {
		return Decision{}, fmt.Errorf("authz: eval: %w", err)
	}
	if len(results) == 0 {
		return Decision{
			Allow:  false,
			Reason: "default-deny: no policy result",
		}, nil
	}
	allow, ok := results[0].Expressions[0].Value.(bool)
	if !ok {
		return Decision{
			Allow:  false,
			Reason: fmt.Sprintf("default-deny: policy returned non-bool %T", results[0].Expressions[0].Value),
		}, nil
	}
	if !allow {
		return Decision{
			Allow:  false,
			Reason: defaultDenyReason(in),
		}, nil
	}
	return Decision{
		Allow:  true,
		Reason: "allowed",
	}, nil
}

// hasAuditPeriodIDsAttr reports whether attrs already carries the
// `audit_period_ids` key. Used by Decide to skip the AttrsResolver hop
// when the caller (typically a unit test) has pre-populated the
// attribute map.
func hasAuditPeriodIDsAttr(attrs map[string]interface{}) bool {
	if attrs == nil {
		return false
	}
	_, ok := attrs["audit_period_ids"]
	return ok
}

// mergeRoles unions two role slices, dropping non-canonical roles.
func mergeRoles(a, b []Role) []Role {
	seen := map[Role]bool{}
	out := []Role{}
	for _, list := range [][]Role{a, b} {
		for _, r := range list {
			if !IsCanonical(r) || seen[r] {
				continue
			}
			seen[r] = true
			out = append(out, r)
		}
	}
	return out
}

func defaultDenyReason(in Input) string {
	if len(in.User.Roles) == 0 {
		return "default-deny: no roles assigned"
	}
	return fmt.Sprintf("default-deny: role %v cannot %s %s", in.User.Roles, in.Action, in.Resource.Type)
}

// toRegoInput converts the Go Input into the JSON-compatible map shape
// that OPA expects. Strings, slices, maps -- no pointers.
func toRegoInput(in Input) map[string]interface{} {
	roleStrings := make([]interface{}, 0, len(in.User.Roles))
	for _, r := range in.User.Roles {
		roleStrings = append(roleStrings, string(r))
	}
	user := map[string]interface{}{
		"id":    in.User.ID,
		"roles": roleStrings,
		"attrs": in.User.Attrs,
	}
	if user["attrs"] == nil {
		user["attrs"] = map[string]interface{}{}
	}
	resource := map[string]interface{}{
		"type":  in.Resource.Type,
		"id":    in.Resource.ID,
		"attrs": in.Resource.Attrs,
	}
	if resource["attrs"] == nil {
		resource["attrs"] = map[string]interface{}{}
	}
	return map[string]interface{}{
		"user":      user,
		"tenant_id": in.TenantID,
		"action":    in.Action,
		"resource":  resource,
		"request": map[string]interface{}{
			"method": in.Request.Method,
			"path":   in.Request.Path,
		},
	}
}

//go:embed all:rego_bundle
var embeddedRegoFS embed.FS

// embeddedPolicies parses every .rego file under the embedded bundle.
// The bundle is populated by `just authz-sync` (or at build time by a
// code-generation step); the source of truth lives at policies/authz/*.
// We use an embedded FS so the binary is self-contained and operators
// don't need to ship the policies directory alongside the binary.
func embeddedPolicies() (map[string]*ast.Module, error) {
	out := map[string]*ast.Module{}
	err := fs.WalkDir(embeddedRegoFS, "rego_bundle", func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(p, ".rego") {
			return nil
		}
		data, err := embeddedRegoFS.ReadFile(p)
		if err != nil {
			return fmt.Errorf("read %s: %w", p, err)
		}
		mod, err := ast.ParseModule(path.Base(p), string(data))
		if err != nil {
			return fmt.Errorf("parse %s: %w", p, err)
		}
		out[p] = mod
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("authz: embedded rego bundle is empty")
	}
	return out, nil
}
