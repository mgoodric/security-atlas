# Slice 410 — contract-tier golden for the dashboard top-risks panel (`GET /v1/risks`) — decisions log

JUDGMENT slice. The build-time subjective calls (the list-only seam shape, the
golden variants/fixtures, the transform-aware consumer-assert shape) are
recorded here per the continuous-batch JUDGMENT convention; the maintainer
iterates post-deployment. This does NOT touch the product-runtime AI-assist
boundary (separate, constitutional).

Cross-references: ADR-0007 (`docs/adr/0007-contract-test-tier.md`), slice 409
decisions log D1 + D6 (`docs/audit-log/409-contract-tier-rollout-dashboard-decisions.md`),
slice 392 (`docs/audit-log/392-contract-test-tier-rollout-decisions.md`), slice
410 spec (`docs/issues/410-contract-tier-risks-panel.md`).

---

## D1 — The DB-seam shape: a LIST-ONLY sub-interface, not a full read seam (AC-1)

Slice 409 D1 deferred this endpoint for two reasons; this slice resolves the
first by choosing the seam shape it named. The risks `Handler` holds
`store *risk.Store`, which exposes a WIDE surface
(Create/List/Get/Delete/Heatmap/ThemeOrgUnitHeatmap/…). A 409-style read seam
that the whole `Handler` depends on — the freshnessdrift pattern, where
`*freshness.Store`/`*drift.Store` each have ~one read method — would here be a
~7-method interface, a far bigger refactor than recording one golden justifies.

