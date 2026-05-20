# Board-narrative tone anti-pattern reference

**Status:** Active reference — consulted by the board-narrative system prompt and the numeric-verification pipeline when board-narrative v0 ships (v2+ work).

**Last reviewed:** 2026-05-20 (filed by slice 182).

**Governance:** modifications require maintainer review under branch protection. See [Section 4](#section-4--living-document-discipline).

---

## Why this file exists

Board narratives are the highest-risk AI-assist surface in security-atlas because board members are typically non-technical and take outputs at face value — the hallucination cost is asymmetric versus other AI-assist surfaces (questionnaires, mapping suggestions, freshness explanations). The constitutional response to that asymmetry is a measured, factual, slightly defensive voice — never marketing voice, never unprompted optimism, never superlatives the data does not earn.

This document is the **canonical reference list** of phrases, framings, and voice patterns that the board-narrative system prompt and downstream verification will reject. It exists as a standalone artifact for three reasons:

1. **Engineering use** — the Section 1 banned-phrase list is meant to be copied verbatim into a `forbidden_phrases:` config or a regex blocklist. The system prompt enforces Section 2 framings as a behavioral constraint. The numeric-verification pipeline cross-references Section 3 to avoid false-positive rejections.
2. **Maintainer use** — when a real board-pack draft surfaces a new failure mode, the maintainer adds an entry here (Section 4 specifies the discipline) and the next prompt revision picks it up. The file is the only place where this list lives.
3. **Operator use** — operators who edit AI-drafted sections can read this to understand why the system rejected a phrase and what to write instead.

Cross-references:

- [`CLAUDE.md`](../../CLAUDE.md) → "AI-assist boundary (hard)" → "Board-narrative AI-assist" subsection — abbreviated tone list lives there as the constitutional commitment; this file is the canonical full list.
- [`docs/adr/0006-board-narrative-ai-assist.md`](../adr/0006-board-narrative-ai-assist.md) — ADR capturing the seven sub-decisions and why this tone discipline is load-bearing.
- [`Plans/canvas/04-evidence-engine.md`](../../Plans/canvas/04-evidence-engine.md) §4.6 — canvas reflection of the seven sub-decisions.
- [`docs/operator/maintenance-cadence.md`](../operator/maintenance-cadence.md) — local-model refresh cadence (the model running this prompt changes over time; this file does not).
- [`docs/issues/182-board-narrative-ai-assist-foundation.md`](../issues/182-board-narrative-ai-assist-foundation.md) — slice that filed this artifact.

---

## Section 1 — Banned phrases (exact-match)

The following phrases are rejected by exact (case-insensitive) match in the LLM's generated draft. The system prompt also instructs the model to avoid them; the post-generation check is the safety net.

**Discipline:** these are intended to be copy-pasted verbatim into a `forbidden_phrases:` config. Each phrase ships with a one-line "why banned" rationale and a "what to write instead" steer.

| #   | Banned phrase                       | Why banned                                                                                                                  | Write instead                                                                                                                      |
| --- | ----------------------------------- | --------------------------------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------- |
| 1   | `we are proud to report`            | Editorial framing the data did not request. Pride is an opinion; board narratives report facts.                             | State the fact: `"Coverage rose from 78% to 84% this quarter."`                                                                    |
| 2   | `exceeded expectations`             | Implies a target that the platform did not commit to and the board did not set. Marketing-pack language.                    | Cite the target if one exists: `"Q2 target was 80% control coverage; achieved 84%."` Otherwise drop the framing.                   |
| 3   | `industry-leading`                  | Unsubstantiated superlative. The system has no benchmark data to support the claim.                                         | Drop or replace with a specific comparison the platform can cite (e.g., `"Our MTTR is below the Verizon DBIR p50."`) if cited.     |
| 4   | `best-in-class`                     | Same failure mode as `industry-leading`. The platform cannot verify "best".                                                 | Drop, or describe the specific control posture that's notable.                                                                     |
| 5   | `world-class`                       | Marketing voice. No measurable referent.                                                                                    | Drop.                                                                                                                              |
| 6   | `robust` (as filler)                | When `robust` appears without a specific posture it modifies, it's filler. Section 3 carves out the legitimate cases.       | Either delete the word or state what's robust about what: `"the change-management process is robust against unauthorized merges."` |
| 7   | `leverage` (as verb)                | When `use` works, `leverage` is hype.                                                                                       | Use `use`, `apply`, or the specific operation: `"We use the OPA engine to evaluate control queries."`                              |
| 8   | unprompted superlative              | Catch-all for `unprecedented`, `revolutionary`, `groundbreaking`, `transformative`, `state-of-the-art`, etc.                | Drop. Boards distrust them.                                                                                                        |
| 9   | `seamlessly`                        | Adverb that implies invisibility of effort the platform cannot verify. Marketing tell.                                      | Drop, or state what worked without friction and how the platform observed it.                                                      |
| 10  | `mission-critical`                  | Filler intensifier. Either every system in scope is mission-critical (trivial) or the platform should name the one that is. | Name the system: `"the PCI-CDE production database tier"`.                                                                         |
| 11  | `cutting-edge`                      | Marketing voice. The platform has no objective referent for "edge".                                                         | Drop, or name the specific feature/version.                                                                                        |
| 12  | `at the forefront`                  | Same family as `cutting-edge`. Self-positioning the platform does not earn from the underlying data.                        | Drop.                                                                                                                              |
| 13  | `synergy` / `synergies`             | Consulting-deck language. Not a measurable noun.                                                                            | State the specific overlap or shared resource being described.                                                                     |
| 14  | `going forward`                     | Filler phrase that adds no information. The narrative is already in present/future tense where relevant.                    | Drop. State the next step directly: `"Next quarter we will..."`                                                                    |
| 15  | `at this point in time`             | Filler for `now`. Padding.                                                                                                  | `now` or drop.                                                                                                                     |
| 16  | `bandwidth`                         | (When used metaphorically for time/attention.) Corporate filler.                                                            | Use `capacity`, `staff time`, or name the constraint.                                                                              |
| 17  | `move the needle`                   | Hype phrase. Boards have heard it a thousand times.                                                                         | State the metric and the magnitude of change.                                                                                      |
| 18  | `paradigm shift`                    | Marketing voice. The platform has not earned this framing.                                                                  | Drop. Describe the specific architectural or process change.                                                                       |
| 19  | `single source of truth` (as boast) | When used to describe the platform's own posture in a board narrative, it's self-congratulation.                            | Acceptable in operator docs or internal architecture writing; banned in board-narrative tone.                                      |
| 20  | `we have built a culture of`        | Untestable claim. Cultural framing the platform cannot evidence.                                                            | Drop, or cite a specific cultural artifact (training-completion %, mandatory-review policy, incident-postmortem cadence).          |

**Notes for the prompt author:**

- The list is **case-insensitive** but **whitespace-sensitive**: `"world class"` (no hyphen) must be added separately if the failure mode is observed.
- Apostrophe variants count as the same phrase: `we're proud` and `we are proud` both reject.
- These are **non-exhaustive**. Section 2 catches the framings these phrases are usually a symptom of; Section 4 specifies how the list grows.

## Section 2 — Banned framings (pattern-match)

Categorical voice patterns that the system prompt rejects even when the specific phrases in Section 1 are absent. These are behavioral rules; the LLM is instructed to avoid them, and human review catches the residue.

| #   | Framing                                  | What it looks like                                                                                                                            | Why banned                                                                                                                       | Correction                                                                                                                                                |
| --- | ---------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------- |
| F1  | Unprompted positive framing              | The data is neutral or mixed, but the narrative leads with a positive spin (`"This quarter saw strong progress in our security posture..."`). | Editorial layer the data does not earn. Boards smell it.                                                                         | Lead with the fact: `"Coverage rose 6 points (78% → 84%); two new findings opened in PCI scope."`                                                         |
| F2  | Marketing voice                          | Adjective stacks, abstract nouns, future-perfect promises (`"By continuing to invest in our world-class capabilities, we will..."`).          | Voice mismatch for a fiduciary-report audience.                                                                                  | Plain English; concrete subjects + concrete verbs.                                                                                                        |
| F3  | Passive-voice deflection of issues       | `"Two findings were identified."` `"A risk was elevated."` Hides the agent.                                                                   | When the platform reports a failure, the board needs to know who/what surfaced it and who owns the next step.                    | Active voice: `"The Q1 PCI sample-test surfaced two findings; both are assigned to the platform team with target-close 2026-06-30."`                      |
| F4  | Future-tense optimism without commitment | `"We will continue to strengthen..."` `"We are committed to improving..."` — promises with no measurable next step.                           | Verbiage. The board cannot hold the platform to it.                                                                              | If there is a next step, name it with owner + date. If not, do not promise.                                                                               |
| F5  | Editorial summary at the top             | A `## Summary` or `## Key takeaways` section that paraphrases the data with editorial color before the data itself appears.                   | The board reads top-to-bottom; the editorial layer biases interpretation.                                                        | The numbered section template (D6 in the seven-decision bundle) avoids this; the summary is the rollup table, not paragraphs.                             |
| F6  | Comparative framing without a baseline   | `"Significantly improved coverage."` `"Substantially reduced risk."`                                                                          | Compared to what? Without a number, the comparative is opinion.                                                                  | Cite the prior-period number and the delta. If the rollup does not have a prior-period number, do not make the comparison.                                |
| F7  | Hedge stack                              | `"We believe that we may have potentially reduced..."` — three hedges on a single claim.                                                      | Either the platform can claim it (with evidence) or it cannot. Hedging signals uncertainty the citations should already convey.  | Drop the hedges; cite the evidence.                                                                                                                       |
| F8  | Anthropomorphizing the platform          | `"security-atlas understands that..."` `"the platform believes..."` `"our system recognizes..."`                                              | The platform does not believe or understand anything. False agency.                                                              | State the rule the platform applies: `"The freshness gate flagged 12 controls past 30-day window."`                                                       |
| F9  | First-person plural for the platform     | When `"we"` refers to the platform itself rather than the org. Confuses agency.                                                               | The platform is not a `we`. The org is the `we` and the platform is the tool.                                                    | Either `"the platform"` (when describing tool behavior) or `"the security team"` (when describing org action).                                            |
| F10 | Hand-wave on causality                   | `"This is largely due to..."` without naming the cause.                                                                                       | Causality is exactly what the board wants. Vague gestures don't pay.                                                             | Name the cause: `"...because the AWS connector landed mid-quarter and surfaced 47 previously-untracked S3 buckets."`                                      |
| F11 | Inferred intent                          | `"The team is focused on..."` `"Management is prioritizing..."` — claims about state-of-mind.                                                 | The platform sees actions, not intent. Reporting intent fabricates.                                                              | Report the action: `"Three new controls were activated this quarter; one was retired."`                                                                   |
| F12 | Audit-binding language without sign-off  | `"Our SOC 2 readiness is..."` `"We are SOC 2-compliant."` — claims that bind the audit position outside the audit-binding workflow.           | Audit-binding belongs to the auditor (D4-style guardrail) and the one-click approval flow. Narratives cannot grant the position. | Describe the underlying posture without binding language: `"Coverage of SOC 2 CC-6 controls is 100%; the audit-period freeze for FY26 Q1 has not begun."` |

## Section 3 — Permitted phrases that are commonly mistaken as banned

This section exists because rule-based banning over-fires when the same word has both legitimate and illegitimate uses. Without this carve-out, prompt engineers and operators would over-correct toward stilted prose. Each entry has the legitimate form and the illegitimate form side by side.

The post-generation regex check excludes these legitimate forms via context-window inspection (see prompt-template implementation when board-narrative v0 lands). Operators editing drafts should treat this section as authoritative.

| #   | Word / phrase          | OK when…                                                                                                                                                                                                                 | NOT OK when…                                                                                          |
| --- | ---------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ | ----------------------------------------------------------------------------------------------------- |
| P1  | `robust`               | Modifying a specific posture with a specific failure mode it resists: `"The change-management process is robust against unauthorized merges."` (carve-out mirrors the parenthetical in CLAUDE.md's banned-phrases list.) | Used as filler in front of an abstract noun: `"We have a robust program."` `"Our robust controls..."` |
| P2  | `leading`              | As a literal verb of motion or causation: `"The Q1 access-review cycle led to two policy changes."`                                                                                                                      | As a self-applied superlative: `"a leading position"`, `"a leading framework"`.                       |
| P3  | `strong`               | Quantified comparison: `"Strong evidence-freshness compared to the prior quarter (94% vs. 88% within window)."`                                                                                                          | As unprompted intensifier: `"a strong security posture"`, `"strong commitment to..."`.                |
| P4  | `improve` / `improved` | Stating a numeric direction of change: `"Coverage improved 6 points (78% → 84%)."` Always with a number alongside.                                                                                                       | Without a number: `"We improved our security posture this quarter."`                                  |
| P5  | `proud`                | NEVER permitted in board-narrative tone. Use a different framing.                                                                                                                                                        | All cases. See ban #1.                                                                                |
| P6  | `confident`            | Citing a specific basis: `"The audit-period freeze hash matches across all sample sets, so the platform can confirm the sample population was stable."`                                                                  | As editorial assertion: `"We are confident in our controls."`                                         |
| P7  | `comprehensive`        | As a literal claim about scope coverage with citation: `"The SCF anchor catalog covers 1,403 controls (current SCF version 2026.2)."`                                                                                    | As marketing intensifier: `"a comprehensive solution"`, `"a comprehensive program"`.                  |
| P8  | `critical`             | As a category label (e.g., severity tier the platform actually uses): `"Two critical-severity findings opened this quarter."`                                                                                            | As intensifier filler: `"It is critical that we..."`, `"critically important"`.                       |
| P9  | `mature` / `maturity`  | When citing a specific maturity-model output with a numeric or named tier: `"The IAM domain rose from CMMI level 2 to level 3 this quarter (CMMI assessment 2026-04-15)."`                                               | As editorial qualifier: `"a mature security program"`.                                                |
| P10 | `effective`            | When citing the specific testing outcome the word summarizes: `"The Q1 SOC 2 sample tests of CC-6.1 returned zero exceptions, so the control is operating effectively for the test period."`                             | As blanket claim: `"Our controls are effective."`                                                     |

## Section 4 — Living-document discipline

This file evolves as the maintainer encounters real failure modes from real board-pack drafts. The discipline:

### 4.1 Who adds entries

The repository maintainer (or any user with write access under branch protection) opens a pull request adding the new entry. Drive-by contributors propose entries via PR; the maintainer reviews and merges.

### 4.2 What triggers a new entry

Any of the following, in order of severity:

1. **The LLM generated a phrase / framing in a draft that the operator caught and corrected.** Add the phrase to Section 1 (if exact-match) or the framing to Section 2 (if pattern-match).
2. **A board member surfaced confusion about the narrative voice or interpretation.** Investigate root cause; if traceable to a tone failure, file an entry.
3. **A new vendor-marketing voice pattern surfaces in the industry that the LLM might pick up from its training data.** Pre-emptively add to Section 2 if the maintainer has reason to expect it in generated output.
4. **A false-positive rejection by the regex / pipeline.** Investigate; if the rejection is legitimate, no change; if over-restrictive, add the legitimate form to Section 3.

### 4.3 PR shape

Each PR that modifies this file MUST:

1. **Add a single entry per PR** unless multiple entries are related to one observed failure mode. Multi-entry PRs are acceptable when they cluster (e.g., adding a Section 1 phrase + the Section 2 framing it instantiates + a Section 3 carve-out).
2. **Cite the source observation** in the PR body — link to the operator's edit, the board feedback, or the false-positive log entry. "Source: operator edit log 2026-08-15" is sufficient; a verbatim quote is better if reproducible.
3. **Include rationale** for each entry. Why banned? What's the failure mode? What does the operator write instead? Section 1 and Section 3 entries have explicit columns for this; Section 2 framings need a "What it looks like" + "Why banned" + "Correction" trio.
4. **Renumber affected entries** if inserting mid-section. Numeric IDs are stable references in commit history and the next prompt revision picks the renumbered list up.
5. **Note prompt-version impact.** Adding an entry to Section 1 or 2 means the next board-narrative prompt revision (D7 snapshot-with-each-generation) picks up the change. The PR body should call out that the next generated narrative will pick up the new rule.

### 4.4 Branch protection

This file lives under `docs/governance/`, and the project's branch-protection rules (per slice 050 / OQ #14 resolution's commitment to the tone-anti-pattern-as-trust-root posture) require maintainer review on PRs that touch this directory. Drive-by contributors can propose; merge requires maintainer approval.

### 4.5 What does NOT live here

- **Implementation code** (system-prompt template, regex blocklist source, numeric-verification library) — those live with board-narrative v0 when it ships.
- **Per-tenant overrides** — the tone discipline is platform-wide; tenants do not get to override the ban on `we are proud to report`.
- **Auditor- or board-specific overrides** — same reason. The tone is a property of the platform, not the audience.
- **Translation or localization** — non-English board narratives are out of scope until a tenant requests them; if/when that lands, this document forks per locale, governed by the same discipline.

### 4.6 Audit trail

This file's git history is the audit trail. Each merged PR is an immutable record of when an entry was added, why, and by whom. No separate audit-log file is required.

---

## Appendix — Reference voice (what the platform writes instead)

The constitutional posture is **measured, factual, slightly defensive**. The reference voices that inform this tone are:

- **Buffer transparency reports** — specific numbers, no marketing, plain English, explicit about uncertainty.
- **GitLab annual report** — fact-driven, names trade-offs, reports failures alongside successes.
- **NIST publications** — clinical, citation-heavy, no editorial layer.
- **Federal-agency Inspector-General reports** — slightly adversarial; surfaces problems without softening; uses passive voice only when the agent is genuinely irrelevant.

The reference voices that the platform **explicitly does not adopt**:

- Vendor security marketing copy (any of the phrases in Section 1 will appear within the first two paragraphs).
- LinkedIn-thought-leadership prose (heavy on Section 2 framings).
- Default GPT-4-style "summary at the top" cadence (F5 framing).
- Default LLM tendency toward positive framing of neutral data (F1 framing).

When in doubt: read the entry back as if you were a non-technical board member reading a fiduciary report. If a sentence sounds like a press release, it fails. If a sentence sounds like a clinical observation, it passes.
