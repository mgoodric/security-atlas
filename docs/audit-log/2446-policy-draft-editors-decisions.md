# Issue #2446: policy draft editors count as authors

Follow-ups from the review of #2440 (#2116). Before this change, the
approval rule compared the approver with the submitter only. Draft edits
were not attributed. An admin could write a draft, have a grc_engineer
submit it, and approve it with no flag.

- `detection_tier_actual`: manual_review
- `detection_tier_target`: unit

## D1: The editor set lives on the policy row

**Decision.** `policies.draft_editors TEXT[]` holds the principal
(`credstore.PrincipalOf`, the bare users(id) uuid for a person) of the
creator and of every caller of `PATCH /v1/policies/{id}`, each once, in
first-edit order. It does not hold a single `last_edited_by`. The
`UpdateDraftPolicy` UPDATE appends the editor in the same statement that
writes the edit.

**Why.** The approval check needs every author, not only the last one.
Reading the set off the row it already loads keeps the check in the same
transaction as the approval, with no extra query. `policy_audit_log` holds
the who-and-when history, and the row holds the set.

## D2: Every editor is an author for the approval rule

**Decision.** `checkApprover` runs the submitter rule unchanged, then
compares the approver (`UserID`, or `CredentialID` for a key-recorded
principal) with each editor. A match is refused with `ErrEditorApproval`
(403) when self-approval is off. When it is on, the match is allowed and
flagged `self_approved`, so the audit row is `self_approved` too.

**Why.** Writing the text is authoring it, whoever presses submit. The
outcome matches the submitter rule, so one setting covers both cases.
`ErrEditorApproval` has its own message so the 403 says why.

## D3: Pre-migration drafts keep the submitter-only check

**Decision.** Existing rows start with `draft_editors = '{}'`. No backfill
from `created_by`.

**Why.** `created_by` is the raw credential id (`jwt:<jti>` for a session),
which names no person. A backfill would either compare nothing or refuse
every approval. Drafts open at upgrade time are few, and the next edit
records its editor.

## D4: The audit log records a body hash and the editor set, never the body

**Decision.** `policy_audit_log` gains `body_sha256` (hex, CHECKed to 64
lowercase hex chars) and `editors TEXT[]`. A `draft_edited` row (new
action value) carries the actor and the hash. `submitted` and
`resubmitted` rows carry the hash and the editor set. The CHECK is
rewritten from the latest definition (`20261108000000`), keeping every
earlier value.

**Why.** The hash ties an approval to the exact text submitted without
copying policy text into an audit table. The approver can show what they
approved by re-hashing the row's body. Keeping the body out follows the
#2375 rule that unified-log payloads hold no more than the source read.

## D5: Linked controls must resolve in the tenant: 422

**Decision.** Create and edit count `controls` rows with
`tenant_id = $tenant AND id = ANY($ids)`. If the count is short of the
distinct ids, the request answers 422 with `ErrUnknownLinkedControl`. A
malformed UUID stays 400, as before.

**Why.** The id is well-formed but names nothing the caller can link, which
makes it a semantic error rather than a syntax error. Before this check,
any UUID, including another tenant's control, satisfied the orphan-publish
rule.

## D6: 1 MiB request cap, 413

**Decision.** `http.MaxBytesReader` caps the create and edit bodies at
1 MiB. An oversized body answers 413, and other decode failures stay 400.

**Why.** Policy documents are tens of KB. 1 MiB leaves ample headroom and
bounds the memory one request can hold.
