# 019 — Risk CRUD + NIST 800-30 default + 5x5 + ALE-band + methodology pluggable

**Cluster:** Risk register
**Estimate:** 2d
**Type:** AFK

## Narrative

Implement the risk register with a pluggable methodology surface. Each `Risk` row has a `methodology` enum (`nist_800_30` | `fair` | `cis_ram` | `iso_27005` | `qualitative_5x5`) and a methodology-specific `inherent_score` JSONB. Default methodology is `nist_800_30` (with 5x5 likelihood × impact + dollar-banded impact). FAIR is supported for the top 3-5 risks the board cares about. Treatment status enum (`accept` | `mitigate` | `transfer` | `avoid`) with validation rules per status (e.g., `mitigate` requires ≥1 linked control). The slice delivers value because users can record real risks with auditor-trusted methodology metadata.

## Acceptance criteria

- [ ] AC-1: `POST /v1/risks` creates a risk with required fields; methodology defaults to `nist_800_30` if omitted
- [ ] AC-2: `inherent_score` JSONB validated against methodology-specific JSON Schema (NIST: likelihood + impact 1-5; FAIR: LEF + LM)
- [ ] AC-3: `treatment=mitigate` cannot be set without at least one `linked_control_id` (validation rejects)
- [ ] AC-4: `treatment=accept` requires `accepted_until` date + `accepter` user
- [ ] AC-5: `treatment=transfer` requires `instrument_reference` (e.g., policy number, SOW reference)
- [ ] AC-6: `GET /v1/risks` lists with filter by treatment, category, methodology
- [ ] AC-7: 5x5 dashboard view at `GET /v1/risks/heatmap` returns the risk grid for the qualitative methodology

## Constitutional invariants honored

- **Invariant 6 (RLS):** risk rows tenant-scoped
- Risk methodology pluggable (per canvas decision); no global override

## Canvas references

- `Plans/canvas/02-primitives.md` §2.2 (Risk entity table)
- `Plans/canvas/06-risk.md` §6.1 (treatment statuses)

## Dependencies

- #002

## Anti-criteria (P0)

- Does NOT enforce a single methodology globally
- Does NOT permit invalid treatment transitions (validation must enforce)
- Does NOT skip methodology-specific score validation

## Skill mix (3–5)

- Go CRUD handlers
- JSON Schema per methodology
- sqlc + JSONB queries
- Validation rule engine
- Postgres check constraints
