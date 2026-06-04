# Slice 428 — decisions log: ADRs for the four load-bearing canvas invariants

**Slice:** `docs/issues/428-pillar-invariant-adrs.md`
**Type:** JUDGMENT (subjective build-time calls made by the implementer and
recorded here; no human sign-off gate).
**Date:** 2026-06-04

This slice retroactively records four founding architecture invariants as ADRs.
The invariants themselves are unchanged — the canvas remains the authority. The
ADRs are additive trade-off-context records, all status **Accepted /
retrospective**.

---

## Decisions made

### D1 — ADR granularity: four ADRs, with #4 and #5 grouped into one

The spec gave four pillars but two of them (#4 multidimensional scope, #5
FrameworkScope intersection) are one model. I produced **four ADR files**
(0011 RLS, 0012 ledger, 0013 UCF, 0014 scope+frameworkscope) and grouped #4/#5
into ADR-0014.

- **Why grouped:** #5's intersection (`applicability_expr ∩
framework_scope.predicate`) is defined _over_ the multidimensional cell space
  #4 establishes. #5 is meaningless without #4; a reader of one must read the
  other. The spec's AC table itself lists "#4/#5" as a single row and the brief
  explicitly asks whether to group them.
- **Why the other three each stand alone:** #6 (RLS), #2 (ledger), #1 (UCF)
  are independent decisions with independent rejected alternatives; splitting
  them is the diligence-answer shape ("where is the decision record for tenant
  isolation?" must resolve to one file).
- This satisfies P0-428-4 (four ADRs, not fewer than four — there ARE four
  files; the grouping is _within_ the count the spec sanctions via the "#4/#5"
  row, not a collapse below four).

### D2 — ADR numbering: 0011–0014, contiguous, distinct slots

`ls docs/adr/[0-9]*.md` → highest is `0010`. Allocated `0011`, `0012`, `0013`,
`0014` in pillar order (RLS, ledger, UCF, scope). Deliberately did NOT
replicate the historical `0003-` double-occupancy collision (slice note in the
spec). Mapping chosen: 0011=#6, 0012=#2, 0013=#1, 0014=#4/#5 — ordered by the
spec's AC-1 enumeration (RLS, ledger, UCF, scope), not by invariant number, so
the numbering reads in the order the spec lists the deliverables.

### D3 — docs-site nav registration via an index stub, not direct ADR pages

The brief said "register each new ADR in the docs-site nav." The ADR files live
in repo-root `docs/adr/`, which is **outside** the mkdocs `docs_dir`
(`docs-site/docs/`). Adding `../../docs/adr/0011-*.md` to the nav would fail
`mkdocs build --strict` (nav entries must resolve inside `docs_dir`).

The established project pattern (verified: `docs-site/docs/design/logo-decision.md`,
`docs-site/docs/oauth-grants.md`, `docs-site/docs/audit/verify-export.md`) is
that the docs site does NOT republish repo-internal `docs/` content — it links
to it via GitHub blob URLs. I followed that pattern: a new
`docs-site/docs/design/architecture-decision-records.md` index page links to
the four ADRs (and the wider set) via blob URLs, and is registered under the
existing "Design decisions" nav section. This is the only registration that
keeps `mkdocs build --strict` green AND matches the house convention.

This also satisfies AC-10 ("if the project maintains an ADR index/README, list
the four there in order"): the project did NOT previously maintain an ADR
index — this slice creates the first one (the docs-site index page), listing
all four in order.

### D4 — How much trade-off history to reconstruct: honest retrospective framing

The invariants are founding commitments; no contemporaneous design-review
transcript exists. Per P0-428-5, I did NOT fabricate "we deliberated X on date
Y." Each ADR is marked **retrospective** in its Status line and reconstructs
the rejected alternative from the canvas's own anti-pattern list (which already
names application-layer tenancy, mutable evidence, per-framework duplicated
controls, scope-as-tree as explicit rejections — `Plans/canvas/01-vision.md`
anti-patterns + the per-invariant canvas prose). The "Alternatives considered"
sections are labeled "recorded retrospectively" so no reader mistakes them for
a contemporaneous deliberation.

### D5 — Citation-not-restatement for security-load-bearing properties (AC-3, threat model)

For the RLS ADR (security-load-bearing per threat-model I/E), I cite the
canonical role model and deny-on-missing-context mechanism rather than
restating them loosely:

- Cited `docs/architecture/rls.md` (the helper `current_tenant_matches`, the
  `app.current_tenant` GUC, `FORCE ROW LEVEL SECURITY`, "There is no
  default-allow path") and `internal/db/rls_integration_test.go`.
- Stated deny-on-missing-context as a property of the `current_setting(...,
true)` → NULL → false-in-USING mechanism, matching the canonical doc exactly,
  so the ADR cannot drift from the implementation.

Likewise the ledger ADR (threat-model T/R) states append-only +
evaluation-never-writes + point-in-time-replay verbatim against canvas §4.3,
and explicitly rejects the "ledger may be compacted" softening the threat model
warns about.

---

## Grilling outcomes (AC-13 — the load-bearing correctness gate)

Each ADR was grilled against its cited canvas section and code package:

| ADR  | Cited against                                                                    | Outcome                                                                                                                   |
| ---- | -------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------- |
| 0011 | canvas §5.4 + `docs/architecture/rls.md` + `internal/db/rls_integration_test.go` | ACCURATE. Deny-on-missing-context mechanism matches the canonical helper. Role model cited, not restated. No drift found. |
| 0012 | canvas §4.3 + `internal/evidence/` + `internal/eval/`                            | ACCURATE. Append-only + eval-read-only + point-in-time replay match §4.3's three bullets verbatim. No drift found.        |
| 0013 | canvas §3 + `Plans/UCF_GRAPH_MODEL.md` + `internal/ucf/`                         | ACCURATE. STRM edges through SCF anchors, no per-framework duplication, derived framework-to-framework. No drift found.   |
| 0014 | canvas §5.1-5.5 + `internal/scope/` + ADR-0001                                   | ACCURATE. `effective_scope = applicability_expr ∩ framework_scope.predicate` matches §5.5 step 2 verbatim. No drift.      |

**No canvas/code drift surfaced during grilling.** Had drift been found it
would be a recorded finding (and possibly a spillover slice), not a silent
paper-over (per the spec's notes). None was found — the ADRs are faithful
restatements-of-rationale around invariants the canvas already states correctly.

---

## Revisit once in use

- **If the project later moves ADRs into the published docs site** (rather than
  linking out via blob URLs), the D3 index-stub approach should be revisited —
  the ADR markdown would move under `docs-site/docs/` and the nav would point
  at the pages directly. That is a docs-architecture change, not this slice's
  scope.
- **If a fifth pillar invariant is added** (e.g. if invariant #10 audit-period
  freezing or #8 OSCAL-wire-format is judged to warrant its own pillar ADR),
  the index table extends and the next free slot (`0015`) is allocated.
- **If the #4/#5 grouping proves to confuse readers** who expect a 1:1
  invariant-to-ADR map, ADR-0014 can be split into 0014 (#4) + a new 00NN (#5)
  with cross-links — cheap to do later, and the grouping is the more honest
  shape today.

---

## Confidence

**HIGH.** The ADRs document already-shipped, canvas-stated invariants; the
load-bearing facts (deny-on-missing-context mechanism, append-only/replay
properties, STRM-through-SCF, intersection formula) were grilled against the
canonical canvas sections and code packages and matched. The only subjective
calls are granularity (D1), numbering (D2), and the nav-registration mechanism
(D3) — all low-regret and reversible. No constitutional-invariant conflict was
encountered (CLAUDE.md and the canvas agree on all four pillars).

---

## Detection-tier classification

- **detection_tier_actual:** `none` — no bug surfaced during the slice (pure
  retrospective documentation; grilling found no canvas/code drift).
- **detection_tier_target:** `none` — expected for a docs-only slice with no
  runtime surface. (The threat-model guard against _misstating_ a
  security-load-bearing property is `manual_review` via the AC-13 grill, which
  passed.)
