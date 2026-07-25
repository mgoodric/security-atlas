# ADR 0020 — Right to erasure vs. the append-only evidence ledger

**Status:** Accepted

**Date:** 2026-07-25

**Decides:** GDPR Art. 17 / CCPA §1798.105 reconciliation with constitutional
invariant #2 (append-only evidence ledger) and invariant #10 (audit-period
freezing).

**Resolves:** [`docs/issues/330-privacy-gdpr-ccpa-audit.md`](../issues/330-privacy-gdpr-ccpa-audit.md)
AC-3 ("**Load-bearing finding: right-to-erasure design**").

**Audit basis:** [`docs/audits/330-privacy-gdpr-ccpa-audit.md`](../audits/330-privacy-gdpr-ccpa-audit.md)
finding PRIV-2 (Critical) + §5 (the erasure-vs-append-only reconciliation).

**Implements through:** [`docs/issues/504-privacy-v0-right-to-erasure.md`](../issues/504-privacy-v0-right-to-erasure.md)

**Supersedes the slice doc's process note.** `docs/issues/330-...md` "Notes for
the implementing agent" instructed the auditing agent to survey the three
candidate designs and **not** pick one, deferring the choice to a follow-up
slice. That instruction is overridden: slice 504 was written assuming this ADR
already existed and cites it as a hard gate (`504:138` AC-7, `504:149`
P0-504-4), so a survey would leave 504 blocked on a document that by
construction never arrives. This ADR decides.

---

## Context

The platform ingests structured personal data about **third-party data
subjects** — the operator's employees and contractors, pulled through the HRIS,
Okta, MDM, GitHub and Slack connectors — into an append-only ledger. These
people have no account, no login, and no visibility into the system holding
their work email, employment status, hire date and termination date
(`internal/api/schemaregistry/schemas/hris.worker_lifecycle/1.0.0.json`,
`.../okta.user_lifecycle/1.0.0.json`).

Their Art. 17 right is unambiguous. The platform's capability is nil:

- `evidence_records` has RLS policies for SELECT and INSERT only, under `FORCE
ROW LEVEL SECURITY`, with a verbatim comment stating the omission is
  deliberate (`migrations/sql/20260511000004_evidence_ledger.sql:100-113`).
- The only user-deletion verb in the codebase is contractually forbidden from
  deleting (`internal/scim/store.go:343-346`, anti-criterion P0-508-1).
- No `DELETE FROM users` exists in `internal/db/queries/` — the sole DML source
  under sqlc.

The tension is real and is the whole point of this ADR: **invariant #2 must not
be weakened to make erasure easier.** An operator's only current path is raw SQL
on the BYPASSRLS `atlas_migrate` DSN, which silently breaks the per-record
content commitment with no audit trail — the worst of every available world.

### Three ratified anchors, established before deciding

The reconciliation does not require reinterpreting invariant #2. Three existing
documents already constrain the answer:

1. **Tombstone disposal is pre-authorized by the invariant's own ADR.**
   [`ADR-0012`](0012-append-only-evidence-ledger.md)`:60-64` — "(Disposal, when
   it comes, is a tombstone, not a mutation — see the data-retention policy; the
   invariant constrains it to tombstones-only.)" `:112-115` accepts the cost
   explicitly: "the data-retention policy is therefore tombstone-based rather
   than row-deleting, which is a more complex erasure story than `DELETE`."
   Invariant #2's own text scopes itself to the **evaluation stage** and the
   application role — "Evaluation never writes to source-of-truth evidence." The
   erasure verb is neither ingestion nor evaluation.
2. **Field-level redaction provably cannot disturb a frozen period.**
   [`ADR-0003`](0003-audit-period-freeze-hash-inputs.md)`:32-49` fixes
   `frozen_hash` over content-only inputs that bind evidence **by sorted id
   array** (`evidence_record_ids`), never by payload content; `frozen_at` and
   `frozen_by` are explicitly excluded. A redaction that preserves `id` and
   `observed_at` therefore leaves `frozen_hash` **byte-identical**. Invariant
   #10 survives with no re-freeze, no re-signature, and no auditor-visible
   population shift.
3. **Row deletion was already foreclosed at the DB layer.**
   `sample_evidence.evidence_record_id` and
   `sample_annotations.evidence_record_id` both carry `REFERENCES
evidence_records(id) ON DELETE RESTRICT`
   (`migrations/sql/20260511000010_audit_samples.sql:151-153`, `:176-178`),
   whose comment states the intent: "Stored explicitly so a re-audit returns the
   same records regardless of any subsequent population mutations." Any evidence
   row ever drawn into an auditor sample is undeletable regardless of role.

