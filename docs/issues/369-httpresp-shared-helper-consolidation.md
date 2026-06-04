# 369 — Consolidate `writeJSON` / `writeError` into shared `internal/api/httpresp`

**Cluster:** Infra
**Estimate:** 3d
**Type:** JUDGMENT
**Status:** `merged` (status reconciled 2026-06-03 — backlog drained per \_STATUS.md SoR; loop terminated batch 184)

## Narrative

Slice 328's comprehensive code-review audit (`docs/audits/328-code-review-comprehensive-report.md` finding **H-1**, severity **High**) surfaced 101 duplicate-helper declarations across 50+ `internal/api/*` packages:

```
  41 func writeError(w http.ResponseWriter, code int, msg string) {
  37 func writeJSON(w http.ResponseWriter, code int, body any) {
  18 func writeServerErr(w http.ResponseWriter, r *http.Request, op string, err error) {
   3 func writeError(w http.ResponseWriter, status int, msg string) {  // parameter rename
   2 func writeJSON(w http.ResponseWriter, status int, body any) {     // parameter rename
```

All 41 `writeError` and 37 `writeJSON` bodies are byte-identical (verified across `internal/api/board/handlers.go:287`, `internal/api/metrics/handlers.go:672`, `internal/api/admintenants/handler.go:717`, `internal/api/adminsso/handler.go:527`, and 10 more samples) to:

```go
func writeJSON(w http.ResponseWriter, code int, body any) {
    w.Header().Set("Content-Type", "application/json")
    w.WriteHeader(code)
    _ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, code int, msg string) {
    writeJSON(w, code, map[string]string{"error": msg})
}
```

The 18 `writeServerErr` declarations are the slice-367 intermediate adapter that delegates to `httperr.WriteInternal` internally — they should be retired in this slice along with the writeJSON/writeError consolidation, completing the slice 367 cleanup.

### What ships

1. **New package `internal/api/httpresp`** — mirror of `internal/api/httperr`'s scope and conventions:

```go
// Package httpresp is the slice 369 shared helper for emitting JSON
// HTTP responses without duplicating the 78-instance writeJSON/
// writeError pattern across internal/api/*.
//
// Slice 367's internal/api/httperr is the precedent — it owns 5xx
// responses (generic error body, request_id, slog). This package owns
// 2xx + 4xx responses (success bodies, user-input errors).
package httpresp

// WriteJSON writes a JSON response with the given status code.
func WriteJSON(w http.ResponseWriter, status int, body any) { /* ... */ }

// WriteError writes a JSON {"error": msg} response with the given status code.
func WriteError(w http.ResponseWriter, status int, msg string) { /* ... */ }
```

2. **Mechanized migration** — 78 `writeJSON` + `writeError` sites in `internal/api/*` rewritten to use `httpresp.WriteJSON` / `httpresp.WriteError`. The bodies are byte-identical so a `sed` sweep is safe; the resulting diff is large but mechanical.

3. **Retire `writeServerErr` wrappers** — the 18 per-package adapters now call `httperr.WriteInternal` directly at the call site. Closes the slice 367 §D2 deferred follow-up.

4. **Custom lint rule** — golangci-lint analyzer (mirrors `cmd/scripts/errleak-lint`) rejecting new `func writeJSON | writeError | writeServerErr` declarations in `internal/api/*`. Catches regressions.

5. **Two integration tests rewired** — pick the two existing handler tests that assert response-shape behavior; reassert against `httpresp.WriteJSON` shape directly to lock the contract.

### JUDGMENT calls

The engineer makes the following design calls and records them in `docs/audit-log/369-httpresp-shared-helper-consolidation-decisions.md`:

- **Parameter name.** `status int` (per chi/standard library convention) vs `code int` (per existing duplicate-instance majority). Recommend `status int` to match net/http.
- **Lint scope.** Reject ONLY new declarations OR also reject all existing duplicates not yet migrated? Recommend reject-new + grandfather-during-migration; flip to reject-all in a follow-up PR after the migration PR lands.
- **`writeServerErr` retirement timing.** Bundle into this slice OR ship as 369-followup? Recommend bundle — the 18 sites change pattern in the same direction as the writeJSON/writeError migration.
- **`web/` parallel?** Frontend has a similar duplication pattern across BFF routes. Out of scope for this slice; file as follow-up if applicable.

### Why this matters

1. **Drift surface.** Slice 367's migration touched 36 files for the 5xx-only case. A future change to the 2xx/4xx response shape (adding `request_id`, switching to RFC 7807, etc.) would touch 50+ files today. Consolidation collapses the maintenance surface to one file.
2. **Inconsistent naming.** Three handlers use `status int` while 41 use `code int` for the same parameter. Five packages also have `writeServerErr`; 45 do not. The lint rule prevents this kind of slow-drift.
3. **AI-navigability.** Code search for "how does this API respond?" surfaces 78 hits today; should be one package after this slice.
4. **Closes slice 367 thread.** The `writeServerErr` retirement is the unwound half of slice 367's decisions log §D2.

