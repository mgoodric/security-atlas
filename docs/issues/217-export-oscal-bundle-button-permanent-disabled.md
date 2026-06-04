# 217 — "Export OSCAL bundle" button permanently disabled on /audits (action affordance with no working surface)

**Cluster:** frontend
**Estimate:** 0.25d (label-honesty path) · 1.5d (wire-up path)
**Type:** JUDGMENT
**Status:** `merged` (status reconciled 2026-06-03 — backlog drained per \_STATUS.md SoR; loop terminated batch 184)

## Narrative

Surfaced during slice 204 audit fleet (audits page), captured as
follow-up per continuous-batch policy.

The mockup at `Plans/mockups/audits.html` renders an "Export OSCAL
bundle" button in the page's primary action area (line 116):

```html
<button class="...border border-slate-200 rounded-md hover:bg-slate-50">
  Export OSCAL bundle
</button>
```

The live `/audits` page renders the same button as PERMANENTLY
disabled (`web/app/(authed)/audits/page.tsx` line 357):

```tsx
<Button variant="outline" size="sm" disabled>
  Export OSCAL bundle
</Button>
```

No conditional logic, no tooltip explaining why, no future-tense
copy. Just a greyed-out button next to three working CSV/JSON/XLSX
export buttons (slice 139, audit-periods data export) and three
working samples export buttons (slice 138). For the operator this
is confusing UI surface: three exports work, two more pretend to
exist (CSV/JSON/XLSX for samples — those DO work), and the button
the mockup positioned as PRIMARY is dead.

Slice-178 honesty audit categorizes a permanently disabled button
with no contextual explanation as a HONESTY-GAP (see slice 183 /
184 / 185 / 186 for analogous closures).

Two resolution paths:

- Path A (label-honesty, 0.25d). Replace "Export OSCAL bundle" with
  a future-tense disclosure: render as a non-button informational
  text "OSCAL bundle export coming in slice <NNN>" OR a Popover
  trigger that explains "Per-period OSCAL SSP/AP/AR/POA&M export
  ships once the per-period detail view lands (see slice 184)."
  This closes the HONESTY-GAP without committing implementation
  time.
- Path B (wire-up, 1.5d). Wire the button to a working list-level
  export that produces a multi-period OSCAL bundle (one component-
  definition per frozen period). Demands the OSCAL bridge service
  is online (Python `compliance-trestle`); demands a cosign-signing
  step for the audit-binding artifact. Bigger lift.

Default recommendation is path A — the user-promised behavior of
"OSCAL bundle" is per-period, not list-level; the list-page button
was a mockup-stage layout choice that doesn't survive contact with
the per-period detail design. The per-period detail page (#184
follow-on) is the right home for the export.

## Threat model

**Verdict.** **mitigations-required (path B only).** OSCAL export
produces an audit-binding artifact. Per CLAUDE.md "AI-assist
boundary," any audit-binding artifact requires one-click human
approval — path B must route through that gate. Path A is doc-only
and clean.

## Acceptance criteria (path A — chosen recommendation)

- **AC-A1.** The `Export OSCAL bundle` button is replaced with a
  Popover or non-button text affordance disclosing future-state:
  "Per-period OSCAL export ships with the per-period detail view."
- **AC-A2.** A `data-testid="audits-oscal-export-future"` token
  exists so slice-178's UI-honesty harness can confirm the
  disclosure is the affordance, not a dead button.
- **AC-A3.** Mockup `Plans/mockups/audits.html` line 116 updates
  to match (button → text or Popover trigger).
- **AC-A4.** Playwright e2e spec asserts the disclosure visible
  text contains "per-period" and that no disabled button with the
  text "Export OSCAL bundle" exists on the page.

## Acceptance criteria (path B — wire up)

- **AC-B1.** Button posts `POST /v1/audit-periods:export` (new
  endpoint) with a `period_ids[]` request body covering all frozen
  periods on the current page.
- **AC-B2.** Server emits a multi-component OSCAL bundle via the
  oscal-bridge Python service.
- **AC-B3.** Response is a signed (cosign) tar.gz bundle with the
  `application/oscal+tar+gzip` content type.
- **AC-B4.** Per the AI-assist boundary, the actual bundle
  download requires a follow-up "Approve & download" click after
  preview — single-click bulk export of audit-binding artifacts
  is prohibited.
- **AC-B5.** Integration test seeds three frozen periods, exports
  them, asserts the bundle contains all three component
  definitions.

## Constitutional invariants honored

- **Invariant 8 (OSCAL is the wire format).** Path B emits OSCAL,
  not a daily-model dump.
- **AI-assist boundary.** Path B's two-click approval gate is the
  audit-binding-artifact mandate.
- **Anti-pattern rejected (vanity buttons).** A permanently-
  disabled action button with no rationale is a documented anti-
  pattern of GRC tools we set out to replace.

## Canvas references

- `Plans/mockups/audits.html` line 116 — the mockup affordance
- `Plans/canvas/08-audit-workflow.md` — OSCAL export pipeline
- `Plans/canvas/04-evidence-engine.md` §4.6.5 — AI-assist boundary

## Dependencies

- **#204** — UI parity audit (surfacing parent)
- **#102** — audits page (the consumer surface)
- **#030** — OSCAL bundle export (the existing per-period export
  primitive that path B would invoke at bulk)
- **#184** — per-period detail view (alternative home for the
  per-period export; supports path A's "moved to detail page"
  framing)

## Anti-criteria (P0 — block merge)

- **P0-217-1.** Does NOT ship a permanently disabled button. The
  current state is what this slice closes.
- **P0-217-2.** Does NOT bypass the AI-assist boundary on path B.
  Two-click approval is mandatory for audit-binding artifacts.
- **P0-217-3.** Does NOT add an undocumented disclosure on path A
  — the future-state copy names the specific slice (or "future
  slice") so the operator can track.
- **P0-217-4.** Does NOT change the existing slice-139 audit-
  periods data export (CSV / JSON / XLSX) — that surface is
  working and stays as-is.

## Skill mix (3-5)

1. shadcn-ui Popover or text affordance composition (path A)
2. Playwright e2e assertion against the slice-178 honesty harness
3. (path B) OSCAL bridge gRPC client + cosign signing
4. (path B) Two-stage approval UX
5. JUDGMENT decision documented in the slice's decisions log
