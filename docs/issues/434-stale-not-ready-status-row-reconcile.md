# 434 — One-time audit + reconcile of the stale `not-ready` `_STATUS.md` rows

**Cluster:** Quality
**Estimate:** M
**Type:** JUDGMENT
**Status:** `ready` (no technical dependency — the audit reads `main` + the merge trail)

## Narrative

`docs/issues/_STATUS.md` is the canonical "what's left" tracker — the
continuous-batch loop's GUARD-1 reads it, every triage decision keys off it.
A live tracker that lies corrupts every downstream "what should I pick up?"
call. Roughly two dozen slices still carry a canonical-row status of
`not-ready`: 111-116, 118, 125, 131, 133, 134, 136-139, 142, 143, 145, 175,
177, 228, 230, 232. The set is heterogeneous and stale in two directions:

- **Stale-merged** — several are demonstrably shipped. Slice 131 merged at
  `29ab44d` (SET LOCAL fix), 142 at `ea674f6` (super_admin management
  surface), 143 at `7332695` (create-tenant flow), per the batch history.
  Their rows should read `merged`, not `not-ready`.
- **Genuinely blocked** — others are correctly `not-ready`: 228/230/232 await
  backing endpoints (each cites the missing endpoint in its Dependencies
  section); 118 awaits maintainer StepSecurity enrolment (an external action,
  not a code dep). Those rows should stay `not-ready` with the blocker named.
- **Superseded** — some may be subsumed by a later slice; those flip to
  `superseded` with the superseding slice cited.

This slice specifies a **bounded, per-row audit**: for each of the ~24 rows,
cross-check `main` (does the implementation exist?) and the merge trail (was
it squash-merged, and under what SHA/PR?), then flip the row to exactly one
of `merged` / `superseded` / keep-`not-ready`, each with a one-line reason.
The output is a corrected tracker plus a short audit note recording the
per-row determination and its evidence.

**Convention constraint (slice 382).** Slice 382 established that only
chore/status-batch branches edit `_STATUS.md` (orchestrator-only edits + a CI
lint guard). This slice DOC specifies the audit and the per-row determinations;
the IMPLEMENTATION that actually edits `_STATUS.md` MUST run on a
chore/status-batch branch so it does not trip the 382 guard. The audit work
product (the per-row evidence table) can live in `docs/audit-log/`; the row
flips land via the status-batch mechanism.

**Scope discipline.** This is a ONE-TIME reconcile of the named ~24 rows. It
does NOT reconcile `_INDEX.md` (slice 071 froze that file by design — leave it
alone). It does NOT mass-flip rows without per-row git verification (that
would be the exact inverse of the drift this slice exists to fix). It does NOT
touch rows outside the named set.

## Threat model

STRIDE pass for a tracker-reconcile slice. The tracker is a markdown control
surface for the development loop, not a runtime artifact, so the runtime
STRIDE categories are mostly N/A; the real risks are Tampering (a wrong flip)
and Repudiation (an unevidenced flip).

**S — Spoofing.** N/A. No endpoint, no identity, no auth surface.

**T — Tampering (the primary risk).** A mis-determined row is the integrity
threat: flipping a still-blocked slice to `merged` makes the loop pick work
that depends on an unmerged dependency; flipping a shipped slice the wrong way
hides ready follow-ons. Mitigation: every flip is gated on a per-row git
check — `git log --oneline --all --grep "<NNN>"` plus a presence check of the
slice's actual implementation files on `main` — recorded in the audit table
(AC-2, AC-3). No flip without cited evidence (AC-4). This is the anti-criterion
that prevents the inverse-drift.

**R — Repudiation.** Each flip must be attributable to evidence. Mitigation:
the audit note (`docs/audit-log/434-*`) records, per row, the determination +
the SHA/PR or the named blocker — a durable trail for why each row changed
(AC-3). The git history of the status-batch commit is the secondary record.

**I — Information disclosure.** N/A. `_STATUS.md` is already in-tree; no
tenant-scoped or secret data is read or written.

**D — Denial of service.** N/A. Bounded to ~24 rows; no unbounded scan.

**E — Elevation of privilege.** N/A. No role check; the only "privilege" is
the slice-382 convention that status edits ride a status-batch branch, which
this slice explicitly honors (it does not bypass the guard).

**Verdict:** has-mitigations — CLEAN provided every flip is per-row git-verified
and recorded. The single load-bearing guard is "no flip without cited
evidence" (AC-4), which directly neutralizes the Tampering threat.

