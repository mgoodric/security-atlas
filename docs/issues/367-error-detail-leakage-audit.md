# 367 — Error detail leakage audit + cleanup across `internal/api/`

**Cluster:** Infra
**Estimate:** 2d
**Type:** JUDGMENT
**Status:** `merged` (status reconciled 2026-06-03 — backlog drained per \_STATUS.md SoR; loop terminated batch 184)

## Narrative

Slice 327's security audit (`docs/audits/327-security-audit-security-auditor-report.md` finding **M-2**, severity **Medium**) surfaced 36 handler sites across `internal/api/` where `err.Error()` is reflected verbatim into the JSON response body. At `StatusBadRequest`/`StatusConflict` the practice is acceptable (the error is about user input the client sent and can fix). At `StatusInternalServerError` the raw error can leak DB schema details (pgx errors include column/table/constraint names), file paths (filesystem errors), library-internal state, or driver version hints — all of which are CWE-209 reconnaissance enablers.

The most concerning sites surfaced by the audit:

| File                                    | Line | Pattern                                                                     |
| --------------------------------------- | ---- | --------------------------------------------------------------------------- |
| `internal/api/schemaregistry/http.go`   | 56   | `writeJSON(w, http.StatusInternalServerError, ... "list: " + err.Error())`  |
| `internal/api/schemaregistry/http.go`   | 85   | `writeJSON(w, http.StatusInternalServerError, ... "get: " + err.Error())`   |
| `internal/api/vendors/handlers.go`      | 401  | `writeJSON(w, http.StatusInternalServerError, ... op + ": " + err.Error())` |
| `internal/api/artifacts/handlers.go`    | 281  | `writeJSON(w, http.StatusInternalServerError, ... op + ": " + err.Error())` |
| `internal/api/controlstate/handlers.go` | 181  | `writeJSON(w, http.StatusInternalServerError, ... err.Error())`             |

A grep across `internal/api/` for `err.Error()` returns 36 matches; most at 4xx codes are arguably fine, but every 5xx site needs replacement with a generic message + server-side log keyed by request ID.

### What ships

1. **Full inventory.** A `find` across `internal/api/` enumerating every site returning `err.Error()` in a response body, classified by HTTP status code returned. The audit surfaced the 5xx-status sites as the immediate concern; the engineer's call is whether 4xx sites need the same treatment.