**Decision:** add an unexported `riskLister` interface carrying JUST the one
`List(ctx, risk.ListFilter) ([]risk.Risk, error)` method the `ListRisks`
(`GET /v1/risks`) path uses, and a `lister riskLister` field on `Handler`.
`New(*risk.Store)` wires `lister` to the same store (production behavior
identical — the concrete `*risk.Store` satisfies the seam verbatim). The
`ListRisks` body reads `h.lister.List(...)` instead of `h.store.List(...)`;
**every other handler keeps using the concrete `h.store` directly**. This is
the minimal faithful seam: narrow by design (slice 409 D1's "the clean shape is
a list-only sub-interface"), no SQL change, no migration, no behavior change.

**P0 compliance:**

- **P0-409-1 (no recorder on the integration surface):** honored. The recorder
  runs on the plain `go test ./...` unit surface via the injected fixed-row
  stub. No `//go:build integration` tag on `handler_contract_test.go` or
  `contractrecord_test.go`. Verified: `go test ./internal/api/risks/...`
  (no `-tags`) records + asserts green.
- **P0-409-2 (no public-API widening):** honored. `riskLister`, the `lister`
  field, and `newHandlerWithLister` are all **unexported**. The exported
  `New(*risk.Store)` signature is byte-for-byte unchanged; production wiring in
  `httpserver.go` is untouched. Only the static type the `ListRisks` path reads
  through changed (concrete → interface the concrete already satisfies).
- **No new Go import** outside packages already in `go.mod`
  (`authctx`/`credstore`/`dbx`/`risk`/`tenancy`/`uuid` are all pre-existing
  deps). `go mod tidy` produced no diff; `go.work.sum` unchanged.

## D2 — The recorder request must satisfy TWO gates, not one

Unlike the freshnessdrift handlers (one gate: a tenant on the context),
`ListRisks` runs `requireProgramRead` FIRST (slice 067, defense-in-depth role
guard) and then `tenantContext`. So the recorder request carries BOTH: a
tenant on the context (`tenancy.WithTenant`) AND an `authctx` credential with a
program-read signal. I set `credstore.Credential{IsApprover: true}` —
`IsApprover` maps to `grc_engineer` in `authz.go hasProgramRead`, the minimal
signal that grants program-read. With both gates satisfied the recorder reaches
the happy `200 + {risks, count}` path with no DB. (Recorded explicitly because
a future maintainer copying the freshnessdrift recorder verbatim would get a
403 and be puzzled — the risks endpoint's authz guard is the difference.)

## D3 — Golden shape: variants (populated + empty), mirroring 392/409

`{_comment, endpoint, variants{}}` with stable contract-identifier keys. Two
variants:

- **`populated`** — TWO rows so the golden exercises both the present and
  absent branches of the nullable fields in one capture:
  - Row 1 (fully populated): opaque `inherent_score`/`residual_score` blobs, a
    two-element `linked_control_ids` array, and `review_due_at` +
    `accepted_until` BOTH set (pins their present-on-wire shape:
    `review_due_at` an RFC3339 timestamp, `accepted_until` a `YYYY-MM-DD`
    string).
  - Row 2 (minimal): empty opaque blobs (the handler's `jsonRaw` defaults them
    to `{}`), NO linked controls (`[]`, never null — the handler builds a
    zero-length slice), and `review_due_at`/`accepted_until` BOTH nil →
    **omitted** on the wire (the `omitempty` branch). This pins the
    load-bearing "nullable fields absent, not `null`" contract the BFF's
    optional `review_due_at?`/`accepted_until?` typing depends on.
- **`empty`** — the empty-set result: `{risks: [], count: 0}`. The
  array-vs-null contract a fresh-tenant e2e run hits; `risks` is `[]`, never
  `null`.

Fixture values are obviously-fake (synthetic UUIDs
`…000000000410`/`11111111…`/`22222222…`/`44444444…`, plain strings, a
`JIRA-410` instrument reference) per slice 314 / GitGuardian — no JWT- or
vendor-shaped literals.

Incidental capture: `severity` (computed from `inherent_score`, slice 067) and
`themes: []` also record. Correct — the golden pins the _real_ handler envelope,
and these are genuinely on the `/v1/risks` wire; the BFF/`DashboardRisk` type
ignores them, which the consumer assert does not contradict (it asserts the
fields the BFF relies on, not the absence of others).

## D4 — The transform-aware consumer assert (the key difference from 409)

Slice 409's dashboard panels are verbatim passthroughs:
`expect(got).toEqual(providerBody)`. The dashboard/risks BFF is NOT.
`getMitigateRisks` (`web/lib/api/dashboard.ts`) fetches `/v1/risks`, then
**unwraps `body.risks`** and returns just the array; the route
(`web/app/api/dashboard/risks/route.ts`) **re-wraps** it as
`{risks, count: risks.length}`.

So the golden variant body IS the upstream `{risks, count}` envelope, and the
consumer assert is transform-aware:

```ts
expect(got).toEqual({
  risks: providerBody.risks,
  count: providerBody.risks.length,
});
```

NOT `toEqual(providerBody)`. The re-wrapped `count` is **recomputed from the
array length**, so it is asserted against `providerBody.risks.length` — pinning
the BFF's own recount, independent of the upstream `count` field. (They happen
to match in the golden, but the BFF contract is `count === risks.length`; an
extra assertion `expect(got.count).toBe(providerBody.risks.length)` makes the
recount explicit.) The field-shape assertions (opaque score blobs present,
`linked_control_ids` an array of strings, optional `review_due_at`/
`accepted_until` typed when present) run over `golden.variants[v].risks`.

## D5 — Drift-sensitivity proof (AC-3)

Proved on the `treatment` field. Renamed the golden's `treatment` →
`treatment_kind` (both rows of the `populated` variant). Result:

- **Provider half failed** (`go test ./internal/api/risks/ -run
TestContract_Risks`): `variant "populated" wire shape drifted from golden` —
  the handler emits `treatment`, the golden now says `treatment_kind`.
- **Consumer half failed** (`npm run test -- lib/contracts/risks.contract.test.ts`):
  `populated.treatment: expected 'undefined' to be 'string'` — the
  `typeof rk.treatment === "string"` field-shape assertion.

Both halves red on a single-field rename; golden restored; both green. This is
the slice-210-class catch (the BE/FE contract gap that hid the login form),
reproduced on the dashboard risks route — the exact bug class ADR-0007 exists
to catch.

## D6 — Zero-new-gate (AC-4) + coverage

No new CI job, no new gate, no new tool, no new dependency (ADR-0007 (d)). The
recorder rides the existing `Go · build + test` unit surface; the consumer
rides `Frontend · vitest` (auto-enrolled by slice 348's `**/*.test.ts`
directory walk). `internal/api/risks/` is on the coverage-gate **exclude** list
(slice 069: HTTP/RLS package integration-tested against real Postgres) — there
is no per-package unit floor to lift, so no ratchet obligation. The recorder
nonetheless **raises** risks-package unit-surface coverage from **4.5% → 13.8%**
(it exercises `ListRisks` + `riskWireFrom` + `severityOf` + `jsonRaw` on the
unit surface, paths previously only integration-covered). Merged coverage gate
is unaffected (no floor change; monotonic non-decrease holds).

## Detection-tier classification (slice 353 Q-13)

- `detection_tier_actual`: `none` — no product bug surfaced during the slice;
  the drift-sensitivity rename (D5) was a deliberate proof, not a found defect.
- `detection_tier_target`: `contract` — this slice's entire purpose is to add
  the contract tier that catches the slice-210 class of BFF↔atlas shape drift
  on the dashboard risks route at the cheapest surface.

## Spillovers

None. The slice was fully in scope: list-only seam + recorder + golden +
transform-aware consumer assert + drift proof + decisions log all landed. Slice
411 (controls-detail + audit-workspace routes, the other 409 D6 spillover)
remains the next contract-tier rollout target and is NOT touched here (per the
spawn note: 411 already covers that surface).
