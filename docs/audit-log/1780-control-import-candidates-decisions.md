# Issue #1780 — Control import: suggestions that need no LLM

Build-time calls made while building
[#1780](https://github.com/mgoodric/security-atlas/issues/1780), under the
bring-your-own-controls spec
([#1734](https://github.com/mgoodric/security-atlas/issues/1734)): every
staged row gets ranked SCF anchor candidates, from the crosswalk and from
catalog keyword search, with no LLM involved.

`detection_tier_actual = none` · `detection_tier_target = none` (no bug
surfaced during the work).

## D1 — Candidates are computed in `Stage`, after the batch commits

**Decision.** `Stager.Stage` commits the batch first, then computes and stores
candidates. A batch with up to 200 staged rows (`AsyncSuggestThreshold`) gets
them before the upload returns. A larger one returns with
`suggestions_status = pending`, and a goroutine on
`context.WithoutCancel(ctx)` fills them in. If computing them fails, the batch
is marked `failed` and the upload still succeeds.

**Why.** The ticket asks for candidates when rows are staged, done in the
background for large files. Committing first means a failed suggestion pass
can't lose a staged upload. 200 rows covers the common "hundred or so
controls" sheet synchronously. The background run is in-process: if the
server restarts mid-run the batch stays `pending`. That's acceptable until the
approval tickets need a retry action. `suggestions_status` makes the state
visible, so nobody has to guess.

## D2 — The SCF release is the current global `scf` version, recorded on the batch

**Decision.** `control_import_batches` gains `scf_framework_version_id`,
`scf_release` and `suggestions_status`. The release is the newest `current`
version of the global `scf` framework, resolved at staging. Crosswalk lookups
are pinned to it through
`ListAnchorsForRequirementWithEdgesByFrameworkVersion`. With no SCF release,
the batch is `unavailable` and rows get no candidates.

**Why.** Approval (#1770) binds an anchor within a release. Recording the
release at staging keeps that anchor's meaning fixed. The pinned query is the
existing requirement-to-anchors lookup the coverage API uses. It reads
`effective_fw_to_scf_edges`, so the tenant overlay edges (#1735) will reach
import candidates without further change.

## D3 — Keyword retrieval reuses `qmapsuggest.Store.RetrieveCandidates`

**Decision.** `controlimport.Store.KeywordAnchors` calls the question-mapping
retrieval, then ranks the results with the importer's existing
`rankAnchorCandidates` (the same keyword-overlap ranker). Nothing is exported
from `qmapsuggest` beyond what it already exported.

**Why.** This is what the ticket asks for. One caveat: that retrieval reads
every `current` SCF version rather than the batch's pinned one. While only one
release is current, that's the same set. If a second current release ever
appears, the release-aware resolver (#1770) is the place to narrow it.

## D4 — Ranking: criteria reached, then the strongest reaching edge

**Decision.** Crosswalk candidates sort by how many of the row's tested
criteria they reach, then by the strongest edge among those reaches, then by
`scf_id`. Keyword candidates (ordered by overlap) follow, skipping anchors
already listed, until the list holds `CandidateListSize` (8) entries.
Crosswalk candidates can fill the whole list on their own.

**Why.** The ticket specifies "criteria reached, then mapping strength". The
maximum is the natural single strength for an anchor reached through several
edges: it keeps an anchor that one strong mapping reaches above one that
several weak mappings reach, among anchors that reach the same number of
criteria.

## D5 — The flag needs tested criteria

**Decision.** `no_candidate_reaches` is set only when a row has tested-under
criteria and no candidate reaches any of them. A row with no tested-under data
is not flagged.

**Why.** The flag means "this may need a tenant overlay edge or an
`anchor_fit` note". A row with no criteria has nothing to reach, so flagging it
would mix it into the hard cases the filter is for.

## D6 — No AI-assist guard columns on candidates

**Decision.** `control_import_candidates` has no `ai_assisted` /
`human_approver` columns, and `source` is limited to `crosswalk | keyword`.

**Why.** Nothing in this table comes from an LLM. The optional LLM rerank is a
later ticket. When it adds `llm` as a source, it adopts
`ai_assist_human_approver_guard` (slice 498), not an inline predicate.

## D7 — One rows endpoint; the batch read carries candidates too

**Decision.** `GET /v1/controls/imports/{id}/rows?status=&no_candidate_reaches=`
returns the batch's release, its suggestions status, and the filtered rows
with candidates. The upload response and `GET /v1/controls/imports/{id}` also
carry each staged row's candidates and flag, from the same row reader. An
unknown `status` or a non-boolean flag is a 400.

**Why.** One reader means one shape. The filtered endpoint is what the review
page and `controls import show` consume.

## D8 — Fixture anchors live in the current SCF release

**Decision.** The integration fixture now inserts its four anchors, with
per-test `scf_id`s, into the current global `scf` release (creating one if the
database has none, as the controls list tests do), not into a per-test
SCF-shaped framework. The fixture CSV gains a CC7.1-only row (ORG-OP-01),
which no anchor reaches.

**Why.** Candidates come from the current SCF release, so the fixtures must
sit in it. Keyword assertions check the source and that candidates exist, not
specific anchors, because the shared catalog may hold other matches.
