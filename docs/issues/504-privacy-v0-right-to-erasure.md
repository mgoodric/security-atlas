# 504 — Privacy v0: right-to-erasure (tombstone) implementation against the append-only ledger

**Cluster:** Privacy
**Estimate:** L (3-4d)
**Type:** JUDGMENT (erasure-vs-ledger-invariant reconciliation; tombstone shape)
**Status:** `not-ready`

> **AMENDED 2026-07-25 against [ADR-0020](../adr/0020-right-to-erasure-vs-append-only-ledger.md).**
> The erasure-design ADR this slice gated on is now ratified. **The design half is
> confirmed — tombstone, no pivot** — but ADR-0020 §7 amends the _mechanism_ in
> seven ways, four of which change the migration. This document has been rewritten
> against the ratified mechanism so the implementing agent does not build to a
> superseded shape. Every material claim below traces to a numbered ADR-0020
> section; where this slice and the ADR once disagreed, the ADR wins and the
> divergence is called out inline.

> **STILL GATED — one commitment remains open.**
>
> 1. **Privacy-v0 ship timing — OPEN.** OQ #7 (resolved 2026-05-20) committed the
>    privacy sibling module to **v2+ when a real prospect surfaces demand**. Slice
>    180 landed the foundation (audit-log `subject_module`, feature-flag
>    module-toggling, sibling-discipline doc) but explicitly deferred the privacy
>    primitives. This slice fires only once privacy-v0 is greenlit. ADR-0020
>    ("What this does NOT decide") is explicit that it does **not** greenlight
>    privacy-v0 and does not un-gate this slice's demand trigger — re-gating is a
>    maintainer call surfaced as audit finding PRIV-2.
> 2. **The erasure design decision — CLEARED.** ADR-0020 (Accepted,
>    2026-07-25) resolves slice 330 AC-3 and selects the **tombstone**:
>    field-level redaction-in-place plus an appended erasure record. Pseudonymisation
>    is rejected (Recital 26 — pseudonymised data is still personal data, so it does
>    not discharge Art. 17); blanket refuse-with-explanation is rejected (Art. 17(3)
>    authorizes retaining _necessary_ data, not declining requests).

## Narrative

**WHY.** GDPR Art. 17 (right to erasure) and CCPA §1798.105 (right to deletion)
give a data subject the right to have their personal data deleted. The platform's
constitutional invariant #2 makes the evidence ledger **append-only** — "Bugs in
evaluation never corrupt the record. Point-in-time replay is always possible."
And audit-period freezing (canvas §8.4) requires frozen sample populations to
remain stable. A naive `DELETE` would violate both invariants, and is in any case
already foreclosed at the DB layer: `sample_evidence.evidence_record_id` and
`sample_annotations.evidence_record_id` both carry `ON DELETE RESTRICT`
(`migrations/sql/20260511000010_audit_samples.sql:151-153`, `:176-178`).

These two obligations genuinely conflict, and slice 330's audit surfaced exactly
this tension. The reconciliation ADR-0020 ratifies is the **tombstone**: the
erasure request does not delete the ledger row; it **redacts the personal-data
fields in place** (replacing them with a fixed, non-reversible sentinel) while
preserving the row's identity, its **per-record content commitment**,
`observed_at`, and structural metadata. The audit trail stays intact and
replayable; the personal data is gone.

> **Correction (ADR-0020 §5) — there is no content-hash _chain_.** Earlier
> revisions of this slice said the redaction preserves "the row's content-hash
> chain position." That vocabulary overstates the coupling.
> `evidence_records.hash` is a **standalone per-record sha256** over canonical
> JSON (`migrations/sql/20260511000000_init.sql:239`, via `canonjson.HashRecord`);
> `idx_evidence_hash` is non-unique and Merkle linkage was explicitly rejected for
> v1 (ADR-0003 alternative 3). Redacting record X has **zero** effect on record Y.
> Say "per-record content commitment," never "chain" — otherwise AC-2's
> integration test asserts a structure that does not exist.

**Invariant #2 is not weakened.** `atlas_app`'s never-UPDATE / never-DELETE
guarantee on `evidence_records`
(`migrations/sql/20260511000004_evidence_ledger.sql:110-113`) stays verbatim. A
separate, narrower, separately-credentialed, GUC-gated capability is added beside
it (see §7 below). ADR-0012`:60-64` already pre-authorized tombstone disposal
inside the invariant's own ADR: "Disposal, when it comes, is a tombstone, not a
mutation — the invariant constrains it to tombstones-only."

