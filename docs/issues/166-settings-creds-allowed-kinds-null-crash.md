# 166 — Production bug: /settings crashes when an api_keys row has empty allowed_kinds

**Cluster:** Quality
**Estimate:** 0.25d
**Type:** SPILLOVER
**Status:** `merged` (status reconciled 2026-06-03 — backlog drained per \_STATUS.md SoR; loop terminated batch 184)

## Narrative

**WHY.** Surfaced during slice 165 (diagnose + fix 11 settings.spec.ts AC failures from slice 164 UNSTABLE merge), captured as follow-up per continuous-batch policy.

The `/settings` page renders the Personal API Tokens table by iterating `list.data` (from `GET /api/admin/credentials`) and dereferencing `c.allowed_kinds.length` at `web/app/(authed)/settings/page.tsx:~883`:

```tsx
<TableCell className="text-xs">
  {c.allowed_kinds.length === 0 ? (
    <span className="text-muted-foreground">any</span>
  ) : (
    c.allowed_kinds.join(", ")
  )}
</TableCell>
```

The TypeScript type at `web/lib/api.ts:596` declares `allowed_kinds: string[]`, but the Go backend returns `"allowed_kinds": null` for every row whose stored array is empty or NULL. The two paths that produce a null are:

1. **Database NULL.** The `api_keys.allowed_kinds` column is `TEXT[] NOT NULL DEFAULT '{}'::text[]` (`migrations/sql/20260511000012_users_sessions_api_keys.sql`), so a literal NULL is impossible at the schema level — but pgx-go decodes an _empty_ `'{}'::text[]` value to a Go nil slice (standard pgx behavior).
2. **Go encoding.** `internal/api/admincreds/http.go:160` assigns `AllowedKinds: c.Kinds` and Go's `encoding/json` marshals a nil `[]string` to `null` (not `[]`).

When the page renders a credentials list that contains at least one row with an empty array (which is the default state of any newly-issued admin credential), `c.allowed_kinds.length` throws `TypeError: Cannot read properties of null (reading 'length')`, React unmounts the `<div data-testid="settings-page">` subtree, and Next.js shows its default error boundary "This page couldn't load." End users with a fresh admin token cannot view the settings page.

**Reproduction (slice 165 evidence):** Playwright run 26100783991 against PR #358. All 10/11 settings.spec.ts ACs that touch sections below the `<header>` fail with `element(s) not found`; the lone passing AC (AC-6 / admin cross-link) lives inside the `<header>` before the first error-triggering render. Trace file's `pageError` payload (verbatim):

```
TypeError: Cannot read properties of null (reading 'length')
    at /_next/static/chunks/04l6-mvui~hb7.js:1:33965
    at Array.map (<anonymous>)
    at D (/_next/static/chunks/04l6-mvui~hb7.js:1:33355)
```

**WHAT.** Pick the cheapest defensive fix at the boundary and ship it. Two equally-narrow options:

- **Frontend (preferred — closest to the crash site).** Make `page.tsx:~883` null-safe:

  ```tsx
  {(c.allowed_kinds?.length ?? 0) === 0 ? (
  ```

  1-line change. Also audit the other `c.allowed_kinds` reference at `page.tsx:~886` (`.join(", ")`) — `null?.join` would also throw, so wrap that branch behind the same null check or default to `[]` at parse time.

- **Backend.** Either initialize the slice before marshalling (`if c.Kinds == nil { c.Kinds = []string{} }` inside the `ListItem` constructor), or define a `MarshalJSON` on `ListItem` that emits `[]` for nil. Slightly more code but fixes other consumers (CLI, tests, future SDKs).

D1 in this slice's decisions log picks ONE of the two. Both are correct.

**SCOPE DISCIPLINE — what's deliberately out:**

