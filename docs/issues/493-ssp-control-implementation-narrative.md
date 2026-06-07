# 493 — SSP export: real control-implementation narratives (not the synthesized placeholder)

**Cluster:** evidence-pipeline (OSCAL)
**Estimate:** M (1-2d)
**Type:** JUDGMENT (what text constitutes a faithful control-implementation statement is a subjective OSCAL-conformance call)
**Status:** `ready`

## Narrative

Canvas §8.2 specifies the OSCAL **SSP** is generated from "org profile + scope
cells + applicable controls + **control implementation narratives** + linked
policies." The SSP exporter (`internal/oscal/aggregate.go`) wires the org
profile, scope cells, applicable controls, and linked policies — but the
**control-implementation narrative is a synthesized placeholder, not the
authored narrative**. Today (`aggregate.go` ~L216 then ~L229-234) the SSP
`Statement` field is filled twice: first with `c.ApplicabilityExpr` (an explicit
`// placeholder until bundle desc wired` note), then overwritten with a
templated string:

```
"<Title> (control family: <family>). Implementation owned by role <owner_role>."
```

The control bundle's actual human-authored `description` — the narrative that
explains _how the control is implemented_, which is the entire point of an SSP
control-implementation statement to an auditor — is **not** carried. The root
cause is mechanical and documented in `aggregate.go`'s own comments:
`ListActiveControls` returns a projection (`ListActiveControlsRow`) that exposes
`Title` and `ControlFamily` but **not** `Description`, so the exporter cannot
reach the authored text. Slice 030's decisions log (`D-narrative`) records this
as a deliberate stopgap.

The consequence: every SSP security-atlas exports has cookie-cutter
implementation statements. An auditor reading the SSP learns the control's
title, family, and owning role — but **not** how the org actually implements it.
That hollows out the single most-read section of an SSP and undercuts the v1
binary success test ("run the next SOC 2 audit out of security-atlas"): an
auditor who opens the SSP and finds templated boilerplate where the
implementation narrative should be reaches for the org's old SSP doc instead.

This slice **wires the authored control description into the SSP**: extend the
read path (`ListActiveControls` → a projection that includes `description`, or a
companion query) so the exporter fills `ControlImplementation.Statement` with
the bundle's authored narrative, falling back to the synthesized summary **only**
when a control has no authored description (a manual/minimal bundle). The
placeholder double-write is removed.

**Scope discipline.** SSP control-implementation statement only. Does NOT change
the AP/AR/POA&M serializers, does NOT add a rich-text / markdown rendering layer
for narratives (the description is carried verbatim as the OSCAL `statement`
prose), and does NOT add per-statement-component decomposition (OSCAL's
`by-component` structure) — a single implementation statement per control is the
v1 shape, decomposition is a follow-on.

## Threat model (STRIDE)

This is a **read-path completeness** change to an export artifact. The SSP is an
audit-binding artifact, so the integrity + confidentiality of what it carries is
the concern; there is no new ingress.

**S — Spoofing.** N/A — no new authenticated surface; the SSP export is the
existing authenticated, tenant-scoped operation.

**T — Tampering (PRIMARY).** The SSP is signed (slice 400/413/414 cosign bundle
signing). Risk: the authored narrative is the operator's own text — but if the
read path joins the wrong tenant's description, or stale/edited descriptions
leak in, the signed SSP misrepresents the control posture.
**Mitigation:** the description is read under the same `app.current_tenant`
RLS context as the rest of the aggregate (one consistent read at export time);
the SSP continues to be cosign-signed so post-export tampering is detectable;
the export reads a point-in-time consistent snapshot (the control rows + their
descriptions in one transactional read), so an in-flight edit cannot produce a
half-old-half-new SSP.

**R — Repudiation.** "Did the SSP reflect the description as of the export?"
**Mitigation:** the export metadata already records the generation timestamp +
requesting user; the signed bundle binds the content to that moment.

**I — Information disclosure.** The control description is operator-authored and
may contain sensitive implementation detail (specific tooling, network
topology). It already belongs in the SSP by design — but the read MUST stay
tenant-scoped so Tenant A's authored narrative never lands in Tenant B's SSP.
**Mitigation:** the description column is read through the tenant-scoped query
path; an integration test proves Tenant A's control descriptions never surface
in Tenant B's SSP export.

**D — Denial of service.** A very long authored description could bloat the SSP.
**Mitigation:** the description is already a bounded column on the controls
table; the SSP carries it verbatim (no expansion). No new unbounded surface.

