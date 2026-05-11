# 018 — FrameworkScope predicate + intersection compute + four-state scope-versioning workflow

**Cluster:** Scope + FrameworkScope
**Estimate:** 2d (bumped from 1.5d after ADR-0001 baked in the four-state workflow)
**Type:** AFK

## Narrative

Implement the per-framework scope predicate and the compute that intersects with `Control.applicability_expr` to produce `effective_scope(control, framework)` — the cells where a control's evidence actually counts for a given framework. For v1, SOC 2 is auditor-defined; the org enters a predicate via a small UI form.

FrameworkScope is governed by the four-state lifecycle decided in [`docs/adr/0001-framework-scope-workflow.md`](../adr/0001-framework-scope-workflow.md): `draft → review → approved → activated`, with a separate `superseded` terminal. Approval ≠ activation — the auditor signs off on the predicate; the org picks `effective_from` separately. Approval evidence is system-signed in-app attestation (always) plus optional file upload (for offline-signed memos). Any predicate edit on `review` or `approved` rows bounces back to `draft` and clears the approval columns — strict re-approval matches PCI's reduce-aggressively pattern.

The compute path: for any `(control, framework)` query, intersect `control.applicability_expr` with the currently-activated `framework_scope.predicate` and return the resulting cells.

The slice delivers value because PCI/HIPAA in phase 2 inherit this infrastructure without rework, AND because slice 030's OSCAL export can serialize a properly-attested scope statement.

## Acceptance criteria

### Schema + workflow state machine

