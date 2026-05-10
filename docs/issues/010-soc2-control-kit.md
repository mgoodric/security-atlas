# 010 — SCF-anchored control kit (50 SOC 2 controls bundled)

**Cluster:** Control-as-code
**Estimate:** 3d
**Type:** HITL

## Narrative

Author the v1 stock control kit: 50 SOC 2 controls expressed in the bundle format from slice 009, each anchored to an SCF concept and mapped via slice 007's crosswalk to the relevant TSC criterion. ~25-30 of them include automated evidence queries that target AWS / GitHub / Okta / 1Password / osquery / Jira data (consumed by slices 044-049 connectors). The rest are `manual_periodic` or `manual_attested`. AWS-specific evidence queries (beyond slice 004's S3 encryption) are absorbed here per the resolution from the gate. HITL: an experienced GRC engineer should review the control text and evidence queries for accuracy before merge. The slice delivers value because a fresh deploy now ships with a usable SOC 2 control library.

## Acceptance criteria

- [ ] AC-1: `connectors/` and `controls/` directories contain 50 control bundles validated by slice 009's parser
- [ ] AC-2: Each control is anchored to an SCF concept; ≥50% have at least one automated evidence query
- [ ] AC-3: SOC 2 TSC requirements are collectively satisfied at ≥80% coverage when controls have passing evidence (verified by slice 008's traversal query)
- [ ] AC-4: Each control specifies `owner_role` and `freshness_class`
- [ ] AC-5: Manual controls (~25) have an `attestation_schema` defining what the attestation captures
- [ ] AC-6: Review log at `docs/audit-log/control-kit-review.md` lists reviewer + date for the bundle

## Constitutional invariants honored

- **Invariant 1 (one control, N satisfactions):** controls anchor through SCF; SOC 2 satisfaction is derived via slice 008's traversal, not duplicated
- **Invariant 7 (SCF canonical):** every control anchored
- **Invariant 9 (manual evidence first-class):** manual controls have full lifecycle metadata, not stub
- **Anti-pattern rejected (policy templates dressed as features):** these are real controls with real evidence queries — not placeholder docs

## Canvas references

- `Plans/canvas/10-roadmap.md` §10.1 ("Control-as-code: Authoring kit + ~50 SOC 2 controls bundled")
- `Plans/canvas/04-evidence-engine.md` §4.4–4.5

## Dependencies

- #009, #007

## Anti-criteria (P0)

- Does NOT use generic placeholder text — every control must reflect actual SOC 2 requirements
- Does NOT skip HITL review
- Does NOT exceed the 50-control budget (extra controls slow down the bundle review)

## Skill mix (3–5)

- SOC 2 TSC domain knowledge
- YAML authoring
- Rego / SQL for evidence queries
- HITL review coordination
- SCF anchor lookup
