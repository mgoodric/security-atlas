# Slice 567 — HIPAA finer-anchor re-point: JUDGMENT decisions log

**Slice:** 567 — re-point the 21 palette-bound HIPAA Security Rule crosswalk rows
(slice 516's residual table) at finer SCF anchors.
**Type:** JUDGMENT (crosswalk-mapping accuracy is a subjective control call).
**Parent dependency:** slice 516 (full HIPAA coverage) — merged.
**Author:** agent (JUDGMENT slice convention — the agent makes the build-time
mapping calls and records them here for post-deployment maintainer review).

**This log covers two passes.** Read it in order; the second pass did not
invalidate the first.

| Pass | Sections | Outcome                                                                                                                                                                                |
| ---- | -------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| 1    | D1–D5    | Scoping. Found the blocker is a catalog-content **governance** decision, not a test-harness gap. Split the seed path into slice 754, blocked 567, and pre-derived all 21 row verdicts. |
| 2    | D6–D9    | Applied. The operator answered the governance question (option **A**, interim). The palette grew by 15 anchors, 16 rows re-pointed, 5 settled in place, both test tiers assert it.     |

**Outcome:** all 21 residual rows are addressed and asserted. `data/crosswalks/hipaa-security-rule.yaml` and `migrations/fixtures/scf-sample.json` changed; no loader code changed.

- detection_tier_actual: `manual_review`
- detection_tier_target: `unit`

> One defect surfaced, in pass 2, and it is worth the aggregate signal. The
> pass-2 fixture growth initially reused the identifiers `IAC-22` and `DCH-06`.
> Both are codes slice 654 had already catalogued as real-but-absent SCF anchors
> **with a different recorded meaning** — 654's log names `IAC-22` as "Least
> Privilege", and pairs `DCH-06` with encryption-at-rest. Titling `IAC-22` as
> "Emergency Access (Break-Glass) Accounts" would have put two contradictory
> claims about the same identifier in one repo. Caught by `manual_review`
> (grepping every new identifier against its prior in-repo references before
> committing); **should** have been caught at `unit` — nothing stops the next
> fixture edit from re-colliding. D8 records the rule and the guard that is
> missing.

---

## Pass 1 — scoping and the split (D1–D5)

> Retained verbatim as the historical record. D1's diagnosis and D2's rejected
> shortcuts still stand; what changed in pass 2 is that the operator **made** the
> governance call D1 correctly refused to make. D5's per-row judgement is the
> input pass 2 applied — D6 is D5 resolved onto real identifiers.

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

## D5-inv — Invariants held through pass 1

- **Invariant #7** — nothing in pass 1 creates a requirement → requirement edge;
  no crosswalk data changed at all. Every proposed target in D5 is a
  requirement → SCF-anchor concept.
- **567 boundary "do not weaken the sample fixture"** —
  `migrations/fixtures/scf-sample.json` was untouched in pass 1, and slice 754's
  AC-3 carries the same protection forward.
- **567 boundary "no re-point without a recorded rationale"** — no row was
  re-pointed in pass 1; every row that _would_ be re-pointed already had its
  rationale here.
- **567 boundary "no unrelated crosswalk edits"** — zero crosswalk edits.

---

## Pass 2 — applying the operator's decision (D6–D9)

> The governance question D1 escalated was answered on 2026-07-25: **option (A),
> as an explicit INTERIM resolution.** Grow the bundled sample fixture with the
> finer anchors the 21 rows need, so `scfseed.EnsureSCFCatalog` resolves them and
> the shared suite stops rolling back. **Read D7 first** — it bounds exactly what
> that decision did and did not settle.

## D6 — The 21 residual rows as applied (the work list, resolved)

D5 recorded each row's target as a control _concept_ because no catalog in this
repo carried the identifiers (D3). Option (A) supplies them: the 15 concepts D5
named are now anchors in the sample palette, and each row below points at one.
The `Now` column is the pre-567 mapping; `Applied` is what ships.

Verdict changes from D5: three rows D5 marked `CHECK` were re-pointed once the
grown palette gave them a real target (rows 2, 9, 16); the other three `CHECK`
rows were checked and deliberately **left in place** (rows 5, 17, 19) — a
`CHECK` verdict was always "decide against the catalog", and for these three the
honest answer was "no". The two `HOLD` rows are unchanged, as D5 settled them.

| #   | Requirement                                                    | Now           | Applied                           | Verdict    | Control rationale                                                                                                                                                                                                                                                |
| --- | -------------------------------------------------------------- | ------------- | --------------------------------- | ---------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| 1   | §164.308(a)(1)(ii)(C) Sanction Policy                          | HRS-01 @ 0.60 | **HRS-07** `subset_of` 0.80       | re-point   | Applying sanctions to non-compliant workforce members is the disciplinary-process control itself, not the HR-security program containing it. HRS-01 over-covered it.                                                                                             |
| 2   | §164.308(a)(4)(ii)(A) Isolate Clearinghouse Functions          | DCH-01 @ 0.55 | **NET-06** `intersects_with` 0.65 | re-point   | Structural isolation of clearinghouse ePHI from the larger organization is segmentation, not data classification. Fit stays partial: the HIPAA obligation is organizational as well as technical, so the lift is modest (0.55 → 0.65, still LOW).                |
| 3   | §164.308(a)(5)(ii)(D) Password Management                      | IAC-01 @ 0.60 | **IAC-10** `equal` 0.85           | re-point   | Creating, changing, and safeguarding passwords **is** authenticator management. IAC-01 is the domain policy head above it.                                                                                                                                       |
| 4   | §164.308(a)(7)(ii)(E) Applications & Data Criticality Analysis | BCD-02 @ 0.60 | **RSK-08** `equal` 0.85           | re-point   | Assessing relative criticality of applications and data in support of the other contingency components **is** a BIA. BCD-02 conflated the analysis input with the plan artifact it feeds.                                                                        |
| 5   | §164.310(a)(2)(i) Contingency Operations                       | BCD-02 @ 0.60 | BCD-02 `intersects_with` 0.60     | left as-is | Genuinely straddles continuity and physical access, and neither family carries a control at that intersection worth minting. An emergency-facility-access anchor would exist only to serve this one row. BCD-02 remains the honest covering anchor.              |
| 6   | §164.310(a)(2)(iv) Maintenance Records                         | PES-04 @ 0.55 | **MNT-02** `intersects_with` 0.70 | re-point   | Documenting repairs and modifications to a facility's security-related physical components is maintenance record-keeping, not physical access control. Partial because HIPAA scopes it to security-related components only.                                      |
| 7   | §164.310(b) Workstation Use                                    | PES-04 @ 0.60 | **HRS-12** `intersects_with` 0.75 | re-point   | "Proper functions, manner of performance, and physical surroundings" is an acceptable-use / rules-of-behavior control; PES-04 covered only the surroundings clause. That clause keeps the relationship partial. Flagged in 481 D7, re-confirmed 516 D5.          |
| 8   | §164.310(d)(2)(iii) Accountability                             | AST-01 @ 0.65 | **DCH-07** `subset_of` 0.75       | re-point   | Recording movements of hardware and media and the person responsible is media chain-of-custody — a control beneath the asset-management policy head, not the head itself.                                                                                        |
| 9   | §164.312(a)(2)(ii) Emergency Access Procedure                  | IAC-21 @ 0.60 | **IAC-24** `equal` 0.80           | re-point   | Obtaining necessary ePHI during an emergency **is** break-glass. Privileged-account management was the covering anchor only because no break-glass anchor existed. Held at 0.80 rather than 0.85: HIPAA's framing is procedural, the anchor's is account-scoped. |
| 10  | §164.312(a)(2)(iii) Automatic Logoff                           | IAC-01 @ 0.60 | **IAC-25** `equal` 0.85           | re-point   | Terminating a session after a predetermined period of inactivity **is** session termination. IAC-01, the domain policy head, badly over-covered it.                                                                                                              |
| 11  | §164.312(c)(1) Integrity                                       | DCH-01 @ 0.65 | **DCH-05** `subset_of` 0.80       | re-point   | Protecting ePHI from improper alteration or destruction is data integrity, not classification-and-handling. The palette-bound row 481 D7 flagged and 516 D5 held. Row 12 carries the cryptographic mechanism that implements this policy-level obligation.       |
| 12  | §164.312(c)(2) Mechanism to Authenticate ePHI                  | DCH-01 @ 0.60 | **CRY-11** `subset_of` 0.80       | re-point   | Electronic mechanisms corroborating that ePHI has not been altered — hashing, digital signatures — is the cryptographic verification that implements row 11. Kept on a distinct anchor rather than collapsing both rows onto one.                                |
| 13  | §164.312(e)(2)(i) Integrity Controls (Transmission)            | NET-04 @ 0.60 | **CRY-12** `subset_of` 0.85       | re-point   | The requirement is modification **detection** in transit, not perimeter enforcement. Boundary protection answered a different question.                                                                                                                          |
| 14  | §164.314(a)(2)(ii) Other Arrangements                          | TPM-01 @ 0.65 | TPM-01 `intersects_with` 0.65     | **hold**   | Final, no catalog dependency. A governmental-entity MOU in place of a business associate contract is an alternative legal instrument, not a security control. No catalog carries an anchor for it.                                                               |
| 15  | §164.314(b)(1) Group Health Plans                              | TPM-01 @ 0.55 | TPM-01 `intersects_with` 0.55     | **hold**   | Final, no catalog dependency. A plan-document amendment obligation on a HIPAA-specific regulatory relationship, not a security control. TPM-01 at a deliberately low 0.55 is the honest mapping.                                                                 |
| 16  | §164.314(b)(2)(i) Plan Safeguards                              | TPM-01 @ 0.55 | **TPM-05** `intersects_with` 0.65 | re-point   | Unlike rows 14–15 this one has a generic shape: require a counterparty to implement safeguards over data handled on your behalf. That is contractual security requirements. Plan-sponsor framing keeps it a modest lift.                                         |
| 17  | §164.314(b)(2)(ii) Adequate Separation                         | TPM-01 @ 0.55 | TPM-01 `intersects_with` 0.55     | left as-is | Entity-level firewalling between plan and sponsor. The nearest finer concept, separation of duties, is role-level within one organization. Re-pointing would trade a correct coarse anchor for a wrong-altitude finer one.                                       |
| 18  | §164.314(b)(2)(iii) Agents Safeguard                           | TPM-04 @ 0.60 | **TPM-05** `subset_of` 0.80       | re-point   | Requiring agents and subcontractors to agree to the same protections is contractual flow-down — a different control from third-party risk _assessment_ (TPM-04).                                                                                                 |
| 19  | §164.316(b)(2)(i) Time Limit                                   | DCH-03 @ 0.60 | DCH-03 `intersects_with` 0.60     | left as-is | A retention-period obligation applied to compliance documentation. Retention is one control concept; splitting compliance-records retention from data retention would mint a distinction the catalog does not draw, to serve one row.                            |
| 20  | §164.316(b)(2)(ii) Availability                                | CPL-01 @ 0.60 | **GOV-02** `subset_of` 0.80       | re-point   | Making documentation available to those responsible for implementing it is dissemination, not compliance management.                                                                                                                                             |
| 21  | §164.316(b)(2)(iii) Updates                                    | GOV-01 @ 0.60 | **GOV-03** `subset_of` 0.80       | re-point   | Reviewing documentation periodically and updating it in response to environmental or operational change is a specific governance-maintenance control, not the governance program head.                                                                           |

**Tally:** 16 re-pointed, 3 checked and left in place, 2 held. 21 of 21 addressed,
every one with a rationale in this table **and** in its own `rationale` field in
`data/crosswalks/hipaa-security-rule.yaml` (the boundary: no row re-pointed
without a recorded control rationale).

**Strength discipline.** No row was lifted past what its rationale earns. The
three `equal`-leaning re-points reach 0.85 because the requirement and the anchor
describe the same control; `subset_of` re-points sit at 0.75–0.85; the two rows
whose fit stays genuinely partial (2, 16) lift only to 0.65 and remain LOW. Five
rows did not move at all. A re-point is not an excuse to inflate.

## D7 — What option (A) settled, and what it explicitly did NOT (the interim boundary)

**This is the load-bearing scope note.** The operator's decision authorizes a
**test-fixture change**. It is deliberately the least-committal path that unblocks
the re-points, and it is **INTERIM**.

What it settles:

- `migrations/fixtures/scf-sample.json` — the bundled **test** palette, marked
  `"release_version": "test-2026.1"` — grows by 15 anchors, from 62 to 77. This is
  the slice 635/641 pattern (nine THR anchors seeded for exactly this class of
  gap) applied deliberately rather than incidentally.
- The finer anchors live in the **shared** seeded catalog, so the assertions run
  in CI on every PR rather than behind an opt-in path that would never execute.
  That is the whole reason (A) unblocks anything (D1).

What it does **NOT** settle — each remains a separate, open item, not decided
here:

| Item                                                            | Status after this slice                                                                                                                                                                                                         |
| --------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **(B) Bundling the real SCF catalog**                           | **DEFERRED — legal.** Still gated on the SCF-redistribution legal review CLAUDE.md lists as _decide before bundling the SCF catalog in releases_. Nothing here bundles, ships, or reproduces an SCF release. Remains slice 754. |
| **(C) Per-crosswalk catalog requirements**                      | **NOT DECIDED.** The most flexible and most invasive option; it reopens the slice-461 seed-order coupling. Untouched. Remains slice 754.                                                                                        |
| Governance rule for what earns a place in the sample palette    | **NOT DECIDED.** 754's AC-1 (an ADR for option A or C) is 754's to discharge. This slice records a decision, not a standing policy.                                                                                             |
| Reconciling fixture identifier numbering against a real release | **OPEN — see D8.** The numbering is fixture-local until (B) lands.                                                                                                                                                              |

Slice 754 therefore stays filed and open. What changed is that 567 is no longer
blocked behind it: the interim fixture resolves 567's need, and 754 now owns the
durable catalog-source question rather than gating a HIPAA data slice on it.

## D8 — Identifier numbering is fixture-local, and two codes had to be renumbered

D3 (pass 1) refused to assert SCF identifiers from memory. Option (A) forces the
issue: a fixture entry needs _an_ identifier. The honest position, recorded here
and in the crosswalk YAML header:

> The sample fixture is a curated **test** palette, not an SCF release. Anchor
> **titles and descriptions** are the control concepts D5 derived, written in the
> fixture's existing house style. Anchor **identifiers** are fixture-local. They
> are plausible, not authoritative, and they are reconciled against real SCF
> numbering when option (B) lands.

Every other anchor in this fixture already carries that caveat — slice 641
flagged it explicitly for the THR domain.

**The collision, and the rule it produced.** Three of the 15 identifiers were
already referenced elsewhere in this repo. Checked one at a time:

| Identifier | Prior in-repo reference                                                                                                                            | Outcome                                                                                                                                                                                                |
| ---------- | -------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `IAC-10`   | Slice 654 remapped `1password.org_policy` off it; 654 anticipates "a more specific credential-management anchor (e.g. IAC-10 family) once present" | **Kept.** 654's reading corroborates Authenticator Management — a password-manager org policy pointing at IAC-10 is consistent.                                                                        |
| `IAC-25`   | `Plans/UCF_GRAPH_MODEL.md` line 226 labels `SCF:IAC-25` "Session management"                                                                       | **Kept.** Independently corroborates Session Termination.                                                                                                                                              |
| `IAC-22`   | Slice 654's log names it **"IAC-22 Least Privilege"**                                                                                              | **Renumbered → `IAC-24`.** A hard contradiction: the repo cannot say IAC-22 is Least Privilege in one file and Emergency Access (Break-Glass) in another.                                              |
| `DCH-06`   | Slice 654 pairs it with `CRY-04` on `aws.s3.bucket_encryption_state` (encryption-at-rest context)                                                  | **Renumbered → `DCH-05`.** No title is recorded, so this is softer than IAC-22 — but the evidence context points away from "Data Integrity", and reusing it would be a guess against a recorded usage. |

`IAC-24` and `DCH-05` were chosen because no file in the repo references either.
The remaining eleven identifiers (`HRS-07`, `HRS-12`, `NET-06`, `RSK-08`,
`MNT-02`, `DCH-07`, `CRY-11`, `CRY-12`, `TPM-05`, `GOV-02`, `GOV-03`) have no
prior in-repo reference at all.

**The rule, for the next fixture edit:** do not reuse an identifier this repo has
already pinned to a different meaning — in particular the twelve real-but-absent
codes slice 654 catalogued (`IAC-09`, `IAC-10`, `IAC-17`, `IAC-18`, `IAC-22`,
`DCH-06`, `MON-02`, `TDA-06`, `TDA-09`, `IRO-02`, `IRO-07`, `IRO-13`). Today that
rule is enforced by review, not by a test — which is why this slice's
`detection_tier_target` is `unit`. A guard asserting fixture identifiers do not
collide with codes recorded elsewhere is a genuine gap; it is **not** filed as
part of this slice because it is a cross-cutting fixture-hygiene concern rather
than HIPAA crosswalk work, and 754 (which owns the catalog-source question) is
the natural home for it.

**One consequence worth surfacing.** `IAC-10` and `DCH-05`'s concept (data
integrity) are now present in the palette. Slice 654 remapped `1password.org_policy`
off `IAC-10` and dropped `DCH-06` from `aws.s3.bucket_encryption_state`
_specifically because those anchors were absent_, and 654's log anticipated
revisiting "once present". Those schema hints are now re-openable. **Deliberately
not touched here** — 567's boundary forbids unrelated edits, and schema
`x-default-scf-anchors` hints are a different surface from crosswalk edges.