**E — Elevation of privilege.** N/A — no new authz; export remains gated to the
existing SSP-export roles.

## Acceptance criteria

- [ ] **AC-1.** The control read path used by the SSP exporter exposes the
      control bundle's authored `description` (extend `ListActiveControls`'s
      projection or add a companion query — sqlc-regenerated).
- [ ] **AC-2.** `ControlImplementation.Statement` in the SSP is filled with the
      authored description when present.
- [ ] **AC-3.** When a control has no authored description (manual/minimal
      bundle), the exporter falls back to a clearly-labeled synthesized summary
      (the existing template) — never an empty statement.
- [ ] **AC-4.** The `ApplicabilityExpr` placeholder write and the double-fill are
      removed; `Statement` is filled exactly once.
- [ ] **AC-5.** The read is tenant-scoped (under `app.current_tenant`) and
      point-in-time consistent with the rest of the aggregate.
- [ ] **AC-6.** Integration test (`//go:build integration`): an SSP exported for a
      tenant with authored control descriptions carries those descriptions
      verbatim as the implementation statements (via the bridge → OSCAL JSON).
- [ ] **AC-7.** Integration test: a control with no description falls back to the
      synthesized summary (AC-3) — no empty statement, no panic.
- [ ] **AC-8.** Tenant-isolation integration test: Tenant A's control
      descriptions never appear in Tenant B's SSP (threat-model I).
- [ ] **AC-9.** The slice-030 decisions-log `D-narrative` entry is updated to
      record that the placeholder is now resolved; a changelog entry for the
      slice.

## Constitutional invariants honored

- **#8 — OSCAL is the wire format.** The SSP now carries the
  control-implementation narratives §8.2 specifies, raising export fidelity.
- **#9 — Manual evidence is first-class.** Manual/minimal controls without an
  authored description still produce a complete (fallback) statement — they are
  not second-class in the SSP.
- **#6 — Tenant isolation via RLS.** The new read stays tenant-scoped; proven by
  AC-8.

## Canvas references

- `Plans/canvas/08-audit-workflow.md` §8.2 — SSP generated from "control
  implementation narratives."
- `Plans/canvas/04-evidence-engine.md` §4.4 — the control bundle's authored
  description is part of control-as-code.
- `docs/audit-log/030-oscal-ssp-poam-export-decisions.md` — the `D-narrative`
  stopgap this slice resolves.

## Dependencies

- **#009** (control bundle format — authored `description`) — `merged`. The
  source of the narrative.
- **#030** (OSCAL SSP/POA&M export) — `merged`. The exporter this slice
  completes.
- **#400 / #413 / #414** (cosign bundle signing) — `merged`. The signed SSP this
  slice fills with real content (signing path unchanged).

## Anti-criteria (P0 — block merge)

- **P0-493-1.** Does NOT emit an empty implementation statement for any control —
  authored description or labeled fallback, never blank (AC-3).
- **P0-493-2.** Does NOT leak one tenant's control descriptions into another's
  SSP (threat-model I, AC-8).
- **P0-493-3.** Does NOT change the AP/AR/POA&M serializers — SSP statement only
  (scope discipline).
- **P0-493-4.** Does NOT leave the `ApplicabilityExpr` placeholder or the
  double-fill in place (AC-4).
- **P0-493-5.** Does NOT alter the cosign signing path — content fidelity only.

## Skill mix (3-5)

`grill-with-docs` · `tdd` (integration-first; the OSCAL-JSON-content assertion +
tenant-isolation test are load-bearing) · `database-designer` (projection /
companion query + sqlc regen) · `security-review` (audit-binding artifact
content + tenant-scoped read) · `simplify`.

## Notes for the implementing agent

- **JUDGMENT calls you own:** the exact fallback-summary wording for
  description-less controls (keep it honestly labeled as auto-generated so an
  auditor isn't misled into thinking it is authored); whether to extend the
  existing `ListActiveControls` projection or add a companion `ListActiveControls
WithDescription` query (the project's sqlc convention favors a purpose-built
  query over widening a shared projection — pattern-match recent slices). Record
  in the decisions log.
- This is a `JUDGMENT` slice because "what text faithfully represents the
  control implementation" is an OSCAL-conformance judgment, not a mechanical AC.
- Detection-tier: an empty-statement regression caught by AC-7 is
  `target=integration, actual=integration`; a tenant-leak caught in review is
  `target=integration, actual=manual_review`.