**WHAT this slice ships (once ungated).**

### 1. `privacy.PersonalDataSurfaces` registry — the shared single source of truth

Slice 505 (DSAR export) reads the same allow-list this slice redacts; whichever
lands first owns creating it. It ships here.

The registry is **not** a hand-maintained Go table of ~40 surfaces. Per ADR-0020
§3 the per-kind personal-data path list lives beside the existing
`x-redaction-rules` as a new **`x-personal-data-paths`** key in the
schema-registry JSONB entry (additive, no migration, same mechanism as
`ExtractRulesFromSchema` at `internal/evidence/redact/redact.go:77-104`). One artifact
then serves ingestion redaction, erasure, and slice 505's DSAR correlation. The
Go-side `privacy.PersonalDataSurfaces` type is the typed accessor over that
artifact plus the non-JSONB column surfaces enumerated in §2.

**Reuse the existing redactor; do not write a second one.**
`internal/evidence/redact/redact.go` already implements the primitive: a narrow
JSONPath subset (`$.field`, `$.a.b.c`, `$.arr[*].field` — grammar at `:117-165`)
over a `structpb.Struct`, replacing matched leaves with a literal marker (`:36`,
`:170-191`), pure and non-logging. Slice 015 runs it at ingestion; **erasure is
the same function run late.**

### 2. Three mechanisms by column shape — the large scope reduction (ADR-0020 §3)

Earlier revisions implied a per-surface sweep across ~40 tables. The actual work
is far smaller:

| Column shape                                                                                                                                                                         | Mechanism                                                                                                                                                                        |
| ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **JSONB payload keys** — `evidence_records.payload` / `source_attribution` / `provenance`, `ai_generations.context_inputs`, `board_narrative_sections.*`                             | Path-based redaction via the **existing** `internal/evidence/redact` package                                                                                                     |
| **Unreferenced `TEXT` actor columns** — `actor`, `*_by`, `authored_by`, `owner_user`, `reviewer`, `treatment_owner`, `hostname`                                                      | Per-row sentinel overwrite. Zero referential fallout: `20260511000030_decisions_audit.sql` states the intent — no FK "because the audit trail must survive a future hard-delete" |
| **`UUID` actor columns** — `action_plan_audit_log.actor_id`, `control_owner_assignment_audit_log.actor_user_id`, `policy_acknowledgments.user_id`, `action_plans.owner_id`, ~20 more | **Do not touch.** Erase the _referent_ once — `users.email`, `display_name`, `idp_issuer`, `idp_subject` → sentinel — leaving an opaque, non-resolving UUID                      |

The UUID branch is legally sufficient: Art. 17 requires erasure of data by which
the subject is identifiable, not destruction of an opaque key that no longer
resolves. **One `UPDATE users` discharges every UUID-referencing audit row
simultaneously.** A UUID column cannot hold a string sentinel anyway.

**Mandatory Recital-26 companion clearing.** The key must be _genuinely_
non-attributable afterwards, so the same transaction must also clear
`local_credentials`, `sessions` (including `user_agent`, `ip_address`,
`geo_country`, `geo_city`), `oauth_token_exchanges.subject_token_sub`,
`oauth_auth_codes`, `oauth_device_codes.approved_by_idp_subject`, and
`scim_credentials` / `scim_audit_log.detail`. `sessions` and `local_credentials`
already declare `ON DELETE CASCADE` from `users` — the cascade was designed and
no code was ever written to trigger it.

**AI surfaces are in scope (ADR-0020 §9.2).** Earlier revisions named
board-narrative author fields but omitted `ai_generations.system_prompt`,
`.context_inputs` and `.raw_draft`
(`migrations/sql/20260607000000_ai_generations.sql`). Per `CLAUDE.md`
board-narrative D1/D3 the prompt corpus contains **cited evidence excerpts for
every claim** and the full prompt is persisted every time — a second, independent
copy of the subject's PII.

### 3. The tombstone has two halves, and both are mandatory (ADR-0020 §2)

ADR-0012 says "a tombstone, **not a mutation**," and
`docs/governance/data-retention.md` §4.5 (`:511-520`) specifies the procedure as
an **append**. Redact-in-place is exactly a mutation, which §4.5 does not
authorize. Two mechanisms wear one word, so an erasure writes **both**:

