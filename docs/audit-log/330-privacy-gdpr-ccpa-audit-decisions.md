# 330 — Privacy audit (GDPR + CCPA/CPRA) · decisions log

**Slice:** 330 — Privacy audit (GDPR + CCPA) via `voltagent-qa-sec:gdpr-ccpa-compliance`
**Date:** 2026-07-25
**Audit HEAD:** `ba2de9b1`
**Audit posture:** operator-side deployability ("can an EU or California operator
run this compliantly?") — NOT slice 329's meta-audit posture
**Report:** [`docs/audits/330-privacy-gdpr-ccpa-audit.md`](../audits/330-privacy-gdpr-ccpa-audit.md)
**Ratified ADR:** [`docs/adr/0020-right-to-erasure-vs-append-only-ledger.md`](../adr/0020-right-to-erasure-vs-append-only-ledger.md)

---

## D0 — The slice was never done; the `merged` status was a bulk-reconcile artifact

**Decision.** Treat slice 330 as **OPEN** and execute it, rather than accepting
the recorded `merged` status. Correct the slice doc's Status line and record the
error inline in both the slice doc and the report's provenance note.

**Evidence the status was wrong.** The slice doc carried `Status: merged (status
reconciled 2026-06-03 — backlog drained per _STATUS.md SoR; loop terminated
batch 184)`. Against that:

- **No deliverable existed.** `docs/audits/` held reports for slices 327, 328,
  329, 331, 332, 333, 334, 335, 337, 348 and 351 — every audit-cluster sibling —
  but nothing for 330.
- **No decisions log existed** at the path AC-2 names (this file).
- **The load-bearing artifact did not exist.** `docs/adr/` topped out at
  `0019-framework-versioning-capability.md`. AC-3's erasure ADR was absent.

**Rationale.** A bulk status reconcile that drains a backlog by editing Status
lines cannot manufacture deliverables. Where the recorded status and the absence
of every named artifact disagree, the artifacts are the source of truth.

**Blast radius — corrected downward from the work-order's framing.** OE-394
states that slices 504, 505, 506 **and 507** each cite "#330 — merged" as a
satisfied dependency. Verified per-file:

| Slice                     | Cites #330 as `merged`? | Where                                                                                                      |
| ------------------------- | ----------------------- | ---------------------------------------------------------------------------------------------------------- |
| 504 — right to erasure    | **Yes**                 | `:156` Dependencies; `:16`; AC-7 `:138`; P0-504-4 `:149` implement against "slice 330 AC-3's ratified ADR" |
| 505 — DSAR export         | **Yes**                 | `:134` Dependencies ("AC-5 directs this follow-up")                                                        |
| 506 — RoPA primitive      | **Yes**                 | `:125` Dependencies ("AC-4 directs this follow-up")                                                        |
| 507 — breach notification | **No**                  | `:135-145` lists #446, #180, #372, #445, privacy-v0 greenlight. Slice 330 appears nowhere in the file      |

**Three** slices, not four. 507 is gated on ADR-0017 / slice 446, independent of
this slice. Recorded so the correction does not propagate further.

**Detection tier.** `detection_tier_actual = manual_review` (the OE-394
work-order author noticed the missing deliverable);
`detection_tier_target = none` — no automated tier could have caught this. It is
a process failure, not a code defect: `_STATUS.md` is a generated, non-gating
file and nothing cross-checks a `merged` status against the existence of the
artifacts its ACs name. Recorded as a candidate for a future discovery primitive
in the slice-345 shape (assert that a JUDGMENT slice marked `merged` has its
named decisions log on disk).

---

## D1 — Audit surface: the nine named surfaces plus a tenth the slice doc implies

**Decision.** Cover the nine privacy surfaces enumerated in the slice doc
narrative, and add a tenth: the **personal-data inventory** — where personal
data actually lands in the schema (report §3).

**Rationale.** The nine named surfaces are all _capability_ questions ("is there
a DSAR path?"). Every one of them is unanswerable without first knowing which
columns hold personal data, and that inventory existed in no form anywhere in the
repo. It is the load-bearing raw material for findings PRIV-1 through PRIV-4 and
is the artifact slices 504 and 505 are separately required to share as
`privacy.PersonalDataSurfaces` (slice 505 P0-505-5). Producing it once, here,
prevents two slices from deriving it independently and drifting.

**Method.** Static read of `migrations/sql/` (192 files),
`internal/db/dbx/models.go`, `internal/db/queries/*.sql`,
`internal/api/schemaregistry/schemas/`, `internal/**`, `web/**`, `connectors/**`,
plus the governance corpus. No live deployment touched (slice-doc boundary; the
OE-394 work-order repeats it).

---

## D2 — Severity rubric: calibrated to operator deployability, deliberately asymmetric with slice 329

**Decision.** Five tiers:

- **Critical** — a data-subject right is structurally unexerciseable **and** the
  platform actively holds the data. Regulator-visible violation for a deploying
  operator.
- **High** — a required GDPR/CCPA artifact or capability is absent and an
  EU/California operator could not deploy compliantly without building it.
- **Medium** — the capability exists but is undocumented, partial, or
  discoverable only by reading code.
- **Low** — present and workable; hardening or documentation sharpening.
- **Informational** — correctly deferred, N/A, or a strong baseline worth
  recording.

**Rationale for the two-limb Critical test.** Neither limb alone justifies
Critical. An absent capability over data the platform does not hold is
theoretical; data held with a working erasure path is compliant. PRIV-2 meets
both limbs — hence the only Critical in the set.

**The asymmetry with slice 329 is intentional.** 329 asked "would a third-party
reviewer flag this?" and rated no finding Critical. 330 asks "could an operator
lawfully deploy?" The two questions reach different verdicts on identical facts,
most visibly on Art. 30, which 329 classed as a correct deferral and this audit
classes as a live High. Reconciled per-finding in report §7 with an explicit
ownership recommendation for each overlap (AC-6). The maintainer decides
ownership; this audit does not silently re-file 329's findings.

---

## D3 — AC-3: decide the erasure design rather than survey it (overrides the slice doc's process note)

**Decision.** Ratify a design as [ADR-0020](../adr/0020-right-to-erasure-vs-append-only-ledger.md)
rather than surveying the three candidates.

**What was overridden.** The slice doc's "Notes for the implementing agent"
says, of the tombstone / pseudonymise / refuse triad: "The agent should NOT pick
one — surfacing the design space + the trade-offs is the deliverable. The actual
decision is a follow-up slice's content." AC-3 itself is softer, requiring the
log to "propose a default design."

**Why overriding is correct, not merely instructed.** The instruction is
self-defeating given what shipped afterwards. Slice 504 was written citing "slice
330 AC-3's **ratified ADR**" as a hard gate (`504:138` AC-7, `504:149`
P0-504-4). If 330 only surveys, the decision lands in a follow-up slice that
does not exist, and 504 stays blocked on an artifact that by construction never
arrives — which is the exact deadlock the missing-deliverable error already
created once. The OE-394 work-order is explicit ("The ADR must make a decision —
an options survey does not unblock slice 504") and is the later, more specific
instruction.

**How the anti-criteria are still honoured.** Deciding the erasure _design_ is
not deciding the open questions. P0-330-1 (OQ #5 hosted offering), P0-330-2
(OQ #7 module shape) and P0-330-3 (OQ #10 breach notification) are each
untouched, and ADR-0020's "What this does NOT decide" section names all three
plus the privacy-v0 demand gate explicitly. The ADR removes the _design_
blocker on 504; it does not greenlight privacy-v0.

**The decision reached.** Tombstone — field-level redaction-in-place plus an
appended erasure record — executed by a separately-credentialed `atlas_erase`
role, with a per-record refuse-as-deferral branch predicated on
`sample_evidence` membership plus a frozen owning period.

**Invariant #2 is not weakened, and this is the crux.** The OE-394 boundary
forbids weakening the append-only ledger to make erasure easier. The design does
not: `atlas_app`'s guarantee at
`migrations/sql/20260511000004_evidence_ledger.sql:110-113` — never UPDATE, never
DELETE `evidence_records` — is untouched verbatim. A **separate, narrower,
NOBYPASSRLS, GUC-gated, column-GRANT-limited** capability is added beside it.
Three ratified documents independently constrain the answer to this shape:
ADR-0012 `:60-64` pre-authorizes tombstone disposal in the invariant's own ADR;
ADR-0003 `:32-49` proves `frozen_hash` binds evidence by id and not by content,
so redaction cannot disturb invariant #10; and `sample_evidence`'s `ON DELETE
RESTRICT` (`20260511000010_audit_samples.sql:151-153`) already forecloses row
deletion at the DB layer.

---

## D4 — The tombstone has two halves, because one merged document forbids the other

**Decision.** An erasure writes **both** an appended erasure record **and** the
in-place field redaction. Neither alone is sufficient.

**Rationale.** ADR-0012 `:60-64` says disposal is "a tombstone, **not a
mutation**," and `docs/governance/data-retention.md` §4.5 (`:511-520`) specifies
the procedure as an _append_: "The original record is never deleted"; "A
tombstone record is **appended**." §6 (`:645-646`) adds "tombstones are
forward-only."

Slice 504's tombstone is _redact-in-place_ — which is precisely a mutation, and
which §4.5 does not authorize. **Two different mechanisms wear the same word.**
Had the ADR ratified redact-in-place alone, slice 504 would have shipped a
mechanism forbidden by a merged governance document. Requiring both halves
satisfies §4.5's append requirement and Art. 17's actual requirement
simultaneously.

**Consequence owned by this slice, not 504.**
`docs/governance/data-retention.md` needs a new §4.5b authorizing field-level
redaction-in-place as a sixth disposal method, bounded to allow-listed
personal-data columns. Bundled with the `:109-110` correction into follow-up F-1
because both edit the same document for the same reason.

---

## D5 — Validation pass against the draft report: four errors corrected before ratification

**Decision.** Re-verify the draft report's factual anchors independently before
ratifying the ADR that rests on them, and correct what fails.

**Rationale.** The ADR is load-bearing for a Critical finding and for three
downstream slices. An anchor that does not hold would propagate into a migration.

**Corrections applied to the report:**

| #   | Claim as drafted                                                                            | Verified reality                                                                                                                                                                                                                                                | Where corrected                |
| --- | ------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------ |
| 1   | Slices 504, 505, 506 **and 507** cite #330 as a satisfied dependency                        | 507 does not — `507:135-145` lists #446/#180/#372/#445/privacy-v0 and never mentions 330. Blast radius is three                                                                                                                                                 | Provenance note; D0 above      |
| 2   | "**Exactly one** table in the schema is immune to that bypass" (§5.1 pt 3, PRIV-9 pt 1)     | `action_plan_audit_log:354` is the only `BEFORE UPDATE OR DELETE` trigger, but `board_packs:183` and `framework_requirements:254` carry conditional UPDATE-only triggers explicitly justified as surviving `atlas_migrate` BYPASSRLS. Three precedents, not one | §5.1 pt 3; PRIV-9 pt 1         |
| 3   | ADR-0020 "confirms the design slice 504 assumed, so **slice 504's spec needs no revision**" | The _design_ needs none; the _spec_ needs seven amendments, four of which change the migration                                                                                                                                                                  | Executive summary; ADR-0020 §7 |
| 4   | `data-retention.md:108-111` makes the false per-record-deletion claim                       | The sentence is at `:109-110`, sits inside §1 "What this policy does not cover" as the _reason_ for excluding Art. 17, and is contradicted by the same document at `:512-514`                                                                                   | PRIV-2 "Compounding"           |

Minor precision fixes also applied: audit-log-family count 22 → 23 with three
(not two) pre-180 scoped-out tables (`audit_sink_failures` was omitted);
"zero hits" → "zero genuine hits" on the telemetry grep, whose only matches are
the English word "plausible" in prose.

**Anchors that held on re-verification:** the `evidence_records` two-policy RLS
shape; `internal/scim/store.go:343-346`; the absence of `DELETE FROM users`; the
`tenant_llm_routing` provider enum with no region column; the eleven-table
`subject_module` drift list; all thirteen `*_nonempty` CHECK constraints; the
telemetry absence; the four downstream slice Status lines.

**Detection tier.** `detection_tier_actual = manual_review`;
`detection_tier_target = manual_review`. An audit report's factual claims are
verifiable only by re-reading the source; there is no automated tier for
"does this prose cite the right line number." Working as intended.

---

## D6 — Follow-up fan-out: split along the privacy-v0 gate, do not re-file what exists

**Decision.** File six follow-up OEs, none of which depend on the privacy-v0
greenlight. Do **not** re-file DSAR, erasure or RoPA.

**Rationale — the structural observation.** Slices 504, 505 and 506 are gated on
a privacy-v0 greenlight tied to prospect demand (OQ #7). But the Art. 15 and
Art. 17 exposure is created by connectors that ship on `main` **today**. The
gate sits on the wrong side of the risk. The documentation half of the remedy —
the personal-data map, the project RoPA, the transfer analysis, the corrected
retention claim — requires no product work and should not inherit the module's
demand gate. That split is the shape of the fan-out.

**P0-330-4 compliance (no bundling).** DSAR, erasure and RoPA remain three
separate tracer-bullets owned by slices 505, 504 and 506 respectively. The six
new OEs are distinct artifacts with distinct audiences, not repackagings:

| OE  | Finding         | Work                                                                                                       | Why not a re-file                                                          |
| --- | --------------- | ---------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------- |
| F-1 | PRIV-2          | Correct `data-retention.md:109-110`; add §4.5b authorizing field-level redaction                           | Governance-document correction; slice 504 is product work                  |
| F-2 | PRIV-4 + PRIV-1 | Publish `docs/governance/personal-data-inventory.md` from report §3; link from `SELF_HOSTING.md`           | The _registry_; slice 505 is the _export workflow_                         |
| F-3 | PRIV-3          | The project's own six-column RoPA as a governance doc                                                      | The _project's_ RoPA; slice 506 is the _operator-facing product primitive_ |
| F-4 | PRIV-5          | Chapter V transfer analysis for the three cloud LLM providers + residency lever or EU-operator guard       | No existing slice covers it; postdates 329                                 |
| F-5 | PRIV-7          | Resolve `subject_module` drift — extend to the eleven, or narrow the pre-commitment                        | Slice 180 is merged; this is its drift                                     |
| F-6 | PRIV-8          | Retention windows for `sessions`, `oauth_token_exchanges`, `oauth_revoked_tokens`; resolve dormant `geo_*` | Slice 375 covered project artifacts and explicitly excluded tenant data    |

**Low findings are report-only.** PRIV-10 (DCO contributor identity) and PRIV-11
(`vendors.owner_user`) get no follow-up, matching the slice-329 and slice-331
precedent for Low.

**PRIV-6 is split deliberately.** The controller/processor _determination_ is
stated in report §4 and rides along with F-2's governance publication. The
missing `tenants.controller_role` field is recorded as a **design input to
privacy-v0**, not filed: adding a role column with no workflow to consume it is
the speculative schema slice 180's own P0-180-1 rejected.

---

## D7 — AC-8 / P0-330-3: breach-notification pressure recorded, shape not proposed

**Decision.** Record four design pressures on OQ #10 at PRIV-9 and propose **no
workflow shape**. OQ #10 stays OPEN.

**Rationale.** `Plans/canvas/11-open-questions.md` OQ #10 says "**This OQ stays
OPEN**"; `docs/adr/0017-breach-disclosure-72h-handoff.md:3` is `ADOPT-DEFERRED`
pending maintainer review. AC-8 and P0-330-3 both forbid pre-deciding it. The
pressures recorded — the immutability-primitive question, Art. 34's dependency
on the personal-data map, the un-enumerated `evidence_records.payload` subject
population, and the cloud-LLM Art. 33(2) processor leg — are observations about
what a future decision must contend with, not a proposed shape.

**One pressure was upgraded to a requirement, but only inside this slice's own
scope.** PRIV-9's immutability-primitive observation becomes a hard requirement
for `privacy.erasure_audit_log` in ADR-0020 §7 (it must carry the trigger, not
just RLS). That is a decision about the erasure log this slice owns, not about
the breach-notification workflow it does not.

---

## D8 — AC-7: no code modified

**Decision.** Documentation only.

**Verification.** The diff comprises `docs/audits/330-privacy-gdpr-ccpa-audit.md`
(new), `docs/adr/0020-right-to-erasure-vs-append-only-ledger.md` (new),
`docs/audit-log/330-privacy-gdpr-ccpa-audit-decisions.md` (new), and
`docs/issues/330-privacy-gdpr-ccpa-audit.md` (Status line + reconcile note).
No file under `migrations/`, `internal/`, `cmd/`, `web/`, `connectors/`, `pkg/`
or `sdk/` is touched. P0-330-5 honoured.

P0-330-7 (do not touch `CLAUDE.md`, canvas, mockups) honoured: `CLAUDE.md` and
`Plans/` are unmodified. The ADR's recommended edits to
`docs/governance/data-retention.md` are **filed as F-1**, not applied here —
that document is a merged governance artifact and amending it is its own
reviewable change, not an audit side-effect.

---

## Summary of findings + dispositions

| ID      | Severity      | Finding                                                                                         | Disposition                                                                                       |
| ------- | ------------- | ----------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------- |
| PRIV-2  | **Critical**  | Right to erasure structurally unexerciseable while the platform holds third-party workforce PII | ADR-0020 ratified (design); slice 504 (product, gated); **F-1** (governance correction, un-gated) |
| PRIV-1  | High          | No DSAR / right-of-access capability; no personal-data registry                                 | Slice 505 (gated); **F-2** (registry, un-gated)                                                   |
| PRIV-3  | High          | No RoPA; no lawful-basis statement for any purpose                                              | Slice 506 (gated); **F-3** (project RoPA, un-gated)                                               |
| PRIV-4  | High          | No shipped personal-data map or operator-facing privacy documentation                           | **F-2**. Dedupe with 329's ISO A.5.34 row — 330 owns it                                           |
| PRIV-5  | High          | Cloud-LLM opt-in is an undocumented Chapter V restricted transfer                               | **F-4**                                                                                           |
| PRIV-6  | Medium        | Controller/processor determination unstated; schema cannot express it                           | Determination stated in report §4 (rides F-2); schema half = design input to privacy-v0           |
| PRIV-7  | Medium        | Slice-180 `subject_module` pre-commitment has drifted (11 tables)                               | **F-5**                                                                                           |
| PRIV-8  | Medium        | IP / User-Agent / OIDC subject retained indefinitely, no TTL                                    | **F-6**                                                                                           |
| PRIV-9  | Informational | Breach-notification design pressure                                                             | Recorded only. OQ #10 stays OPEN (D7)                                                             |
| PRIV-10 | Low           | DCO mandates contributor identity into public history, no notice                                | Report-only                                                                                       |
| PRIV-11 | Low           | `vendors.owner_user` plaintext email; hard-delete asymmetry                                     | Report-only                                                                                       |

**Detection-tier classification (slice 353 / Q-13).**
`detection_tier_actual = manual_review` · `detection_tier_target = manual_review`.
Privacy-design gaps of this class — an absent capability, an unstated
determination, a false claim in a governance document — are not detectable by
any test tier. The one sub-finding that _is_ mechanically detectable is PRIV-7's
`subject_module` drift, whose target tier is a slice-345-shape discovery
primitive; F-5 carries that option.
