package authz

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"
	"sync/atomic"

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
//
// Slice 378: the prepared query is stored behind an atomic.Pointer so
// the bundle can be hot-reloaded without restarting the process. Read
// path (Decide) loads the pointer once per call and Eval's against
// whichever snapshot it captured — in-flight calls during a Reload see
// EITHER the old query OR the new one, never a partial state. The
// guarantee is the entire load-bearing contract of slice 378.
type Engine struct {
	// query holds the active *rego.PreparedEvalQuery. Stored behind an
	// atomic.Pointer so Reload can swap the bundle without a mutex on
	// the read path. NEVER access via field-load: use loadQuery /
	// storeQuery to keep the atomic contract intact.
	query atomic.Pointer[rego.PreparedEvalQuery]

	// bundleSHA holds the SHA-256 of the currently-loaded bundle's
	// source bytes (sorted by filename, NUL-separated). Stored behind
	// an atomic.Pointer to a string so a snapshot read from the
	// reload-audit path matches the atomic swap of the query pointer.
	bundleSHA atomic.Pointer[string]

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
	modules, sources, err := embeddedPoliciesWithSources()
	if err != nil {
		return nil, fmt.Errorf("authz: load embedded policies: %w", err)
	}
	q, err := prepareQuery(ctx, modules)
	if err != nil {
		return nil, fmt.Errorf("authz: prepare query: %w", err)
	}
	e := &Engine{resolver: resolver}
	e.storeQuery(q)
	sha := bundleSHA256(sources)
	e.storeBundleSHA(sha)
	return e, nil
}

// prepareQuery builds a fresh *rego.PreparedEvalQuery from the supplied
// parsed modules. Factored out of NewEngine + Reload so the two paths
// produce structurally-identical queries (same Query, same Store
// shape).
func prepareQuery(ctx context.Context, modules map[string]*ast.Module) (rego.PreparedEvalQuery, error) {
	opts := []func(*rego.Rego){
		rego.Query("data.authz.allow"),
		rego.Store(inmem.New()),
	}
	// Iterate modules in sorted-filename order so the resulting
	// PreparedEvalQuery is deterministic across calls — handy for
	// equality assertions in tests and for the matrix-validator's
	// pre-swap probe.
	keys := make([]string, 0, len(modules))
	for k := range modules {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		opts = append(opts, rego.ParsedModule(modules[k]))
	}
	return rego.New(opts...).PrepareForEval(ctx)
}

// loadQuery returns the active prepared query. Read path uses this
// once per Decide call so an in-flight Reload cannot tear the value.
func (e *Engine) loadQuery() *rego.PreparedEvalQuery {
	return e.query.Load()
}

// storeQuery atomically replaces the active prepared query. Only the
// Reload path and the NewEngine constructor invoke this.
func (e *Engine) storeQuery(q rego.PreparedEvalQuery) {
	e.query.Store(&q)
}

// BundleSHA256 returns the SHA-256 fingerprint of the currently-loaded
// bundle. Empty string when no bundle has been loaded (which is only
// reachable on a zero-value Engine — NewEngine + Reload both set it).
//
// The fingerprint is computed over the bundle source bytes (sorted by
// filename, NUL-separated). It is exposed for the slice-378 audit log
// which records before / after SHA values on every Reload.
func (e *Engine) BundleSHA256() string {
	if p := e.bundleSHA.Load(); p != nil {
		return *p
	}
	return ""
}

func (e *Engine) storeBundleSHA(sha string) {
	e.bundleSHA.Store(&sha)
}

// MatrixValidator runs the canonical slice-026 role × endpoint matrix
// against a candidate prepared query and returns nil on full pass or
// an error describing the first failure. It is supplied to Reload so
// the matrix can be evaluated against the NEW query BEFORE the atomic
// swap (slice 378 AC-3). nil is treated as "no validation requested"
// — Reload then swaps without a matrix probe; callers wiring the
// production reload path MUST supply a real validator to honour the
// constitutional gate that a malformed reload never reaches Decide.
type MatrixValidator func(ctx context.Context, candidate *rego.PreparedEvalQuery) error