| Half                             | What it does                                                                                                                                                                       |
| -------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **(a) Appended erasure record**  | A new row in `privacy.erasure_audit_log` naming the affected record by primary key, the lawful request, the basis, the sentinel version, and the `(id, hash, erasure_hash)` triple |
| **(b) In-place field redaction** | Personal-data columns / JSONB paths overwritten with the sentinel, under a column allow-list                                                                                       |

**Governance dependency — owned by ADR-0020, not this slice.**
`docs/governance/data-retention.md` needs a new **§4.5b — "Ledger field-level
erasure (redaction-in-place under a recorded lawful request)"** as a sixth
disposal method, plus the `:109-110` correction (PRIV-2). **Until §4.5b exists,
half (b) is forbidden by a merged governance document.** Tracked as ADR-0020
follow-up F-1.

### 4. `privacy.erasure_requests` table

In the `privacy.*` sibling namespace that privacy-v0 creates. Per ADR-0020 §9.1
the free-text refusal basis is **rejected** in favour of closed enums, so the DPO
gets a countable register:

| Column                                                                          | Shape                                                                                                                              |
| ------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------- |
| `subject_identifier`                                                            | operator-supplied; no automated subject discovery in v0                                                                            |
| `regime`                                                                        | `TEXT CHECK (regime IN ('gdpr','ccpa','both'))`                                                                                    |
| `refusal_basis`                                                                 | nullable `TEXT CHECK (refusal_basis IN ('gdpr_17_3_b','gdpr_17_3_e','ccpa_105_d_1','ccpa_105_d_2','ccpa_105_d_7','ccpa_105_d_8'))` |
| `status`                                                                        | `requested` / `in_progress` / `completed` / `completed_partial` / `refused`                                                        |
| `requested_at`, `requested_by`, `confirmed_by`, `completed_at`, `operator_note` |                                                                                                                                    |
| identity-verification attestation                                               | recorded at request time (out-of-band verification, per threat model S)                                                            |
| `due_at`                                                                        | **two SLA clocks derived from `regime`** — GDPR Art. 12(3) 1 month (+2 extendable); CCPA 45 days (+45 extendable)                  |

Plus a **24-month retention floor on the request row itself** (CCPA regs require
retaining request records for 24 months) — the sweep must not erase it.

Per-surface **disposition** rows carry `retained` + `retain_until` for the
deferral path (§6 below), which is what `completed_partial` reports.

### 5. The hash stays immutable; verify gains a third verdict (ADR-0020 §5)

**`hash` is NOT recomputed.** Rewriting it would make the redaction invisible to
anyone holding the ingest `Receipt.Hash` already returned to the connector
(`internal/evidence/streambuf/streambuf.go:345`) and to any external party
composing per-record hashes with `frozen_hash`. Silent redaction is the one thing
ADR-0012 and ADR-0020 agree must not happen.

**Add to `evidence_records`:** `erased_at TIMESTAMPTZ NULL`,
`erasure_request_id UUID NULL`, `erasure_hash TEXT NULL` (sha256 of the
post-redaction reconstruction), `erasure_sentinel_version TEXT NULL`.

**Extend `VerifyLedgerRow` from a boolean to three verdicts**
(`internal/evidence/ingest/verify.go:158-164`, today
`(ok bool, recomputed string, err error)`):

| Verdict    | Condition                                                    |
| ---------- | ------------------------------------------------------------ |
| `CLEAN`    | recomputed == `hash`                                         |
| `REDACTED` | `erased_at IS NOT NULL` **and** recomputed == `erasure_hash` |
| `CORRUPT`  | everything else                                              |

Without this, **every erased row reads `CORRUPT` to the operator's own integrity
tool** — `verify.go:44-46` says so in writing. The `atlas evidence verify` CLI
must print the erasure-request id on a `REDACTED` row so the divergence
self-documents. Tamper-evidence is **preserved and refined**, not weakened: an
unexplained divergence is still `CORRUPT`; an explained one carries its own
recorded lawful basis.

**Export residue — must be stated, not left silent.** `internal/oscal/sign.go:142-153`
computes the bundle digest over the bundle's own member bytes, so a later ledger
redaction neither invalidates nor reaches an already-signed bundle. It verifies
forever and permanently contains the subject's PII. Erasure is therefore
**incomplete by construction for distributed copies** — unavoidable, not a bug.
That converts the residue into a notification obligation (Art. 17(2); CCPA
§1798.105(c)(3)) the platform cannot currently discharge, because export events
record no record-id set
(`migrations/sql/20260519000000_audit_periods_vendors_export.sql:54-56`). This
slice must **either** capture an export manifest (record-id set + recipient +
`exported_at`) **or** emit a best-effort export-recipient notification worklist
from the `me_audit_log` export rows. It may not leave this unstated. The same
§1798.105(c)(3) notification bites on `tenant_llm_routing` where the provider is
a cloud LLM (anthropic / openai / bedrock).