### Why now

H-1 from the slice 328 audit. Mechanical cleanup; comfortable to schedule any time before the next 2xx/4xx response-shape evolution.

**Trigger:** filed 2026-05-28 from slice 328 audit.

## Threat model

Code-quality consolidation only. STRIDE pass on the migration activity:

- **S (Spoofing):** N/A.
- **T (Tampering):** N/A.
- **R (Repudiation):** N/A (no log-shape change).
- **I (Information disclosure):** **Improved** — centralizing the response-body shape makes it easier to add future hardening (e.g., a `request_id` field in every error response) without missing handlers.
- **D (Denial of service):** N/A.
- **E (Elevation of privilege):** N/A.

## Acceptance criteria

- [ ] **AC-1.** `internal/api/httpresp` exists with `WriteJSON` and `WriteError` exported and tested.
- [ ] **AC-2.** All 78 `writeJSON` + `writeError` declarations across `internal/api/*` removed; call sites use `httpresp.WriteJSON` / `httpresp.WriteError`.
- [ ] **AC-3.** All 18 `writeServerErr` declarations removed; call sites use `httperr.WriteInternal` directly.
- [ ] **AC-4.** Custom golangci-lint analyzer (or `analysistest` fixture) rejects new declarations of `writeJSON | writeError | writeServerErr` in `internal/api/*`. Wired into CI as hard failure.
- [ ] **AC-5.** Two existing integration tests reassert response-shape contract against `httpresp.WriteJSON` directly.
- [ ] **AC-6.** No behavior change at the HTTP wire — response bodies and status codes identical before and after.
- [ ] **AC-7.** `pre-commit run --all-files` passes; CI green; coverage gate doesn't regress.

## Constitutional invariants honored

- **Article VII (Simplicity Gate).** Consolidates 101 duplicate sites into 1 package.
- **Article VIII (Anti-abstraction Gate).** The shared package IS the abstraction — but it replaces 50+ per-package abstractions, so the net abstraction count drops.

## Canvas references

- Slice 328 audit report `docs/audits/328-code-review-comprehensive-report.md` finding H-1
- Slice 367 precedent (`internal/api/httperr`)
- Slice 367 decisions log §D2 (`writeServerErr` deferred follow-up)

## Dependencies

- **#367** (httperr) — `merged`. Establishes the shared-helper pattern this slice extends.
- **#069** (testing discipline) — `merged`. CI surfaces the new lint rule.

## Anti-criteria (P0 — block merge)

- **P0-369-1.** Does NOT change response-body shape or status codes at the HTTP wire. Mechanical rename + delegation only.
- **P0-369-2.** Does NOT bundle httpresp creation into the same PR as the migration if the diff becomes unreviewable. Acceptable to ship in two PRs (package + lint, then migration) — engineer's call.
- **P0-369-3.** Does NOT remove `writeServerErr` from packages where its sentinel-mapping logic (`writePackError`, `writeBundleError`, `writeStoreErr`) does real work — only the trivial delegate-to-httperr.WriteInternal wrappers retire.
- **P0-369-4.** Does NOT auto-merge.
- **P0-369-5.** Does NOT regress coverage floor — write missing tests in the SAME PR as any floor lift per the CLAUDE.md testing discipline section.

## Skill mix

- `tdd` — RED-first tests for the httpresp package
- `simplify` — pre-PR quality pass on the migration diff

## Notes for the implementing agent

Suggested phased approach:

1. **Phase 1 (1d):** Create `internal/api/httpresp` with full test coverage. Wire lint rule (analyzer + CI). Migrate ONE handler package end-to-end as the pattern proof. Land as PR #1.
2. **Phase 2 (1.5d):** Mechanized migration of the remaining 49 packages. Diff will be large but byte-identical changes — review with a `git diff --stat` lens. Land as PR #2.
3. **Phase 3 (0.5d):** Retire the 18 `writeServerErr` trivial wrappers. Audit each — some `writeServerErr` impls do real sentinel-mapping work (`writePackError`, etc.) and should NOT be retired. Land as PR #3.

The lint rule shape: a custom `golang.org/x/tools/go/analysis` analyzer that walks AST top-level FuncDecls and rejects any `writeJSON | writeError | writeServerErr` whose receiver is package-scoped and whose package path matches `internal/api/.+`.

Two integration tests to rewire (suggested): pick handlers that already assert response-shape behavior. `internal/api/board/handlers_test.go` and `internal/api/metrics/handlers_test.go` are reasonable picks — both have direct response-shape assertions today.

If the diff size becomes unreviewable, split by alphabetical package prefix (admin\* → batch 1, controls/board/audit → batch 2, etc.). The cleanup is internally serial but externally parallelizable.
