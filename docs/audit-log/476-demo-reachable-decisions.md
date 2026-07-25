# Slice 476 — demo-data operator-reachability: JUDGMENT decisions log

**Slice:** 476 — Make seeded demo data reachable by the operator who loads it
**Type:** JUDGMENT (access-grant shape + super-admin switch UX call)
**Branch:** `fix/476-demo-reachable`
**Outcome:** verification + a small CLI seed hint (the mechanism was already
shipped by slices 478/479; this slice proves the journey and closes the one
discoverability rough edge).

---

## Context: the reduction

Slice 476 was filed (2026-06-06) from live maintainer use: after the demo seed
succeeded on a self-host edge box, the operator had **no way to reach the demo
data**. The demo seed (slice 205/278) creates a SEPARATE `demo` tenant; the
tenant switcher (slice 192) is deliberately membership-bounded (P0-192-5: never
show a tenant you are not a member of, even for a super-admin), and the seed did
not add the operator to the demo tenant — so the switcher stayed hidden and the
demo data was unreachable.

The slice doc named two candidate mechanisms (Option 1 seed-grants-access;
Option 2 super-admin switch-to-any-tenant). **Both were subsequently subsumed by
later slices before 476 was picked up:**

- **Slice 478** shipped `POST /v1/admin/users/assign` with `self_assign=true` —
  Option 1 done as an explicit, audited, operator-initiated grant, INCLUDING the
  load-bearing local-auth case (the `urn:atlas:local` + origin-user_id synthetic
  key that makes an empty-IdP identity enumerable without the empty-tuple
  over-match; slice 478 D1 / P0-478-2).
- **Slice 479** shipped the `/admin/users` UI: "Add me to a tenant" self-assign
  dialog + the AC-4 re-auth notice (P0-479-3: NO auto-switch).
- **Slice 192** already enumerates the resulting membership into
  `available_tenants`, which the switcher reads.

So the mechanism slice 476 originally owned is shipped, end-to-end, by
478 + 479 + 192. Re-building any of it here would duplicate those slices and
violate this slice's scope discipline (and P0-476-3, which forbids building the
general cross-tenant user-management UI). **Slice 476 therefore reduces to: prove
the composed journey works, and add the smallest hint if there is a genuine
rough edge.** Per the brief, "journey already works, here's the proving test"
is a legitimate honest outcome — that is essentially the outcome here, plus one
small CLI hint.

---

## D1 — Test surface: integration, not a new Playwright e2e

**Decision:** prove the journey with a Go integration test
(`internal/api/adminusers/demo_reachable_integration_test.go`), not a new
Playwright spec.

**Rationale:**

- The journey's load-bearing parts are all server/DB surfaces: the seeder's
  cross-tenant BYPASSRLS write (205), the slice-478 synthetic local-auth key,
  and the slice-192 resolver enumeration. Asserting the COMPOSED journey at the
  data layer is where the real proof lives.
- The two UI surfaces the journey ends in — the slice-479 `/admin/users` page
  and the slice-192 tenant-switcher — are ALREADY e2e-covered
  (`web/e2e/admin-users.spec.ts`, `web/e2e/tenant-switch.spec.ts`,
  `web/e2e/admin-demo.spec.ts`). They are thin wrappers over the exact APIs the
  integration test drives. A new chromedp spec would add flake surface (the
  slice 340/341 lineage) without covering anything the existing specs miss.
- The unique thing slice 476 must prove is the COMPOSITION of 205 + 478 + 192
  against the REAL demo dataset. 478's own `assign_integration_test.go` proves
  self-assign → `available_tenants` (AC-4) and the local-auth no-over-match
  (AC-5) — but against a tenant literally named `test-self-demo`, NOT the real
  `demoseed.Seeder`. No prior test chains the real seeder → self-assign → reach
  → assert the 50/20/200 dataset is present. That composition is this slice.
