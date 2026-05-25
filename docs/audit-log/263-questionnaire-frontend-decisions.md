# Slice 263 — decisions log

> JUDGMENT slice. Eight design points resolved via `/idea-to-slice`
> grilling on 2026-05-24 against the slice 155 backend +
> `Plans/mockups/questionnaire.html`. The maintainer iterates
> post-deployment if any decision proves wrong; the build-time call
> lives here.

## Decisions

### D1 — Empty-state shape

**Question:** When the tenant has zero questionnaires, what does the
list view render?

**Options:**

- (a) Single hero CTA: drag-drop zone only.
- (b) Hero CTA + helper-text card explaining the workflow.
- (c) Hero CTA + a "Try a sample CAIQ" button.

**Choice:** **(a) Single hero CTA.** No roster cards, no helper-text
card, no sample-questionnaire CTA.

**Rationale:** The empty state is honest. A helper card adds visual
noise without adding signal; a sample CTA invites the operator to
populate the tenant with throw-away data that pollutes the library.
The drag-drop zone is self-explanatory.

**Codified at:** AC-2, AC-11 (empty state), ISC-11.

### D2 — Post-upload navigation

**Question:** After a successful Excel import, where does the operator
land?

**Options:**

- (a) Stay on the list view; show the new questionnaire as a row.
- (b) Navigate directly to Stage C (`/questionnaires/{id}`).
- (c) Show a Stage B column-mapping review intercept first.

**Choice:** **(b) Navigate directly to Stage C.** Stage B is deferred
to slice 264 per P0-263-5; the platform's header-row heuristic auto-
detects the column shape at import time today.

**Rationale:** The operator's intent on dragging an .xlsx onto the
zone is "now let me answer questions." Bouncing them back to a roster
is friction; intercepting with a column-mapping review they
overwhelmingly accept verbatim is bigger friction. If the heuristic
miss rate proves high in shipped data, slice 264 re-introduces the
intercept without changing this wire shape.

**Codified at:** AC-5, ISC-17.

### D3 — Suggestions endpoint ranking

**Question:** How does the suggestions panel rank prior answers?

**Options:**

- (a) Top 3 by SCF-anchor frequency (deterministic, slice 155 D2).
- (b) Top 3 by similarity score from an LLM embedding.
- (c) Top 3 by most-recent.

**Choice:** **(a) Top 3 by SCF-anchor frequency** as ranked by the
slice 155 `SuggestForAnchor` SQL query — most-recent-N for the given
anchor. Slice 155 D2 already locked this in; this slice consumes
verbatim.

**Rationale:** (b) violates P0-263-1's AI-assist boundary (no LLM in
this slice). (c) is what slice 155 already does internally; (a) is
the operator-facing label for the same shape. The panel renders the
top-N verbatim — the FE does not re-rank.

**Codified at:** AC-12, ISC-26, ISC-27.

### D4 — Save-to-library control

**Question:** How does the operator promote an answer to the canonical
AnswerLibrary?

**Options:**

- (a) Per-answer checkbox, default OFF.
- (b) Auto-save every authored answer (no toggle).
- (c) Separate "Save to library" button next to the save button.

**Choice:** **(a) Per-answer checkbox, default OFF.** Located below
the citation chips so the affordance is co-located with the answer.
The autosave PATCH carries `save_to_library: true` when the box is
checked.

**Rationale:** (b) pollutes the library with draft / wrong / one-off
answers — the library is the source of truth for "what we say about
SCF:IAC-06" and must be operator-curated. (c) is two clicks for the
common case; (a) lets the operator promote inline.

**Codified at:** AC-18, ISC-35, ISC-36.

### D5 — Citation picker

**Question:** How does the operator attach a control / evidence
citation to an answer?

**Options:**

- (a) Unified ⌘K-style command palette wrapping slice 268's
  `/v1/search` (controls + evidence in one palette).
- (b) Two separate buttons (Cite control / Cite evidence) each opening
  a typed list.
