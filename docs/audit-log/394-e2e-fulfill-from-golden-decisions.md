# Slice 394 — teach the `/e2e/` `route.fulfill` mocks to load from contract goldens — decisions log

JUDGMENT slice. The build-time subjective calls (helper shape, which
specs/routes migrate, the escape-hatch design) are recorded here per the
continuous-batch JUDGMENT convention; the maintainer iterates
post-deployment. This does NOT touch the product-runtime AI-assist
boundary (separate, constitutional).

Cross-references: ADR-0007 (`docs/adr/0007-contract-test-tier.md`), slice
392 (`docs/audit-log/392-contract-test-tier-rollout-decisions.md` D5 — the
deferral this slice resolves), slice 409
(`docs/audit-log/409-contract-tier-rollout-dashboard-decisions.md` D5 —
the unblock), slice 334 P-1 (`docs/audits/334-test-framework-review.md` —
the mock-density finding), slice 394 spec
(`docs/issues/394-e2e-mocks-load-from-contract-goldens.md`).

---

## D1 — The `fulfillFromGolden` helper shape (AC-1)

**Location:** `web/e2e/test-utils/fulfill-from-golden.ts`.

Rationale for the path: the vitest consumer half already reads the goldens
from `web/lib/test-utils/`-adjacent code; but the Playwright e2e helper is
a _different_ runtime (it serves bodies via `route.fulfill`, it does not
assert the BFF). Keeping it under `web/e2e/test-utils/` (a new sibling of
`web/e2e/fixtures.ts` / `seed.ts` / `global-setup.ts`) matches the e2e
harness's own convention — every e2e support module lives under `e2e/`,
and `eslint.config.mjs` globally ignores `e2e/**`, so a helper there shares
the specs' lint exemption (the specs are intentionally outside the
`lib/api/**` max-lines / typed-lint surface). It is imported by relative
path from the specs exactly as `./fixtures` and `./seed` already are.

**Signature:**

```ts
async function fulfillFromGolden(
  route: Route,
  endpoint: GoldenEndpoint, // a string-literal union of the 9 covered endpoints
  variant: string, // the golden variant key ("populated" | "empty" | …)
  options?: { status?: number; override?: Record<string, unknown> },
): Promise<void>;
```

- **`route`** — the Playwright `Route` handed to a `page.route(pattern, …)`
  callback. The helper owns the `route.fulfill(...)` call.
- **`endpoint`** — a typed union (`GoldenEndpoint`) of the nine covered
  endpoints, NOT a free string. This is the load-bearing safety: a typo
  cannot silently fall through to a non-golden path; `tsc` rejects an
  unknown endpoint. The union maps each endpoint to its golden filename
  via a const record (`me` → `me.golden.json`, etc.).
- **`variant`** — the variant key inside the golden (`populated`, `empty`,
  `release`, `synthetic_admin`, …). The helper throws a descriptive error
  (listing the available variants) if the key is absent — a missing
  variant is a test-author bug, surfaced loudly at run time rather than
  serving an empty body.
- **`options.status`** — defaults to `200`. Lets a spec serve a golden
  body under a non-200 status when that is the contract under test (rare;
  the goldens are happy-path bodies).
- **`options.override`** — the escape hatch (see D3). A shallow-by-key
  deep merge applied over the golden body before serialization, so a spec
  can pin a field the golden does not carry the exact value for (e.g. the
  credential-bearer `display_name`) without abandoning the golden as the
  base.

**Why `route` is a parameter, not a pattern.** The helper does NOT call
`page.route` itself — the spec still owns the URL-glob registration
(`page.route("**/api/install-state", …)`) because the glob, the
method-guard (`route.request().method() !== "GET"` → `route.fallback()`),
and the per-test ordering are all spec-local concerns. The helper owns
exactly one thing: turning `(endpoint, variant, override)` into a
`route.fulfill({ status, contentType, body })`. This keeps it a leaf
utility, composes with the existing method-guard idiom, and does not
re-implement Playwright's routing.

**Endpoint → golden map (D1 inventory).** Nine endpoints, all under
`web/lib/contracts/`:

| `GoldenEndpoint`    | Golden file                     | From slice |
| ------------------- | ------------------------------- | ---------- |
| `me`                | `me.golden.json`                | 392        |
| `version`           | `version.golden.json`           | 392        |
| `install-state`     | `install-state.golden.json`     | 349/392    |
| `demo-status`       | `demo-status.golden.json`       | 392        |
| `framework-posture` | `framework-posture.golden.json` | 409        |
| `activity`          | `activity.golden.json`          | 409        |
| `upcoming`          | `upcoming.golden.json`          | 409        |
| `freshness`         | `freshness.golden.json`         | 409        |
| `drift`             | `drift.golden.json`             | 409        |

