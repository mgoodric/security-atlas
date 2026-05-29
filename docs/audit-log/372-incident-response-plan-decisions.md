# 372 — Incident response plan · decisions log

**Slice:** 372 — Incident response plan (governance document)
**Slice type:** JUDGMENT
**Filed:** 2026-05-28
**Closes:** Slice 329 compliance meta-audit finding **H-1** (most-load-bearing for v1 binary success criterion).
**Companion document:** `docs/governance/incident-response.md`

---

## D1 — Severity tier shape: P0 / P1 / P2 / P3

**Decision.** Adopt a four-tier P0 / P1 / P2 / P3 severity rubric as
specified in the slice 372 narrative (the slice doc explicitly lists
P0 / P1 / P2 / P3 with the example calibration: P0 = active
exploitation in the wild, P1 = confirmed vulnerability not yet
exploited, P2 = suspected vulnerability, P3 = security-relevant
operational issue).

**Alternatives considered.**

1. **Critical / High / Medium / Low.** The vocabulary used by the
   slice 329 audit rubric and by CVSS-equivalent ratings. Pro: familiar
   to compliance reviewers; aligns with the audit-finding nomenclature.
   Con: collides semantically with CVSS — "Critical" in a CVSS rating
   describes vulnerability severity, while in an IR plan it describes
   response posture. Mixing them risks ambiguity in incident logs
   ("Critical CVE escalated to High incident?").
2. **S0 / S1 / S2 / S3.** Common at large engineering orgs. Pro:
   clearly distinct from CVSS. Con: opaque to non-engineering readers;
   "S0" sounds like a product code, not an emergency level.
3. **P0 / P1 / P2 / P3.** Common at engineering-led incident
   programs (Google SRE workbook nomenclature, PagerDuty default
   labels). Pro: distinct from CVSS; well-understood by the
   security-engineering audience; the slice 372 narrative explicitly
   chose this shape.

**Rationale.** Following the slice doc choice resolves D1 directly.
The P-prefix is unambiguous against CVSS Critical/High/Medium/Low
ratings and is the dominant convention in incident-response programs
the maintainer respects. Four tiers (not three, not five) match the
slice doc's calibration examples without forcing a forced-fit fifth
tier.

**Boundary.** Severity refers to **response posture**, not
**vulnerability rating**. A CVSS 9.8 CVE not under active exploitation
is P1 in this plan, not P0. A CVSS 5.5 being actively exploited is P0.
The IR plan document at §2 documents this calibration explicitly.

---

## D2 — Solo-maintainer role devolution: explicit and named

**Decision.** Document the standard NIST SP 800-61r3 roles (IC, Tech
Lead, Comms Lead, Scribe, On-call) **and** explicitly state that all
five devolve to the single maintainer at this stage of the project.
Name the on-call rotation "single-person on-call" with no fallback
beyond GOVERNANCE.md's bus-factor / succession plan.

**Alternatives considered.**

1. **Omit roles entirely.** "Roles" is a SaaS-org concept; a solo
   project has no roles. Pro: shorter document; honest. Con: third-
   party diligence reviewers expect to see the role inventory — a
   document with no role section looks incomplete or naive. Auditors
   read the section and confirm the project understands the role
   surface even when one person holds all of it.
2. **Name aspirational roles without devolution.** List IC / Comms
   Lead / Tech Lead as if they were separate people. Pro: looks
   professional. Con: **dishonest** — it makes commitments the
   maintainer cannot keep. P0-372-1 anti-criterion explicitly bans
   this; the slice doc's narrative §2 calls it out as a known failure
   mode.
3. **Document roles + name devolution.** Adopted.

**Rationale.** The slice doc explicitly requires role devolution to
be documented. The IR plan §3 names each role, then in a dedicated
"Solo-maintainer role devolution" subsection explains that all
devolve to the maintainer. This is the maximum-honesty / maximum-
information shape — the reader sees both the standard role surface
AND the project's current single-person reality, and the document
remains audit-actionable without making false claims.

**Anti-pattern explicitly rejected.** "24/7 on-call rotation" for a
solo maintainer is false. The document says best-effort during
business hours for all tiers; best-effort outside business hours for
P0 only. P0-372-1 enforces this.

**Future evolution.** The GOVERNANCE.md advisory-council trigger
(≥ 3 active outside contributors with ≥ 6 months sustained
involvement) is the named point at which this devolution is
re-evaluated. Updated inline at that future trigger.

---

## D3 — Communication-channel inventory: existing channels only; no new infrastructure

**Decision.** The communications playbook (§6) enumerates channels
the project **already has**: GitHub Private Vulnerability Reporting,
GitHub Security Advisory, CVE assignment through GitHub, CHANGELOG
`### Security` section, GitHub release notes, the prospective
`SECURITY-ACKNOWLEDGEMENTS.md` file (forward-referenced from
SECURITY.md), and a prospective `SECURITY-INCIDENTS.md` file for
public statements. Do **not** commit to an operator mailing list,
Discord server, or status page in this slice.

