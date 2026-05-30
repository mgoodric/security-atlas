# 349 — Contract-test-tier evaluation decisions log

**Slice:** 349
**Type:** JUDGMENT
**Date:** 2026-05-29
**Companion artifact:** `docs/adr/0007-contract-test-tier.md`

The ADR is the evaluation deliverable (four options scored, decision +
rationale). This log records the JUDGMENT calls Claude made building the
slice, per the JUDGMENT-slice convention.

---

## D1 — Headline decision: PILOT then ADOPT (Option 1, golden-file), broad rollout deferred

**Decision:** Ship a tight golden-file contract pilot for one endpoint
pair now; file a follow-on (slice 392) for broad rollout. NOT "no tier."

**Why not "no tier" (Option 4):** the gap is real AND has already bitten
(slice 210 BE/FE contract bug). It is genuinely unowned — the slice-140
openapi-drift guard verifies route presence + auth tier + structural
shape but NOT response bodies (`internal/api/openapi/validator.go`
"What it does NOT check"; `RouteSpec` carries no body field). The e2e
suite mocks the upstream; the vitest tests invent it. Recommending "no
tier" would have ignored a demonstrated bug class. This is the
honest-evaluation discipline the task demanded: I checked whether the
four surfaces + slice-339 openapi drift already cover the contract, and
they provably do not at the body level.

**Why not adopt-broadly-now:** slice doc P0-1 forbids it, and it is the
right call — the cost of a broad rollout is unknown until the mechanism
is proven on one pair. Pilot proves; follow-on scales.

## D2 — Option 1 over Options 2/3

- **Option 2 (schemathesis/OpenAPI):** rejected as the answer because it
  depends on the OpenAPI spec carrying complete, accurate response-body
  schemas, which it does not (bodies are an Ack placeholder). Enriching
  the spec is a large slice of its own; adding a Python runtime + a
  fifth CI job is disproportionate to the gap. Poor slice-069 fit.
- **Option 3 (`buf breaking`):** rejected for the wire-shape gap because
  the BFF↔atlas surface is HTTP/REST, not gRPC — `buf breaking` cannot
  see install-state at all. Also forbidden by slice doc P0-2 as the sole
  answer. (`buf breaking` remains reasonable for the _gRPC connector_
  contract — a separate, out-of-scope slice.)
- **No Pact:** a provider/consumer broker is heavyweight cargo-cult for
  a monorepo where provider and consumer ship in one PR. Rejected on the
  project's anti-cargo-cult-tooling discipline.

## D3 — Pilot endpoint: `GET /v1/install-state` alone, not the install-state + demo/status pair

**Decision:** Pilot install-state only, not the slice-doc-suggested pair
with `/v1/admin/demo/status`.

**Rationale:** install-state has BOTH the demonstrated slice-210 bug
history AND a non-trivial conditional shape (`tenant_id,omitempty`),
making it the higher-value single proof. `demo/status` is a trivial
`{enabled: bool}` — it adds proof-surface near zero while doubling the
pilot's moving parts. I made demo/status the natural first target of the
rollout follow-on (slice 392) instead. This is a tightening of scope,
not an expansion — consistent with the slice's "keep the pilot tight"
directive (Notes for the implementing agent).

## D4 — Provider recorder runs on the Go UNIT surface, not integration

**Decision:** `TestContract_InstallState` runs on plain `go test ./...`
(unit), not the `-tags=integration` surface.

**Rationale:** the handler reads through the `PlatformStatus` interface,
which the existing `fakePlatformStatus` already satisfies — no DB
needed. Running on the unit surface makes the contract golden cheap to
regenerate (no Postgres bring-up) and keeps the provider half in the
fastest tier. The recorded body is still the REAL handler output (same
`installStateResponse` struct, same `json.Encoder`), so the contract is
not weakened by avoiding the DB.

## D5 — Golden lives under `web/lib/contracts/`, shared via relative path

**Decision:** the golden is committed at
`web/lib/contracts/install-state.golden.json`; the Go recorder reaches
it via `../../web/lib/contracts/...`.

**Rationale:** the consumer (vitest) cannot resolve a path inside the Go
module tree cleanly, but the provider (Go) can resolve a relative path
out to the web tree. Putting the golden on the consumer's side and
having the provider write to it is the lower-friction direction. A
future repo split would publish the golden as an artifact instead;
noted in ADR-0007 Consequences, not a v1 concern.

## D6 — Shape-equivalence compare, not byte-equivalence

**Decision:** both halves canonicalize through a generic map before
comparing, so the compare tolerates the golden's pretty-printing and
struct field order.

**Rationale:** a byte-exact compare would make the test fragile to
JSON formatting (indentation, key order) rather than to the actual
contract (field names + types + presence). Shape-equivalence catches
the bug class that matters (rename/add/drop/retype) without false
positives on cosmetics.

## D7 — Drift sensitivity proven empirically before commit

**Decision:** I injected a `first_install` → `firstInstall` rename into
the golden and confirmed BOTH halves fail (provider golden mismatch;
consumer "first_install must be boolean"), then restored the golden.

**Rationale:** an evaluation that _claims_ drift sensitivity without
proving it is the inflation the project's `feedback_engineer_claim_*`
MEMORY entries warn against. The pilot's whole value proposition is
"catches the slice-210 class of bug"; I verified that empirically rather
than asserting it. Result recorded in ADR-0007 "Drift sensitivity —
proven, not asserted."

## D8 — Follow-on filed, not rolled out (AC-4)

**Decision:** filed `docs/issues/392-contract-test-tier-rollout.md`
(next free issue number; orchestrator de-collides if a parallel engineer
raced the same number).

**Rationale:** AC-4 requires a rollout follow-on when the decision is
"ship the tier." The follow-on carries the anti-criteria forward (no new
tooling, no new CI job, keep goldens small) so the rollout cannot drift
into the cargo-cult shapes Option 1 was chosen to avoid.

---

## detection_tier classification (slice 333 Q-13 convention, opt-in)

- `detection_tier_actual`: n/a — this slice adds a tier, it did not
  catch a bug.
- `detection_tier_target` for the slice-210 class of bug this tier
  addresses: **contract** (previously leaked to `production` /
  fix-forward; the new tier moves the target to the contract surface,
  caught at Go-unit + vitest in milliseconds).

## Acceptance-criteria self-check

| AC                                               | Status | Evidence                                                                                                                                                     |
| ------------------------------------------------ | ------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| AC-1 evaluation doc, 4 options scored on (a)-(d) | PASS   | `docs/adr/0007-contract-test-tier.md` "Options evaluated" — all four scored on build cost, maintenance cost, drift sensitivity, slice-069 fit.               |
| AC-2 pilot for one endpoint pair                 | PASS   | `internal/api/install_state_contract_test.go` + `web/lib/contracts/install-state.{golden.json,contract.test.ts}`; both green; drift-sensitivity proven (D7). |
| AC-3 decisions log                               | PASS   | this file.                                                                                                                                                   |
| AC-4 follow-on filed (ship-the-tier decision)    | PASS   | `docs/issues/392-contract-test-tier-rollout.md`.                                                                                                             |
| AC-5 "no tier" rationale                         | n/a    | decision was ADOPT-via-pilot, not "no tier"; AC-5 is the alternate branch.                                                                                   |
| AC-6 cross-refs slice 333 Q-1 + slice 334 P-1    | PASS   | ADR-0007 "Cross-references" + Context.                                                                                                                       |
