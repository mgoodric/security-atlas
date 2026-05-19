# 166 — Decisions log: settings credentials table null-deref fix

**Slice:** 166 — Settings credentials table null-deref on empty `allowed_kinds`
**Cluster:** Quality
**Type:** SPILLOVER (JUDGMENT — D1 selection)
**Branch:** `quality/166-allowed-kinds-null-safe-deref`
**Status at decision time:** `in-progress`

## D1 — Frontend null-safe deref vs backend non-nil marshal

**Choice: Option A — frontend null-safe deref via a pure helper module.**

**Reasoning (in order of weight).**

1. **Closest to the crash site.** The slice doc explicitly recommended Option A as "preferred — closest to the crash site." The bug surfaces in a React render path; the fix lives in the same file, the same render path, the same test surface. No cross-language indirection, no need to coordinate Go-side marshalling with TS-side decoding, no wire-format conversation.

2. **Smallest blast radius.** A backend change at `internal/api/admincreds/http.go:160` affects every consumer of `GET /v1/admin/credentials` — the settings page, the admin/api-keys page, the CLI (`security-atlas admin credentials list`), future SDKs, audit-log enrichment. A null→`[]` coercion is semantically equivalent, but the change touches a wire-shape boundary; that's a different review surface and different rollback story. The frontend fix is purely visual coercion at one render site (with a helper that the admin/api-keys page can adopt later via slice 169).

3. **Lower test-harness drag.** The frontend has a fast vitest suite (503 tests in <1s) and a co-located test pattern (`token-state.ts` + `token-state.test.ts`, `session-line.ts` + `session-line.test.ts`, etc.). A 35-line pure helper drops cleanly into that pattern. Adding the equivalent backend coverage would need either a Go unit test on the marshalling path (~30 lines of fixtures + golden JSON) or an integration test against the real handler with a seeded empty-kinds row.

4. **The TypeScript type lie is contained.** Per P0-A4, the wire shape `allowed_kinds: string[]` is the correct long-term contract — null is a bug-shaped artifact of the encoder chain. Fixing it in the renderer (coerce on read) honors the type at the seam without propagating the null into the type system. Option B would have been a corner-case "also fix the type to string[] | null" temptation, which would have failed P0-A4. By choosing the frontend coercion, the type stays honest.

**Diff size.** Option A landed as:

- `web/app/(authed)/settings/allowed-kinds-display.ts` — new pure helper module (46 lines, 35 substantive).
- `web/app/(authed)/settings/allowed-kinds-display.test.ts` — inline regression (88 lines, 11 tests).
- `web/app/(authed)/settings/page.tsx` — 1 new import line + 2 substantive line changes at the render site (3 total). Within the AC-2 ≤5 ceiling.

**Confidence.** High (0.92). The fix is mechanically simple, fully covered by the new vitest regression, and the existing 7 currently-green `settings.spec.ts` ACs continue to pass (no behavior change for non-empty arrays; identical "any" rendering for empty/null arrays). The risk surface is bounded to the one render site; if Option A is ever reverted (e.g., as part of a backend-side cleanup), the slice 165 fixture workaround acts as a belt-and-suspenders safety net.

## D2 — Alternative ruled out: Backend non-nil marshal (Option B)

**Why ruled out for THIS slice.**

The slice doc explicitly framed Option B as "slightly more code but fixes other consumers (CLI, tests, future SDKs)." All three weight-points above favor Option A for the production DoS fix specifically. **Option B is still valuable as a follow-up** — making `allowed_kinds` consistently `[]` on the wire is the long-term correct shape, eliminates the nil-slice→null-marshal gotcha for every future consumer, and reduces the cognitive overhead of "is this field maybe null?" for downstream code.

Concrete way to land Option B later (as a future slice — NOT in this PR):

- Add a `MarshalJSON` method on `internal/api/admincreds/http.go:ListItem` that emits `[]` for nil `Kinds`. OR initialize `c.Kinds = []string{}` at the conversion site if more readable.
- Add a Go unit test asserting `json.Marshal(ListItem{Kinds: nil})` produces `"allowed_kinds":[]`.
- Apply the same pattern to any other backend struct that holds a `[]string` field marshalled to a frontend-typed `string[]`.
- Once both ends coerce, slice 165's fixture workaround (`fixtures/e2e/settings.sql` + `web/e2e/seed.ts` non-empty allowed_kinds) becomes purely a clarity-of-intent seed, not a crash-prevention workaround.

This is the long-tail cleanup; the slice 166 production fix unblocks the crash regardless of whether or when Option B lands. Filed as future spillover scope when the maintainer chooses to invest the cycle.

## D3 — In-scope sibling crash site: admin/api-keys page

**Found during the slice 166 P0-A1 audit.**

`grep -rn "allowed_kinds" web/app/ web/lib/ web/components/` surfaced an identical crash pattern at `web/app/admin/api-keys/page.tsx:200`:

```tsx
{
  c.allowed_kinds.length === 0 ? (
    <span className="text-muted-foreground">any</span>
  ) : (
    c.allowed_kinds.join(", ")
  );
}
```

This is the SAME exact bug — same data source (the `AdminCredential` list response), same null-deref pattern, same React-unmount blast radius for the admin/api-keys page. An admin user with an empty-kinds credential cannot view that page either.

**Why NOT fix it in this PR.** The slice doc scopes the fix to the `/settings` credentials table render path:

> "Does NOT widen the fix beyond the `/settings` credentials table render path. The pattern `someField.length` against backend slices may surface elsewhere..."

