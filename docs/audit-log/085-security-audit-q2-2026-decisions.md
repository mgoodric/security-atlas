# Decisions log — Slice 085 (Security audit Q2 2026)

Slice ships the Q2 2026 first-pass security audit + tracks remediation. ACs 1-3 already shipped in PR #167 (commit `9f1a56f`); this slice closes AC-4 (README "Security" section) + AC-5 (CI green).

## Build-time judgment calls

### D1 — README "Security" section placement: before Contributing (high confidence)

**Decision:** insert the new `## Security` section between `## Documentation` and `## Contributing`.

**Alternatives considered:**

- After Contributing — would put it adjacent to License, semantically wrong (Security is upstream of contribution mechanics).
- As a subsection of Contributing — buries it; security is a first-class topic per the constitutional principles.
- Replacing the existing one-line reference under Contributing — loses the line's specific "do not open a public issue" warning, which serves a different (reactive) purpose than the Security section's discovery surface.

**Rationale:** the existing one-line under Contributing stays (it's a tactical "don't do X" callout for the would-be reporter), and the new `## Security` section becomes the discovery surface for: SECURITY.md (the policy), pipeline hardening, audit reports, audit cadence, remediation tracking. Both serve different reader intents.

### D2 — audit cadence wording: "quarterly + after major auth/middleware changes" (high confidence)

**Decision:** explicit cadence statement: "quarterly first-pass review, plus an additional audit after any major change to authentication, authorization, middleware, or evidence-ingestion code paths."

**Alternatives considered:**

- Generic "regular audits" — too soft; doesn't bind future maintainers to a cadence.
- "After every major release" — too rigid; minor releases can introduce material auth changes.
- Add a specific frequency to evidence-ingestion (e.g., "after every new connector") — overshoots; connectors share enough code that a full audit per connector would be busywork.

**Rationale:** the canvas's Architecture Invariant #2 (separation of ingestion and evaluation) makes evidence-ingestion code paths load-bearing for the integrity guarantee. Touching that surface or any auth/authz/middleware surface is the trigger for an unscheduled audit. Quarterly covers everything else.

### D3 — explicit disclaimer that first-pass audit is not a substitute for paid pentest (high confidence)

**Decision:** include the one-line statement: "First-pass audits are not a substitute for third-party penetration testing — they catch the high-yield patterns automated scanners miss."

**Alternatives considered:**

- Omit (the audit report itself says so under Methodology) — but README readers may not click through.
- Stronger wording ("a paid pentest is REQUIRED before production deployment") — overreaches; some self-hosters legitimately operate without one for small-blast-radius use cases.

**Rationale:** sets honest expectations. The README is the discovery surface; this disclaimer here prevents a security-budget-constrained reader from believing the audit reports alone make the platform pentest-equivalent.

### D4 — link target style: docs/audits/ directory AND the specific 2026-Q2 report (high confidence)

**Decision:** link to both `docs/audits/` (the directory, future audits will land alongside) and the specific `2026-Q2-security-audit.md` (the canonical first report).

**Alternatives considered:**

- Only link the directory — readers click through, fine, but loses the specific finding-count context for the current audit.
- Only link the specific report — future audits invisible until someone updates the README.

**Rationale:** dual-link pattern is the discovery-friendly form. The directory link is the durable surface; the report link is the "you can read it now" surface.

### D5 — no auto-batch of 086-089: explicit per P0-A4 (high confidence)

**Decision:** do NOT batch this slice with the four remediation slices (086 / 087 / 088 / 089). They land separately, each with its own review.

**Rationale:** literally written into slice 085's P0-A4. Solo iteration in the continuous-loop pattern. Future iterations can batch 086/087/088 together (disjoint surfaces) or run as 4-way batch if conflict-safety holds.

## Acceptance criteria status

- [x] AC-1: `docs/audits/2026-Q2-security-audit.md` exists with required sections. (Shipped PR #167 commit `9f1a56f`.)
- [x] AC-2: Four follow-on remediation slices exist in `docs/issues/`, status `ready`: 086, 087, 088, 089. (Shipped PR #167.)
- [x] AC-3: `_STATUS.md` updated with counts + drift section. (Shipped PR #167.)
- [x] AC-4: README.md "Security" section linking to `docs/audits/` + audit cadence. **This PR.**
- [x] AC-5: Pre-commit clean. CI green. **Verified at PR open + merge.**

## Constitutional invariants

- **AI-assist boundary** — no AI-generated audit findings; the README copy is human-orchestrator authored, every finding cites a file path.
- **Working norms — Cite sources** — README's audit-cadence sentence cites the canvas invariants (auth / middleware / evidence-ingestion as the audit-trigger surfaces).
