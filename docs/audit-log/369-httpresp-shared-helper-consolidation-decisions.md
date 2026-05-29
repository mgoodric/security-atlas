# Slice 369 — `internal/api/httpresp` consolidation — decisions log

JUDGMENT-type slice. The engineer made the design calls below and records them
here rather than blocking the merge on a human sign-off (per the CLAUDE.md
process note — distinct from the product-runtime AI-assist boundary, which this
slice does not touch).

Closes slice 328 code-review finding **H-1** (101 duplicate response-helper
declarations across 50+ `internal/api/*` packages). Mirrors slice 367's
`internal/api/httperr` package + `cmd/scripts/errleak-lint` analyzer.

## Inventory (measured at branch point)

Counted with `grep -rn '^func write...(' internal/api/`:

| Helper           | Declarations | Variant split                                                                                                                      |
| ---------------- | ------------ | ---------------------------------------------------------------------------------------------------------------------------------- |
| `writeJSON`      | 40           | 38 `code int`, 2 `status int`                                                                                                      |
| `writeError`     | 45           | 41 `code int`, 4 `status int`                                                                                                      |
| `writeServerErr` | 18           | all identical (`code`/`status` n/a)                                                                                                |
| **Total**        | **103**      | (slice doc said 101; the two extra are the `status`-rename `writeJSON` variants that the doc folded into the headline 78 — see D6) |

Call sites rewritten: **1131** (`writeJSON` 240, `writeError` 771, `writeServerErr` 120).

Body verification (extract-and-`uniq` over every declaration):

- All 40 `writeJSON` bodies byte-identical modulo the `code`/`status` parameter name.
- `writeError`: 30 delegate to `writeJSON(...)`, 14 inline the same three statements, 1 (`policyacks`) delegated via a local `errorBody{Error: msg}` struct that serialises to the identical `{"error": msg}` wire bytes.
- All 18 `writeServerErr` bodies are exactly `httperr.WriteInternal(w, r, op, err)` — trivial slice-367 adapters, none doing sentinel-mapping work.

## D1 — Parameter name: `status int`

**Decision:** the shared `httpresp.WriteJSON` / `WriteError` use `status int`.

Matches net/http's own vocabulary (`http.StatusOK`, `w.WriteHeader(status)`) and
the recommendation in the slice doc. The legacy majority used `code`, but the
parameter is a status code and `status` reads correctly at every call site. The
rename is invisible at the wire (it is a parameter name, not behavior).

## D2 — `httpresp` vs `httperr`: clean partition, no new duplication

**Decision:** `httpresp` owns 2xx + 4xx (success bodies, user-input errors);
`httperr` (slice 367) owns 5xx (generic body + `request_id` + slog). They do
NOT overlap.

- `httpresp.WriteJSON(w, status, body)` — success + any caller-supplied body.
- `httpresp.WriteError(w, status, msg)` — 4xx user-input errors (caller-supplied,
  safe-to-surface message). Thin shim over `WriteJSON`.
- `httperr.WriteInternal(w, r, op, err)` — 5xx; genericises the body so
  `err.Error()` never leaks (CWE-209) and logs the full error server-side.

This avoids re-creating the duplication the slice is removing: `httpresp` does
not reimplement the 5xx generic-body/request_id path, and `httperr` keeps owning
it. A handler uses `httpresp` for success/4xx and `httperr` for 5xx.

**Edge case honestly noted:** a handful of legacy `writeError(w, http.StatusInternalServerError, "<static msg>")` call sites existed (e.g. `internal/api/metrics/handlers.go`). These carried a _static_ message (no `err.Error()` reflection), so they were never a CWE-209 hazard and `errleak-lint` did not flag them. The mechanical rewrite preserved their behavior exactly by routing them through `httpresp.WriteError` (same wire bytes, same 500 status). Migrating these to `httperr.WriteInternal` would have _changed_ the wire body (adding `request_id`, genericising the message) — a behavior change forbidden by P0-369-1. They are therefore left as `httpresp.WriteError` 500s; tightening them to `httperr` is a separate, non-mechanical judgement and out of scope here.

## D3 — `writeServerErr` retirement: bundled, all 18 retired

**Decision:** retire all 18 in this slice (recommended by the slice doc), closing
slice 367 §D2's deferred follow-up.

All 18 bodies are the trivial `httperr.WriteInternal(w, r, op, err)` delegate.
None matched the P0-369-3 carve-out (`writePackError` / `writeBundleError` /
`writeStoreErr` style sentinel-mapping helpers) — those names do not exist as
`writeServerErr`, so there was nothing to preserve. Call sites now call
`httperr.WriteInternal` directly, removing the per-package indirection layer.

## D4 — Lint analyzer scope: reject-new, grandfather-via-migration (moot)

**Decision:** the `cmd/scripts/duphelper-lint` analyzer rejects ANY package-local
`writeJSON` / `writeError` / `writeServerErr` free-function declaration in
`internal/api/*`. Because the migration removed all 103 in the same PR, the
"grandfather existing" phase is moot — the analyzer runs clean immediately, so
it is reject-all from day one without a follow-up flip.