- [ ] AC-1: Migration adds `framework_scopes.state` text column (CHECK enum: `draft | review | approved | activated | superseded`), `predicate` jsonb (boolean over scope dimensions, same shape as slice 017's `applicability_expr`), `predicate_hash` text (sha256 of canonicalized predicate, recomputed on save), and the approval/activation columns per ADR-0001 (`approver_user_id`, `approved_at`, `predicate_hash_at_approval`, `approval_evidence_file_url`, `approval_evidence_file_hash`, `effective_from`, `superseded_by`, `superseded_at`)
- [ ] AC-2: `BEFORE UPDATE` trigger on `framework_scopes` compares `OLD.predicate_hash` to `NEW.predicate_hash`; if they differ AND `OLD.state IN ('review', 'approved')`, forces `NEW.state='draft'` and NULLs the approval columns (`approver_user_id`, `approved_at`, `predicate_hash_at_approval`, `approval_evidence_file_url`, `approval_evidence_file_hash`). Integration test proves this rule fires.
- [ ] AC-3: Partial unique index enforces at most one row per `(tenant_id, framework_version_id)` in state `activated` with `effective_from <= now()`. Integration test attempts to activate a second row and gets a constraint violation.
- [ ] AC-4: RLS on `framework_scopes` follows the slice 014 / 017 four-policy pattern (`tenant_read`, `tenant_write WITH CHECK`, `tenant_update USING + WITH CHECK`, `tenant_delete`) so cross-tenant access fails. Integration test verifies cross-tenant denial via NOSUPERUSER NOBYPASSRLS app role.

### API

- [ ] AC-5: `POST /v1/framework-scopes` creates a `draft` row with `predicate` validated as a JSON-encoded boolean over declared scope dimensions (reuses slice 017's `applicability_expr` validator).
- [ ] AC-6: `PATCH /v1/framework-scopes/{id}/submit` transitions `draft → review`; requires no extra fields.
- [ ] AC-7: `PATCH /v1/framework-scopes/{id}/approve` transitions `review → approved`; requires authenticated `approver` role; records `approver_user_id` from credstore, `approved_at = now()`, `predicate_hash_at_approval = predicate_hash`. Optional body: `approval_evidence_file_url` + `approval_evidence_file_hash` (URL points at slice-036 storage; hash recorded but not verified).
- [ ] AC-8: `PATCH /v1/framework-scopes/{id}/activate` transitions `approved → activated`; requires `effective_from` in the body (timestamptz). Atomically supersedes the previously-activated row for the same `(tenant_id, framework_version_id)` (sets its `superseded_by` to this row's id, `superseded_at = now()`, `state = 'superseded'`).
- [ ] AC-9: `PATCH /v1/framework-scopes/{id}` with a new `predicate` body, regardless of current state — the trigger from AC-2 ensures the row ends up in `draft` if it was in `review` or `approved`. Handler responds 200 with the new state + a deprecation-banner-friendly JSON field `{"approval_invalidated": true}` so the UI can show the banner.
- [ ] AC-10: `GET /v1/framework-scopes?framework_version=SOC2:2017&state=activated` returns the currently-active scope for that framework. Filterable by `state`.
- [ ] AC-11: `GET /v1/controls/{id}/effective-scope?framework_version=SOC2:2017` returns the intersection of the control's `applicability_expr` with the currently-active `framework_scope.predicate`. An out-of-scope control returns empty `effective_scope`; the coverage computation downstream yields `n/a` (NOT fail).

### Seeding + versioning

- [ ] AC-12: SOC 2 default `FrameworkScope` seedable via config — a single `activated` row keyed to the `SOC2:2017` framework version, predicate `true` (everything in scope) is the safe default. Solo deployments use this without modification.
- [ ] AC-13: Historical queries (`?as_of=<timestamp>`) on `GET /v1/framework-scopes` return the row that was `activated` at that timestamp (the row whose `effective_from <= as_of` AND (`superseded_at IS NULL OR superseded_at > as_of`)).

### UI (Next.js, Web frontend)

- [ ] AC-14: `/framework-scopes/{framework_version_slug}` page lists active + historical scopes for the framework; current `activated` row highlighted; `draft` and `review` rows shown with state badges.
- [ ] AC-15: Edit form for `draft` and `review` rows: JSON-encoded predicate textarea + a simple visual builder (selectors per declared scope dimension); save submits the predicate; banner surfaces `approval_invalidated: true` from the API.
- [ ] AC-16: Approve action visible only to users with `approver` role; modal asks for optional evidence file upload + confirmation.
- [ ] AC-17: Activate action shows an `effective_from` date/time picker; defaults to `now()`.

## Constitutional invariants honored

- **Invariant 5 (FrameworkScope intersection):** the entire premise of this slice
- **Invariant 6 (RLS):** four-policy tenant isolation (matches slice 014/017 pattern)
- **Invariant 8 (OSCAL wire format — partial):** approval timestamps and predicate text are OSCAL-exportable; slice 030 uses these in SSP "Implementation Statement" emission

## Canvas references

- `Plans/canvas/05-scopes.md` §5.5 (FrameworkScope entity + intersection model)
- `docs/adr/0001-framework-scope-workflow.md` (this slice's workflow design)

## Dependencies

- #017 (scope dimensions + applicability_expr engine — for predicate validation + cell enumeration)
- #034 OPTIONAL (OIDC RP + local users + role plumbing — if not merged, this slice can reuse the slice-014 `IsAdmin`-style role gate and add a sibling `IsApprover` check; full RBAC arrives in 035)
- #036 OPTIONAL (S3 artifact store — for the optional `approval_evidence_file` upload; this slice ships URL+hash columns and a stub upload path until 036 lands)

## Anti-criteria (P0)

- Does NOT permit `predicate` mutation on `activated` rows without forcing the row back to `draft` (must be a new version, with re-approval). The trigger in AC-2 is non-negotiable.
- Does NOT permit two simultaneously-`activated` rows for the same `(tenant_id, framework_version_id)` (partial unique index in AC-3).
- Does NOT auto-approve scope changes.
- Does NOT compute coverage over cells outside the currently-active framework's scope predicate.
- Does NOT verify the signature on the uploaded `approval_evidence_file` — the system records the file's content hash but treats verification as the auditor's domain (anti-pattern: pretending to have cryptographic provenance the system doesn't have).
- Does NOT branch behavior on `tenant_count == 1` (per #13 resolution: multi-tenant is the baseline; solo is just a single-tenant deployment).

## Skill mix (3–5)

- Go predicate engine (intersection compute reusing slice 017's evaluator)
- Postgres versioned-row patterns + state-machine triggers (`BEFORE UPDATE`)
- API design with workflow state transitions (separate sub-resource endpoints: `/submit`, `/approve`, `/activate`)
- Compliance domain modeling (auditor-vs-org role separation)
- Next.js form (predicate editor UI + state-badge surfaces)