### 6. Frozen-period handling — redacted and flagged (ADR-0020 §8, amendment 6)

**This resolves a contradiction that was inside this document.** AC-3 previously
said a frozen-period redaction is "redacted but flagged"; threat model T said such
a redaction is "**rejected**." Per ADR-0003`:32-49`, `frozen_hash` is fixed over
content-only inputs that bind evidence **by sorted id array**
(`evidence_record_ids`), never by payload content — so a redaction preserving `id`
and `observed_at` leaves `frozen_hash` **byte-identical**. **Redacted and flagged
is correct**; the "rejected" wording is withdrawn.

> **`redacted_under_freeze` is an auditor-legibility marker, explicitly NOT an
> integrity mechanism.** It exists so the auditor reads "redacted per erasure
> request ER-NNN" instead of a phantom-missing field. It is not load-bearing for
> any hash. Do not let the next reader believe otherwise.

**Refusal is a per-record deferral, never a per-request refusal.** Erase
everywhere **except** rows matching the predicate; those get a `retained`
disposition with a recorded basis and a `retain_until` equal to the end of the
carve-out, and a scheduled re-evaluation completes the erasure when `retain_until`
passes.

Operative GDPR carve-outs are **only two**: Art. 17(3)(b) (legal obligation) and
Art. 17(3)(e) (legal claims). **Art. 17(3)(d)** (archiving/research in the public
interest) is a stretch for commercial GRC evidence and is **explicitly rejected**
so nobody reaches for it later.

The scoping predicate is **not** `audit_periods.frozen_at` alone — a frozen period
can span months of untouched evidence, and "frozen ⇒ retained" is precisely the
unlawful blanket. Use the narrower conjunction of _sample membership_ and a frozen
owning period:

```sql
EXISTS (
  SELECT 1
  FROM sample_evidence se
  JOIN samples s        ON (s.tenant_id, s.id) = (se.tenant_id, se.sample_id)
  JOIN audit_periods ap ON ap.id = s.audit_period_id
  WHERE se.evidence_record_id = ev.id
    AND ap.status = 'frozen'
    AND ap.frozen_at IS NOT NULL
)
```

Both legs exist today (`20260511000010_audit_samples.sql:132-153`;
`20260511000020_audit_periods.sql:66-102`). CCPA may _additionally_ assert
(d)(2) — "detect security incidents, protect against malicious, deceptive,
fraudulent, or illegal activity" — at the **evidence-kind category level**, which
GDPR does not permit. A single global refusal predicate is wrong in both
directions.

**`privacy.erasure_legal_holds` must be built.** Grep for `legal_hold` across
`migrations/sql/`: **zero matches.** `docs/governance/data-retention.md` §6
defines legal hold as a manual maintainer process and points at
`docs/governance/legal-holds.md`, which does not exist. This slice ships a minimal
`privacy.erasure_legal_holds` (`tenant_id`, `hold_ref` matching the `HOLD-NN`
shape at data-retention `:678-680`, `scope_predicate`, `opened_at`, `opened_by`,
`basis`, `released_at`), and a refusal citing a hold **must reference an open
row**. A refusal citing a document that does not exist is not audit-defensible.

### 7. Authorization — the `atlas_erase` role (ADR-0020 §7)

Four parts, three of which this slice previously lacked:

1. **Tenant `admin` only.** `cred.IsAdmin` is correct as a floor and stands.
2. **Two-verb separation of duties.** Verb 1 _records_ the request
   (`privacy.erasure_requests` INSERT: subject identifier, out-of-band
   identity-verification attestation, `regime`); verb 2 _executes_ against a
   request id in a separate call. Where the subject resolves to a `users` row
   holding `admin` or `auditor`, a table CHECK requires
   `confirmed_by <> requested_by` — a second distinct admin.
3. **`super_admin` is explicitly denied.** `super_admins`
   (`migrations/sql/20260521030000_super_admins_full.sql`) is the cross-tenant
   surface; a cross-tenant erasure verb is a catastrophic primitive with no lawful
   use — erasure is per-controller, per-tenant. The denial is named here
   deliberately, because it is not obvious and would otherwise be added later
   "for convenience."