## D9 — Invariants and boundaries held; how it was verified

- **Invariant #7 (requirement → SCF anchor, never requirement → requirement).**
  Every one of the 21 rows is a `requirement_code` → `scf_anchor` edge. No
  requirement-to-requirement mapping was introduced; the standing
  `TestImport_NoDirectRequirementToRequirementTableExists` guard is untouched and
  green.
- **"Do NOT weaken or delete the sample fixture; add alongside it."** The change
  is strictly additive: 62 anchors → 77, zero removed, zero retitled, zero
  duplicated. Asserted two ways —
  `TestLoad_SamplePaletteStillCarriesPre567Anchors` walks **all five** bundled
  crosswalks and fails on any dangling anchor, and
  `TestHIPAAFinerAnchor_SamplePaletteNotWeakened` imports all five against the
  grown palette through real Postgres and re-checks the pre-567 sentinel edges.
- **"Do NOT re-point a row without a recorded control rationale."** Every one of
  the 21 rows carries a `rationale` in the YAML naming its verdict and reasoning;
  the unit test fails on an empty rationale for any residual row.
- **"Do NOT bundle unrelated crosswalk edits."** Only
  `data/crosswalks/hipaa-security-rule.yaml` changed. The other four crosswalks
  are byte-identical.
- **The importer runs clean.** `soc2import` imports the updated HIPAA crosswalk
  with zero dangling edges. The rollback-on-nonexistent-anchor behaviour
  (`TestHIPAAImport_RejectsEdgeToNonexistentAnchor`) still proves out against the
  grown palette — the property that made this slice hard in the first place is
  intact.