---

## Decision

### 1. The design: tombstone — field-level redaction-in-place, row retained, never deleted

**Tombstone is the single default.** Not pseudonymisation, not
refuse-with-explanation. An erasure request redacts personal-data fields in
place, retains the row, its identity and its temporal anchors, and appends a
separate immutable erasure record.

**Why not pseudonymisation.** Recital 26 treats pseudonymised data as **still
personal data**, so it does not discharge Art. 17. It also requires a stable
keyed mapping and the schema has no key-management surface for one
(`internal/auth/keystore` is JWT-signing specific). Pseudonymisation **is** the
right primitive for slice 505's subject-correlation problem — a stable subject
hash to join across surfaces without materialising the identifier — which is a
different job with a different ADR.

**Why refusal cannot be the default.** Art. 17(3)(b) and 17(3)(e) are genuine
carve-outs, but they authorize retaining **the specific data necessary** for the
carve-out. They do not authorize declining the request. As a blanket posture,
refusal would be unlawful. It survives as a narrow per-record branch (§4).

### 2. The tombstone has two halves, and both are mandatory

This is the trap the audit's first pass conflated and slice 504 currently gets
wrong. ADR-0012 says "a tombstone, **not a mutation**," and
`docs/governance/data-retention.md` §4.5 (`:511-520`) specifies the procedure as
an **append**: "The original record is never deleted"; "A tombstone record is
**appended** that names the original." §6 (`:645-646`) reinforces it:
"tombstones are **forward-only**."

Slice 504's tombstone is _redact-in-place_ — which is exactly a mutation, and
which §4.5 does **not** authorize. Two mechanisms wear one word. Therefore an
erasure writes **both**:

| Half                             | What it does                                                                                                                                                                       | Satisfies                                                                                                        |
| -------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------- |
| **(a) Appended erasure record**  | A new row in `privacy.erasure_audit_log` naming the affected record by primary key, the lawful request, the basis, the sentinel version, and the `(id, hash, erasure_hash)` triple | ADR-0012's "tombstone, not a mutation"; data-retention §4.5 append-only-with-supersede; §6 legal-hold vocabulary |
| **(b) In-place field redaction** | Personal-data columns/JSONB paths overwritten with the sentinel, under a column allow-list                                                                                         | Art. 17's actual requirement — the data must stop being there                                                    |

**Governance dependency this ADR owns, not slice 504.**
`docs/governance/data-retention.md` needs a new **§4.5b — "Ledger field-level
erasure (redaction-in-place under a recorded lawful request)"** as a sixth
disposal method, bounded to allow-listed personal-data columns, plus the
`:109-110` correction (PRIV-2). Until §4.5b exists, half (b) is forbidden by a
merged governance document. Filed as OE follow-up F-1.

### 3. Three mechanisms by column shape — and the UUID insight

Slice 504's narrative implies a per-surface sweep across ~40 tables. The actual
work is far smaller, and the reduction is the single most useful thing this ADR
tells 504:

| Column shape                                                                                                                                                                     | Mechanism                                                                                                                                                                        |
| -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| JSONB payload keys — `evidence_records.payload` / `source_attribution` / `provenance`, `ai_generations.context_inputs`, `board_narrative_sections.*`                             | Path-based redaction via the **existing** `internal/evidence/redact` package                                                                                                     |
| Unreferenced `TEXT` actor columns — `actor`, `*_by`, `authored_by`, `owner_user`, `reviewer`, `treatment_owner`, `hostname`                                                      | Per-row sentinel overwrite. Zero referential fallout: `20260511000030_decisions_audit.sql` states the intent — no FK "because the audit trail must survive a future hard-delete" |
| `UUID` actor columns — `action_plan_audit_log.actor_id`, `control_owner_assignment_audit_log.actor_user_id`, `policy_acknowledgments.user_id`, `action_plans.owner_id`, ~20 more | **Do not touch.** Erase the _referent_ once — `users.email`, `display_name`, `idp_issuer`, `idp_subject` → sentinel — leaving an opaque, non-resolving UUID                      |

