# ADR 0007 — Contract-test tier for the BFF↔atlas wire shape

**Status:** Accepted — PILOT shipped (slice 349); broad rollout deferred to a follow-on slice.

**Date:** 2026-05-29

**Resolves:** slice 333 finding **Q-1** (`docs/audits/333-qa-strategy-gap-analysis.md`) — no test tier pins the HTTP wire shape between the Next.js BFF (consumer) and the Go atlas API (provider). Reinforced by slice 334 finding **P-1** (`docs/audits/334-test-framework-review.md`) — the `/e2e/` suite mocks the upstream atlas API in 57 `route.fulfill` calls, so a real BFF↔atlas shape regression is not caught.

**Implements through:** `docs/issues/349-contract-test-tier-evaluation.md`.

**Slot note:** The slice doc names the file `docs/adr/00NN-contract-test-tier.md`. At pickup (2026-05-29) slots 0001–0006 are occupied (note: 0003 is double-occupied — `0003-audit-period-freeze-hash-inputs.md` and `0003-oauth-authorization-server.md` — a pre-existing collision this ADR does not touch). Next free slot is 0007.

---

## Context

The four-surface merged gate (CLAUDE.md "Testing discipline") is:

| Surface             | What it exercises                                                                                                             |
| ------------------- | ----------------------------------------------------------------------------------------------------------------------------- |
| Go unit             | Per-package Go logic, including the atlas HTTP handlers in isolation.                                                         |
| Go integration      | RLS + migrations + handlers against real Postgres/NATS/MinIO — atlas in isolation.                                            |
| Frontend vitest     | BFF route handlers, `lib/api.ts`, `lib/api/bff.ts` — the consumer in isolation, with **hand-written upstream mocks**.         |
| Frontend Playwright | User flows against a real Next.js process with the upstream atlas API **mocked** in 57 `route.fulfill` calls (slice 334 P-1). |

Two of these exercise the provider in isolation; two exercise the consumer with an _invented_ picture of the provider. Nothing binds the consumer's picture to the provider's reality. That is the gap.

### The gap is concrete, not theoretical

`web/app/api/install-state/route.ts` (the BFF) consumes atlas's `GET /v1/install-state`. Its vitest test (`web/app/api/install-state/route.test.ts`) hand-writes the upstream mock:

```ts
const upstreamBody = JSON.stringify({ first_install: true });
```

The Go handler (`internal/api/install_state.go`) actually emits:

```go
type installStateResponse struct {
    FirstInstall bool   `json:"first_install"`
    TenantID     string `json:"tenant_id,omitempty"` // slice 210
}
```

The literal `{ first_install: true }` in the test is not tied to the Go struct. If the handler renamed `first_install` → `firstInstall`, both the vitest test and the Playwright mocks stay green on their own invented shape; the BFF breaks silently in production. **This exact class of bug already happened** — slice 210 (MEMORY: "210 closes slice 209 BE/FE contract gap that hid email/password login form on fresh installs") shipped because the install-state response gained a `tenant_id` field the login form needed and the BFF↔atlas contract had drifted.

### Why the slice-140 openapi-drift check does not close it

slice 339 added 12 OAuth endpoints to `docs/openapi.yaml`; slice 140 ships a **BLOCKING** `openapi-drift-check` CI guard. That guard is real and valuable — but it verifies **route presence + auth tier + structural shape**, NOT response bodies. From `internal/api/openapi/validator.go` ("What it does NOT check"):

> Per-operation request/response body schemas (v1 spec uses the Ack envelope as a placeholder — a follow-on slice refines).

And `RouteSpec` (`internal/api/openapi/routes.go`) carries `Method, Path, Tag, Tier, Internal, Summary` — **no response-body field at all**. The drift guard cannot catch a body-shape change because it never models bodies. The wire-shape contract is genuinely unowned.

---

## Options evaluated

Scored against (a) build cost, (b) maintenance cost, (c) drift-detection sensitivity, (d) fit with the slice-069 ratchet contract. Scale: Low / Medium / High; for (c) and (d), higher is better.

### Option 1 — Golden-file contract (provider records, consumer asserts)

The provider-side Go test records the real handler's response bodies to a committed golden; the consumer-side vitest test asserts the BFF against the same golden. A `-update` flag regenerates the golden on an intentional change, in the same PR as the shape change.

