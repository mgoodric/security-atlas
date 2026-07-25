# Slice 567 — HIPAA finer-anchor re-point: JUDGMENT decisions log (pre-work + split)

**Slice:** 567 — re-point the 21 palette-bound HIPAA Security Rule crosswalk rows
(slice 516's residual table) at the finer SCF anchors that exist only in the
operator's full catalog.
**Type:** JUDGMENT (crosswalk-mapping accuracy is a subjective control call).
**Parent dependency:** slice 516 (full HIPAA coverage) — merged.
**Outcome of this pass:** the test-path half is **split into slice 754** and this
slice is **blocked behind it**. No crosswalk YAML changed. The per-row control
judgement is done here as pre-work so the follow-on applies it rather than
re-deriving it.
**Author:** agent (JUDGMENT slice convention — the agent makes the build-time
mapping calls and records them here for post-deployment maintainer review).

- detection_tier_actual: none
- detection_tier_target: none

> No bug surfaced during this pass. The pass was scoping + control judgement; the
> crosswalk data and the loader are untouched, so there was nothing to catch at
> any tier. The one thing worth naming for the aggregate signal: the blocker was
> found by **manual_review** of the seeding path (`scfseed.EnsureSCFCatalog` +
> the shared `-p 1` suite), and that is the correct tier for an architectural
> dependency — no test would have surfaced it, because the current tests pass.

---

## D1 — The blocker is not a missing test, it is a missing catalog decision (the split)

Slice 567's Status said the work depends on "a test path that seeds the operator's
FULL SCF catalog, not the 53-anchor sample fixture." Scoping that path changed the
diagnosis: **the dependency is not a test-harness gap, it is a catalog-content
governance decision.**

The chain, verified in the tree rather than assumed:

1. `internal/api/soc2import` imports every bundled crosswalk against ONE seeded
   catalog. `scfseed.EnsureSCFCatalog` hardcodes
   `migrations/fixtures/scf-sample.json` (`scfseed.go` resolves the path via
   `runtime.Caller`); there is no per-crosswalk catalog selection.
2. The importer rolls the whole transaction back on an unresolvable anchor —
   `TestHIPAAImport_RejectsEdgeToNonexistentAnchor` is the standing proof.
3. Therefore a row re-pointed at a full-catalog-only anchor does not fail one new
   assertion; it fails the **entire** `soc2import` suite for all five frameworks,
   because the shared seed is the sample fixture.

So "add a second, opt-in full-catalog test path alongside the sample" does not
unblock the re-point. Whatever catalog the shared suite seeds must carry the finer
anchors. That is a decision about what the project bundles, and it collides with
two standing positions the project has deliberately taken:

- **SCF redistribution is under legal review.** CLAUDE.md lists "SCF redistribution
  terms (legal review)" as _decide before bundling the SCF catalog in releases_.
  Bundling a real SCF release JSON as a test fixture is that decision.
- **Catalog expansion was explicitly deferred.** Slice 654's decisions log records
  catalog expansion as "a separate governance call, deliberately not done."
  Growing the shared sample palette to serve HIPAA reverses that posture.

Both are Matt's calls, not an agent's. Slice 567's own "If blocked" clause names
this outcome as the expected one: file the test path as its own slice, block 567
behind it, stop. Done — slice 754 (`docs/issues/754-full-scf-catalog-test-path.md`)
carries the three candidate catalog sources and the acceptance criteria; 567's
Status now points at it.

**Confidence:** HIGH on the mechanism (read from `scfseed.go`, `import.go`, and
the existing rollback test, not inferred). HIGH that the decision is above the
agent's line.

## D2 — Why the four tempting shortcuts were rejected

Recorded so the follow-on does not re-litigate them.

| Shortcut                                                                   | Rejected because                                                                                                                                                                                                                                                                                                                                           |
| -------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Author a ~1,400-control full-catalog fixture in-repo                       | Slice 641 already recorded that SCF's authoritative per-control prose ships in the downloadable workbook and is not retrievable in-repo or from public pages. Writing 1,400 SCF identifiers + titles from memory would fabricate an audit-facing catalog. The crosswalk is an audit-facing claim; a fabricated anchor palette poisons it at the root.      |
| Env-var-gated full-catalog test (`t.Skip` when unset)                      | Skipped in CI, so slice 567's AC-1 ("exists **and runs**") is unmet — and it does not unblock anything, because the shared suite still seeds the sample fixture (D1).                                                                                                                                                                                      |
| Point the re-pointed rows at anchors and let the sample-fixture suite fail | Violates 567's own P0 ("Does NOT reference an anchor the seeded catalog lacks") and breaks four unrelated frameworks' suites.                                                                                                                                                                                                                              |
| Quietly grow `scf-sample.json` with the ~13 anchors HIPAA needs            | Mechanically smallest and it has precedent (slices 635/641 seeded the nine THR-domain anchors for this exact class of gap) — but it is precisely the governance call slice 654 deferred, and it grows the palette five crosswalks resolve against. Legitimate as **option (A)** in slice 754, illegitimate as an unannounced side effect of a HIPAA slice. |

## D3 — Anchor targets are named as CONTROL CONCEPTS, not as SCF IDs

The residual table below does not assert specific finer SCF identifiers
(e.g. "re-point to IAC-27"). The full SCF catalog is not in this repo and is not
retrievable from here; asserting an ID would be inventing an audit-facing fact.
What IS the control judgement — and what this pass genuinely settles — is _which
control concept each row should land on, and whether a finer landing is warranted
at all_. Resolving the concept to an SCF identifier is a mechanical lookup the
follow-on performs against whichever catalog slice 754 lands, and it must be
grep-verified present before the row is written (the slice 654 discipline).

## D4 — Palette re-check: no new in-palette opportunity has opened since slice 516

Slice 516 mapped against a 53-anchor fixture. The fixture now carries **62**
anchors — slices 635 and 641 added `THR-01`..`THR-07`, `THR-09`, `THR-10` (nine
anchors; the fixture carries no `THR-08`). All nine additions
are Threat-Management domain; none of the 21 residual rows is a threat-intel,
threat-hunting, insider-threat, or vulnerability-disclosure concept. So the
in-palette pass slice 516 D5 performed (which found exactly one genuine lift,
§164.308(a)(8) → CPL-01 @ 0.75) is still current, and there is no
re-point available today that does not depend on slice 754. Verified by reading
the fixture's 62 `scf_id` values against the 21 rows.

Incidental correction for the record: slice 516's log and the crosswalk YAML
comments both say "53-anchor sample fixture." That count was accurate when 516
was written and is now stale at 62. The follow-on should refresh those references
when it edits the YAML; this pass does not touch the data files.

## D5 — All 21 residual rows, addressed (the work list for the follow-on)

Every row from slice 516's residual table is given a verdict here. Current mapping
columns are read from `data/crosswalks/hipaa-security-rule.yaml` and match slice
516's table exactly (all 21 confirmed present at the recorded anchor / type /
strength). `RE-POINT` = a finer anchor is very likely to exist and the current
anchor genuinely under-fits. `CHECK` = plausible finer fit, decide against the
real catalog. `HOLD` = leave as-is permanently; the reason is final and does not
depend on slice 754.