- (c) A modal dialog with tabs.

**Choice:** **(a) Unified ⌘K palette via slice 268.** Builds a
minimal command-palette popover inline (shadcn `<Command>` is not in
the dep tree; the popover follows the slice 223 global-search.tsx
pattern with input + grouped results). The palette posts to
`/api/search?types=controls,evidence`.

**Rationale:** Reuse > rebuild. Slice 268's unified search is exactly
the shape we need — typed-namespace search returning controls +
evidence rows with relevance scores. (b) duplicates the search code.
(c) is heavy chrome for a quick attach.

**Codified at:** AC-15, AC-16, AC-17, ISC-31, ISC-32.

### D6 — Sidebar placement

**Question:** Where in the sidebar does Questionnaires live?

**Options:**

- (a) Operations cluster (same cluster as Calendar / Vendors).
- (b) Audit cluster (next to Audits / Policies).
- (c) Top-level under Dashboard.

**Choice:** **(a) Operations cluster.** Entry visible to ALL authed
users (matches Calendar/Vendors precedent). Per-tenant write authz is
enforced at the API layer (slice 155).

**Rationale:** A questionnaire is an operations artifact — vendor
diligence, customer security review, etc. It's not strictly an audit-
period artifact (those are SOC 2 / ISO type-II). Putting it next to
Vendors is the most intuitive match for the workflow the security
leader runs.

**Codified at:** AC-21, AC-22, ISC-38, ISC-39.

### D7 — AI-assist boundary enforcement

**Question:** What does "no AI-assist in this slice" mean at the UI
layer?

**Options:**

- (a) Suggestions endpoint is deterministic (slice 155 D2); calling
  it from the FE is fine because it's not an LLM call. The panel
  styling must avoid implying AI inference.
- (b) Don't call the suggestions endpoint at all in this slice.
- (c) Call the endpoint but wrap the panel in an "Experimental: AI-
  assisted suggestions" badge to set expectations.

**Choice:** **(a) Deterministic suggestions only; styling avoids AI
language.** The panel header reads "Prior answers for SCF:<anchor>"
— operator-authored prior answers surfaced for copy-paste. No model
badges, no confidence numbers, no retrieval-context panels.

**Rationale:** The suggestions endpoint is a SQL "most-recent-N for
this anchor" query. Hiding it would be wasteful (it's already
shipped). Calling it but advertising it as AI would be dishonest +
violate P0-263-1.

**Codified at:** AC-12, AC-13, ISC-26..ISC-30, P0-263-1 invariant.

### D8 — Stage B disposition

**Question:** Does this slice ship Stage B (column-mapping review)?

**Choice:** **No.** Deferred to slice 264 per P0-263-5. Stage A → Stage
C goes directly; Stage B is a post-upload intercept that doesn't break
the wire shape when added later.

**Rationale:** Slice 155 D3 already deferred manual column-mapping;
the platform's header-row heuristic does the work today. Re-introducing
Stage B is a small slice (the upload response already carries
`unmapped_columns` if the heuristic missed). Reserving for slice 264.

**Codified at:** P0-263-5, "What ships in this slice / Stage B".

## Notes for future maintainers

- The set-state-in-effect ESLint rule (`react-hooks/set-state-in-effect`)
  is honored throughout the questionnaire components via the
  `setTimeout(0)` microtask pattern slice 223 established. Do NOT
  remove the wrappers without auditing the cascading-render risk.
- The slice 268 unified search endpoint is the single citation
  authority. If a future slice adds a "Cite a policy" affordance, it
  should extend slice 268's type whitelist (add `policies` to the
  union) rather than building a parallel picker.
- The suggestions panel is the canonical example of "deterministic
  prior-answer surfacing presented honestly." Future "AI suggestions"
  work (if any lands per the CLAUDE.md AI-assist boundary) should
  carry the model-name + version metadata fields the boundary
  requires, and should sit in a separate panel — not by re-skinning
  this one.
