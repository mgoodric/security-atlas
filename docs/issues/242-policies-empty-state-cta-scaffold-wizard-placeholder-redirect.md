# 242 — Policies empty-state: "Scaffold five foundational policies" CTA redirects to unrelated admin page

**Cluster:** policies (UI honesty)
**Estimate:** 0.5d
**Type:** AFK
**Status:** `merged` (status reconciled 2026-06-03 — backlog drained per \_STATUS.md SoR; loop terminated batch 184)
**Parent:** #204 (UI parity audit fleet — `/policies` page)

## Narrative

Surfaced by the slice 204 audit of `/policies` against
`Plans/mockups/policies.html` (see
`docs/audit-log/204-page-audit-policies.md`).

When the operator visits `/policies` on a fresh deployment with
zero rows (verified at
`https://atlas-edge.home.gmoney.sh/v1/policies` →
`{"count":0,"policies":[]}`), the empty-state card renders the
mockup's primary CTA: **"Scaffold five foundational policies"**
(`Plans/mockups/policies.html` lines 320–327;
`web/app/(authed)/policies/page.tsx` lines 374–417).

The CTA reads as a promise: clicking it will scaffold the five
SOC 2 foundational policies (Information Security, Acceptable
Use, Access Control, Incident Response, Change Management) with
the operator's org name pre-filled.

It does **not** do that. The implementing slice (101) explicitly
acknowledges via its `P0-A4` anti-criterion that no scaffold
wizard exists, and the CTA's `onClick` handler points at
`/admin/credentials` — an unrelated admin surface:

```ts
// web/app/(authed)/policies/page.tsx, line 410
onClick: () => router.push("/admin/credentials"),
```

This is the slice-178 honesty-gap class: the button text claims
capability X, the runtime behavior delivers unrelated destination
Y. The audit's intent is to surface this as a real spillover so
the maintainer can decide between (a) shipping the actual
scaffold wizard, or (b) softening the CTA copy to match what the
button actually does.

The audit does **not** prescribe which path; that's the
maintainer's call.

## Threat model

**Verdict.** **no-mitigations-needed.** UI-only honesty fix.

## Acceptance criteria

- **AC-1.** The `/policies` empty-state CTA EITHER:
  - **(a)** Ships an actual scaffold-wizard route (suggested:
    `/policies/scaffold` — new App Router segment) that, given
    the operator's org name, inserts the five foundational
    policy rows via `POST /v1/policies` (each as `status:
draft`, version `v0.1-draft`, with templated body_md
    citing the SOC 2 trust-service criteria the policy
    addresses), OR
  - **(b)** Softens the CTA copy + destination so the CTA
    accurately describes what it does. Example soft copy:
    "Browse policy templates" → `/docs/policy-templates`, OR
    remove the CTA and leave the empty-state body text as the
    informational message.
- **AC-2.** If path (a) is taken: the wizard requires a one-click
  confirmation per the canvas's anti-pattern about "policy
  template libraries dressed as a feature" — the operator
  approves the five drafts BEFORE they land in the policy table.
  No row inserts without the operator's explicit click-through.
- **AC-3.** If path (a) is taken: the five templates are the
  high-signal five (Information Security, Acceptable Use, Access
  Control, Incident Response, Change Management), NOT a
  50-template placeholder library (anti-pattern §1.6).
- **AC-4.** If path (b) is taken: the slice's primary deliverable
  is updating the CTA copy + destination + the implementing-
  slice-101 `P0-A4` reference so future slices don't carry the
  forward-looking-UI claim forward.
- **AC-5.** Decisions log entry at
  `docs/audit-log/242-policies-empty-state-cta-decisions.md`:
  (D1) path-(a)-vs-(b) chosen + rationale,
  (D2) if (a): the template-content authorship (Claude
  drafts using the SOC 2 TSC + canvas §4.5 as input,
  records the decision per `JUDGMENT` slice convention),
  (D3) if (b): the soft-copy + new destination,
  (D4) the slice 101 `P0-A4` reference update path.
- **AC-6.** Type updates if path (a): the slice is `Type:
JUDGMENT` (template authorship is a subjective call). If path
  (b): `Type: AFK`.
- **AC-7.** Unit test or Playwright spec asserts: empty-state
  CTA destination matches the chosen path. Playwright spec
  preferred since the empty-state branch is straightforward
  to seed (no rows in dev-seed for the policy table → empty
  state renders).
- **AC-8.** Pre-commit clean, DCO sign-off, Co-Authored-By trailer.

## Constitutional invariants honored

- **Anti-pattern explicitly rejected.** "Vanity trust centers" /
  "policy template libraries dressed as a feature" — both
  apply. Path (a) ships ONLY the five high-signal templates;
  path (b) backs off the claim. Either honors the anti-pattern.
- **AI-assist boundary.** If path (a): the templates are
  human-approved per-policy at draft → published transition (the
  scaffold inserts them as `draft`; the operator publishes after
  review). No AI-generated text reaches production state without
  the operator's explicit publish click.
- **Invariant 9 (manual evidence is first-class).** Policy bodies
  remain manual; the scaffold inserts drafts, the operator owns
  the publish.

## Canvas references

- `Plans/canvas/01-vision.md` §1.6 — UI-honesty anti-pattern +
  "policy template libraries dressed as a feature"
- `Plans/canvas/04-evidence-engine.md` §4.5 — policy lifecycle
- `docs/audit-log/178-ui-honesty-first-pass.md` — same honesty-gap
  class
- `Plans/mockups/policies.html` lines 320–327 — empty-state CTA
  shape

## Dependencies

- **#204** (audit parent) — `in-progress`.
- **#101** (policies list view with the lying CTA) — merged.
- **#022** (policy primitive + `POST /v1/policies`) — merged. If
  path (a) is chosen, the scaffold wizard uses this endpoint.

## Anti-criteria (P0 — block merge)

- **P0-242-1.** Does NOT ship a 50-template policy library if
  path (a) is chosen — five high-signal templates only, per
  the anti-pattern.
- **P0-242-2.** Does NOT insert scaffold rows without the
  operator's explicit click-through approval (AC-2).
- **P0-242-3.** Does NOT silently publish drafts. Scaffold
  inserts at `draft`; operator publishes per policy.
- **P0-242-4.** Does NOT redirect the CTA to yet another
  unrelated admin page. The fix is either ship the destination
  or update the copy — not move the lie.
- **P0-242-5.** Does NOT use vendor-prefixed test fixture tokens.

## Skill mix

1. shadcn/ui — CTA copy + button shape stays consistent.
2. Next.js App Router — new `/policies/scaffold` segment if
   path (a).
3. `JUDGMENT` slice discipline — template authorship per slice
   convention with decisions log.
4. SOC 2 TSC knowledge — the five high-signal policy templates'
   content alignment with CC1.4 / CC6.1 / CC7.4 / CC8.1 / CC9.1.
5. Slice 178 honesty-gap classification — reused vocabulary.
