# Controls

<!-- prettier-ignore-start -->
!!! info "What you'll learn"

    - What a Control is in security-atlas (and why automated and manual
      ones are first-class peers)
    - How a Control is shaped — fields, lifecycle, ownership, scope
    - How to view, attest to, and reason about your control set
<!-- prettier-ignore-end -->

A **Control** in security-atlas is a single requirement that produces a
pass / fail / `n/a` / inconclusive state per scope cell, per point in
time. Controls live in the [Unified Control Framework
graph](https://github.com/mgoodric/security-atlas/blob/main/Plans/UCF_GRAPH_MODEL.md):
**one control, N framework satisfactions** — never one row per
framework.

## The shape

| Field                  | What it means                                                                           |
| ---------------------- | --------------------------------------------------------------------------------------- |
| `scf_id`               | Canonical SCF code (e.g. `IAC-01`). The anchor that mappings hang off.                  |
| `title`, `description` | Human-readable.                                                                         |
| `control_family`       | SCF taxonomy: AAA, AST, BCD, CFG, CHG, CLD, CPL, CRY, ...                               |
| `implementation_type`  | `automated` · `semi_automated` · `manual_attested` · `manual_periodic`                  |
| `owner_role`           | Who owns it (RACI). Resolved to a real person via the org's role assignments.           |
| `lifecycle_state`      | `draft` → `proposed` → `active` → `deprecated` → `retired`. Soft-versioned; reversible. |
| `applicability_expr`   | Boolean over scope dimensions — when this control applies.                              |
| `evidence_query_ids[]` | What evidence the platform reads to evaluate this control.                              |
| `policy_ids[]`         | What governance documents reference it.                                                 |

A `manual_attested` control has the same surface as an `automated` one —
lifecycle, ownership, freshness, scope, evidence trail. The only
difference is the evaluation source: an authorized owner uploads the
evidence (a screenshot, a signed PDF, a meeting log) or asserts state
with a digital acknowledgment. **Constitutional invariant 9 — manual
evidence is first-class.**

## Evidence-query languages (control-as-code)

A control bundle's `evidence_queries[]` declares one or more queries the
evaluation engine runs over the control's **in-window evidence records**
(the records inside the control's freshness window). Each query has a
`language`. Three languages are supported:

| `language` | Runs                                                             | A query yields                                                              |
| ---------- | ---------------------------------------------------------------- | --------------------------------------------------------------------------- |
| `rego`     | An OPA policy in a capability-restricted sandbox.                | The policy's `result` assignment (`pass`/`fail`/`na`/`inconclusive`).       |
| `sql`      | A read-only SQL query over a tenant-scoped `evidence` view.      | One column per row: a `pass`/`fail`/`na`/`inconclusive` text, or a boolean. |
| `jsonpath` | A Goessner-dialect JSON-path over each record's JSONB `payload`. | Per-record: a non-empty/truthy match is `pass`, no match is `fail`.         |

When a control declares more than one query, the per-query results roll up
through the standard precedence — **any `fail` → `fail`; else any `pass` →
`pass`; else `inconclusive`; else `na`** — so a mixed-language control is
consistent with a single-language one. A control with **zero** declared
queries falls back to rolling up the raw `result` of its in-window evidence
records.

A query whose language is not one of the three above, or that cannot be
parsed/compiled, **fails loudly**: the control evaluates to `inconclusive`
with the error surfaced and logged. It never silently produces no state.

### The SQL evidence-query sandbox

A `sql` evidence query is **read-only and evidence-only**. It does NOT run
against the live database. It runs against a single read-only relation named
`evidence`, materialised from the control's in-window record set, inside a
`READ ONLY` transaction:

```sql
-- The author SELECT may reference ONLY the `evidence` relation, which exposes:
--   result      text          -- the record's pass/fail/na/inconclusive
--   observed_at timestamptz    -- when the evidence was observed
--   payload     jsonb          -- the record's JSONB payload
SELECT bool_and((payload->>'mfa_enabled')::boolean) FROM evidence;
```

Constraints (all enforced):

- **Single read-only `SELECT`** (or `WITH … SELECT`). Multi-statement input,
  any write/DDL/DML keyword (`INSERT`, `UPDATE`, `DROP`, `COPY`, `SET`,
  `pg_sleep`, …), and any schema-qualified reference (e.g.
  `public.evidence_records`) are rejected before the query runs.
- **No reach beyond `evidence`.** The query runs with an empty `search_path`
  and against a read-only transaction, so it cannot read another tenant's
  evidence, the `users`/`api_keys` tables, or any other relation — only its
  own in-window evidence records. (Constitutional invariants #2 read-only
  evaluation + #6 tenant isolation.)
- **Bounded runtime.** A per-query `statement_timeout` applies; a query that
  exceeds it evaluates to `inconclusive`, never a hang.

The result contract: the query SELECTs exactly one column. A boolean maps
`true`→`pass` / `false`→`fail`; a text value must already be a result
enum member. Zero rows is treated as `fail` (the asserted condition matched
nothing).

### The JSON-path evidence-query surface

A `jsonpath` query is evaluated **in process** against each in-window
record's JSONB `payload` — there is no database reach at all. The path is a
Goessner-dialect expression:

```text
# pass iff the payload reports encryption is on:
$.encrypted
# pass iff at least one check passed:
$.checks[?(@.passed==true)]
```

A record "passes" the query when the path resolves to a present, truthy
value (a non-empty array/object, a non-empty string, a non-zero number, or
`true`); it "fails" when the path matches nothing or a falsy value. The
per-record results roll up through the same precedence as the other
languages. A per-query timeout bounds runaway expressions.

### Not supported (scope boundary)

`sigma` is **not** an evidence-query language. Sigma is detection-as-code
(alerting), a separate concern from control evaluation; it is intentionally
excluded from the evidence-query engine. Enforcement hooks (Kyverno /
Custodian) and a query-authoring UI are likewise out of scope — evidence
queries are authored in the uploaded control-bundle YAML.

## Browsing the control set

Sign in and open **Controls** in the sidebar. The list view shows every
Control in the active tenant, filterable by family, framework
satisfaction, owner, and lifecycle. The hero-dashboard screenshot in the
[README](https://github.com/mgoodric/security-atlas/blob/main/README.md#screenshots)
shows the control browser in context.

Click any row to open the **Control detail** view. From there you can:

- See the framework requirements this control satisfies (SOC 2, ISO
  27001, etc.) — each shown with its STRM relationship type (`equal`,
  `subset_of`, `intersects`, `superset_of`, `no_relationship`).
- See current pass / fail state per scope cell.
- See the evidence query that drives evaluation and the latest matching
  evidence records.
- Open the policy that governs it (if linked).
- Open the risks it treats (if any).

## Attesting to a manual control

Manual controls render the same detail view as automated ones, with one
extra surface: an **Attest** button visible only to the owner role.

1. Open the Control detail view.
2. Click **Attest**.
3. Upload the supporting artifact (PDF / image / log file).
4. Add the attestation narrative (what you confirmed, on what date, against
   what evidence).
5. Submit.

The attestation lands in the evidence ledger as a record with
`source_attribution.actor = user:<your-id>` and `evidence_kind` matching
the manual schema the control declares. After it lands, the control's
`freshness_class` clock resets; the lifecycle clock starts ticking
toward the next required attestation.

## Bulk import via OSCAL

Controls are not authored one-at-a-time for SCF anchors — the SCF
catalog importer (slice 006) seeds the canonical anchor set on first
boot. For framework-specific control sets (the SOC 2 v2017 50-control
kit shipped in slice 010, for example), the platform reads OSCAL
catalogs:

```sh
just atlas-cli catalog import \
  --framework soc2 \
  --version v2017 \
  --catalog ./catalogs/soc2-v2017.oscal.json
```

The importer is idempotent — re-running with the same OSCAL file is a
no-op (content-addressed by sha256 of the catalog). See [Framework
setup](../framework-setup.md) for the end-to-end framework activation
flow.

## What changes when a control retires

`retired` controls remain in the evidence ledger and the audit trail —
they are not deleted. New evidence stops being collected; existing
mappings and historical control state are preserved for point-in-time
replay. Retiring a control does NOT retroactively remove satisfaction
from any frozen [AuditPeriod](../first-audit.md).

This is the practical answer to the "what about the year we DID have
that control?" auditor question — the answer is "open the audit period
from that year and the control state is exactly what it was."

## AI gap explanation (non-binding)

On a control's detail view, when a control is in an evidence-freshness gap
the **Why this state** card can show a plain-language explanation of _why_,
generated by a local model (Ollama, no data leaves your deployment). It
reads the same deterministic freshness facts shown elsewhere on the page and
phrases them in prose, citing the specific control and evidence records it
refers to.

This explanation is a **comprehension aid, not an audit artifact**:

- It is clearly labelled "AI-generated explanation (model X) — not an audit
  artifact" and names the model that produced it.
- It is **never published, approved, or exported** — there is no workflow
  button on it. It is informational, for you, in your own view.
- It is **regenerated on demand** and never stored. Refreshing the page
  re-derives it; it is not a record.
- Every id it cites is verified to be one of _your_ records before you see
  it. If any citation cannot be verified, the explanation is withheld and you
  see the deterministic freshness facts on their own — the platform never
  shows you an explanation that references coverage it cannot confirm.

The underlying freshness/evidence facts (the rollup) always render whether or
not the AI explanation is available.

## Next steps

- [Risks →](risks.md) — what controls treat
- [Evidence →](evidence.md) — what controls read
- [Policy →](policy.md) — what controls implement
- [Scope →](scope.md) — where controls apply
- [Framework →](framework.md) — what controls satisfy

---

## Was this helpful?

Tell us in [GitHub
Discussions](https://github.com/mgoodric/security-atlas/discussions).
Page issues can also be filed via the **Edit this page** link above.