4. **A dedicated DB role `atlas_erase`, not `atlas_migrate`.**
   - `atlas_erase`, **NOBYPASSRLS**.
   - `GRANT UPDATE (payload, source_attribution, erased_at, erasure_request_id,
erasure_hash, erasure_sentinel_version) ON evidence_records TO atlas_erase`
     — the allow-list becomes a **database privilege**, so a code bug cannot reach
     `observed_at` or `id`. Column-level GRANT precedent exists:
     `GRANT UPDATE (status) ON framework_versions TO atlas_app`
     (`20260612090000_framework_versioning.sql:263`).
   - A new `erasure_update` policy on `evidence_records` `FOR UPDATE`, with
     `USING` / `WITH CHECK` requiring `current_tenant_matches(tenant_id)` **and** a
     session GUC `app.erasure_request_id` resolving to a request row in status
     `executing`. No GUC ⇒ no rows ⇒ deny, mirroring invariant #6's
     RLS-denies-on-missing-context posture.

**`privacy.erasure_audit_log` needs the immutability trigger, not just RLS.**
Four-policy RLS (slice 036 pattern) is role-scoped only and does not bind a
privileged connection. This table is the sole surviving evidence that the erased
data existed and what became of it, written by the most privileged verb in the
product. Adopt the `action_plan_audit_log` pattern verbatim: a
`BEFORE UPDATE OR DELETE … FOR EACH ROW` trigger raising `restrict_violation`
(`20260612070000_action_plans.sql:345-356`). It carries `subject_module = 'privacy'`
per slice 180.

### 8. What is preserved — the invariant-#10 contract (ADR-0020 §4)

**Immutable under erasure, always:** `id`, `tenant_id`, `observed_at`,
`observed_at_nanos`, and row existence.

**Additionally preserved** for auditor legibility and evaluation replay:
`control_id`, `control_ref`, `scope_id`, `scope_canonical`, `result`,
`evidence_kind`, `schema_version`, `ingested_at`, `ingestion_path`,
`credential_id`, `idempotency_key`, `payload_uri`.

Enforced two ways, belt and suspenders: the UPDATE names only allow-listed
columns, **and** the column-level GRANT makes the allow-list a database privilege
rather than a code convention.

### 9. The sentinel: `<<ERASED>>`, request-invariant (ADR-0020 §6)

**Decided value: `<<ERASED>>`.**

- **It satisfies every CHECK.** Thirteen `*_nonempty` constraints
  (`CHECK (length(col) > 0)`) exist across the schema, from
  `artifact_access_log_actor_nonempty` (`20260511000008_artifacts.sql:131`) through
  `scim_group_members_user_id_nonempty` (`20260612040000_scim_groups.sql:119`).
  **The empty string is not a viable sentinel** — this is the non-obvious
  constraint the implementing agent must carry.
- **Same envelope, different token.** `redact.Marker = "<<REDACTED>>"`
  (`internal/evidence/redact/redact.go:36`) already exists. The `<<…>>` family
  reads as the same mechanism; a **different** token keeps an ingestion-time schema
  redaction (no lawful request, applied to every record of that kind) from ever
  being confused with a post-hoc erasure (one subject, one recorded request, one
  lawful basis).
- **It must be request-invariant.** The previously-proposed `[redacted:ER-NNN]` is
  **rejected**. Embedding the request id in the field value makes the sentinel a
  cross-surface correlator: anyone with ordinary read access could `SELECT` for
  `ER-042` and reconstruct the complete footprint of a supposedly-erased subject.
  Under Recital 26 that residual linkability is arguably itself personal data — it
  would reintroduce exactly the identifiability the design exists to remove. It
  also defeats a cheap `= '<<ERASED>>'` completeness assertion. The linkage belongs
  in `evidence_records.erasure_request_id` and the erasure audit row, both
  access-controlled.
- It is **not** a hash or cipher of the original.
- Version it via `erasure_sentinel_version` so a future change is detectable
  rather than archaeological.

**SCOPE DISCIPLINE — what's deliberately out.**

- **The DSAR export workflow** — that is slice 505 (a sibling follow-up of the
  same slice-330 audit). This slice is deletion only. Both share the
  `privacy.PersonalDataSurfaces` registry created here.
