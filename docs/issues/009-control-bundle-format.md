# 009 — Control bundle format spec + parser + upload API

**Cluster:** Control-as-code
**Estimate:** 2d
**Type:** AFK

## Narrative

Define and implement the control-as-code bundle format: a YAML manifest declaring metadata (id, SCF anchor, title, description, family, implementation_type, freshness_class, owner_role, applicability_expr, linked_policies), zero or more evidence queries (Rego/SQL/JSON-path expressions over the evidence ledger), an optional manual-evidence schema (for `manual_periodic` or `manual_attested` controls), and a tests directory with fixture evidence + expected pass/fail. Build a parser that validates the bundle against a JSON Schema and a Go-side validator. Build the upload API (`POST /v1/controls:upload-bundle`) that accepts a tarball or directory, validates it, and persists the control. The slice delivers value because anyone can write a YAML control bundle, upload it, and see it appear in the catalog — proving the authoring path.

## Acceptance criteria

- [ ] AC-1: `docs/spec/control-bundle.md` documents the bundle format with at least one full example
- [ ] AC-2: `security-atlas-cli controls validate ./my-control/` reports schema errors clearly
- [ ] AC-3: `security-atlas-cli controls upload ./my-control/` posts the bundle and creates a `controls` row
- [ ] AC-4: A bundle missing required metadata (e.g., `scf_anchor_id`) is rejected at parse with an error pointing at the field
- [ ] AC-5: A bundle whose `applicability_expr` references an unknown scope dimension is rejected
- [ ] AC-6: Re-uploading the same bundle id is a version bump (creates a new control row, supersedes the prior)

## Constitutional invariants honored

- **Invariant 7 (SCF canonical):** every bundle declares its `scf_anchor_id` — controls cannot exist without being anchored
- **Invariant 9 (manual evidence first-class):** manual controls use the same bundle format, distinguished by `implementation_type`

## Canvas references

- `Plans/canvas/04-evidence-engine.md` §4.4 (control-as-code)
- `Plans/canvas/02-primitives.md` §2.1 (Control entity fields)

## Dependencies

- #002

## Anti-criteria (P0)

- Does NOT accept a bundle without an SCF anchor (anchoring is non-negotiable)
- Does NOT permit OSCAL-component-definition import in this slice — that's a v2 extension
- Does NOT execute evidence queries in this slice — only stores them

## Skill mix (3–5)

- YAML + JSON Schema validation
- Go (parser + validator)
- Tarball handling (archive/tar)
- sqlc upsert patterns
- CLI design (cobra subcommands)