The helper reads the golden with `readFileSync` + `JSON.parse` at call
time (the goldens are a few hundred bytes; no caching complexity is
warranted, and reading per-call keeps the helper stateless and
`fullyParallel`-safe — no shared mutable module state across workers).

## D2 — Which specs/routes migrate (AC-2)

I inventoried every `page.route(...)` / `context.route(...)` in
`web/e2e/*.spec.ts` whose target URL maps to one of the nine
golden-covered endpoints AND hand-writes a response **body** (a
`route.abort()` / `route.continue()` carries no body, so there is nothing
to drift; those are left untouched — there is no golden to load).

The dashboard happy-path specs (`dashboard.spec.ts` AC-2/AC-4/AC-5/AC-6,
`evidence freshness panel binds …`) do NOT hand-write bodies — they assert
against **real seeded data** via `seedFromFixture("dashboard")`. They have
no `route.fulfill` body to migrate; they are correctly out of scope.

The genuine hand-written-body sites for golden-covered routes:

| Spec                                               | Route                                                                               | Mapped golden:variant                                        | Migrated?                                                                                                                                                       |
| -------------------------------------------------- | ----------------------------------------------------------------------------------- | ------------------------------------------------------------ | --------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `first-time-login.spec.ts`                         | `**/api/install-state` `{first_install:true}`                                       | `install-state:fresh_install_without_tenant`                 | YES                                                                                                                                                             |
| `first-time-login.spec.ts`                         | `**/api/install-state` `{first_install:false}`                                      | `install-state:post_first_install`                           | YES                                                                                                                                                             |
| `first-time-login.spec.ts`                         | `**/api/install-state` `503` (no body)                                              | — (error escape hatch)                                       | YES — via `options.status` + the documented error path; no golden body, so it stays a hand-written `status:503` fulfill, now annotated as the AC-3 escape hatch |
| `dashboard.spec.ts` (slice-229 AC-2)               | `**/api/dashboard/freshness` 87%-populated                                          | `freshness:populated` + `override`                           | YES — golden base + override (the deterministic 87% the assertion needs)                                                                                        |
| `dashboard.spec.ts` (slice-229 AC-5 + P0-229-2)    | `**/api/dashboard/freshness` empty                                                  | `freshness:empty`                                            | YES                                                                                                                                                             |
| `dashboard.spec.ts` (slice-229 AC-4)               | `**/api/dashboard/freshness` `abort()`                                              | — (error path, no body)                                      | left as `abort()` — no golden body to load                                                                                                                      |
| `dashboard.spec.ts` (AC-7)                         | `**/api/dashboard/drift` `abort()` + `**/api/dashboard/freshness` slow `continue()` | — (no body)                                                  | left untouched — `abort`/`continue` carry no body                                                                                                               |
| `settings-profile-credential-bearer.spec.ts`       | `**/api/me` synthetic-credential profile                                            | `me:synthetic_admin` + `override{display_name}`              | YES — golden base + override                                                                                                                                    |
| `settings-notifications-credential-bearer.spec.ts` | `**/api/me` synthetic-credential profile                                            | `me:synthetic_admin` + `override{display_name, owner_roles}` | YES — golden base + override                                                                                                                                    |