| #   | Requirement                                                    | Now           | Verdict  | Target control concept + control rationale                                                                                                                                                                                                                                                                                                                        | Proposed strength                              |
| --- | -------------------------------------------------------------- | ------------- | -------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ---------------------------------------------- |
| 1   | §164.308(a)(1)(ii)(C) Sanction Policy                          | HRS-01 @ 0.60 | RE-POINT | A dedicated **personnel-sanctions** control in the HR-security family. "Apply appropriate sanctions against workforce members who fail to comply" is a specific disciplinary-process control, not the HR-security program as a whole; HRS-01 over-covers it.                                                                                                      | ~0.80 `subset_of`                              |
| 2   | §164.308(a)(4)(ii)(A) Isolate Clearinghouse Functions          | DCH-01 @ 0.55 | CHECK    | A **segmentation / separation-of-environments** control. The requirement is structural isolation of a clearinghouse's ePHI from the larger organization — closer to segmentation than to data classification. Weak either way; only lift if the catalog carries a genuine environment-separation anchor.                                                          | ~0.65 if re-pointed                            |
| 3   | §164.308(a)(5)(ii)(D) Password Management                      | IAC-01 @ 0.60 | RE-POINT | A dedicated **authenticator-management** control (create / change / safeguard authenticators). Named in 567's narrative. IAC-01 is the domain policy head; the requirement is the specific authenticator-lifecycle control beneath it.                                                                                                                            | ~0.85 `equal`-leaning                          |
| 4   | §164.308(a)(7)(ii)(E) Applications & Data Criticality Analysis | BCD-02 @ 0.60 | RE-POINT | A **business-impact-analysis (BIA)** control in the continuity family. Criticality analysis IS a BIA; mapping it to the continuity _plan_ conflates the input with the artifact.                                                                                                                                                                                  | ~0.85 `equal`-leaning                          |
| 5   | §164.310(a)(2)(i) Contingency Operations                       | BCD-02 @ 0.60 | CHECK    | A **facility-access-during-contingency** control, if one exists. The requirement genuinely straddles continuity and physical access; if the catalog has no emergency-facility-access anchor, BCD-02 stays correct as the covering anchor.                                                                                                                         | hold 0.60 unless a real straddle anchor exists |
| 6   | §164.310(a)(2)(iv) Maintenance Records                         | PES-04 @ 0.55 | RE-POINT | A **controlled-maintenance / maintenance-records** control (SCF carries a Maintenance domain). Documenting repairs and modifications to a facility's physical security components is maintenance record-keeping, not physical access control.                                                                                                                     | ~0.70 `intersects_with`                        |
| 7   | §164.310(b) Workstation Use                                    | PES-04 @ 0.60 | RE-POINT | An **acceptable-use / rules-of-behavior** control. Named in slice 481 D7 and re-confirmed in 516 D5. "Proper functions, manner of performance, and physical surroundings of a workstation" is a use-policy control; physical access control covers only the surroundings clause.                                                                                  | ~0.75 `intersects_with`                        |
| 8   | §164.310(d)(2)(iii) Accountability                             | AST-01 @ 0.65 | RE-POINT | A **media custody / media-transport** control. "Record the movements of hardware and electronic media and any person responsible therefor" is chain-of-custody for media, narrower and more specific than the asset-management policy head.                                                                                                                       | ~0.75 `subset_of`                              |
| 9   | §164.312(a)(2)(ii) Emergency Access Procedure                  | IAC-21 @ 0.60 | CHECK    | A dedicated **emergency-access / break-glass** control. Named in 567's narrative as "may exist." If present it is a clean fit; if absent, privileged-account-management remains the honest covering anchor.                                                                                                                                                       | ~0.80 if present, else hold                    |
| 10  | §164.312(a)(2)(iii) Automatic Logoff                           | IAC-01 @ 0.60 | RE-POINT | A **session-lock / session-termination** control. Named in 567's narrative. Terminating a session after inactivity is a session-management control; IAC-01 (domain policy head) badly over-covers it.                                                                                                                                                             | ~0.85 `equal`-leaning                          |
| 11  | §164.312(c)(1) Integrity                                       | DCH-01 @ 0.65 | RE-POINT | A dedicated **information/data-integrity** control. Called out in 481 D7, held in 516 D5 as palette-bound, and named again in 567's narrative. Protecting ePHI from improper alteration or destruction is integrity, not classification-and-handling.                                                                                                             | ~0.80 `subset_of`                              |
| 12  | §164.312(c)(2) Mechanism to Authenticate ePHI                  | DCH-01 @ 0.60 | RE-POINT | A **cryptographic integrity-verification** control (hashing / non-repudiation). "Electronic mechanisms to corroborate that ePHI has not been altered or destroyed" is the cryptographic mechanism implementing row 11 — a different, finer anchor than row 11's policy-level integrity control. Keep the two distinct.                                            | ~0.80 `subset_of`                              |
| 13  | §164.312(e)(2)(i) Integrity Controls (Transmission)            | NET-04 @ 0.60 | RE-POINT | A **transmission-integrity** control (the SC-8 analog). Named in 567's narrative. Boundary protection is about perimeter enforcement; the requirement is about detecting modification of ePHI in transit.                                                                                                                                                         | ~0.85 `subset_of`                              |
| 14  | §164.314(a)(2)(ii) Other Arrangements                          | TPM-01 @ 0.65 | **HOLD** | **Leave as-is — final.** This is the alternative-instrument facet: a government entity may use a memorandum of understanding in place of a business-associate contract. No control framework carries an anchor for "MOU in lieu of a BAA"; TPM-01 (Third-Party Management) is and remains the correct covering anchor. No finer anchor will exist in any catalog. | keep 0.65                                      |
| 15  | §164.314(b)(1) Group Health Plans                              | TPM-01 @ 0.55 | **HOLD** | **Leave as-is — final.** A HIPAA-specific plan-document amendment obligation on the group health plan / plan-sponsor relationship. It is a regulatory arrangement, not a security control; there is no SCF analog to find. TPM-01 as covering anchor at a deliberately low 0.55 is the honest mapping.                                                            | keep 0.55                                      |
| 16  | §164.314(b)(2)(i) Plan Safeguards                              | TPM-01 @ 0.55 | CHECK    | A **third-party security-requirements / contractual-safeguards** control. Unlike rows 14-15 this one does have a generic shape ("implement safeguards that reasonably protect ePHI created or received on behalf of the plan"), so a contract-requirements anchor may fit better than the third-party-management head. Modest lift at best.                       | ~0.65 if re-pointed                            |
| 17  | §164.314(b)(2)(ii) Adequate Separation                         | TPM-01 @ 0.55 | CHECK    | A **separation-of-duties / access-restriction** control. Organizational firewalling between a plan and its sponsor has a generic analog in separation of duties, but the fit is loose — the HIPAA concept is entity-level, the SCF concept is role-level. Re-point only if the catalog has an entity-separation anchor.                                           | ~0.65 if re-pointed                            |
| 18  | §164.314(b)(2)(iii) Agents Safeguard                           | TPM-04 @ 0.60 | RE-POINT | A **subcontractor flow-down / third-party contract-requirements** control. The requirement is specifically that agents and subcontractors agree to the same restrictions — flow-down, which is a distinct named control concept from third-party risk _assessment_.                                                                                               | ~0.80 `subset_of`                              |
| 19  | §164.316(b)(2)(i) Time Limit                                   | DCH-03 @ 0.60 | CHECK    | A **documentation / records-retention** control. Six-year retention of the Security Rule _documentation_ is compliance-records retention, not general data retention. If the catalog separates the two, re-point; if retention is one control, DCH-03 stays.                                                                                                      | ~0.75 if re-pointed                            |
| 20  | §164.316(b)(2)(ii) Availability                                | CPL-01 @ 0.60 | RE-POINT | A **security-documentation publication / dissemination** control in the governance family. "Make documentation available to those persons responsible for implementing the procedures" is dissemination, not compliance management.                                                                                                                               | ~0.80 `subset_of`                              |
| 21  | §164.316(b)(2)(iii) Updates                                    | GOV-01 @ 0.60 | RE-POINT | A **periodic review-and-update of security documentation** control. Reviewing documentation periodically and updating it in response to environmental or operational change is a specific governance-maintenance control, distinct from the governance program head.                                                                                              | ~0.80 `subset_of`                              |

**Tally:** 13 RE-POINT, 6 CHECK, 2 HOLD (settled now, no dependency on 754).
21 of 21 rows addressed.

## D6 — Invariants held through this pass

- **Invariant #7** — nothing in this pass creates a requirement → requirement edge;
  no crosswalk data changed at all. Every proposed target in D5 is a
  requirement → SCF-anchor concept.
- **567 boundary "do not weaken the sample fixture"** —
  `migrations/fixtures/scf-sample.json` is untouched, and slice 754's AC-3 carries
  the same protection forward.
- **567 boundary "no re-point without a recorded rationale"** — no row was
  re-pointed; every row that _will_ be re-pointed already has its rationale here.
- **567 boundary "no unrelated crosswalk edits"** — zero crosswalk edits.

## Out of scope (deliberate)

- NO crosswalk YAML change (blocked behind slice 754 — D1).
- NO change to `scfseed`, the sample fixture, or any loader code.
- NO SCF identifiers asserted from memory (D3).
- NO covered-entity workflow / BAA tracking / required-vs-addressable decision
  flow — still the deferred slice 517.