The UUID branch is legally sufficient: Art. 17 requires erasure of data by which
the subject is identifiable, not destruction of an opaque key that no longer
resolves. **One `UPDATE users` discharges every UUID-referencing audit row
simultaneously.** A UUID column cannot hold a string sentinel anyway.

**Mandatory Recital-26 companion.** The key must be _genuinely_
non-attributable afterwards, so the same transaction must clear
`local_credentials`, `sessions` (including `user_agent`, `ip_address`,
`geo_country`, `geo_city`), `oauth_token_exchanges.subject_token_sub`,
`oauth_auth_codes`, `oauth_device_codes.approved_by_idp_subject`, and
`scim_credentials` / `scim_audit_log.detail`. `sessions` and `local_credentials`
already declare `ON DELETE CASCADE` from `users` — the cascade was designed and
no code was ever written to trigger it.

**Reuse the existing redactor; do not write a second one.**
`internal/evidence/redact/redact.go` already implements the needed primitive: a
narrow JSONPath subset (`$.field`, `$.a.b.c`, `$.arr[*].field` — grammar at
`:117-165`) over a `structpb.Struct`, replacing matched leaves with a literal
marker (`:36`, `:170-191`), pure and non-logging. Slice 015 runs it at
ingestion; **erasure is the same function run late.** The per-kind path list
lives beside `x-redaction-rules` as a new `x-personal-data-paths` key in the
schema-registry JSONB entry (additive, no migration, same mechanism as
`ExtractRulesFromSchema` `:77-104`). One artifact then serves ingestion
redaction, erasure, and slice 505's DSAR correlation — which is also the
`privacy.PersonalDataSurfaces` registry 504 and 505 are required to share.

### 4. What is preserved — the invariant-#10 contract

**Immutable under erasure, always:** `id`, `tenant_id`, `observed_at`,
`observed_at_nanos`, and row existence.

**Additionally preserved** for auditor legibility and evaluation replay:
`control_id`, `control_ref`, `scope_id`, `scope_canonical`, `result`,
`evidence_kind`, `schema_version`, `ingested_at`, `ingestion_path`,
`credential_id`, `idempotency_key`, `payload_uri`.

Enforced two ways, belt and suspenders: the UPDATE names only allow-listed
columns, **and** a column-level GRANT makes the allow-list a database privilege
rather than a code convention (§6).

Per anchor 2 above, this leaves every `frozen_hash` valid and every frozen
sample population stable. The sample is still drawn, still counted, still cited.

### 5. The content hash: keep it immutable, add a third verify state

**There is no hash chain.** `evidence_records.hash` is a standalone per-record
sha256 over canonical JSON of the payload
(`migrations/sql/20260511000000_init.sql:239`, via
`canonjson.HashRecord`); `idx_evidence_hash` is non-unique and Merkle linkage
was explicitly rejected for v1 (ADR-0003 alternative 3). Redacting record X has
**zero** effect on record Y. Slice 504's "content-hash chain" vocabulary
(`504:40`, AC-2 `:128`, P0-504-1 `:144-145`) overstates the coupling and must be
corrected to **per-record content commitment**, or AC-2's integration test will
assert a structure that does not exist.

**`hash` is NOT recomputed.** Rewriting it would make the redaction invisible to
anyone holding the ingest `Receipt.Hash` already returned to the connector
(`internal/evidence/streambuf/streambuf.go:345`) and to any external party
composing per-record hashes with `frozen_hash`. Silent redaction is the one
property ADR-0012 and this ADR agree is unacceptable.

**Add to `evidence_records`:** `erased_at TIMESTAMPTZ NULL`,
`erasure_request_id UUID NULL`, `erasure_hash TEXT NULL` (sha256 of the
post-redaction reconstruction), `erasure_sentinel_version TEXT NULL`.

**Extend `VerifyLedgerRow` to three values** (`internal/evidence/ingest/verify.go`):

| Verdict    | Condition                                                                                                     |
| ---------- | ------------------------------------------------------------------------------------------------------------- |
| `CLEAN`    | recomputed == `hash`                                                                                          |
| `REDACTED` | `erased_at IS NOT NULL` **and** recomputed == `erasure_hash` — lawful, explained, re-verifiable going forward |
| `CORRUPT`  | everything else                                                                                               |

Without this, every erased row reads **CORRUPT** to the operator's own integrity
tool — `verify.go:44-46` says so in writing: "The corruption AC-3 introduces
(mutating the `payload` column in place) still changes the recomputed hash and
is reported." Slice 504 does not mention `verify.go` at all. The CLI must print
the erasure-request id on a `REDACTED` row so the divergence self-documents.