2. **Per-handler fix.** For every `5xx + err.Error()` site:

   - Generate a request ID at the chi middleware layer (slice 069's test infrastructure already wires `httplog`; the slog context likely already carries one).
   - Server-side log the full error with the request ID at slog level `Error`.
   - Client-side return a generic message: `{"error": "internal error", "request_id": "..."}` (or whichever shape matches existing convention).

3. **Lint rule.** A custom golangci-lint check (or `analysistest`-style precommit check) that rejects new code calling `writeJSON(... http.StatusInternalServerError, ... err.Error() ...)`. Catches regressions.

4. **Audit log row** for every 5xx (already largely present in some handlers via slice 040 unifiedlog; confirm coverage across the cleaned-up sites).

### JUDGMENT calls

The engineer makes the following design calls and records them in `docs/audit-log/367-...-decisions.md`:

- **Scope.** Tackle 5xx sites only, OR include 4xx sites where the wrapped error is from a deeper layer (pgx, encoding/json, fs)? Recommend 5xx-only for v1 cleanup; 4xx tightening is a follow-on if needed.
- **Generic message wording.** "internal error" vs "server error; see request id <uuid>" vs "an unexpected error occurred". Recommend the request-id-bearing variant so operators can pivot from a user-reported bug to log lookup.
- **Request-ID generation.** Reuse existing chi middleware OR add new? Confirm slice 040 or 069's infrastructure already provides a request ID.
- **Lint enforcement strength.** Hard CI failure OR advisory-only warning? Recommend hard failure on new code; existing-code grandfather is the cleanup PR itself.

### Why this matters

CWE-209 is a recon enabler. An attacker probing the API can map internal schema and library versions from error messages, shortening reconnaissance time. The fix is mechanical but high-volume (36 sites + lint rule). The lint rule is the durable improvement; the cleanup is one-time.

### Why now

M-2 from the slice-327 audit. Slot in a quarterly hardening batch.

**Trigger:** filed 2026-05-28 from slice 327 audit.

## Threat model

STRIDE pass:

- **S (Spoofing):** N/A.
- **T (Tampering):** N/A.
- **R (Repudiation):** _Improved_ — request ID makes audit trail tighter.
- **I (Information disclosure):** Primary fix surface. CWE-209 closure.
- **D (Denial of service):** N/A.
- **E (Elevation of privilege):** N/A.

## Acceptance criteria

- [ ] **AC-1.** A `docs/audit-log/367-...-decisions.md` enumerates every `err.Error()` site in `internal/api/` with a disposition (fix / keep-with-rationale / out-of-scope).
- [ ] **AC-2.** Every 5xx-status site in the disposition table is fixed: server-side log + generic client message with request ID.
- [ ] **AC-3.** A request ID is reliably available in the chi context for every `internal/api/` handler (verify existing infrastructure; add if missing).
- [ ] **AC-4.** A custom lint rule rejects new `writeJSON(... http.StatusInternalServerError, ... err.Error() ...)` patterns in CI.
- [ ] **AC-5.** Integration tests confirm the new response shape (`{"error": "internal error", "request_id": "..."}`) for at least 3 representative endpoints across schemaregistry, vendors, artifacts.
- [ ] **AC-6.** No regression in the existing 4xx error-shape behavior (those continue to reflect specific user-input errors).
- [ ] **AC-7.** `pre-commit run --all-files` passes; CI green.

## Constitutional invariants honored

- **Survive third-party security review (canvas §6).** Closes M-2.
- **Audit log first-class.** Request ID + server-side log + audit row strengthen the audit trail.

## Canvas references

- Audit report `docs/audits/327-security-audit-security-auditor-report.md` finding M-2
- Slice 040 / 069 — request-ID + unified log infrastructure

## Dependencies

- **#040** (program dashboard view) — `merged`. Slog logging conventions established.
- **#069** (testing discipline) — `merged`. Lint surfaces in CI.

## Anti-criteria (P0 — block merge)

- **P0-367-1.** Does NOT remove specific `ErrNotFound` / `ErrUnauthorized` / `ErrForbidden` translations — those are well-known sentinels mapped to specific status codes (404/401/403) and carry no internal-leak risk. Only the catch-all `5xx + err.Error()` patterns are in scope.
- **P0-367-2.** Does NOT remove or weaken server-side logging — every error continues to be logged with the same or greater detail. Only client-facing surface is genericized.
- **P0-367-3.** Does NOT bypass the lint rule via inline comments to keep old code "as is" — the cleanup is the deliverable.
- **P0-367-4.** Does NOT auto-merge.

## Skill mix

- `tdd` — RED-first tests for new response shape
- `simplify` — pre-PR quality pass

## Notes for the implementing agent

The cleanup is mechanical but high-volume. Suggested phased approach:

1. **Phase 1 (1d):** inventory + decisions log + 5xx fixes in 5 highest-traffic handlers (schemaregistry, vendors, artifacts, controlstate, controls/history_export).
2. **Phase 2 (1d):** remaining 5xx fixes + lint rule + CI wiring.

The lint rule shape: a small Go vet-style analyzer that walks `writeJSON` call sites and rejects when the response-body argument contains a call to `(error).Error()` AND the status-code argument is in the 5xx range. The `golang.org/x/tools/go/analysis` framework is the standard surface.

If a request-ID middleware doesn't exist yet, add one in a small companion PR before tackling the handlers; do not bundle middleware addition with the cleanup.
