# Post-incident review template

> **This is a template.** Copy it to `docs/audit-log/incident-NNN-<slug>.md`
> for a real review (where `NNN` is the sequential incident number and
> `<slug>` is the kebab-case description matching the per-incident log
> at `docs/incidents/YYYY-MM-DD-<slug>.md`). Delete this preamble in the
> copy.
>
> The governing process lives in
> [`docs/governance/incident-response.md`](../governance/incident-response.md)
> §9 "Post-incident review" — read it before filling this out. Mandatory
> for P0 and P1; optional for P2 and P3. Blameless.

---

+++
incident_id = "YYYY-MM-DD-<slug>"
review_id = "incident-NNN-<slug>"
severity = "P0" | "P1" | "P2" | "P3"
discovered_at = "YYYY-MM-DDTHH:MM:SSZ"
resolved_at = "YYYY-MM-DDTHH:MM:SSZ"
review_date = "YYYY-MM-DD"
root_cause = "<one-line summary>"
action_items = ["slice-NNN", "slice-MMM"]
+++

# Post-incident review · incident NNN · `<slug>`

**Incident log:** [`docs/incidents/YYYY-MM-DD-<slug>.md`](../incidents/YYYY-MM-DD-<slug>.md)
**Severity:** P-tier
**Reviewers:** maintainer (+ co-IC if applicable)
**Reviewed:** YYYY-MM-DD

---

## Timeline

| Time (UTC) | Event |
| ---------- | ----- |
| ...        | ...   |

## What happened

<One-paragraph factual summary. No interpretation. State events as
they unfolded; reserve analysis for the "Root cause" section below.>

## Root cause

<The technical / process / human factor that allowed the incident.
Single sentence at the top, then a paragraph of detail. If the root
cause was an interaction between multiple factors, name them all.>

## What went well

<Things the response surface did right. Cite specifically — "Dependabot
caught the CVE the same hour the upstream advisory dropped" rather than
"our monitoring was good".>

## What went poorly

<Things that were slower or less complete than they should have been.
Specific failures, not general handwaving. "It took 4 hours to find
the credential in the leaked commit because the file was a JSON blob
without obvious naming" — not "investigation was slow".>

## What could detect this earlier

<The single most valuable question of the review. What signal — if
monitored — would have surfaced this before it became an incident?
The answer is the seed of the next slice.>

## Action items

- [ ] **#NNN** — <short description; file via `/idea-to-slice`>
- [ ] **#MMM** — <short description; file via `/idea-to-slice`>
- [ ] **<config / docs change>** — <if a one-line PR-able change, list
      here with the PR link when filed>

## Slices spawned

- #NNN — <short description>
- #MMM — <short description>

## What we are explicitly not doing

<Containment-style commitments we decided NOT to make and why. This
section guards against over-correction.

Examples that belong here: "we are NOT introducing 24/7 paging because
the project is solo-maintained"; "we are NOT broadening the GitGuardian
allowlist because the false-positive rate would mask future leaks".>

---

## Notes for the reviewer

- **Be blameless.** Single-person on-call means there is no one else
  to blame. The function of the review is to surface what could
  change next time, not to assign fault.
- **Every action item becomes a slice or a PR.** Do not leave todos
  in the document. File the slice via `/idea-to-slice` and link it
  back here.
- **The maintainer reviews open action items at every quarterly
  governance checkin.** Stalled action items are explicitly
  acknowledged with a status update; no silent dropping.
- **Public by default.** If attack-vector detail must be redacted,
  use `[redacted — see private archive]` placeholders here and hold
  the unredacted version privately.