This is why tamper-evidence is **preserved and refined**, not weakened: an
unexplained divergence is still `CORRUPT`; an explained one carries its own
recorded lawful basis.

**Already-signed export bundles: nothing can be done, and one thing must be.**
`internal/oscal/sign.go:142-153` computes the bundle digest over the bundle's
own member bytes, so a later ledger redaction neither invalidates nor reaches a
signed bundle. It verifies forever, and it permanently contains the subject's
PII. Erasure is therefore **incomplete by construction for distributed copies** —
unavoidable, not a bug. That converts the residue into a **notification**
obligation (Art. 17(2); CCPA §1798.105(c)(3)) which the platform cannot
currently discharge: export events record no record-id set
(`migrations/sql/20260519000000_audit_periods_vendors_export.sql:54-56`). Slice
504 must either capture an export manifest (record-id set + recipient +
`exported_at`) or emit a best-effort export-recipient notification worklist from
the `me_audit_log` export rows. It may not leave this unstated.

### 6. The sentinel: `<<ERASED>>`, request-invariant

**Decided value: `<<ERASED>>`.**

- **It satisfies every CHECK.** Thirteen `*_nonempty` constraints
  (`CHECK (length(col) > 0)`) exist across the schema — verified individually,
  from `artifact_access_log_actor_nonempty` (`20260511000008_artifacts.sql:131`)
  through `scim_group_members_user_id_nonempty`
  (`20260612040000_scim_groups.sql:119`). **The empty string is not a viable
  sentinel** and this is the non-obvious constraint 504 must carry.
- **It reuses an established envelope but not the same token.**
  `redact.Marker = "<<REDACTED>>"` (`redact.go:36`) already exists. Same
  `<<…>>` family so it reads as the same mechanism; a **different** token so an
  ingestion-time schema redaction (no lawful request, applied to every record of
  that kind) is never confused with a post-hoc erasure (one subject, one
  recorded request, one lawful basis). Conflating them would destroy the audit
  story for both.
- **It must be request-invariant.** Slice 504's proposed
  `[redacted:ER-NNN]` (`504:108`) is **rejected**. Embedding the request id in
  the field value makes the sentinel a cross-surface correlator: anyone with
  ordinary read access can `SELECT` for `ER-042` and reconstruct the complete
  footprint of a supposedly-erased subject across all ~40 surfaces. Under
  Recital 26 that residual linkability is arguably itself personal data — the
  design would reintroduce exactly the identifiability it exists to remove. It
  also defeats a cheap `= '<<ERASED>>'` completeness assertion. The linkage
  belongs in `evidence_records.erasure_request_id` and the erasure audit row,
  both access-controlled.
- It is **not** a hash or cipher of the original. Slice 504's AC-6 gets this
  right and stands unchanged.
- Version it via `erasure_sentinel_version` so a future change is detectable
  rather than archaeological.

### 7. Authorization: a new `atlas_erase` role — the move that keeps invariant #2 intact

Four parts, three of which slice 504 lacks:

1. **Tenant `admin` only.** Slice 504 AC-5 / `cred.IsAdmin` is correct as a
   floor and stands.
2. **Two-verb separation of duties.** Slice 504's threat model S correctly names
   weaponised erasure — "erase the CISO's audit trail" (`504:86-87`) — then
   mitigates it with `IsAdmin`, the very privilege the attacker holds. Split it:
   verb 1 _records_ the request (`privacy.erasure_requests` INSERT: subject
   identifier, out-of-band identity-verification attestation, `regime`); verb 2
   _executes_ against a request id in a separate call. Where the subject
   resolves to a `users` row holding `admin` or `auditor`, a table CHECK
   requires `confirmed_by <> requested_by` — a second distinct admin.
3. **`super_admin` is explicitly denied.** `super_admins` is the cross-tenant
   surface (`migrations/sql/20260521030000_super_admins_full.sql`). A
   cross-tenant erasure verb is a catastrophic primitive with no lawful use:
   erasure is per-controller, per-tenant. Naming the denial here is deliberate —
   it is not obvious, and it would otherwise be added later "for convenience."