- **The RoPA primitive** — slice 506.
- **Pseudonymization-instead-of-tombstone.** Rejected by ADR-0020 (Recital 26).
  Pseudonymisation **is** the right primitive for slice 505's subject-correlation
  problem — a different job with a different ADR.
- **Cross-tenant erasure / hosted-offering processor obligations** — the
  self-host operator is the controller (slice 330 controller/processor finding);
  hosted-offering erasure mechanics wait on OQ #5 (hosted offering decision).
  `tenants.controller_role` (audit finding PRIV-6) is likewise out — ADR-0020
  records it as a design input to privacy-v0, not a column to add speculatively.
- **Automated subject-discovery.** The operator supplies the subject identifier;
  the platform does not crawl free-text evidence bodies for PII matches in v0.
- **Breach-notification workflow** — OQ #10, slice 507. Untouched by ADR-0020.

## Threat model (STRIDE)

Erasure is a **destructive, high-privilege, legally-load-bearing** operation
against the most sensitive records in the platform. The threat surface is
substantial.

**S — Spoofing.** An attacker forging an erasure request could weaponize the
right-to-erasure into a data-destruction attack ("erase the CISO's audit trail").
**Mitigation:** `cred.IsAdmin` alone is insufficient — it is the very privilege
the attacker holds. Per ADR-0020 §7.2 the verb is **split in two**: verb 1 records
the request with an out-of-band identity-verification attestation, verb 2 executes
against that request id. Where the subject resolves to a `users` row holding
`admin` or `auditor`, a table CHECK requires `confirmed_by <> requested_by` — a
second distinct admin. `super_admin` is explicitly denied (§7.3). Execution runs
under the separately-credentialed `atlas_erase` role, not `atlas_migrate`.

**T — Tampering (PRIMARY).** The redaction must not be usable to silently alter
audit history beyond the personal-data fields. **Mitigation:** the redaction
touches only an allow-listed set of personal-data columns, enforced twice — the
UPDATE names only those columns, **and** a column-level GRANT to `atlas_erase`
makes the allow-list a database privilege, so a code bug cannot reach
`observed_at` or `id`. The `erasure_update` RLS policy additionally requires the
`app.erasure_request_id` GUC to resolve to a request in status `executing`; no GUC
⇒ no rows ⇒ deny.

> A redaction touching a frozen-period sample population is **redacted and
> flagged, not rejected.** The earlier "rejected" wording here contradicted AC-3
> and is withdrawn — ADR-0003 proves `frozen_hash` is unaffected because it binds
> evidence by sorted id array, never by payload content. See §6 above.

**R — Repudiation.** A missed or mis-applied erasure is a compliance liability.
**Mitigation:** every redaction writes an append-only `subject_module='privacy'`
audit-log row into `privacy.erasure_audit_log`, protected by a
`BEFORE UPDATE OR DELETE` trigger raising `restrict_violation` (**not** RLS alone
— RLS is role-scoped and does not bind a privileged connection). Refusals record a
mandatory closed-enum `(regime, refusal_basis)` pair, and a refusal citing a legal
hold must reference an open `privacy.erasure_legal_holds` row. Because `hash` is
never recomputed, an erased row is **detectable by design**: `atlas evidence
verify` reports `REDACTED` with the erasure-request id rather than hiding the
change.

**I — Information disclosure.** The tombstone sentinel must not leak the original
value, **and must not become a correlator.** **Mitigation:** the sentinel is the
fixed, request-invariant `<<ERASED>>` — not a hash or cipher of the original, and
deliberately **not** the previously-proposed `[redacted:ER-NNN]`, which would let
any ordinary reader `SELECT` a request id and rebuild the erased subject's
cross-surface footprint (Recital 26 residual linkability). RLS confines the
erasure-request table to the owning tenant; the request↔record linkage lives only
in access-controlled columns.

**D — Denial of service.** A wide erasure sweep across all personal-data surfaces
could lock many rows. **Mitigation:** the sweep batches per surface within a
bounded transaction; large subjects are processed in chunks with progress in the
request status. The §2 scope reduction cuts the blast radius substantially — the
~20 UUID-column surfaces are not touched at all; one `UPDATE users` discharges
them. Not user-triggerable at volume (admin-gated, one subject per request).

**E — Elevation of privilege.** Erasure-confirm is the highest-privilege privacy
action. **Mitigation:** admin-only; no role below admin can confirm or refuse; the
action cannot be reached via any ordinary-user surface; `super_admin` is denied
outright; the DB role is NOBYPASSRLS.

