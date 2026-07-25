# Slice 679 — Vendor UX/data decisions log

Type: JUDGMENT. Three vendor-surface defects (ATLAS-030 / 031 / 032),
re-verified on `main` build `2a3805b` in the demo tenant.

- detection_tier_actual: manual_review
- detection_tier_target: manual_review

(No bug surfaced from a missing test tier during the build. The three
defects were caught by the 2026-06-10 demo-tenant manual audit, which is
the correct tier for "the seeded data reads wrong" / "the copy promises a
control that does not exist" / "two values render concatenated" — these
are visual/data-shape findings a manual walk-through surfaces. The slice
adds the regression tests so the next occurrence is caught earlier:
email-shape at `unit` (vitest + Go), delete-confirm + row-separation at
`playwright`, cross-tenant delete at `integration`.)

## Decisions made

### D1 — Owner field: keep as email, add validation (ATLAS-032). Confidence: high

**Options considered:**

- (a) Keep "Owner (email)", add client-side email validation, fix the
  seed to a valid `@demo.example` address.
- (b) Relabel the field to "Owner" / "Owner (role)", drop email
  validation, keep the role string in the seed.

**Chosen: (a).** Three existing surfaces already treat this field as an
email and would have to be unwound under (b):

1. The form label literally reads "Owner (email)" and the placeholder is
   `alice@example.com`.
2. The slice-139 vendor export runs `OwnerUser` through `MaskEmail`,
   masking it to `*@domain.tld` — that masking is meaningless for a role
   string and is the whole point for an email.
3. The store's `OwnerUser` is a free-form identifier the canvas (export
   threat model) documents as "in v1, usually a user email".

Relabelling would fight all three and silently change the export's
masking contract. Validating-as-email is the lowest-friction call that
makes the field, the seed, the placeholder, and the export agree.

Validation is **present-but-malformed only** — an empty owner stays
allowed (the field was never `required`; forcing it would be a scope
creep beyond "validate as email"). The seed now stamps
`fictionalUserEmail(i)` (the existing helper that yields
`firstname@demo.example`), replacing the `ownerRoles[...]` role string.

### D2 — Delete affordance: ADD the control with a confirm (ATLAS-031). Confidence: high

**Options considered:**

- (a) ADD the Delete vendor control the edit-page copy already promises,
  with a confirm dialog.
- (b) REMOVE the copy ("Delete removes the row and its cell bindings")
  and ship no control.

**Chosen: (a)** — the slice's default lean, and the stronger call here
because the **entire backend already exists**: `Store.Delete` (RLS-scoped
via `inTx` → `ApplyTenant`, CASCADE on `vendor_scope_cells`), the
`DELETE /v1/vendors/{id}` handler, the route mount in `httpserver.go`,
and integration coverage (`TestDeleteVendor_Removes` /
`_IdempotentOnMissingRow`, slice 287). The only missing pieces were the
BFF DELETE forward, the client action wrapper, and the UI control. The
copy was describing a capability that was wired end-to-end except for the
last mile — removing the copy would have thrown away a working feature.

The control is a controlled `Dialog` (the established slice-097 primitive,
same pattern as `admin/demo/demo-controls.tsx`), NOT a bare
`window.confirm`. Anti-criterion honored: the DELETE fires only from the
dialog's confirm button (`handleConfirm`), never from the trigger; the
e2e spec asserts `deleteCalled === false` after the dialog opens and
`true` only after confirm. Cell-binding cleanup is the platform's CASCADE
(no orphans).

### D3 — Row name/domain separation: separate block-level lines (ATLAS-030). Confidence: high

The pre-existing render placed the domain in an inline `ml-2` span after
the name link. The audit still read it as concatenated
("Pinecone Bankpineconebank.example") — an inline left-margin is too easy
to lose at narrow widths / against the name's font weight. Chose a
`flex flex-col` stack: name as primary text, domain as secondary
`text-muted-foreground` on its own line, each with a stable `data-testid`
(`vendor-name` / `vendor-domain`). The Playwright spec asserts each value
renders as exact, un-concatenated text in a distinct element.

### D4 — AC-3 (read-only vendor detail / review-history): deferred to a follow-on slice. Confidence: medium

AC-3 asks to "consider a read-only vendor summary / review history on the
detail page". This is materially larger than the three defects this slice
clusters — it is a new view (the detail page is currently the edit form
itself), with its own data shape (review history is not yet a wire
surface). Per the continuous-batch spillover policy it is captured as a
follow-on slice rather than stretched into this one. The slice still
satisfies AC-3's floor ("at minimum the edit page should not be the only
view") only partially — the edit page remains the only view in this
slice; the spillover owns the read-only view. Recorded explicitly so the
maintainer can prioritize it.

## Revisit once in use

- **D1 empty-owner policy.** Re-check whether an empty owner should stay
  allowed once vendor ownership feeds anything downstream (e.g. review
  reminders routed to the owner). If ownership becomes load-bearing, the
  field likely becomes `required` + validated, not merely
  validated-when-present.
- **D1 validation strictness.** `isEmail` is a deliberately conservative
  single-regex predicate, not an RFC 5322 parser. If operators hit a
  legitimate address it rejects (quoted local-parts, IDN domains),
  loosen the regex — but only with a failing test that names the address.
- **D2 confirm UX.** The confirm dialog is text-acknowledgement only (no
  "type the vendor name to confirm"). If a real operator deletes the
  wrong vendor in anger, consider a name-typed confirm or a soft-delete /
  undo window. Hard delete + CASCADE is the v1 shape; there is no undo.
- **D4 spillover.** The read-only vendor detail + review-history view is
  the iteration backlog item — promote when vendor review history becomes
  a real wire surface.

## Confidence summary

| Decision                               | Confidence |
| -------------------------------------- | ---------- |
| D1 owner-email validation + seed fix   | high       |
| D2 add Delete control with confirm     | high       |
| D3 name/domain separation              | high       |
| D4 defer read-only detail to spillover | medium     |
