# 663 — Risk-create fresh-tenant mitigate dead-end — decisions log

Slice type: **JUDGMENT**. This log records the build-time calls made for slice
663 and what the maintainer should re-evaluate once the product runs against
real operators. It does NOT block merge (see `Plans/prompts/04-per-slice-template.md`).

- detection_tier_actual: none
- detection_tier_target: none

(No bug surfaced _during_ the slice — the slice itself fixes a UX dead-end that
the 2026-06-10 empty-tenant audit (ATLAS-006) already caught at the
`manual_review` tier. The new vitest + Playwright assertions are the regression
guard going forward; the cheapest tier that now catches a regression is `unit`
for the default constant and `playwright` for the end-to-end empty-tenant flow.)

## Decisions made

### D1 — Fix shape: AC-2 option (a), default Treatment to `avoid` (not (b), not accept)

**Options considered (from AC-2):**

- (a) Default Treatment to a value that does not require linked controls.
- (b) Relax the linked-controls requirement to optional when the tenant has
  zero controls.
- (c) Both.

**Chosen: (a), defaulting the opening treatment to `avoid`.**

**Rationale:**

1. **`avoid` is the only treatment with zero required satellite fields.** Canvas
   §6.1 (`Plans/canvas/06-risk.md`): `accept` requires `accepter` +
   `accepted_until`; `transfer` requires `instrument_reference`; `mitigate`
   requires ≥1 linked control; `avoid` is status-only. The form (slice 105)
   deliberately omits the accept/transfer satellite inputs, so defaulting to
   `accept` would just move the dead-end (the server rejects `accept` without
   `accepted_until` + `accepter` via `internal/risk/treatment.go`). `avoid` is
   the only default that is submittable end-to-end with the fields the form
   actually renders.
2. **It matches the server's own omitted-treatment default.**
   `internal/api/risks/handlers.go` already falls back to `RiskTreatmentAvoid`
   when the wire `treatment` is empty. Aligning the client opening default with
   the server's safe-default keeps the two consistent and means no server change
   is needed — the fix is a one-constant client change.
3. **Option (b) was rejected** because relaxing the mitigate rule when zero
   controls exist would (i) diverge client-side validation from the server's
   `ValidateTreatment` (which still hard-requires a control for mitigate),
   producing a submit-then-500 path, and (ii) weaken a canvas §6.1 invariant for
   a UX problem that a default change solves without touching any invariant. A
   default is reversible per-form; a relaxed rule is a semantic change to what
   "mitigate" means.
4. **Option (c) was rejected** as strictly more surface area than (a) for no
   additional benefit once (a) closes the dead-end.

**AC-3 preserved:** the mitigate-requires-control rule is unchanged. An operator
in a populated tenant who selects `mitigate` still must link ≥1 control, both
client-side (`validateRiskForm`) and server-side (`ValidateTreatment`). The
existing empty-state affordance in `control-multi-select.tsx` (which already
guides a fresh operator to switch treatment) composes with the new default.

Confidence: **high.**

### D2 — Pin the default as an exported constant (`DEFAULT_TREATMENT`) in `validate.ts`

The opening default could have been a bare literal in `risk-form.tsx`'s
`initialState()`. Instead it is an exported, documented constant in `validate.ts`
so it is unit-testable without standing up the React tree (the slice-151
extraction precedent), and so the rationale lives next to the validation rule it
interacts with. `risk-form.tsx` consumes the constant.

Confidence: **high.**

### D3 — No server-side change

The server already defaults omitted treatment to `avoid` and already enforces
the per-treatment rules correctly. Slice 663 is a client-default fix; touching
`ValidateTreatment` or the handler would be scope creep against the anti-criteria
("does NOT remove control-linkage generally"). The slice checked both the
client-side validation and the server-side enforcement (AC requirement) and
confirmed they are already consistent under the `avoid` default.

Confidence: **high.**

## Revisit once in use

- **Re-check the default once onboarding seeds controls.** If a future
  onboarding flow seeds a starter control bundle on tenant creation, the
  fresh-tenant dead-end no longer exists and the "best first treatment to land
  on" question becomes a pure UX preference. At that point reconsider whether
  `mitigate` (the most common real-world treatment) should return as the default,
  since the dead-end that motivated `avoid` would be gone. Tie this to the
  control-bundle-on-bootstrap work if/when it lands.
- **Watch for operator confusion about `avoid` as the landing treatment.**
  `avoid` ("activity stopped / not entered") is semantically the least common
  real treatment. If usage telemetry shows operators leaving the default
  unchanged and accidentally recording risks as `avoid`, consider (i) a one-line
  inline hint nudging the operator to pick the real treatment, or (ii) a
  no-default "— select treatment —" placeholder that forces an explicit choice.
  Deferred now to avoid adding a required-field gate that could re-introduce a
  different friction.
- **Revisit if the form grows the accept/transfer satellite inputs.** Once the
  form renders `accepted_until` / `accepter` / `instrument_reference` (a future
  richer-editor slice), `accept` becomes a submittable default too, widening the
  default options. Re-evaluate the default then.

## Confidence summary

| Decision                                   | Confidence |
| ------------------------------------------ | ---------- |
| D1 — default to `avoid` (option (a))       | high       |
| D2 — exported `DEFAULT_TREATMENT` constant | high       |
| D3 — no server-side change                 | high       |