**Alternatives considered.**

1. **Commit to a status page.** Pro: standard SaaS expectation;
   makes operational incidents visible. Con: requires hosting +
   maintenance + content discipline that does not exist today;
   contradicts P0-372-1 (no commitments security-atlas can't keep).
2. **Commit to an operator mailing list.** Pro: lets the project
   reach known operators during a P0. Con: requires the project to
   collect and maintain operator email addresses, which is a
   privacy / GDPR surface security-atlas does not currently have.
   Defer until v2+ when operator adoption is measurable.
3. **Commit to a Discord / Slack / Matrix channel.** Pro: lower
   maintenance than a mailing list. Con: introduces a new
   infrastructure dependency; not actually appropriate for security
   incident communications (channel members are not necessarily
   operators; public visibility of P0 discussion could aid attackers).
4. **Document existing channels only; add new channels via future
   slices when operator adoption warrants.** Adopted.

**Rationale.** The slice doc P0 anti-criteria explicitly ban
commitments the maintainer cannot deliver and mandates that the
plan describes capabilities, not aspirations. Existing channels
(GitHub PVR / advisories / CHANGELOG) are the honest answer today.
The §6 communications playbook closes by listing the "out of scope"
items so a reviewer sees the project has considered them and
explicitly declined to commit, rather than not noticing.

**Anti-pattern explicitly rejected.** "When a P0 happens, we will
email all our operators" — the project has no operator email list.
The document instead says "if an operator mailing list is
established (future slice), it is used here", which is forward-
honest without making a current commitment.

---

## D4 — Tabletop cadence: annual

**Decision.** Commit to **annual** tabletop exercises, with the
first tabletop due **2027-05-28** (one year from this document's
filing date). Tabletop output is recorded at
`docs/audit-log/tabletop-YYYY-MM-DD.md`.

**Alternatives considered.**

1. **Every six months.** Pro: keeps the plan fresh. Con: high cost
   for a solo maintainer; tabletop exercises take half a day to do
   well. Six-month cadence over a multi-year horizon would consume
   significant maintainer time without proportional readiness gain
   given the low baseline incident rate.
2. **Annual.** Pro: aligns with most SOC 2 readiness cadences;
   matches the document's own annual review cadence so the two
   activities can be done in the same maintainer window; sufficient
   for a project at this stage of adoption. Con: a year is long
   enough that the playbooks may drift out of date between exercises.
3. **Tied to incident cadence — tabletop after every N real
   incidents.** Pro: ratchets discipline to real activity. Con:
   incident cadence is too low and too unpredictable to be a useful
   trigger; the project might go 18 months without a real incident.
4. **Annual, with chaos experiments as supplementary substrate.**
   Adopted. The slice 335 chaos backlog (when executed in v2+ slices
   354-358) provides operational stress on the response machinery
   beyond the annual tabletop.

**Rationale.** Annual is the right answer for the project's current
stage of adoption and the maintainer's bandwidth. The chaos backlog
provides supplementary stress; the quarterly audit cadence provides
passive testing via High / Critical findings becoming incidents.
Triple-redundant testing surface is sufficient for the v1 binary
criterion.

**Re-evaluation trigger.** When adoption increases (per
GOVERNANCE.md re-evaluation trigger metrics), the cadence is
revisited. Until then annual is the commitment.

---

## D5 — Incident log location: `docs/incidents/` (public-by-default)

**Decision.** Per-incident logs live at
`docs/incidents/YYYY-MM-DD-<slug>.md`, public by default. Where
attack-vector detail must be redacted, the public log carries a
`[redacted]` placeholder and the unredacted material is held
privately by the maintainer.

**Alternatives considered.**

1. **Private repo or external storage.** Pro: avoids any risk of
   redaction failures exposing material that should stay private.
   Con: introduces external infrastructure; defeats the project's
   slice-process audit-trail discipline (every other change in the
   project is publicly recorded; making incidents the exception is
   structurally inconsistent).
2. **Public-by-default with explicit redaction.** Adopted.
3. **Private until resolved, then public.** Pro: avoids exposing
   in-flight investigation. Con: introduces a publish-step the
   maintainer must remember to perform; risks logs never becoming
   public if the publish step is dropped.

**Rationale.** Transparency is the project's working assumption
across every other surface (canvas + ADRs + slice docs + decisions
logs). Making incidents the exception would weaken the audit-trail
posture the project depends on. Explicit redaction with a
documented `[redacted]` convention handles the rare cases where
detail must be withheld.

**Boundary.** Reporter PII is redacted by default (per SECURITY.md
"Recognition" — credit given only with reporter permission).
Third-party PII is redacted by default. Attack-vector detail is
redacted only when its publication would aid an attacker re-
targeting the same vector.

---

## D6 — Frontmatter format: TOML between `+++` markers

**Decision.** Incident log frontmatter and post-incident review
template frontmatter use **TOML between `+++` markers**, matching
the existing convention at `CODE_OF_CONDUCT.md`.

**Alternatives considered.**

1. **YAML between `---` markers.** Pro: more common in
   Markdown-static-site ecosystems. Con: the project's existing
   precedent is TOML; adopting a second convention for governance
   docs is inconsistency for inconsistency's sake.
2. **TOML between `+++` markers.** Adopted.
3. **No frontmatter.** Pro: simpler. Con: forecloses future
   automation (e.g., a maintainer-facing dashboard of open
   incidents that parses frontmatter for status).

**Rationale.** Match existing project convention; preserve a path
to lightweight automation; no new dependency required.

---

## D7 — SECURITY.md update scope: one-line cross-reference; no other changes

**Decision.** Update `SECURITY.md` to cross-reference
`docs/governance/incident-response.md` in the existing "Disclosure
policy" section. **Do not** change the SLA targets, the supported-
versions table, the reporting procedure, or the safe-harbor
clause.

**Alternatives considered.**

1. **No SECURITY.md update.** Pro: zero surface area; lowest
   merge risk. Con: leaves the IR plan undiscoverable from the
   GitHub-recognized community-health file that auditors find first.
2. **Restructure SECURITY.md to incorporate the IR plan content.**
   Con: violates slice 372 P0-372-2 ("Does NOT modify SECURITY.md's
   existing intake process — it cross-references, it does NOT
   replace"); duplicates content; creates drift surface between two
   documents.
3. **One-line cross-reference in the "Disclosure policy" section.**
   Adopted.

**Rationale.** Minimum-surface change that closes the
discoverability gap. The cross-reference points to the IR plan; the
IR plan §1 "Why this document exists" explains the relationship to
SECURITY.md. Bi-directional cross-reference resolved.

**Boundary.** This decision is also an "engineer-as-collaborator"
edge case the slice's process notes call out — adjacent gaps that
are 1-line fixes are in scope. The SECURITY.md cross-reference
qualifies; broader SECURITY.md restructuring does not.

---

## D8 — Out-of-scope governance content explicitly named

**Decision.** The IR plan §1 "What this plan does not cover"
explicitly enumerates incidents inside operator-hosted deployments,
customer-side compliance program incidents, Code of Conduct
violations, and general bug reports as out of scope. The document
does not leave the scope question implicit.

**Alternatives considered.**

1. **Leave scope implicit.** Pro: shorter document. Con: an
   operator reading the plan and trying to use it as a template for
   their own deployment would not know whether security-atlas
   claims that scope. Auditors would ask.
2. **Explicit scope-out section.** Adopted.

**Rationale.** Calibrates reader expectation. Reduces the surface
for misunderstanding. Mirrors the slice-doc P0 anti-criteria
pattern (state what the slice will not do, not just what it will).

---

## Decisions not made in this slice (deferred)

- **Operator mailing list creation.** The §6 communications
  playbook forward-references this as "if established (future
  slice)" — actual creation is out of scope for slice 372.
- **`SECURITY-ACKNOWLEDGEMENTS.md` file creation.** The slice 329
  audit L-2 finding flagged the forward reference; creation is
  out of scope here. The IR plan §6 simply describes when the
  file would be used.
- **`SECURITY-INCIDENTS.md` file creation.** Reserved for
  reputation-affecting P0 incidents; created on first use.
- **Per-incident automation / dashboard.** The TOML frontmatter
  format (D6) preserves the path; the dashboard itself is a
  future slice.
- **Specific tooling choice for monitoring / paging.** P0-372-2
  bans tool mandates. The §4 detection table describes channels;
  the channels are GitHub-native + CI-native; no third-party
  pager / dashboard is committed.

---

## Cross-references to constitutional commitments

- **AI-assist boundary (hard) — `CLAUDE.md`.** This document is
  fully human-authored. No AI-drafted content was approved without
  human review per the schema-level enforcement
  (`ai_assisted=true ↔ human_approver` invariant).
- **Anti-pattern rejection — `CLAUDE.md` "Anti-patterns we
  explicitly reject".** The IR plan does not propose proprietary
  collector agents, AI-generated incident responses without human
  approval, or vanity trust-center surfaces. The plan describes
  the response capabilities the project actually has.
- **Tone discipline — `docs/governance/board-narrative-tone-anti-patterns.md`.**
  The IR plan §6 explicitly references the tone discipline; the
  document itself avoids the banned phrase list (no "industry-
  leading", no "robust" as filler, no "leverage" as verb, no
  unprompted superlatives).
- **Slice 329 audit binding.** This slice closes finding H-1; the
  audit report explicitly named slice 372 as the load-bearing
  spillover for the v1 binary success criterion.
