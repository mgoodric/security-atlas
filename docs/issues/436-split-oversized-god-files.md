# 436 — Split the three oversized hand-written god-files

**Cluster:** Quality
**Estimate:** M
**Type:** JUDGMENT
**Status:** `ready` (no dependency — the split convention is already set)

## Narrative

Three hand-written files have grown past the point where they are pleasant to
edit or safe to touch in parallel:

| File                                      | LOC  |
| ----------------------------------------- | ---- |
| `web/app/(authed)/settings/page.tsx`      | 1749 |
| `web/app/(authed)/controls/[id]/page.tsx` | 1426 |
| `internal/api/httpserver.go`              | 1413 |

These are the three largest hand-written files in the tree (excluding the
sqlc-generated `internal/db/dbx/*` DO-NOT-EDIT files, which are machine output
and out of scope). Each is a high merge-collision surface: two concurrent
slices that both touch the settings page, or both register a route in
`httpserver.go`, will conflict, and the file's size makes the conflict
expensive to resolve. The per-domain split convention is already established —
slices 370/396 split `web/lib/api.ts` into `web/lib/api/*.ts` per domain — so
this slice applies the same move to the three god-files. The split is
**behavior-preserving**: same rendered UI, same routes, same handlers — just
reorganized into per-tab / per-section / per-domain units.

The three target shapes:

- **`settings/page.tsx`** → extract each settings tab into a `_tabs/`
  component (e.g. `_tabs/profile.tsx`, `_tabs/api-keys.tsx`, …); the page file
  becomes a thin tab-shell that composes them.
- **`controls/[id]/page.tsx`** → extract each detail section into its own
  component (coverage, evidence, policies, risks, history, attestations …);
  the page file composes the sections.
- **`internal/api/httpserver.go`** → split route registration into per-domain
  `register_*.go` files **in the same package** (e.g. `register_controls.go`,
  `register_audit.go`, `register_board.go`), mirroring how 370/396 split the
  client. No package move — same `package api`, same exported symbols.

**Scope discipline.** This is one slice with three behavior-preserving
deliverables. If, on contact, any one split balloons past a clean bounded
change (e.g. the control-detail split forces a state-management refactor), the
implementing agent SHOULD split THAT deliverable into its own follow-on slice
rather than scope-creep this one — note the spillover in the decisions log and
ship the two that stayed bounded. The slice does NOT add features, change
behavior, rename routes, or alter any API contract. It does NOT touch the
sqlc-generated `dbx/*` files.

## Threat model

STRIDE pass for a behavior-preserving structural refactor. The dominant risk
is regression-via-omission (a route or a test hook silently dropped during the
move), which maps to Tampering and — for `httpserver.go`, which wires the auth
middleware — Elevation of privilege.

**S — Spoofing.** N/A — no new endpoints; the same routes register, just from
split files. Mitigation against accidental change: the route table before and
after the split is identical (AC-5).

**T — Tampering (primary).** A behavior-preserving split that silently drops a
route registration, a middleware wrap, or a Playwright `data-testid` is the
core risk — the change "looks done" but a flow is broken. Mitigation: the e2e
suite is the behavior-preservation oracle (AC-4); every `data-testid` and ARIA
hook in the three files is preserved verbatim (AC-3, P0); the registered-route
set is asserted unchanged (AC-5).

**R — Repudiation.** N/A — no audit-logged operation changes.

**I — Information disclosure.** N/A in net effect — no read path's tenant
scoping changes; the split moves code, it does not alter what data a handler
returns. (A handler's RLS context is unchanged because the handler body is
unchanged.)

**D — Denial of service.** N/A — no new input surface; same handlers, same
caps.

**E — Elevation of privilege (`httpserver.go`-specific).** `httpserver.go`
wires the auth middleware onto route groups. Splitting route registration
risks moving a route OUT from under its auth/role middleware (e.g. registering
an admin route on the unauthenticated mux). Mitigation: each `register_*.go`
receives the same middleware-wrapped router/group it had inline; AC-6 asserts
every route that was authenticated/admin-gated before is still gated after —
verified by the auth-path e2e specs and a route-middleware audit. This is the
load-bearing P0 guard for the backend deliverable.

**Verdict:** has-mitigations — safe as a pure move provided (1) every testid is
preserved (AC-3), (2) the route+middleware table is proven unchanged (AC-5,
AC-6), and (3) the e2e suite is green (AC-4). The auth-middleware preservation
on the `httpserver.go` split is the one place a structural move can become a
real privilege regression; AC-6 exists specifically to catch it.

## Acceptance criteria

- [ ] **AC-1.** `web/app/(authed)/settings/page.tsx` is split — each settings
      tab lives in a `_tabs/` component; the page file is a thin composing
      shell and is materially smaller (target: well under the original 1749
      LOC, no single extracted file approaching it).