**Verification actually run** (not inferred): the full `go test ./internal/...
./cmd/...` unit sweep, and `go test -tags=integration -p 1` over
`internal/api/soc2import`, `internal/api/scfimport`, and `internal/api/scfseed`
against a real Postgres 16 with the forward migrations applied. All green.

## Test path (slice 567 AC-1)

Two tiers, both running in CI, following the slice-353 Q-2 convention:

| File                                                             | Tier                     | What it proves                                                                                                                                                                                  |
| ---------------------------------------------------------------- | ------------------------ | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `internal/api/soc2import/hipaa_finer_anchor_test.go`             | pure Go, no DB           | All 21 rows carry their recorded anchor / STRM type / strength / non-empty rationale; every anchor resolves against the bundled fixture; no bundled crosswalk dangles.                          |
| `internal/api/soc2import/hipaa_finer_anchor_integration_test.go` | `//go:build integration` | The same 21-row table asserted through real `fw_to_scf_edges` rows; the 15 added anchors are seeded in the current SCF framework version; all five crosswalks import against the grown palette. |

The 21-row table is declared **once**, in the untagged file, and shared with the
integration file — the two tiers cannot drift apart about what was decided.

## Out of scope (deliberate)

- NO change to `scfseed`, the importer, or any loader code — pure data + tests.
- NO SCF release bundled, reproduced, or shipped (D7 — option B stays deferred
  pending legal review).
- NO change to the other four bundled crosswalks.
- NO re-opening of slice 654's schema `x-default-scf-anchors` remaps, though D8
  notes two are now re-openable.
- NO covered-entity workflow / BAA tracking / required-vs-addressable decision
  flow — still the deferred slice 517.
