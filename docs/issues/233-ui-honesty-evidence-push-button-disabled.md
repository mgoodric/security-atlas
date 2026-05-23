# 233 — UI honesty: /evidence "Push evidence" CTA is disabled with no affordance

**Cluster:** Quality / UI hygiene
**Estimate:** 0.5d (option A — replace with linked instructions) · 2.0d (option B — ship inline push UI)
**Type:** AFK
**Status:** `ready`
**Parent:** #204 (UI parity audit fleet)

## Narrative

Surfaced during the slice 204 per-page audit of `/evidence`
(audit log: `docs/audit-log/204-page-audit-evidence.md`).

The mockup at `Plans/mockups/evidence.html` (lines 117-121) renders
the page-level primary action as a live `<button>` styled
`bg-brand-600 ... hover:bg-brand-700` with an upload-arrow icon and
the label "Push evidence". The mockup implies that clicking this
button opens an inline push affordance — a JSONL paste / file-upload
modal, or an inline credential-issuance flow.

The live page at `https://atlas-edge.home.gmoney.sh/evidence` ships
the same label but the button has the `disabled` attribute set
permanently. Source: `web/app/(authed)/evidence/page.tsx` lines
333-335:

```tsx
<Button size="sm" disabled>
  Push evidence
</Button>
```

There is no hover text, no tooltip, no link to the CLI documentation,
no link to `/admin/credentials`, no inline disclosure of "Push happens
via CLI / SDK — see docs". A solo security leader landing on this
page expecting to push their first evidence record will hover the
CTA, see it disabled, get no feedback, and abandon the path.

This is the slice 178 HONESTY-GAP class: a button that is permanently
dead with no signposting. Two paths:

- **Option A (0.5d).** Replace the disabled button with a real
  primary-styled link pointing to the relevant doc page (e.g.
  `/admin/credentials` or the CLI quickstart). Label becomes
  "Push evidence →" and the click navigates somewhere useful. Add a
  one-line subtitle under the page H1 directing operators to the
  CLI/SDK path. This is the slice 178 first-pass-style cheap close.

- **Option B (2.0d).** Ship a minimal in-page Push dialog: a
  `<Dialog>` with a paste-JSONL textarea + a manual evidence
  upload form that POSTs to `/v1/evidence:push` via the BFF.
  Heavier; reuses the slice 003 push wire shape end-to-end.

The maintainer picks A or B at start. Defaulting to A in the AC
shape below.

## Threat model

**Verdict.** **no-mitigations-needed.** Option A is chrome only.
Option B reuses the existing `/v1/evidence:push` endpoint whose
threat model was settled in slice 003.

## Acceptance criteria (Option A — chosen path)

- **AC-1.** The `<Button size="sm" disabled>` in
  `web/app/(authed)/evidence/page.tsx` is replaced with a primary-
  styled `<a>` linking to `/admin/credentials` (or the CLI
  quickstart anchor in `/docs/cli`, whichever the maintainer picks
  in step 0). Label becomes `Push evidence →`.
- **AC-2.** The page subtitle (`Append-only · ingestion separated
from evaluation · point-in-time replay always possible`) gains a
  follow-up sentence: `Push via CLI or SDK — see Push evidence →`
  with the second clause linked to the same destination.
- **AC-3.** Playwright spec for `/evidence` updated: any assertion
  that the Push button is `disabled` is removed; a new assertion
  confirms the link's `href` is the chosen destination and is
  navigable.
- **AC-4.** Slice 204 audit's HONESTY-GAP finding F-204-E-1 is
  resolved on the next audit run.

## Constitutional invariants honored

- **Invariant 3 (Evidence SDK exposes one canonical inbound API).**
  This slice does not change the wire — it just signposts the
  existing pathway honestly.
- **Anti-pattern rejected:** Permanently-disabled CTAs without any
  textual cue about why they're disabled or what to do instead.

## Canvas references

- `Plans/canvas/04-evidence-engine.md` — Evidence SDK push profile
- `Plans/EVIDENCE_SDK.md` — full push contract
- `Plans/mockups/evidence.html` lines 117-121 — the mockup CTA

## Dependencies

- **#204** (UI parity audit fleet) — `in-progress`. Surfacing
  parent.
- **#003** (Evidence SDK) — `merged`. The wire the link signposts.
- **#152** (admin API-keys management surface) — `merged`. The
  destination Option A's link points to, if the maintainer picks
  `/admin/credentials` as the target.

## Anti-criteria (P0 — block merge)

- **P0-233-1.** Does NOT ship a full inline Push dialog in Option
  A. That's Option B, a separate path.
- **P0-233-2.** Does NOT modify the `/v1/evidence:push` wire
  contract or any backend handler.
- **P0-233-3.** Does NOT touch the slice 204 audit harness.

## Skill mix (3-5)

1. Next.js App Router — primary CTA replacement
2. Playwright spec update — keeping the slice-069 functional flow
   green
3. shadcn/ui Button / link variants