4. **A dedicated DB role `atlas_erase`, not `atlas_migrate`.** The audit's
   implied path (privileged BYPASSRLS SQL on the migrate DSN) is the worst
   option: unbounded, unaudited, untyped. Instead:
   - `atlas_erase`, **NOBYPASSRLS**.
   - `GRANT UPDATE (payload, source_attribution, erased_at, erasure_request_id,
erasure_hash, erasure_sentinel_version) ON evidence_records TO atlas_erase`
     — the allow-list becomes a database privilege, so a code bug cannot reach
     `observed_at` or `id`. Column-level GRANT precedent already exists:
     `GRANT UPDATE (status) ON framework_versions TO atlas_app`
     (`20260612090000_framework_versioning.sql:263`).
   - A new `erasure_update` policy on `evidence_records` `FOR UPDATE`, with
     `USING` / `WITH CHECK` requiring `current_tenant_matches(tenant_id)` **and**
     a session GUC `app.erasure_request_id` resolving to a request row in status
     `executing`. No GUC ⇒ no rows ⇒ deny, mirroring invariant #6's
     RLS-denies-on-missing-context posture.

**`atlas_app` gains nothing.** The guarantee at
`20260511000004_evidence_ledger.sql:110-113` — atlas_app can never UPDATE or
DELETE `evidence_records` — is untouched, verbatim. **Invariant #2 is not
weakened; a separate, narrower, separately-credentialed, GUC-gated capability is
added beside it.** That distinction is the entire reconciliation.

**The erasure audit log needs the trigger, not just RLS.** Slice 504 AC-4 asks
for "append-only … (four-policy RLS, slice 036 pattern)", which is **role-scoped
only** and does not bind a privileged connection. `privacy.erasure_audit_log` is
the sole surviving evidence that the erased data existed and what became of it,
written by the most privileged verb in the product. It must adopt the
`action_plan_audit_log` pattern verbatim: a `BEFORE UPDATE OR DELETE … FOR EACH
ROW` trigger raising `restrict_violation`
(`20260612070000_action_plans.sql:345-356`) — the only unconditional
UPDATE-and-DELETE trigger in the schema, with two further conditional BYPASSRLS-
surviving precedents (`board_packs:183`, `framework_requirements:254`). It
carries `subject_module = 'privacy'` per slice 180.

### 8. Refusal is a per-record deferral, never a per-request refusal

Erase everywhere **except** rows matching the predicate; those get a `retained`
disposition with a recorded basis and a `retain_until` date equal to the end of
the carve-out; a scheduled re-evaluation completes the erasure when
`retain_until` passes.

**Operative GDPR carve-outs — only two.** Art. 17(3)(b) (legal obligation) and
Art. 17(3)(e) (establishment, exercise or defence of legal claims). **Art.
17(3)(d)** (archiving/research in the public interest) is a stretch for
commercial GRC evidence and is **explicitly rejected** here so nobody reaches
for it later.

**The scoping predicate.** Not `audit_periods.frozen_at` alone — a frozen period
can span months of untouched evidence, and "frozen ⇒ retained" is precisely the
unlawful blanket. Use the narrower conjunction of _sample membership_ and a
frozen owning period:

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
`20260511000020_audit_periods.sql:66-102`). This is the strongest available
predicate because sample membership is exactly where erasure would prejudice the
defence of a legal claim — the audit opinion — and it is per-record and
time-bounded, which is what 17(3)(e) requires. It is also the same set the DB
already protects with `ON DELETE RESTRICT`.

**The legal-hold leg does not exist and must be built.** Grep for `legal_hold`
across `migrations/sql/`: **zero matches.**
`docs/governance/data-retention.md` §6 defines legal hold as a manual maintainer
process, and the tracking file it points at (`docs/governance/legal-holds.md`)
does not exist — the reference is forward-only. Slice 504's refusal path cites
"e.g. a legal-hold obligation" (`504:64`) with nothing to key off. **Decision:**
slice 504 ships a minimal `privacy.erasure_legal_holds` (`tenant_id`,
`hold_ref` matching the `HOLD-NN` shape at data-retention `:678-680`,
`scope_predicate`, `opened_at`, `opened_by`, `basis`, `released_at`), and a
refusal citing a hold must reference an open row. A refusal citing a document
that does not exist is not audit-defensible.

### 9. CCPA/CPRA deltas that change the design