| Axis                  | Score          | Notes                                                                                                                                                                                                                |
| --------------------- | -------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| (a) build cost        | **Low**        | Reuses existing surfaces. One Go test + one vitest test + one JSON golden per endpoint pair. Zero new deps, zero new CI jobs.                                                                                        |
| (b) maintenance cost  | **Low–Medium** | Golden-file drift is its own discipline (slice 334 U-3 flags this generally), but the `-update` flow makes an intentional change a one-command regeneration. The golden is small (a few hundred bytes per endpoint). |
| (c) drift sensitivity | **High**       | Catches renamed / added / dropped / retyped fields. The pilot proves it: injecting `first_install` → `firstInstall` fails **both** halves.                                                                           |
| (d) slice-069 fit     | **High**       | Runs on the existing Go-unit and vitest surfaces — no fifth CI job, no new gate. The ratchet contract is untouched; the golden is just another committed test fixture.                                               |

### Option 2 — Schemathesis / OpenAPI-driven

Generate request/response contract tests from `docs/openapi.yaml` and run them against atlas and the BFF's expectations.

| Axis                  | Score                              | Notes                                                                                                                                                                                                                                                                                                      |
| --------------------- | ---------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| (a) build cost        | **High**                           | New Python tool in the toolchain (schemathesis), CI wiring, and — the killer — it depends on the OpenAPI spec carrying **complete, accurate response-body schemas**, which it does not (validator.go: bodies are an Ack placeholder). The spec would have to be enriched first (a large slice of its own). |
| (b) maintenance cost  | **Medium–High**                    | Spec + tool + the body-schema completeness burden. slice 339 watches drift, not completeness.                                                                                                                                                                                                              |
| (c) drift sensitivity | **High** (once bodies are modeled) | Spec-driven, surfaces schema drift in CI.                                                                                                                                                                                                                                                                  |
| (d) slice-069 fit     | **Low**                            | Adds a fifth surface and a new language runtime to the gate. Larger blast radius than the gap warrants.                                                                                                                                                                                                    |

### Option 3 — gRPC contract testing via `buf breaking`

`proto/` already holds `evidence.proto`, `connectors.proto`, `oscal.proto`, `admin/credentials.proto`. `buf breaking` catches incompatible proto changes.

| Axis                  | Score                               | Notes                                                                                                                                    |
| --------------------- | ----------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------- |
| (a) build cost        | **Low**                             | `buf` is a small addition.                                                                                                               |
| (b) maintenance cost  | **Low**                             |                                                                                                                                          |
| (c) drift sensitivity | **High for gRPC, ZERO for the gap** | The BFF↔atlas surface is **HTTP/REST**, not gRPC. `buf breaking` cannot see the install-state wire shape at all.                        |
| (d) slice-069 fit     | n/a                                 | Forbidden as the answer by slice doc **P0-2** without separately addressing HTTP-only surfaces — which is most of the BFF's consumption. |

Rejected for the wire-shape gap. (`buf breaking` is a reasonable _separate_ slice for the gRPC connector contract; out of scope here.)

### Option 4 — Do nothing; rely on slice 351 (critical-flow e2e audit) + Q-10 (ui-honesty promotion)

| Axis                  | Score                           | Notes                                                                                                                                                                                                                                                                                        |
| --------------------- | ------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| (a) build cost        | **Zero**                        | No new tier.                                                                                                                                                                                                                                                                                 |
| (b) maintenance cost  | **Zero**                        |                                                                                                                                                                                                                                                                                              |
| (c) drift sensitivity | **Low for per-endpoint shape**  | The `e2e-audit/` real-services harness catches end-to-end _happy paths_, not per-endpoint field-level drift, and is **informational only** (Q-10). It would catch a shape break only if a happy-path assertion happened to traverse the broken field — the same fragility as the status quo. |
| (d) slice-069 fit     | **High** (it is the status quo) |                                                                                                                                                                                                                                                                                              |

---

## Decision

**Ship Option 1 as a tight PILOT now; defer broad rollout to a follow-on slice.**

Rationale:

1. **The gap is real and has already bitten** (slice 210). It is not a hypothetical worth ignoring.
2. **It is genuinely unowned.** The slice-140 openapi-drift guard does not model bodies; the e2e suite mocks the upstream; the vitest tests invent it. No existing surface closes it.
3. **Option 1 is the only option that closes the gap without a new tier, a new language, or a new CI job.** It is cargo-cult-free: no Pact broker (heavyweight provider/consumer broker infrastructure is unjustified for a monorepo where provider and consumer ship in one PR), no schemathesis runtime, no proto tooling that cannot see HTTP.
4. **It composes with, rather than replaces, the existing surfaces** (slice doc P0-3: the `/e2e/` mocked suite stays). The golden becomes the single source of truth the e2e mocks _should_ match; a follow-on can teach the mocks to load from the golden.