## Acceptance criteria

- [ ] **AC-1.** `privacy.erasure_requests` migration is idempotent + reversible;
      lives in the `privacy.*` namespace; RLS-scoped to the owning tenant. Carries
      the closed `regime` and `refusal_basis` CHECK enums, the
      `completed_partial` status, the `confirmed_by <> requested_by` CHECK for
      admin/auditor subjects, both regime-derived SLA clocks, and the 24-month
      request-row retention floor.
- [ ] **AC-1b.** `privacy.erasure_legal_holds` is built (`hold_ref` matching the
      `HOLD-NN` shape); a refusal citing a hold must reference an **open** row.
- [ ] **AC-1c.** `privacy.erasure_audit_log` is protected by a
      `BEFORE UPDATE OR DELETE … FOR EACH ROW` trigger raising
      `restrict_violation` (the `action_plan_audit_log` pattern), not RLS alone —
      integration test asserts UPDATE and DELETE both fail.
- [ ] **AC-1d.** The `atlas_erase` NOBYPASSRLS role, its column-level
      `GRANT UPDATE (…)` on `evidence_records`, and the `erasure_update` policy
      exist; an integration test asserts (i) `atlas_erase` cannot UPDATE
      `observed_at` or `id`, (ii) no `app.erasure_request_id` GUC ⇒ zero rows
      visible to the UPDATE, and (iii) `atlas_app` still cannot UPDATE or DELETE
      `evidence_records` at all.