| Axis                            | GDPR Art. 17                                                                                                                                                    | CCPA/CPRA §1798.105                                                                                                                                              | Design consequence                                                                                                                                                                                                                         |
| ------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| **Security-evidence retention** | No carve-out. Recital 49 supports network security as an Art. 6(1)(f) legitimate interest, but Art. 17(1)(c) then permits objection and forces a balancing test | **(d)(2)** retain to "detect security incidents, protect against malicious, deceptive, fraudulent, or illegal activity"; **(d)(7)** solely-internal aligned uses | **The biggest divergence.** A security-control evidence ledger is near the paradigm case for (d)(2). CCPA offers a **category-level** basis; GDPR only a **per-record** one. A single global refusal predicate is wrong in both directions |
| Legal obligation                | Art. 17(3)(b)                                                                                                                                                   | §1798.105(d)(8)                                                                                                                                                  | Symmetric — the one place a shared basis token is safe                                                                                                                                                                                     |
| Legal claims                    | Art. 17(3)(e)                                                                                                                                                   | No direct analogue                                                                                                                                               | GDPR-only branch                                                                                                                                                                                                                           |
| **Downstream deletion**         | Art. 17(2) — "reasonable steps to inform," qualified by available technology and cost                                                                           | **§1798.105(c)(3)** — affirmatively **notify** service providers, contractors and third parties to delete, unqualified                                           | CCPA's is stronger. Bites on `tenant_llm_routing` (provider ∈ anthropic/openai/bedrock) and on the export-bundle residue of §5                                                                                                             |
| Deadline                        | Art. 12(3): 1 month, +2 extendable                                                                                                                              | 45 days, +45 extendable                                                                                                                                          | **Two SLA clocks**, derived from `regime`                                                                                                                                                                                                  |
| Request record-keeping          | Art. 5(2), implied                                                                                                                                              | CCPA regs: retain request records **24 months**                                                                                                                  | An express minimum retention _for the erasure-request row itself_, which the sweep must not erase                                                                                                                                          |

**Three concrete consequences:**

1. **The refusal basis is a closed `(regime, provision)` pair, not free text.**
   Slice 504's nullable free-text column (`504:47`) is rejected. Use `regime