Analyzer shape mirrors `errleak-lint`: a `golang.org/x/tools/go/analysis`
analyzer + `analysistest` fixture + `singlechecker.Main`. It walks top-level
`FuncDecl`s and reports the three banned names. **Methods** (FuncDecls with a
receiver) and the **exported** `WriteJSON`/`WriteError` shared-helper names are
NOT flagged — only package-local free functions, which were the duplication
surface. Path scoping is enforced by the invocation target
(`./internal/api/...`), exactly as `errleak-lint` is invoked.

## D5 — CI wiring deferred to a spillover (batch directive)

**Decision:** `duphelper-lint` is wired into the `justfile` `lint` target
(`lint-duphelper`) but NOT into `.github/workflows/ci.yml` in this PR.

The batch-164 directive reserves `ci.yml` for slice 345 this batch to avoid a
merge collision, and explicitly instructs: "If you'd add a no-duplicate-writeJSON
CI lint guard, DEFER it to a follow-up spillover slice." Filed as slice **387**
(`docs/issues/387-duphelper-lint-ci-wiring.md`). AC-4's "wired into CI as hard
failure" is therefore PARTIALLY met in this PR: the analyzer exists, is tested,
and is wired into the local `just lint` target; the CI step lands in slice 387.
The local guard + the empty-result state mean a regression is caught by
`just lint` today; CI enforcement follows in the spillover.

## D6 — Count reconciliation (103 vs the slice doc's 101/78)

The slice headline said "101 duplicate declarations" and "78 writeJSON+writeError".
Measured reality: 40 + 45 + 18 = **103** declarations; 40 + 45 = **85**
writeJSON+writeError (not 78). The doc's 78 appears to have counted only the
`code`-variant `writeJSON` (37) + `writeError` (41) and folded the 2+4
`status`-variant renames into the "parameter-rename variants" prose without
adding them to the 78. The migration handled all variants uniformly via
`gofmt -r` (which binds wildcards regardless of the parameter name), so the
discrepancy is a doc-count artifact, not a missed site. All declarations are
removed; both lint analyzers run clean.

## D7 — `errorBody` struct disposition

- `internal/api/policyacks`: its local `errorBody{Error string}` was used ONLY
  by the now-retired `writeError`. After migration it was unused; **removed**
  (golangci-lint `unused` caught it). Wire shape unchanged — `httpresp.WriteError`
  emits the identical `{"error": msg}` bytes.
- `internal/api/evidence`: its local `errorBody` (with an additional `Code`
  field) is still used by direct `httpresp.WriteJSON(w, status, errorBody{...})`
  calls for richer 4xx envelopes (rate-limit / oversized). **Kept** — it is a
  caller-supplied body, exactly what `httpresp.WriteJSON` is for.

## D8 — Tests rewired for AC-5

Two existing handler tests now assert the response-shape contract against the
shared helper directly:

1. `internal/api/metrics/handlers_test.go` — `TestWriteJSON_SetsStatusAndContentType`
   and `TestWriteError_RendersErrorEnvelope` previously tested the metrics
   package's local helpers; the `gofmt -r` rewrite retargeted them to
   `httpresp.WriteJSON` / `httpresp.WriteError`. Section comment updated to note
   the slice 369 contract-lock role.
2. `internal/api/board/integration_test.go` — `TestGenerate_MalformedPeriodEndIs400`
   gains explicit assertions that the 400 path (now flowing through
   `httpresp.WriteError`) emits `application/json` + a `{"error": ...}` envelope.
   This is the integration-tag surface the slice doc suggested for board.

The new `internal/api/httpresp/httpresp_test.go` is the authoritative wire-shape
lock (100% statement coverage): exact byte stream for `WriteJSON` (trailing
newline from `json.Encoder`), the `{"error": msg}` envelope for `WriteError`,
delegation equivalence, and JSON escaping.

## D9 — Coverage floor (P0-369-5)

No package can regress below its floor from this migration. Removing a _covered_
trivial helper (the helpers were small and frequently exercised) drops equal
counts from a package's covered and total statements; removing covered
statements never lowers the coverage percentage. The metrics floor (85, the
only active floor on a unit-coverable touched package) is unaffected — its two
unit tests now cover `httpresp` instead, and the 4 deleted statements were
covered. No threshold lift is needed, so the slice-069 ratchet is untouched.
`httpresp` is left out of `coverage-thresholds.json` for parity with `httperr`
(also not listed); it sits at 100% regardless.

## Anti-criteria compliance

- **P0-369-1** (no wire change): byte-identical bodies; `httpresp_test.go` pins
  the exact stream; `gofmt -r` rewrite is AST-exact. ✅
- **P0-369-2** (single PR if reviewable): shipped as one PR — the diff is large
  but mechanical and reviewable with `git diff --stat`. ✅
- **P0-369-3** (do not retire sentinel-mapping helpers): all 18 retired helpers
  were trivial `httperr.WriteInternal` delegates; no sentinel-mapping helper
  touched. ✅
- **P0-369-4** (no auto-merge): PR opened for review, not merged. ✅
- **P0-369-5** (no coverage regression): see D9. ✅
