# 324 — Evidence SDK docs alignment — decisions log

Slice 324 is `Type: JUDGMENT` — docs authorship of constitutional-grade
language. This log records the subjective build-time judgment calls made
while landing the reword, in the JUDGMENT-slice format (Decisions made ·
Revisit once in use · Confidence per decision). It does NOT block merge.

## Decisions made

### 1. The two-profile language is preserved, not deleted

**Chosen:** preserve the two-profile (connector / pusher) language across
`CLAUDE.md` invariant #3, `Plans/EVIDENCE_SDK.md` §1 + §3, and
`Plans/canvas/04-evidence-engine.md` §4.1 — but reframe it as
operator-facing metadata about a connector's source-side fetch direction,
not as a platform-side wire-format dimension.

Three alternatives were considered:

- **(A) Delete the two-profile framing entirely.** Rejected. The framing
  is genuinely useful: it distinguishes "AWS connector polls on a
  schedule" from "GitHub connector also accepts webhooks." That
  distinction matters at operator-onboarding time (the operator picks
  which connectors to install and which side of the network the
  scheduling lives on) and at community-connector-authoring time (the
  contributor decides whether their connector is a scheduled-poll loop
  or a webhook receiver before they write a line of code). Deleting it
  would force operators and contributors to rediscover the distinction
  from connector binaries one at a time.

- **(B) Preserve the framing but make no mention of "wire" at all.**
  Rejected. The original drift happened precisely because the docs were
  silent about where on the architecture the profile boundary lives. A
  contributor reading the old docs reasonably inferred "the connector
  profile is the connector-side gRPC contract the platform calls" —
  because that's the only place a `Pull(kind, since, scope_filter)`
  method signature naturally lives. The reword has to **explicitly**
  say "platform-side wire surface is always push" to inoculate against
  the same drift returning.

- **(C) Preserve the framing AND explicitly correct the wire claim.**
  **Chosen.** Each of the three docs surfaces gets the same shape:
  rename the table's "Direction" column to "Source-side direction"
  (`CLAUDE.md` is one sentence so it folds the correction inline);
  rename "Who initiates" to "Who initiates the source-side fetch";
  add a paragraph explicitly noting the platform-side wire is always
  push. In `EVIDENCE_SDK.md` §3, replace the phantom-methods table
  with an in-process loop diagram + an explicit "What does NOT exist
  on the wire" callout listing the seven phantom RPCs by name so
  community connector authors know what NOT to implement against.