- [ ] **AC-2.** `web/app/(authed)/controls/[id]/page.tsx` is split into
      per-section components; the page file composes them and is materially
      smaller than the original 1426 LOC.
- [ ] **AC-3.** Every `data-testid`, ARIA attribute, and id referenced by the
      Playwright e2e suite is preserved verbatim across both frontend splits —
      no testid renamed, dropped, or relocated out of its rendered position.
- [ ] **AC-4.** `npm run test:e2e` (Playwright) passes — the behavior-
      preservation oracle; and `npm run test` (vitest) passes for any touched
      module-logic.
- [ ] **AC-5.** `internal/api/httpserver.go` route registration is split into
      per-domain `register_*.go` files in the same `package api`; the complete
      set of registered routes (method + path) is identical before and after,
      asserted by a route-table comparison or an existing routes test.
- [ ] **AC-6.** Every route that was authenticated / admin-gated before the
      split is still wrapped by the same auth/role middleware after — proven by
      the auth-path integration/e2e specs passing and a route-middleware audit
      showing no route moved off its gate (the EoP guard).
- [ ] **AC-7.** No API contract changes: same request/response shapes, same
      status codes, same paths. (No OpenAPI drift; the contract-tier goldens
      where they exist still pass.)
- [ ] **AC-8.** No sqlc-generated `internal/db/dbx/*` file is touched.
- [ ] **AC-9.** The Go unit tier (`go test ./...`), the integration tier
      (`go test -tags=integration -p 1 ./internal/...`), and `golangci-lint`
      all pass for the backend split.
- [ ] **AC-10.** If any one of the three splits is deferred as too large, the
      decisions log records WHY and a follow-on slice is filed for it; the
      other two still land complete.

## Constitutional invariants honored

- **Invariant #6 (tenant isolation via RLS).** Handler bodies are unchanged,
  so RLS context plumbing is untouched; AC-6 additionally proves no route
  escapes its auth/role gate.
- Honors the slice-370/396 per-domain split convention (the precedent this
  slice extends from the API client to the page/server god-files).
- Style: no emojis; Go house style for the backend split; the frontend split
  follows the existing `_tabs/` / per-section component convention in `web/`.

## Canvas references

- None directly — this is a structural/maintainability refactor below the
  canvas design layer. The nearest governance is CLAUDE.md "Testing
  discipline" (Playwright as the de-facto component-test tier that proves the
  frontend split is behavior-preserving) and the slice-370/396 split precedent.

## Dependencies

- **#370** / **#396** — `merged`. The per-domain split convention this slice
  mirrors (`web/lib/api.ts` → `web/lib/api/*`).
- No technical blocking dependency — the three files exist on `main` today.

## Anti-criteria (P0 — block merge)

- Does NOT rename, drop, or relocate any Playwright `data-testid` / ARIA hook
  (P0 — the frontend behavior-preservation guard).
- Does NOT move any route off its auth/role middleware in the `httpserver.go`
  split (P0 — the EoP guard; AC-6 proves it).
- Does NOT change any API contract — same paths, shapes, status codes; no
  OpenAPI drift.
- Does NOT add features or change rendered behavior — pure structural move.
- Does NOT touch sqlc-generated `internal/db/dbx/*` files.
- Does NOT scope-creep a ballooning split into this slice — defer it to a
  follow-on and ship the bounded two (AC-10).

## Skill mix (3-5)

- `monorepo-navigator` — map the three files' internal seams before cutting.
- `simplify` — the split IS a simplify pass; apply it deliberately.
- `playwright-pro` / `verify` — run the e2e suite as the behavior oracle.
- `security-review` — mandatory on the `httpserver.go` split (route +
  middleware wiring).
- `ship-gate` — confirm route table + contract goldens unchanged.

## Notes for the implementing agent

Sequence the work so each deliverable is independently green before starting
the next — they share no code, so a clean per-file split avoids a tangled
three-way diff. Start with `httpserver.go` (the route-table assertion gives the
crispest behavior-preservation proof), then the two pages (Playwright is the
oracle). For the frontend splits, the `_tabs/` convention for settings and a
per-section folder for control-detail are the natural shapes; keep state that
several sections share lifted in the page shell and pass it down, rather than
duplicating fetches — but if control-detail's shared state forces a
non-trivial refactor, that is your signal to defer it to a follow-on (AC-10)
rather than expand this slice. The `httpserver.go` split MUST keep the same
`package api` and the same exported registration entrypoint — callers
(`cmd/atlas`) should not need to change. Record in the decisions log: the
per-section boundary choices (subjective) and any deferral.