**Left hand-mocked, by design (NOT a gap — exactly 394's AC-3 anticipated
set + 409 D6's deferrals):**

- `/api/me/tenants`, `/api/me/preferences`, `/api/me/sessions`
  (`tenant-switch.spec.ts`, `settings-*-credential-bearer.spec.ts`) —
  these are **distinct endpoints** from `/v1/me`; they have NO golden
  (their goldens are not in the slice-392/409 set). Hand-written stays.
- `/api/dashboard/risks` (top-risks panel), `/api/controls/*`,
  `/api/board/*`, `/api/policies/*` (`controls-*`, `risks-*`,
  `board-pack-*`, `policies-list`, `control-detail-*`, `questionnaires`,
  `admin-*` specs) — their goldens are tracked as #410 (risks) and #411
  (controls/audit) per slice 409 D6. No golden ⇒ no migration. These are
  the per-test-variation escape-hatch case AC-3 names.

## D3 — The escape hatch (AC-3): error / pagination / empty-set / field-override

The happy-path goldens carry `populated` + `empty` variants only. Three
classes of body the goldens do NOT carry, and how each is served:

1. **Empty-set** — IS a golden variant (`empty`). Served by
   `fulfillFromGolden(route, endpoint, "empty")`. No escape hatch needed;
   this is the load-bearing array-vs-null contract (409 D3) the consumer
   half already pins, now reused by the e2e mock.

2. **Error states (4xx/5xx)** — NOT a golden body (goldens are 200-shape).
   Served by a hand-written `route.fulfill({ status: 503, body: "" })`
   (or a `route.abort()`). This stays hand-written **by design** — there
   is no recorded provider body for an error, so there is nothing to load.
   The pattern is documented inline in `fulfill-from-golden.ts` and
   demonstrated in `first-time-login.spec.ts` (the 503 fallback test, now
   annotated as the escape hatch) and `dashboard.spec.ts` AC-4 (the
   `abort()` error subtitle).

3. **Field-override (a populated variant with a spec-specific value)** —
   served by the golden base + `options.override`. The credential-bearer
   specs need `display_name` to end in a specific 4-char suffix
   (`…1f3a`) because the visible assertion reads the formatted credential
   label; the golden's `synthetic_admin` carries `…ad01`. Rather than
   abandon the golden, the spec passes
   `override: { display_name: "API key 1f3a" }`. The golden remains the
   base shape (every other field — `roles: []`, `is_admin`, `tenant_role`,
   the always-present array contract — comes from the recorded truth), so
   the spec cannot drift on the **shape**; only the one value the
   assertion pins is overridden, and that override is explicit and
   reviewable. This is the slice-276 lesson applied: the page consumes
   more of the type than the test reads, so the base body MUST be
   shape-complete — and a golden is shape-complete by construction.

   The slice-229 freshness 87% test is the same pattern: the assertion
   needs a deterministic `87% within window`, so it overrides the
   `buckets` + `total` + `total_stale` onto the `populated` base. The
   golden's `bucket: "class"` key and array-shape come from the recorded
   truth; the numbers are the spec's deterministic override.

**Tested:** AC-3 is tested two ways — (a) `first-time-login.spec.ts` now
runs the two install-state variants through `fulfillFromGolden` AND keeps
the 503 escape-hatch test (golden path + override-absent error path in one
spec); (b) the credential-bearer specs exercise `override`. Additionally a
self-test (`web/e2e/test-utils/fulfill-from-golden.contract.test.ts`,
vitest — it imports node `fs`, no browser) asserts the helper's pure logic:
the endpoint→file map resolves, a known variant returns the recorded body,
an unknown variant throws, and `override` deep-merges over the base. This
self-test rides the **existing vitest surface** (slice 348's `**/*.test.ts`
directory walk), so it is zero-new-gate (AC-4) and gives the helper fast
unit feedback independent of a full e2e bring-up.

## D4 — Zero-new-gate (AC-4)

No CI job added; no `ci.yml` change. The migrated specs ride the existing
`Frontend · Playwright e2e` surface; the helper self-test rides the
existing `Frontend · vitest` surface (auto-enrolled by the slice-348
directory walk). ADR-0007's "rides the existing surfaces, no fifth gate"
constraint is honored — this slice does not even add a surface, it makes
an existing surface (the e2e mocks) consume the goldens an existing tier
already records.

## D5 — Discouraging new hand-written bodies for golden-covered routes

The slice doc's optional "note (or lint)" item: a lint over `e2e/**` is
not feasible (eslint globally ignores `e2e/**`, and a body-shape lint is
exactly the openapi-drift-guard's "cannot model bodies" limitation). I
chose the **note** path: `web/e2e/README.md` gains a
"Golden-backed route mocks" section instructing authors to reach for
`fulfillFromGolden` for any route in the nine-endpoint set, and to add the
endpoint to the helper's union (recording its golden first) rather than
hand-writing a body. The helper's typed `GoldenEndpoint` union is the
mechanical enforcement for the routes that ARE covered: you cannot pass an
uncovered endpoint to the helper, and the README tells you to record a
golden before hand-writing. This is the lightest discipline that does not
add a gate (consistent with ADR-0007's whole posture).

## Spillovers

None new. The residual hand-mocked routes (`/v1/risks`, `/v1/controls/*`,
`/v1/board`, `/v1/policies`) are already tracked as #410 + #411 (slice 409
D6); this slice's AC-3 escape hatch is exactly their interim handling, so
no new slice is warranted. No golden was found to be wrong; no real
provider/consumer mismatch surfaced (the credential-bearer `display_name`
divergence is a spec-assertion-specific value, correctly handled by
`override`, not a golden defect).

## Detection-tier classification (slice 353 Q-13)

- `detection_tier_actual`: `none` — no bug surfaced during the slice. The
  `display_name` golden-vs-spec divergence is a deliberate spec assertion
  value (the formatted-credential-label test), resolved by the documented
  `override` path, not a found defect.
- `detection_tier_target`: `contract` — this slice extends the contract
  tier's reach so the e2e mocks (`playwright` tier) serve the
  contract-tier-recorded truth, closing the slice-334 P-1 mock-vs-reality
  drift for the golden-covered routes at the source.