**Confidence: high.** Approach (C) is what the slice doc's AC-1 / AC-2 /
AC-3 / AC-4 pre-committed to, and it is the only approach that
simultaneously preserves the useful operator-facing distinction (the
slice's anti-criterion P0-324-2 forbids deletion) and inoculates
against the original drift returning. The reword reads as "X is
preserved, and we now describe X more precisely" — which is the slice
doc's stated test for a successful rewrite.

### 2. Wording chosen for the column rename: "Source-side direction"

**Chosen:** the column header was renamed from `Direction` to
`Source-side direction`, with the row values rewritten from
`Platform → Source` / `Source → Platform` (which described a wire that
doesn't exist) to `Connector → Source` / `Source → Connector` (which
describes the actual in-process loop). The "Who initiates" column was
renamed to `Who initiates the source-side fetch`.

Alternatives considered:

- **`Fetch mode`** (e.g. column header = "Fetch mode"; values = "pull",
  "push"). Rejected. Loses the directional information; "pull" and
  "push" become the answer to the column rather than a property of it.
- **`Retrieval direction`**. Rejected as marginally less natural than
  "Source-side direction"; an English-speaker scanning the table reads
  "source-side direction" as "what direction does the connector go to
  reach the source" without effort.
- **`Inbound direction`** (from the platform's perspective). Rejected.
  All evidence is inbound to the platform; the table is about the
  connector-source axis, and "inbound" only makes sense if there's
  some "outbound" surface to contrast with.
- **No column rename — just edit the row values.** Rejected. The drift
  was driven by the original column header (`Direction`) being
  ambiguous; renaming the column is the load-bearing fix.

`Connector → Source` is the right shape for the row value (not
`Connector → External`, not `Platform-process → Source`, not
`Outbound`) because every other doc in the repo refers to the
connector's source as "the source" (`connectors/aws/README.md` uses
"the source"; `docs-site/docs/connector-authoring.md` uses "the
source") and reusing that noun keeps the docs coherent.

**Confidence: high.** This is the exact wording the slice doc's AC-2
pre-committed to (`"source-side direction" (or equivalent)`); the
"or equivalent" clause is acknowledged but not exercised — the
slice doc's wording is already the cleanest shape.

### 3. The "what does NOT exist on the wire" callout lives in §3, not §1

**Chosen:** the explicit "What does NOT exist on the wire" callout
listing the seven phantom RPCs (`Describe`, `AuthMethods`, `HealthCheck`,
`ListEvidenceKinds`, `Pull`, `Subscribe`, `VerifyProvenance`) is
inserted at the end of `Plans/EVIDENCE_SDK.md` §3, not at the top of
§1 or §3.

Alternative considered: put the callout at the top of §3 (or even §1)
as a banner so readers see it before they read the loop description.

Rejected because:

- A banner-style "we used to claim X but actually" framing reads as
  apologetic, which violates the slice doc's stated tone test ("the
  rewrite should read as X is preserved and we now describe X more
  precisely," not "we used to claim X, now we claim less than X").
- The callout's audience is the community connector author who is
  considering implementing one of the phantom RPCs. That author has,
  by then, already read §3's in-process-loop description. The callout
  catches them right when they are most likely to be tempted to file
  a "but the docs also mentioned a `Pull` method" issue.

**Confidence: high.** The slice doc's AC-3 explicitly requests "an
explicit 'What does NOT exist on the wire' callout listing the seven
phantom RPCs"; the placement choice is the only judgment.

### 4. `proto/README.md` is touched; `docs/issues/004-...` is not

**Chosen:** `proto/README.md` is updated (it claimed a future
`proto/connector/v1/connector.proto` with the phantom methods that was
never built) but `docs/issues/004-aws-connector-s3-encryption.md` is NOT
touched (it is the historical slice ticket; the text reflects what was
planned at the time slice 004 shipped, and editing closed slice docs is
generally discouraged).

Rationale:

- `proto/README.md` is an operator-facing reference doc that contributors
  read when navigating the proto tree. Leaving it claiming a
  `proto/connector/v1/connector.proto` with `Describe` / `Pull` /
  `Subscribe` / `VerifyProvenance` actively misleads — both the file
  name is wrong (`connector` singular, never built) and the method list
  is the phantom set. This is a docs-discipline fix.
- `docs/issues/004-...` is the historical merged-slice ticket. Slice 004
  did, in its narrative, describe building toward those phantom methods
  — but it then shipped what shipped (push-only `Register`/`List` plus
  `Push`). The slice ticket is part of the audit trail; rewriting it
  retroactively would be lossy. Future contributors looking at the
  drift can find the divergence in the ticket itself and trace it
  forward to slice 324's reconciliation.

**Confidence: medium-high.** The principle "historical slice tickets are
not retroactively edited" is a project norm but is not formally written
down anywhere in `CLAUDE.md`. The choice here matches how prior docs-
alignment slices have handled similar drift (decisions log files are
written forward, not back; tickets stay frozen).

### 5. Watchlist cadence: annual, aligned with Q2 audit anniversary

**Chosen:** the `BootstrapTenantID` BYPASSRLS re-audit cadence is set
to **annual**, aligned with the Q2 security-audit anniversary (next
due 2027-Q2).

Alternatives considered:

- **Semi-annual** (Q2 + Q4). Rejected as over-engineered for a single
  BYPASSRLS path that is well-documented in source comments, has not
  changed since slice 210, and whose risk profile is unlikely to drift
  inside a 6-month window.
- **Annual, aligned with a fixed calendar date** (e.g. every 27 May).
  Rejected because the surrounding security-audit cadence is the
  natural hook for this kind of re-audit — folding the watchlist into
  the annual audit scope is one fewer recurring calendar item the
  maintainer has to track.
- **Quarterly.** Rejected. Way too frequent for a documented, stable,
  single-call-site BYPASSRLS path. Quarterly cadence would be
  appropriate for an actively-evolving auth surface, not for a
  stable graceful-degradation path.

**Confidence: medium.** The maintainer is the right judge here — if the
install-state surface meaningfully changes (additional fields,
additional callers, or retirement of the pre-slice-210-install
fallback path) the cadence should tighten. The watchlist entry says
this explicitly.

### 6. The watchlist file lives at `docs/audits/_FOLLOWUP_WATCHLIST.md`

**Chosen:** new file at `docs/audits/_FOLLOWUP_WATCHLIST.md`. Co-located
with the existing audit reports (`docs/audits/2026-Q2-security-audit.md`,
`docs/audits/2026-Q2-repo-cleanup.md`); the leading underscore signals
"this is the index / register, not a dated audit report" — matching the
pattern of `docs/issues/_STATUS.md` and `docs/issues/_INDEX.md`.

Alternatives considered:

- `docs/audit-log/_FOLLOWUP_WATCHLIST.md` (the decisions-log folder).
  Rejected. `docs/audit-log/` is per-slice JUDGMENT-decisions logs;
  it's the wrong taxonomic slot.
- `docs/audits/followups.md` (no underscore). Rejected. The leading
  underscore pattern matches `_STATUS.md` and `_INDEX.md`; readers
  scanning the directory pick up "this is the index, not a dated
  artifact" immediately.

**Confidence: high.** This is taxonomic-slot judgment, not architecturally
load-bearing. The file's content is what matters; the path is just
the file's address.

## Revisit once in use

- **Reword retention check (1 review cycle):** the next time a community
  connector PR opens against the repo, confirm the contributor's
  understanding of the wire contract (do they reference `Push` only?
  do they ask about a non-existent `Pull` RPC?). If a contributor
  still gets it wrong, the §3 callout needs to be louder — possibly
  a banner at the top of §3, possibly a `CONNECTOR_AUTHORING.md` at
  the repo root.
- **Watchlist hygiene (annual):** at each Q2 security audit, check
  whether the `BootstrapTenantID` entry is still the only entry in
  the file. If not, this file may need restructuring (e.g. group
  by risk class, not chronological).
- **Tone-discipline retention:** the reword stays within the project's
  measured-factual tone (CLAUDE.md AI-assist banned-phrase list). If a
  future reword by a different contributor introduces marketing-y
  framing ("we are proud to report we have aligned the docs..."),
  that's a regression and should be caught in PR review.

## What this slice deliberately did NOT do

- Add `Pull` / `Subscribe` / `Describe` / `AuthMethods` / `HealthCheck`
  / `ListEvidenceKinds` / `VerifyProvenance` RPCs to the wire (P0-324-1).
- Touch any `.proto` file beyond docstring stability (none were touched).
- Touch connector binaries or their `profiles_supported` declarations
  (P0-324-3).
- Touch `docs/issues/_INDEX.md` (P0-324-6).
- Roll up the OAuth grants map (slice 325) or the legacy-bearer 410
  responder retirement (slice 326) into this PR (P0-324-4).
- Roll up a second constitutional invariant rewording — none was found
  during the audit. If a future review surfaces another invariant
  that disagrees with shipped reality, that's a separate slice
  (P0-324 spillover discipline).