- Does NOT widen the fix beyond the `/settings` credentials table render path. The pattern `someField.length` against backend slices may surface elsewhere (audit-log, evidence list, controls list); separate audit slices can sweep for it.
- Does NOT change the wire shape. `allowed_kinds: null` and `allowed_kinds: []` carry the same semantic (any-kind), so a client expecting one tolerates the other; we just defensively coerce on read.
- Does NOT replace the slice 165 fixture workaround. Slice 165 left a fixture-only mitigation (settings.sql + seed.ts populate `allowed_kinds` non-empty for the harness row) so the Playwright lane unblocks before this slice ships. Both can coexist; the fixture remains a reasonable seed even after the production fix.

## Threat model

**Spoofing.** N/A.

**Tampering.** N/A — defensive read coercion, no semantic change to the field.

**Repudiation.** N/A — no audit-log surface.

**Information disclosure.** N/A — the field is non-sensitive metadata.

**Denial of service.** This slice FIXES a real DoS surface — any admin user with a freshly-issued credential cannot view their settings page. Closing the slice closes the DoS.

**Elevation of privilege.** N/A.

## Acceptance criteria

- [ ] AC-1: Pick the boundary (frontend null-safe deref OR backend non-nil marshal) in the decisions log.
- [ ] AC-2: Apply the chosen fix; diff is ≤ 5 substantive lines.
- [ ] AC-3: Settings page renders for an admin with at least one api_keys row whose `allowed_kinds` is empty (regression: file an inline vitest that asserts the section renders when the BFF returns `allowed_kinds: null`).
- [ ] AC-4: All 11 settings.spec.ts ACs continue to pass in CI (this is the slice 165 contract; the fixture workaround can remain in place or be removed once this fix lands).
- [ ] AC-5: Decisions log at `docs/audit-log/166-settings-creds-allowed-kinds-null-crash-decisions.md` records (a) which boundary won + reasoning; (b) the alternative ruled out; (c) confidence; (d) whether to remove the slice 165 fixture workaround now that the production fix is in place.

## Anti-criteria (P0 — block merge)

- **P0-A1**: Does NOT change the wire shape of `GET /v1/admin/credentials` beyond the nil-vs-empty coercion. The TypeScript type stays `allowed_kinds: string[]`.
- **P0-A2**: Does NOT remove the slice 165 fixture workaround in the SAME PR. Decoupling the production fix from the fixture rollback keeps each PR's diff narrow and reverts independent.

## Canvas references

- `web/app/(authed)/settings/page.tsx` — crash site.
- `web/lib/api.ts:592-604` — `AdminCredential` TypeScript type (declares `string[]`, gets `null`).
- `internal/api/admincreds/http.go:114-176` — Go ListItem + List handler.
- `internal/auth/apikeystore/apikeystore.go:283-286` — the INSERT path normalizes nil to empty slice; the READ path doesn't.
- `migrations/sql/20260511000012_users_sessions_api_keys.sql` — schema (`NOT NULL DEFAULT '{}'`).
- Slice 165 (`165-settings-spec-ac-diagnosis-fix.md`) — parent slice; surfaced this bug during diagnosis.

## Dependencies

- #165 — in-review (fixture workaround that unblocks Playwright). This slice can land in parallel or after.

## Notes for the implementing agent

- The Playwright trace from PR #358 (run 26100783991) is the canonical reproduction. The settings.spec.ts AC bodies all reach the un-commented form in slice 164, so once this bug is fixed, slice 165's fixture workaround becomes redundant (file a follow-up slice or include the workaround revert here — see AC-5 / P0-A2).
- Defensive null-safety pass should also cover the `c.allowed_kinds.join(", ")` branch at the same render site. A `string[] | null` decoded to `[]` at parse time would handle both deref sites in one place.
- Schema invariant: `allowed_kinds TEXT[] NOT NULL DEFAULT '{}'::text[]`. The bug is purely in the Go → JSON marshalling step (Go's nil-slice → JSON null). Frontend coercion is the cheapest fix; backend coercion is the most thorough.