## Acceptance criteria

- [ ] **AC-1.** Every row in the named set (111-116, 118, 125, 131, 133, 134,
      136-139, 142, 143, 145, 175, 177, 228, 230, 232) is audited — none
      skipped, none added.
- [ ] **AC-2.** For each row, a per-row determination is recorded: one of
      `merged` / `superseded` / keep-`not-ready`, with the deciding evidence
      (merge SHA + PR for `merged`; superseding slice number for `superseded`;
      named blocker for keep-`not-ready`).
- [ ] **AC-3.** The audit work product is written to
      `docs/audit-log/434-stale-not-ready-status-row-reconcile.md` as a table
      (row · current status · determined status · evidence) — the durable
      repudiation trail.
- [ ] **AC-4.** No row is flipped to `merged` or `superseded` without a cited
      SHA/PR or superseding-slice reference verified against `main` and the
      git log — the anti-inverse-drift guard.
- [ ] **AC-5.** Rows that are genuinely blocked (e.g. 228/230/232 missing
      backing endpoints; 118 awaiting maintainer StepSecurity enrolment) stay
      `not-ready`, with the blocker named in the row notes if not already.
- [ ] **AC-6.** The `_STATUS.md` edits land on a chore/status-batch branch per
      the slice-382 convention (the implementation does not bypass the
      orchestrator-only-edit guard).
- [ ] **AC-7.** `_STATUS.md`'s top-of-file `**Last reconciled:**` marker is
      updated to reflect this reconcile.
- [ ] **AC-8.** `_INDEX.md` is NOT touched (slice 071 froze it).
- [ ] **AC-9.** No row OUTSIDE the named set is modified.

## Constitutional invariants honored

- No architecture invariant is touched — this is a tracker reconcile, no
  schema/auth/tenancy/RLS surface.
- Honors the slice-382 status-edit convention (status edits ride a
  status-batch branch + CI lint guard).
- Honors the slice-071 `_INDEX.md` freeze (explicitly out of scope).
- Style: no emojis; markdown table for the audit work product.

## Canvas references

- None directly. The relevant governance is in CLAUDE.md ("the system of
  record for implementation is `main` plus the merge trail in
  `docs/issues/_STATUS.md`") and in slice 382's status-row-convention spec.

## Dependencies

- **#382** — `merged`. Defines the status-row-edit convention this slice's
  implementation must run within (orchestrator-only edits + CI lint guard).
- No technical code dependency — the audit reads `main` and the git log.

## Anti-criteria (P0 — block merge)

- Does NOT mass-flip rows without per-row git verification (the inverse-drift;
  this is the load-bearing P0 from the threat model).
- Does NOT touch `_INDEX.md` (slice 071 freeze — out of scope).
- Does NOT edit `_STATUS.md` from a feature branch — the row flips ride a
  chore/status-batch branch per slice 382.
- Does NOT modify any row outside the named ~24-row set.
- Does NOT flip a genuinely-blocked row to `merged` to "tidy up" — a named,
  still-live blocker keeps the row `not-ready`.

## Skill mix (3-5)

- `git-worktree-manager` / git — per-row merge-SHA + file-presence verification.
- `tech-debt-tracker` — structure the per-row audit table.
- `simplify` — pre-PR pass.
- `ship-gate` — confirm `_INDEX.md` untouched + only named rows changed.

## Notes for the implementing agent

The per-row check that resolves each determination:

1. `git log --oneline --all --grep "\b<NNN>\b"` — find the merge commit (and
   its PR via the squash subject).
2. Presence check on `main` — does the slice's implementation actually exist?
   (e.g. for 131, the SET-LOCAL fix in the relevant handler; for 142, the
   super_admin management UI under `web/app/.../admin/` + its API handler.)
3. If both confirm a merge → row is `merged`, cite the SHA + PR.
4. If a later slice subsumes it → `superseded`, cite the later slice.
5. If neither → keep `not-ready`, and confirm the blocker is named in the row.

Known-stale-merged seeds from the batch history (verify each, do not flip on
the memo alone): 131 `29ab44d`, 142 `ea674f6`, 143 `7332695`. Known-blocked
seeds: 228/230/232 (backing endpoints), 118 (StepSecurity enrolment). Treat
these as starting hypotheses to confirm against the git log, not as facts to
copy — the whole point of this slice is that the memo and the tracker have
already drifted apart once.