TEXT CHECK (regime IN ('gdpr','ccpa','both'))` and `refusal_basis TEXT CHECK
(refusal_basis IN ('gdpr_17_3_b','gdpr_17_3_e','ccpa_105_d_1','ccpa_105_d_2',
'ccpa_105_d_7','ccpa_105_d_8'))`. The gating predicate then differs by regime:
   GDPR takes the narrow `sample_evidence` conjunction of §8; CCPA may
   additionally assert (d)(2) at the evidence-kind category level. Closed enums
   also give the DPO a countable register.
2. **The sweep must include the AI surfaces.** Slice 504's surface list
   (`504:50-52`) names board-narrative author fields but omits
   `ai_generations.system_prompt`, `.context_inputs` and `.raw_draft`
   (`20260607000000_ai_generations.sql`). Per `CLAUDE.md` board-narrative D1/D3,
   the prompt corpus contains **cited evidence excerpts for every claim** and
   the full prompt is persisted every time — a second, independent copy of the
   subject's PII. Its existence plus a current-or-historical cloud routing row
   is what triggers the §1798.105(c)(3) service-provider notification.
3. **Two due-date computations** on the request row, derived from `regime`, plus
   a 24-month retention floor on the request row itself.

---

## Consequences

### What this unblocks

Slice 504's hard gate (`504:138` AC-7, `504:149` P0-504-4) is satisfied on the
**design** half. The tombstone design 504 assumed is **confirmed** — no pivot,
`P0-504-1` stands unchanged. Slices 505 (DSAR) and 506 (RoPA) cite this audit
as their originating dependency and are likewise no longer resting on a
non-existent artifact.

**Slice 504 is not, however, ready to build as written.** §2, §3, §5, §6, §7,
§8 and §9 amend the mechanism in seven ways, four of which change the migration:

| #   | Amendment                                                                                                                                                                                                                                                                                                             | Changes                      |
| --- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ---------------------------- |
| 1   | `hash` not recomputed; `erased_at` / `erasure_hash` / `erasure_request_id` / `erasure_sentinel_version` columns; `verify.go` gains a `REDACTED` verdict                                                                                                                                                               | Migration + CLI              |
| 2   | New `atlas_erase` role + `erasure_update` policy + column-level GRANT                                                                                                                                                                                                                                                 | Migration                    |
| 3   | `privacy.erasure_audit_log` needs the immutability **trigger**, not just RLS                                                                                                                                                                                                                                          | Migration                    |
| 4   | `privacy.erasure_legal_holds` must be built                                                                                                                                                                                                                                                                           | Migration                    |
| 5   | Three-mechanism split + the UUID-referent insight + Recital-26 companion clearing                                                                                                                                                                                                                                     | Spec (large scope reduction) |
| 6   | Four spec corrections: "content-hash chain" → per-record commitment; AC-3-vs-threat-model-T contradiction resolved as _redacted and flagged_; sentinel `<<ERASED>>` not `[redacted:ER-NNN]`; status enum gains `completed_partial` + per-surface disposition with `retain_until` + closed `(regime, provision)` enums | Spec                         |
| 7   | Add `ai_generations` surfaces; CCPA §1798.105(c)(3) notification worklist; export-manifest gap named                                                                                                                                                                                                                  | Spec                         |

On amendment 6's contradiction: slice 504 AC-3 (`:129-132`) says a
frozen-period redaction is "redacted but flagged"; threat model T (`:98-99`)
says such a redaction is "**rejected**." Per ADR-0003, **redacted and flagged**
is correct — `frozen_hash` is provably unaffected — so `redacted_under_freeze`
survives as an **auditor-legibility marker, explicitly not an integrity
mechanism.** Say so in the slice, or the next reader will believe the flag is
load-bearing for the hash.

### Accepted costs

- **Erasure is incomplete for distributed copies.** Already-exported,
  cosign-signed bundles retain the PII forever (§5). Unavoidable; converted into
  a notification obligation.
- **A second privileged DB role to operate.** `atlas_erase` is a new
  credential-management surface for the self-host operator. Accepted: the
  alternative is raw `atlas_migrate` SQL, which is strictly worse.
- **Redaction is detectable, not silent.** An erased row's stored `hash` no
  longer matches its payload. Deliberate — detectability is the property that
  makes the reconciliation honest.
- **Two regimes, two clocks, two predicates.** More branching than a single
  global policy. Accepted: the GDPR/CCPA divergence on security-evidence
  retention is real and a unified predicate would be wrong under both.

### What this does NOT decide

- **Open question #7 (privacy-module shape).** Not reopened. The sibling
  resolution stands. This ADR does not greenlight privacy-v0 and does not
  un-gate slice 504's _demand_ trigger — it only removes the _design_ blocker.
  Re-gating is a maintainer call, surfaced in the audit's PRIV-2, not decided
  here.
- **Open question #10 (breach-notification workflow shape).** Untouched. Stays
  open per the audit's AC-8 / P0-330-3.
- **Open question #5 (hosted offering).** Untouched. If a hosted offering ever
  fires, the project becomes an Art. 28 processor and owes DSAR/erasure
  assistance obligations this ADR does not address.
- **`tenants.controller_role`.** The audit's PRIV-6 notes the schema cannot
  record which tenants are controller-side and which processor-side, which
  matters because an erasure request routed to a processor-tenant must be
  **forwarded to the controller**, not actioned locally. Recorded as a design
  input to privacy-v0; adding a role column with no workflow to consume it is
  the speculative schema slice 180's own P0-180-1 rejected.

## Alternatives considered

**Hard-delete the row.** Rejected: violates invariant #2 outright, and is
already foreclosed at the DB layer by `sample_evidence`'s `ON DELETE RESTRICT`.

**Pseudonymise as the erasure answer.** Rejected: Recital 26 treats
pseudonymised data as still personal, so it does not discharge Art. 17. Retained
as the correct primitive for slice 505's correlation problem.

**Refuse-with-explanation as the default.** Rejected: Art. 17(3) authorizes
retaining necessary data, not declining requests. Blanket refusal would be
unlawful. Retained as a scoped per-record deferral (§8).

**Redact via `atlas_migrate` BYPASSRLS, no new role.** Rejected: unbounded,
unaudited, untyped, and it would make the most dangerous verb in the product
indistinguishable from a schema migration.

**Recompute `hash` after redaction.** Rejected: makes redaction invisible to any
holder of the original ingest receipt. Silent redaction is the one thing ADR-0012
and this ADR agree must not happen.

**Empty string as the sentinel.** Rejected: thirteen `*_nonempty` CHECK
constraints would reject it.

**Append-only erasure record without in-place redaction.** Rejected: satisfies
data-retention §4.5's letter while leaving the personal data exactly where it
was. Does not discharge Art. 17 at all.