And the orchestrator's P0-A1 sets the threshold at "≥ 3 other render paths" before triggering a project-wide sweep. We found exactly 1 other render path, so we do NOT trigger the sweep — but per slice spillover policy (Amendment 2), we DO file the follow-on slice.

**Action taken.** Filed `docs/issues/169-admin-api-keys-allowed-kinds-null-safe-deref.md` as a Quality/0.1d follow-on. That slice will simply import the same helper from `web/app/(authed)/settings/allowed-kinds-display.ts` into `web/app/admin/api-keys/page.tsx` and apply the same 2-line render-site change. The helper module is intentionally generic — not coupled to the `/settings` page beyond being placed in that directory tree — so adopting it from the admin page is a 4-line PR (1 import + 3 render-site lines).

## D4 — Whether to remove the slice 165 fixture workaround (AC-5d)

**Choice: KEEP the slice 165 fixture workaround. Do NOT remove it in this PR.**

**Reasoning.**

1. **Belt-and-suspenders defense.** The slice 165 iter 2 commit (`fe2e33d` → reconcile cherry-pick) added two harness mitigations:

   - `fixtures/e2e/settings.sql`: seeds the test row with `ARRAY['evidence.kind.v1']::TEXT[]` instead of the default empty array.
   - `web/e2e/seed.ts` (settings branch): the same non-empty seed for the bootstrap-then-test lane.

   These are cheap (two-line seed changes) and they make the test data more like real-world admin credentials (which usually have some kind restriction). The harness expense is minimal; the regression-risk reduction is real — if the slice 166 production fix is ever reverted (e.g., a future refactor accidentally removes the helper import), the fixture workaround still keeps the Playwright lane green and surfaces the regression in production code review, not in a CI-red panic.

2. **P0-A6 explicit.** The orchestrator's P0-A6 said: "Do NOT bundle the slice 165 fixture workaround removal in this PR. That's a separate concern; file as spillover if you want to remove it." This is the unambiguous instruction.

3. **The slice 165 doc itself says "can coexist."** From the slice 166 doc: "Both can coexist; the fixture remains a reasonable seed even after the production fix." The fixture is not technical debt — it's reasonable test data.

4. **Independent reverts.** Decoupling the production fix (this PR) from the fixture rollback (a hypothetical future PR) makes each diff narrow and each revert independent. If we ever do remove the fixture, that PR is a 4-line diff that's trivially reviewable in isolation. If we'd bundled it here, the slice 166 PR would have grown by 50+ lines of seed/fixture changes and the revert story would entangle the production fix with the test seed.

**Future option.** A maintainer-only spillover (file as slice 170+ if/when desired) can revert the slice 165 fixture workaround to "default-empty allowed_kinds" once we have additional confidence that the slice 166 helper holds in production. That revert would also serve as a defensive regression test: if the helper were ever broken, the Playwright lane would fail loudly with the original `TypeError`. But that's a defense-in-depth choice for later, not part of this slice.

## Capability invocation audit

The orchestrator did not pre-list capability selections (subagent role, not Algorithm-mode primary). The slice was solved with:

- Read + Edit + Write tools for code changes.
- Bash for build/test verification.
- Grep for P0-A1 audit.

No skills were invoked (the slice did not need /simplify or /diagnose — the bug was pre-diagnosed by slice 165 iter 2, and the fix was mechanically simple).

## AC self-check

| AC   | Status   | Evidence                                                                                                                                                 |
| ---- | -------- | -------------------------------------------------------------------------------------------------------------------------------------------------------- |
| AC-1 | PASS     | D1 above selects Option A (frontend null-safe deref via helper module); D2 documents the alternative ruled out                                           |
| AC-2 | PASS     | `web/app/(authed)/settings/page.tsx` diff = 3 substantive lines (1 import + 2 render-site swaps); under the ≤5 ceiling                                   |
| AC-3 | PASS     | `web/app/(authed)/settings/allowed-kinds-display.test.ts` covers null + undefined + [] + single-kind + multi-kind + order-preservation (11 tests, green) |
| AC-4 | PRESERVE | No behavior change for non-empty arrays; identical "any" rendering for empty/null arrays; settings.spec.ts ACs unchanged (slice 168's job to flip the 4) |
| AC-5 | PASS     | D1 (a, b, c boundary winner + alternative + confidence) + D4 (d fixture workaround decision) above                                                       |

| Anti-criterion | Audit                                                                                                                                          |
| -------------- | ---------------------------------------------------------------------------------------------------------------------------------------------- |
| P0-A1          | RESPECTED: identical pattern found in admin/api-keys/page.tsx but filed as slice 169 follow-on; did NOT widen this PR to a sweep               |
| P0-A2          | RESPECTED: wire shape semantics unchanged (null and [] still both mean "any-kind"); fix is purely defensive read coercion                      |
| P0-A3          | RESPECTED: slice 165 fixture workaround NOT removed in this PR (see D4 above)                                                                  |
| P0-A4          | RESPECTED: TypeScript type at `web/lib/api.ts:596` remains `allowed_kinds: string[]`; null is coerced at the render boundary, not in the type  |
| P0-A5          | RESPECTED: inline vitest regression at allowed-kinds-display.test.ts locks the null branch (11 tests, all green)                               |
| P0-A6          | RESPECTED: slice 165 fixture workaround removal NOT bundled here; if ever desired, file as separate spillover (see D4 future-option paragraph) |
