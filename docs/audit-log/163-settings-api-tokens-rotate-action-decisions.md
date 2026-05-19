# 163 — Settings API tokens Rotate action — decisions log

Slice 163 wires the Rotate action into the user-facing `/settings`
Personal API Tokens table. The backend rotate path (`POST
/v1/admin/credentials/:id/rotate`) shipped in slice 062 and the BFF
proxy route (`POST /api/admin/credentials/:id/rotate`) shipped in
slice 060. This slice is pure-frontend wiring.

Filed from slice 154's settings-page audit finding F8
(`docs/audit-log/154-settings-page-audit-decisions.md`).

This log records the JUDGMENT-eligible build-time calls Claude made
inline rather than holding the merge on a human sign-off (per the
project's JUDGMENT-slice posture). The product runtime AI-assist
boundary (CLAUDE.md → "AI-assist boundary (hard)") is unaffected:
nothing here publishes an audit-binding artifact, fabricates control
coverage, or auto-approves a mapping. This is purely about how the
slice was built.

---

## D1 — Choose **rotate-now-atomic** over rotate-with-grace-period UI or defer

The task brief gave three options for the rotate-flow shape:

1. **rotate-now-atomic** (default) — clicking Rotate on a row mints
   the successor immediately. The backend's existing grace window
   (slice 062's `rotationGrace`, surfaced in the response as
   `predecessor_expires_at`) is informational only; the UI does not
   add a grace-period picker.
2. **rotate-with-grace-period** — the UI adds a "grace period" picker
   so the operator can override the backend default.
3. **defer-until-tokens-list-API ships** — wait for a redesigned list
   shape before wiring rotate at all.

**Decision: rotate-now-atomic.**

**Why:**

- The backend's `rotationGrace` is a single tenant-wide value
  (currently a constant in `internal/api/credstore/credstore.go`). There
  is no API surface for overriding it per-call, so a UI picker would be
  fiction on top of a fixed backend. Adding the picker would require
  backend work (P0-163-3 explicitly forbids backend changes).
- The slice 062 admin-creds rotate flow at `/admin/api-keys` ships
  rotate-now-atomic and has been in production-shape since slice 062.
  Adopting a different UX for the user-facing `/settings` page would
  give a tenant admin two different rotate experiences for the same
  underlying credentials, surprising at best.
- Defer (option 3) is rejected because there is no pending list-API
  redesign on the roadmap. The existing list shape exposes `last4 +
rotated_from + scope_predicate + allowed_kinds + last_used_at`,
  which is sufficient for both the predecessor-chain rendering and the
  predecessor's last4 in the modal copy.

Typical PAT (Personal Access Token) UX in the broader ecosystem
(GitHub, GitLab, Linear, Stripe dashboards) is rotate-now-atomic. The
grace window is a backend concern; the dashboard surface just shows
the new bearer once and the predecessor's retirement deadline. This
slice follows that precedent.

**Confidence:** HIGH. The other two options have weak motivation
relative to the implementation cost.

**Revisit when:** the backend gains a per-call grace-window override
(no slice on the roadmap), OR the list API gains a redesigned shape
that fundamentally changes how rotated credentials are represented.

---

## D2 — RotateConfirmModal is a sibling of RevokeConfirmModal, not a generalisation

Two options for the confirm-modal:

- (a) Parameterise `RevokeConfirmModal` with a `mode: "revoke" | "rotate"`
  prop and switch copy + button colour internally.
- (b) Write a sibling `RotateConfirmModal` that mirrors the structure
  but ships independent copy.

**Decision: (b) — sibling component.**

**Why:**

- The two flows have different destructive postures. Revoke is a
  one-way kill; rotate mints a successor with a grace window. Stuffing
  both into one component would mean the body paragraph branches on
  `mode`, the button label branches on `mode`, the button colour
  branches on `mode`, and the secondary copy about "predecessor keeps
  working until..." only appears in one mode. The branchy version
  exceeds the simplicity threshold by every measure (line count,
  cyclomatic complexity, reading effort).
- Article VIII (Anti-Abstraction Gate) of the project constitution
  says: trust the framework, use features directly, no unnecessary
  wrapper layers. Two parallel ~30-line components are MORE direct
  than one ~60-line component with a mode switch.
- The two confirm modals share a layout primitive (the
  `role="dialog"` + `<Card>` shell) but at <10 lines of duplication
  the cost of extracting a shared primitive (a third file or a generic
  `ConfirmModal`) exceeds the duplication cost. Re-evaluate if a
  third confirm modal lands in this section.

**Confidence:** HIGH.

**Revisit when:** a third confirm modal (e.g. a hypothetical "expire
on date" action) lands in the API tokens section — at that point the
duplication is structural and worth extracting.

---

## D3 — Wire-shape reconciliation: derive successor-link from `rotated_from`, not `superseded_by`

The slice doc's AC-4 reads:

> AC-4: Predecessor row renders muted "rotated → …{last4}" badge
> when the list response carries `superseded_by` (slice 062 wire
> shape).

The slice 062 wire shape (verified by reading
`internal/api/admincreds/http.go` line 126 and
`web/lib/api.ts` line 603) actually exposes the inverse direction:

```go
type ListItem struct {
    // ...
    RotatedFrom string `json:"rotated_from,omitempty"`
}
```

i.e. the **successor** row carries `rotated_from = <predecessor.id>`,
NOT the predecessor carrying `superseded_by = <successor.id>`.

**Decision:** honor the actual wire shape (`rotated_from` on the
successor) and derive the forward direction on the frontend by
building a `Map<predecessor_id, {id, last4}>` from the list before
the table-row render pass. The map keys on `successor.rotated_from`
and values are the successor's `{id, last4}` tuple. The predecessor's
row looks itself up in the map by its own id to discover its
successor.

**Why not just add `superseded_by` to the wire shape:**

- P0-163-3 of the slice doc explicitly forbids backend changes
  ("NO change to slice 062 backend behavior or wire shape. This is a
  pure-frontend wiring slice.")
- The two directions carry the same information; the derivation is
  O(N) over the list size which is bounded by the tenant's PAT count
  (typically low double digits).
- Changing the wire shape would invalidate the existing /admin/api-keys
  page's behaviour and the slice 062 contract tests. The juice is not
  worth the squeeze for what is fundamentally a frontend rendering
  concern.

**Acceptance criteria satisfied:** the muted "rotated → …last4" badge
renders on predecessor rows, derived from the successor's
`rotated_from` field — which is functionally identical to "renders
when the list response carries `superseded_by`" since both express
the same predecessor-to-successor link.

**Confidence:** HIGH. The slice doc mistake is benign; the wire shape
is the source of truth.

**Revisit when:** the slice doc is amended OR the wire shape adds
`superseded_by` (would simplify the frontend by ~5 lines).

---

## D4 — `FreshTokenCallout` widened via discriminated-union prop, not duplicated

Two options for the callout:

- (a) Duplicate `FreshTokenCallout` into a sibling
  `RotatedTokenCallout` with rotate-flavour copy.
- (b) Widen `FreshTokenCallout`'s prop to a discriminated union
  (`variant: "issued" | "rotated"`) so one component renders both
  flavours.

**Decision: (b) — discriminated-union prop.**

**Why:**

- The callout's CHROME (alert variant, code block with bearer, Copy
  button, Dismiss button, data-testid root) is identical between the
  two flavours. Duplicating that chrome doubles the surface area
  someone has to keep in sync (and the plaintext-once invariant has to
  hold across BOTH paths — see P0-163-1).
- The variant-specific bits are the title, the helper paragraph, and
  the metadata footer. Those amount to ~10 lines of branchy JSX,
  comfortably below the threshold where the branching becomes a
  smell.
- TypeScript's discriminated-union prop type ensures the callsite
  cannot pass `predecessor_last4` when `variant === "issued"` and
  cannot omit it when `variant === "rotated"`. The type system enforces
  the call-site contract.
- This is the same pattern used by the existing /admin/api-keys
  `FreshSecretCallout` (a `value: FreshSecret` discriminated union),
  so the slice 062/063 precedent is preserved.

The plaintext-once invariant is preserved because the bearer flows
through state ONCE, into the callout's `props.bearer`, and into the
`<code>` JSX node. There is no `localStorage` write, no DOM
duplication, no copy-paste of the bearer string. The instant the user
dispatches DISMISS, the callout unmounts and `props.bearer` is GC'd.

**Confidence:** HIGH.

**Revisit when:** a third callout variant lands (unlikely — issue and
rotate are the only operations that return a new bearer).

---

## D5 — e2e spec body stays commented per slice 082 quarantine pattern

The slice 163 AC-6 calls for a Playwright e2e asserting
rotate-twice-in-a-row produces a fresh secret each time and chains
the predecessor rows correctly.

Two options:

- (a) Write the spec body live (uncommented) — but this would fail in
  CI because the seed-data harness (slice 164) is not on main yet, and
  the rotate flow needs at least one seeded token row to exercise.
- (b) Write the spec body inside a commented block — the established
  pattern from slice 082 / slice 154 AC-7/8/9/10. The test contract is
  reviewable in the diff; the assertions become live once slice 164
  (or a future seed-harness slice) un-quarantines.

**Decision: (b) — commented body.**

**Why:**

- The slice 082 / 154 precedent is established and the task brief
  explicitly invokes it ("commented body, per slice 082 quarantine
  pattern + slice 154 AC-9 precedent").
- AC-9 of slice 154 (API tokens section renders empty-state or row
  table) already ships as a commented body in the same file. Mixing
  a live-uncommented AC-11 next to a commented AC-9 would be
  inconsistent.
- The reviewer can read the assertions in the diff, confirm the
  contract matches the slice doc's AC-6, and re-enable in a future
  slice when seed-harness coverage exists.

The commented body covers:

- Rotate the first row → callout shows rotate-flavour copy, bearer
  captured, DISMISS, bearer absent from DOM.
- Rotate the successor → fresh bearer (distinct from the first), DISMISS,
  fresh bearer absent from DOM.
- Chain assertion: TWO predecessor rows now carry the rotated-to link.
- Reload assertion: NEITHER bearer appears anywhere.

**Confidence:** HIGH. The pattern is established and the contract
is preserved.

**Revisit when:** slice 164 lands (un-comments AC-9) AND a future
slice tackles AC-11 un-comment using the same seed harness.

---

## Summary table

| ID  | Decision                                                       | Confidence | Reversible? |
| --- | -------------------------------------------------------------- | ---------- | ----------- |
| D1  | rotate-now-atomic, no grace-period UI                          | HIGH       | YES         |
| D2  | RotateConfirmModal as sibling of RevokeConfirmModal            | HIGH       | YES         |
| D3  | Derive successor-link from `rotated_from`, not `superseded_by` | HIGH       | YES         |
| D4  | FreshTokenCallout widened via discriminated union              | HIGH       | YES         |
| D5  | e2e spec body stays commented per slice 082 pattern            | HIGH       | YES         |

All five decisions are reversible (no migrations, no wire-shape
changes, no contracts shipped to external callers). The slice's
deliverable is purely cosmetic-plus-reducer-state at the
JavaScript/TypeScript layer.

---

## Anti-criteria honored

- **P0-163-1** Plaintext-once invariant for ROTATED bearer matches
  ISSUED: the reducer's `ROTATED` case returns a fresh
  `kind: "rotated"` state with only the new bearer; DISMISS reduces
  back to `kind: "none"`. The reducer test
  `app/(authed)/settings/token-state.test.ts` asserts this with
  `JSON.stringify(state).not.toContain(previous_bearer)`.
- **P0-163-2** NO new BFF route created. The existing
  `/api/admin/credentials/[id]/rotate` route (slice 060) is reused
  verbatim. The new `rotateCred` wrapper in `page.tsx` calls it.
- **P0-163-3** NO change to slice 062 backend behavior or wire shape.
  Pure-frontend wiring; no Go files touched; no migrations.
- **P0-163-4** NO vendor-prefixed test fixture tokens. All test
  bearers in `token-state.test.ts` use the `test-` prefix and
  descriptive suffixes (`test-rotated-successor-bearer`,
  `test-issued-bearer-before-rotate`, etc.). No `sk-`, `xoxb-`, `ghp_`,
  `pat-`, etc.

Product runtime AI-assist boundary: UNCHANGED. This slice neither
publishes an audit-binding artifact, fabricates control coverage,
auto-approves a mapping, nor uses Tenant A data to seed Tenant B
drafts. The slice 062 / 154 / 163 chain is purely about how
credentials are managed in the settings UI — orthogonal to the AI
boundary.