This is **not** a "build a fifth merged gate" decision. It is "add a shared golden fixture, recorded by the provider's existing unit test, asserted by the consumer's existing vitest test." The pilot proves the mechanism on one endpoint; the follow-on extends it to the high-traffic endpoint pairs.

### Pilot scope (shipped in slice 349)

- **Endpoint:** `GET /v1/install-state` — chosen over the slice-doc's suggested `+ /api/admin/demo/status` pair because install-state carries the demonstrated slice-210 bug history AND a non-trivial shape (the conditional `tenant_id` field), making it the higher-value single proof. `demo/status` is a trivial `{enabled: bool}` and adds little to the proof; it is a natural first target for the rollout follow-on.
- **Provider recorder:** `internal/api/install_state_contract_test.go` — `TestContract_InstallState`. Drives the real handler across three variants (fresh-with-tenant, fresh-without-tenant, post-install), canonicalizes the bodies, and diffs against the golden. Regenerate with `go test ./internal/api/ -run TestContract_InstallState -update`. Runs on the **plain `go test ./...` unit surface** (the handler reads through the `PlatformStatus` interface; no DB needed).
- **Golden (shared truth):** `web/lib/contracts/install-state.golden.json`.
- **Consumer assertions:** `web/lib/contracts/install-state.contract.test.ts` — reads the golden, asserts every variant carries a boolean `first_install` (the BFF's load-bearing assumption), and drives the real BFF `GET` handler with each recorded body to confirm verbatim passthrough. Runs on the existing **vitest** surface (auto-enrolled by slice 348's `**/*.test.ts` directory walk).

### Drift sensitivity — proven, not asserted

Injecting `first_install` → `firstInstall` into the golden during development failed **both** halves (provider golden mismatch; consumer "first_install must be boolean"). That is the proof the tier catches the slice-210 class of bug. Verified before commit; the golden was restored.

---

## Consequences

**Positive:**

- The slice-210 class of silent BFF↔atlas drift is now caught at the cheapest surface (Go unit + vitest, milliseconds) for the piloted endpoint.
- A single golden becomes the source of truth that the e2e `route.fulfill` mocks _should_ be derived from — a path the rollout follow-on can take to retire P-1's mock-vs-reality fragility.
- Zero new tooling, zero new CI jobs, zero new gate. The slice-069 ratchet contract is untouched.

**Negative / accepted trade-offs:**

- **Golden-file discipline.** A genuine shape change needs a `-update` run in the same PR (slice 334 U-3's general caution). The `-update` flow keeps this to one command and the diff is reviewable.
- **Coverage, not completeness.** The pilot covers one endpoint pair. The other ~107 BFF routes remain unguarded until the rollout follow-on lands. This is intentional (slice doc P0-1: pilot only).
- **Provider/consumer in one repo.** The golden is committed and shared via a relative path (`internal/api → ../../web/lib/contracts`). A future repo split would need the golden published as an artifact; noted, not a v1 concern.

**Follow-on (filed per AC-4):** `docs/issues/392-contract-test-tier-rollout.md` — extend the golden-file contract tier to the high-traffic BFF↔atlas endpoint pairs (`/v1/admin/demo/status`, `/v1/me`, `/v1/version`, `/v1/metrics`, …) and evaluate teaching the `/e2e/` `route.fulfill` mocks to load from the goldens. **Do NOT roll out in slice 349.**

---

## Cross-references

- **Slice 333 Q-1** (`docs/audits/333-qa-strategy-gap-analysis.md`) — the strategy finding this ADR resolves.
- **Slice 334 P-1** (`docs/audits/334-test-framework-review.md`) — the framework-level mock-density finding.
- **Slice 339** (`docs/issues/339-openapi-oauth-endpoint-spec-drift.md`) + **slice 140** (`internal/api/openapi/`) — the OpenAPI drift guard whose scope (presence + tier, not bodies) leaves this gap open.
- **Slice 210** (MEMORY `project_slice_210_landed.md`) — the demonstrated BE/FE contract bug this tier would have caught.
- **Slice 348** (`web/vitest.config.ts`) — the `**/*.test.ts` directory-walk that auto-enrolls the consumer test.
- **Slice 351** (`docs/issues/351-e2e-critical-flow-gap-audit.md`) + Q-10 ui-honesty promotion — the compensating path that Option 4 would have relied on; complementary, not a substitute.