- The seed-harness precondition gate (the brief's spillover trigger) does not
  bite: `internal/api/adminusers/...` is already enrolled in the integration
  shard manifest (`scripts/integration-shards.txt` B1), and the test seeds its
  own demo data via the real seeder + the slice-478 fixtures — no docker-compose
  seed-harness gap, no spillover needed.

**Confidence:** high. Verified by running all three new tests + the full
`adminusers` integration package green against a real migrated Postgres.

## D2 — Outcome: verification-only on the mechanism; one code hint

**Decision:** no code change to the access-grant / switch mechanism — 478/479/192
already implement it correctly and the journey passes end-to-end. The only code
change is the CLI seed hint (D3).

**Rationale:** the integration test is the evidence. Pre-assign, the demo tenant
is NOT in `available_tenants` (the membership bound holds); post-assign it IS,
`current_tenant` is unchanged (no auto-switch), and the dataset is present. A
normal user who never self-assigned still cannot see the demo tenant (P0-476-1
preserved). All ACs the reduction leaves (AC-1/AC-2/AC-3/AC-5/AC-7) are proven.

## D3 — Seed hint: a CLI "reach the demo data" block (the one rough edge)

**Decision:** add a three-line hint to the `atlas-cli demo seed` success output
pointing the operator at the reach-it journey. Do NOT touch the 479 UI or the
478 API.

**Rationale:** the genuine rough edge slice 476 was filed for is
DISCOVERABILITY. The CLI seed output printed the new `tenant_id` + counts +
admin credentials, but never told the operator that the demo data lives in a
SEPARATE tenant they must self-assign to (and re-auth) before it appears in the
switcher. An operator who runs the seed and stops there hits a dead end — the
exact failure in the maintainer report. The hint is pure guidance text on the
existing success path; it builds no surface (479 owns the UI, 478 the API),
honors the membership bound as the REASON a grant is required (not a bug), and is
the smallest possible affordance. The HTTP/UI seed paths already surface the
journey via the slice-479 re-auth notice, so the CLI was the remaining gap.

**Why not a UI hint too:** the slice-479 self-assign dialog + re-auth notice
already guide the UI operator; adding a demo-specific banner there would creep
into 479's surface (P0-476-3). The CLI was the un-guided path.

## D4 — Neutral unique slug + teardown cleanup

**Decision:** the test seeds the demo dataset under a unique
`test-demo-<hex>` slug (never the literal `demo`), and `t.Cleanup` tears it down
via the seeder's own `Teardown`.

**Rationale:** the seeder is idempotent on slug, so a fixed slug would collide
across parallel runs / a shared CI DB and could refuse-as-already-seeded. A
unique neutral slug keeps fixtures neutral (P0-478-6 / P0-A7), guarantees a
fresh seed (asserted via `Idempotent=false`), and never pollutes a real `demo`
tenant. Tearing down through the seeder's own `Teardown` (rather than a raw
DELETE) additionally exercises the AC-6 seed/teardown interplay and the
forensic-mark guard.

---

## Anti-criteria compliance

- **P0-476-1** (non-super-admin cannot reach a tenant they are not entitled to):
  proven by `TestDemoReachable_NormalUserCannotSeeDemoTenant`.
- **P0-476-2** (no auto-switch): asserted — `current_tenant` is unchanged after
  self-assign; the hint explicitly tells the operator to switch manually.
- **P0-476-3** (do not build the general cross-tenant user-management UI):
  honored — zero UI code; the only code change is CLI guidance text.
- **P0-476-4** (do not bypass RLS / token-exchange): honored — the journey runs
  entirely through the shipped 478 assign path + the 192 resolver; the test
  asserts the EXISTING bound, it does not relax it.

## Detection-tier classification (slice 353 Q-13)

- `detection_tier_actual`: `production` — the reachability gap was found in live
  maintainer use on a self-host edge box (the slice's filing context), not by an
  automated tier.
- `detection_tier_target`: `integration` — the composed seed → self-assign →
  reach → dataset-present journey is an integration-tier contract; this slice
  adds exactly that test so the regression is caught at the integration tier
  going forward. (The aggregate `target=integration, actual=production` reading
  is the expected shape for a gap surfaced before its proving test existed.)

## Verification evidence

- `go vet -tags=integration ./internal/api/adminusers/` — clean.
- `go test -tags=integration -p 1 -run TestDemoReachable -v ./internal/api/adminusers/`
  — all three PASS against a real migrated Postgres.
- `go test -tags=integration -p 1 ./internal/api/adminusers/` — full package green
  (the shared slice-478 harness is unbroken).
- `go build ./...` — clean. `golangci-lint run` (with and without the
  `integration` build tag) on `cmd/atlas-cli/...` + `internal/api/adminusers/...`
  — 0 issues.