// Reload prepares a new query from the supplied modules and atomically
// swaps it into the Engine in place of the currently-loaded query. The
// receiver's resolver + attrsResolver are preserved across the swap.
//
// Sequence (slice 378 AC-1 + AC-3):
//
//  1. Compile the candidate query from `modules`. A compile error
//     short-circuits the reload — the engine continues to serve the
//     prior query.
//  2. If `validator` is non-nil, run it against the candidate. A
//     validator error short-circuits the reload identically — the
//     prior query stays installed. This is the slice-026 matrix gate
//     that prevents a permissive bundle from reaching production.
//  3. Atomically swap the candidate into the Engine via
//     atomic.Pointer.Store. The new bundle SHA-256 is stored
//     identically (separate atomic — no requirement that the two
//     swaps appear simultaneous; an observer reading both fields in
//     between sees old query + new SHA OR new query + old SHA, both
//     non-load-bearing for the Decide path).
//
// `sources` is the original byte-string of each module keyed by
// filename. Used to compute the post-reload SHA-256 fingerprint. nil
// sources are accepted (Reload then preserves the pre-reload SHA),
// but production callers should always supply them so the audit log
// captures the post-reload fingerprint.
//
// Errors are wrapped with `authz: reload:` so callers can match the
// prefix for log filtering.
func (e *Engine) Reload(ctx context.Context, modules map[string]*ast.Module, sources map[string][]byte, validator MatrixValidator) error {
	if len(modules) == 0 {
		return fmt.Errorf("authz: reload: modules map is empty")
	}
	candidate, err := prepareQuery(ctx, modules)
	if err != nil {
		return fmt.Errorf("authz: reload: prepare query: %w", err)
	}
	if validator != nil {
		if vErr := validator(ctx, &candidate); vErr != nil {
			return fmt.Errorf("authz: reload: matrix validation failed: %w", vErr)
		}
	}
	e.storeQuery(candidate)
	if sources != nil {
		e.storeBundleSHA(bundleSHA256(sources))
	}
	return nil
}

// ReloadFromEmbedded reloads the engine from the embedded
// policies/authz/*.rego bundle. Convenience wrapper around Reload for
// the HTTP `POST /v1/admin/authz-bundle/reload` endpoint, which v1
// scopes to the embedded bundle (slice 378 P0-4 — user-authored
// bundles are v3+ work).
//
// The same validator semantics as Reload apply.
func (e *Engine) ReloadFromEmbedded(ctx context.Context, validator MatrixValidator) error {
	modules, sources, err := embeddedPoliciesWithSources()
	if err != nil {
		return fmt.Errorf("authz: reload: load embedded policies: %w", err)
	}
	return e.Reload(ctx, modules, sources, validator)
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

	// Slice 378: load the prepared-query pointer ONCE per Decide call.
	// If a concurrent Reload swaps the pointer between this Load and
	// the Eval below, this call still completes against the pre-swap
	// query — atomic.Pointer.Load returns a self-consistent snapshot.
	q := e.loadQuery()
	if q == nil {
		// Defensive: a zero-value Engine reaches this line. Production
		// callers go through NewEngine which always installs a query;
		// tests that construct a bare Engine without calling NewEngine
		// see a structured default-deny instead of a nil-pointer panic.
		return Decision{
			Allow:  false,
			Reason: "default-deny: authz engine has no loaded query",
		}, nil
	}
	results, err := q.Eval(ctx, rego.EvalInput(toRegoInput(in)))
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

// embeddedPoliciesWithSources parses every .rego file under the
// embedded bundle AND returns the raw source bytes per filename. The
// source map feeds the SHA-256 fingerprint computation that the
// slice-378 reload-audit path records.
//
// The bundle is populated by `just authz-sync` (or at build time by a
// code-generation step); the source of truth lives at
// policies/authz/*. We use an embedded FS so the binary is
// self-contained and operators don't need to ship the policies
// directory alongside the binary.
func embeddedPoliciesWithSources() (map[string]*ast.Module, map[string][]byte, error) {
	modules := map[string]*ast.Module{}
	sources := map[string][]byte{}
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
		modules[p] = mod
		// Copy the bytes so the source map isn't aliased to the
		// embed.FS internal buffer (defence-in-depth; embed.FS is
		// read-only today but copying keeps the API contract clean).
		sources[p] = append([]byte(nil), data...)
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	if len(modules) == 0 {
		return nil, nil, fmt.Errorf("authz: embedded rego bundle is empty")
	}
	return modules, sources, nil
}

// bundleSHA256 returns the SHA-256 fingerprint of the bundle's source
// bytes. Sources are folded into the hash in sorted-filename order
// with a NUL separator between filename and content (so "ab" + "c"
// cannot collide with "a" + "bc"). The hex-encoded digest is the
// stable identifier recorded in the audit log on every reload.
func bundleSHA256(sources map[string][]byte) string {
	if len(sources) == 0 {
		return ""
	}
	names := make([]string, 0, len(sources))
	for k := range sources {
		names = append(names, k)
	}
	sort.Strings(names)
	h := sha256.New()
	for _, n := range names {
		h.Write([]byte(n))
		h.Write([]byte{0})
		h.Write(sources[n])
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}