- [ ] **AC-2.** The tombstone redaction overwrites only the allow-listed
      personal-data columns and leaves the **per-record content commitment**,
      `observed_at`, `observed_at_nanos`, and linkage immutable. Integration test
      asserts the record still reconstructs and re-verifies (**not** "the chain
      replays" — there is no chain; see Narrative).
- [ ] **AC-2b.** `evidence_records` gains `erased_at`, `erasure_request_id`,
      `erasure_hash`, `erasure_sentinel_version`; `hash` is **never** recomputed.
      `VerifyLedgerRow` returns three verdicts (`CLEAN` / `REDACTED` / `CORRUPT`)
      and `atlas evidence verify` prints the erasure-request id on `REDACTED`.
      Integration test asserts an erased row reads `REDACTED`, not `CORRUPT`.
- [ ] **AC-3.** A redaction touching a frozen-period sample population is
      **redacted and flagged** `redacted_under_freeze`, the auditor surface shows
      "redacted per ER-NNN" rather than a phantom-missing field, and an
      integration test asserts the owning period's `frozen_hash` is
      **byte-identical** before and after.
- [ ] **AC-4.** Every redaction writes a `subject_module='privacy'` append-only
      audit-log row; refusals record a mandatory closed-enum lawful basis (no
      silent drop). Retained rows carry a `retain_until` and the request reports
      `completed_partial`.
- [ ] **AC-5.** Erasure-request, erasure-execute and refuse are admin-only (403
      for non-admin); `super_admin` is denied; the two verbs are separate calls.
- [ ] **AC-6.** The tombstone sentinel is the fixed, **request-invariant**
      `<<ERASED>>` — non-reversible, no hash/cipher of the original, and carrying
      no request id. Unit test asserts the sentinel shape and that it is distinct
      from `redact.Marker`.
- [ ] **AC-7.** `privacy.PersonalDataSurfaces` is the single source of truth for
      personal-data columns, backed by the `x-personal-data-paths` schema-registry
      key, and reuses `internal/evidence/redact` rather than a second redactor.
      Its coverage includes `ai_generations.system_prompt` / `.context_inputs` /
      `.raw_draft`.
- [ ] **AC-8.** The implemented design matches ADR-0020: tombstone, both halves
      (appended erasure record **and** in-place redaction), the three-mechanism
      split, and the Recital-26 companion clearing of `local_credentials`,
      `sessions`, `oauth_*` and `scim_*` surfaces.
- [ ] **AC-9.** The export residue is addressed, not left silent: **either** an
      export manifest (record-id set + recipient + `exported_at`) **or** a
      best-effort §1798.105(c)(3) notification worklist derived from the
      `me_audit_log` export rows and from `tenant_llm_routing` cloud-provider rows.

## Anti-criteria (P0 — block merge)

- **P0-504-1.** Does NOT `DELETE` any ledger row, and does NOT alter a row's
  `id`, `tenant_id`, `observed_at`, `observed_at_nanos`, or per-record content
  commitment (violates invariant #2).
- **P0-504-2.** Does NOT silently drop an erasure request — every request ends
  `completed`, `completed_partial` or `refused` with a documented closed-enum
  basis.
- **P0-504-3.** Does NOT expose erasure-confirm to any non-admin role, and does
  NOT extend it to `super_admin`.
- **P0-504-4.** Does NOT begin before privacy-v0 is greenlit. (Slice 330 AC-3's
  erasure-design ADR is ratified as ADR-0020 — that half of the gate is cleared.)
- **P0-504-5.** Does NOT recompute `evidence_records.hash` after redaction, and
  does NOT ship the redaction without the `REDACTED` verify verdict — an erased
  row must never read `CORRUPT` to the operator's own integrity tool.
- **P0-504-6.** Does NOT perform the redaction through `atlas_migrate` /
  BYPASSRLS. The sweep runs as `atlas_erase` under RLS.
- **P0-504-7.** Does NOT embed the erasure-request id (or any subject-derived
  value) in the sentinel.
- **P0-504-8.** Does NOT ship in-place redaction before
  `docs/governance/data-retention.md` §4.5b exists (ADR-0020 follow-up F-1) — a
  merged governance document currently authorizes append-only tombstones and not
  redaction-in-place.

## Dependencies

- **#180** (privacy-module foundation) — `merged`. Provides `subject_module` +
  sibling discipline. **Caveat:** eleven audit-log tables that landed after
  2026-05-20 lack the `subject_module` column (slice-180 drift). Which tables can
  carry AC-4's `subject_module='privacy'` row depends on that resolution —
  tracked as ADR-0020 follow-up F-5.
- **#330** (privacy GDPR/CCPA audit) — `merged`. AC-3 mandated the erasure-design
  ADR; P0-330-4 directs this follow-up.
- **[ADR-0020](../adr/0020-right-to-erasure-vs-append-only-ledger.md)** —
  `Accepted` 2026-07-25. Ratifies the tombstone and fixes the mechanism this
  document now specifies. **Cleared.**
- **ADR-0020 follow-up F-1** — `data-retention.md` §4.5b. **Blocks half (b)**
  (in-place redaction) until it merges.
- **Privacy-v0 greenlight** — pending real-prospect demand (OQ #7). **Hard gate,
  still open.** ADR-0020 explicitly does not clear it.
- **Invariant #2** (append-only ledger) + **canvas §8.4** (audit-period freezing)
  — the constraints the tombstone design reconciles.
- **#505** (DSAR export) — sibling; consumes the `PersonalDataSurfaces` registry
  this slice creates.

## Canvas references

- `Plans/canvas/04-evidence-engine.md` §4.3 (append-only ledger, invariant #2)
- `Plans/canvas/08-audit-workflow.md` §8.4 (audit-period freezing)
- `Plans/canvas/11-open-questions.md` #7 (privacy sibling-module resolution)
- `docs/issues/330-privacy-gdpr-ccpa-audit.md` AC-3 (erasure-design finding)
- `docs/audits/330-privacy-gdpr-ccpa-audit.md` PRIV-2 + §5 (the reconciliation)
- `docs/adr/0012-append-only-evidence-ledger.md` `:60-64` (tombstone pre-authorized)
- `docs/adr/0003-audit-period-freeze-hash-inputs.md` `:32-49` (`frozen_hash` inputs)
- `docs/governance/data-retention.md` §4.5, §6 (disposal methods, legal hold)

## Constitutional invariants honored

- **#2** append-only evidence ledger — tombstone redacts in place, never deletes;
  `atlas_app`'s never-UPDATE/never-DELETE guarantee is untouched verbatim and a
  narrower `atlas_erase` capability is added beside it.
- **#6** RLS tenant isolation — erasure-request table, legal-hold table, erasure
  audit log and the redaction sweep are all tenant-scoped; `atlas_erase` is
  NOBYPASSRLS and the `erasure_update` policy denies on missing GUC.
- **#10 / canvas §8.4** audit-period freezing — `frozen_hash` binds evidence by
  sorted id array, so a redaction preserving `id` and `observed_at` leaves every
  frozen population byte-identical. No re-freeze, no re-signature.
- **AI-assist boundary** — N/A as a generation surface, but `ai_generations`
  (`system_prompt` / `context_inputs` / `raw_draft`) is an in-scope **erasure**
  surface: the persisted prompt corpus is a second copy of the subject's PII.
