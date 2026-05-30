# Slice 370 — `web/lib/api.ts` split: build-time decisions log

JUDGMENT slice. The maintainer iterates post-merge; this log records the
build-time calls so the rewrite is reviewable. Closes slice 328 audit
finding **H-2** (the 2901-LOC / 219-export `web/lib/api.ts` god-file).

## Phase scope of THIS PR

The slice doc lays out three phases across three PRs:

1. **Phase 1 (this PR):** split into per-domain files + backward-compat
   barrel shim at `web/lib/api.ts`. **Zero import-site churn** — the 176
   existing `@/lib/api` import sites keep working through the barrel.
2. **Phase 2 (spillover):** mechanical migration of the 176 import sites
   to per-domain paths.
3. **Phase 3 (spillover):** delete the barrel shim.

The eslint `max-lines` guard (AC-5) is folded into Phase 1 because the
new domain files are all well under 600 LOC and the rule is cheap to add
now; it also prevents the split from regressing before Phase 2/3 land.

### D1 — Split granularity: per-domain

Per-domain (14 files), NOT per-resource (40+). Matches the established
`web/lib/api/audit-*.ts` / `metrics.ts` / `*-export.ts` convention. The
existing `===== Slice NNN — <domain> =====` section banners in the
god-file already drew the domain boundaries; the split follows them.

### D2 — SESSION_COOKIE (M-3) rename: NOT bundled

Slice 328 §M-3 suggested folding the `SESSION_COOKIE → ATLAS_JWT_COOKIE`
rename into Phase 2's import-site migration. This PR is Phase 1
(structural split + barrel, zero import churn), so M-3 has no surface to
ride on here. Deferred to the Phase 2 spillover where the import sites
open anyway. P0-370-2 honored (no M-3 in Phase 1).

### D3 — Shim retention: one follow-up window

The barrel shim is a transition aid (P0-370-3). Phase 2 (import-site
migration) and Phase 3 (shim delete) are filed as spillover slices so
the shim does not linger beyond one slice's worth of follow-up.

### D4 — eslint rule strength: hard fail (`error`)

`max-lines: 600` as an `error` (hard CI fail), scoped to
`web/lib/api/**/*.ts` (excluding `*.test.ts`). Recommended by the slice
doc to prevent regression. Every new domain file is well under 600 LOC,
so the rule passes clean today.

### D5 — Shared internal helpers: `_shared.ts`

`web/lib/api.ts` had four non-exported helpers used by multiple
functions: `apiFetch`, `bffControlFetch`, `boardPackJSON`, `vendorQuery`.
Per the slice doc ("if two domains share a helper, extract to
`web/lib/api/_shared.ts`"):

- `apiFetch` (server-side bearer fetch + APIError) → `_shared.ts`,
  exported so domain files import it. Used by nearly every domain.
- `bffControlFetch` (browser BFF fetch + error-body unwrap) → `_shared.ts`.
  Used by controls, dashboard, risks-hierarchy, calendar.
- `boardPackJSON` → stays private inside `board.ts` (single-domain use).
- `vendorQuery` → stays private inside `vendors.ts` (single-domain use).

`apiBaseURL` and `APIError` are the two PUBLIC primitives the existing
siblings (`bff.ts`, `audit-server.ts`, `metrics.ts`) already import from
`@/lib/api`. They move to `base.ts`. The barrel re-exports them so those
siblings keep resolving `@/lib/api` unchanged.

**Barrel surface guarantee:** `_shared.ts` is the ONE file the barrel
does NOT `export *` from — `apiFetch`/`bffControlFetch` were never public
and must not become public. Every other new file is re-exported, so the
barrel's public surface is byte-for-byte the same 219 symbols (AC-1).

### D6 — Shared anchor types live with `anchors.ts`

`Anchor`, `FrameworkVersion`, `Requirement`, `RequirementWithMapping`,
`AnchorDetail` are catalog primitives. `AnchorWithState` (used by the
controls list) extends `Anchor`. Rather than a separate `types.ts`, the
anchor types stay in `anchors.ts` and `controls-list.ts` imports
`AnchorWithState` from it. Keeps the catalog vocabulary in one file.

## Split mapping (219 exports → 14 domain files)

| Target file                   | Domain                                                                          | Exports moved                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                    |
| ----------------------------- | ------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `lib/api/base.ts`             | URL + error primitives                                                          | `apiBaseURL`, `APIError`                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                         |
| `lib/api/_shared.ts`          | internal helpers (NOT re-exported)                                              | `apiFetch`, `bffControlFetch` (private→pkg-internal)                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                             |
| `lib/api/anchors.ts`          | SCF anchors + catalog types                                                     | `Anchor`, `FrameworkVersion`, `Requirement`, `RequirementWithMapping`, `AnchorDetail`, `AnchorState`, `AnchorWithState`, `listAnchors`, `listAnchorsWithState`, `getAnchorRequirements`                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                          |
| `lib/api/controls-list.ts`    | controls list + scope cells + tenant controls                                   | `ControlsListResponse`, `fetchControlsList`, `ScopeCell`, `ScopeCellsListResponse`, `listScopeCells`, `fetchScopeCells`, `TenantControl`, `TenantControlsListResponse`, `fetchTenantControls`, `fetchTenantControlsList`                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                         |
| `lib/api/vendors.ts`          | vendor lite                                                                     | `VendorCriticality`, `VendorReviewCadence`, `Vendor`, `VendorWrite`, `VendorBurndownBand`, `VendorBurndown`, `VendorListFilter`, `listVendors`, `getVendor`, `createVendor`, `updateVendor`, `getVendorBurndown`                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                 |
| `lib/api/framework-scopes.ts` | FrameworkScope                                                                  | `FrameworkScopeState`, `FrameworkScope`, `FrameworkScopeCreate`, `FrameworkScopePatchResponse`, `listFrameworkScopes`, `createFrameworkScope`, `patchFrameworkScopePredicate`, `transitionFrameworkScope`                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                        |
| `lib/api/attest.ts`           | manual attestation + artifacts                                                  | `AttestForm`, `AttestSubmitRequest`, `AttestSubmitResponse`, `getAttestForm`, `submitAttestation`, `ArtifactUploadResponse`, `uploadArtifact`                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                    |
| `lib/api/admin.ts`            | admin (credentials, features, super-admins, tenants, demo, sso)                 | `AdminCredential`, `AdminCredentialListResponse`, `AdminCredentialIssueRequest`, `AdminCredentialIssueResponse`, `AdminCredentialRotateResponse`, `listAdminCredentials`, `issueAdminCredential`, `rotateAdminCredential`, `revokeAdminCredential`, `FeatureFlag`, `FeatureFlagListResponse`, `FeatureFlagPatchResponse`, `listFeatureFlags`, `patchFeatureFlag`, `SuperAdminRow`, `SuperAdminListResponse`, `listSuperAdmins`, `grantSuperAdmin`, `demoteSuperAdmin`, `TenantRow`, `TenantListResponse`, `CreateTenantRequest`, `CreateTenantResponse`, `listAdminTenants`, `createAdminTenant`, `DemoStatusResponse`, `DemoSeedResponse`, `DemoTeardownResponse`, `getAdminDemoStatus`, `postAdminDemoSeed`, `postAdminDemoTeardown`, `AdminSSOConfig`, `AdminSSOPatchRequest`, `getAdminSSO`, `patchAdminSSO` |
| `lib/api/control-detail.ts`   | control detail view (coverage/state/effectiveness/scope/policies/risks/history) | `ControlWire`, `ControlAnchorWire`, `CoverageRequirement`, `ControlCoverage`, `ControlStateEntry`, `ControlStateResponse`, `ControlEffectiveness`, `EffectiveScopeCell`, `EffectiveScopeResponse`, `ControlLinkedPolicy`, `ControlLinkedPoliciesResponse`, `ControlLinkedRisk`, `ControlLinkedRisksResponse`, `ControlHistoryEntry`, `ControlHistoryResponse`, `getControlCoverage`, `getControlState`, `getControlEffectiveness`, `getControlEffectiveScope`, `getControlPolicies`, `getControlRisks`, `getControlHistory`, `fetchControlCoverage`, `fetchControlState`, `fetchControlEffectiveness`, `fetchControlEffectiveScope`, `fetchControlPolicies`, `fetchControlRisks`, `fetchControlHistory`                                                                                                          |
| `lib/api/dashboard.ts`        | program dashboard                                                               | `DriftRow`, `DriftReport`, `FreshnessBucket`, `FreshnessReport`, `DashboardRisk`, `RiskListResponse`, `ExpiringException`, `ExpiringExceptionsResponse`, `FrameworkPostureRow`, `FrameworkPostureReport`, `UpcomingItem`, `UpcomingResponse`, `ActivityEvent`, `ActivityFeedResponse`, `getControlDrift`, `getEvidenceFreshness`, `getMitigateRisks`, `getExpiringExceptions`, `getFrameworkPosture`, `getActivity`, `getUpcoming`, `fetchDashboardDrift`, `fetchDashboardFreshness`, `fetchDashboardRisks`, `fetchDashboardUpcoming`, `fetchDashboardFrameworkPosture`, `fetchDashboardActivity`                                                                                                                                                                                                                |
| `lib/api/risk-hierarchy.ts`   | hierarchical risk dashboard                                                     | `OrgUnit`, `OrgUnitListResponse`, `RiskTheme`, `RiskThemeListResponse`, `AggregationRule`, `AggregationRuleListResponse`, `Decision`, `DecisionListResponse`, `DecisionFilter`, `getOrgUnits`, `getRiskThemes`, `getAggregationRules`, `getDecisions`, `getOverdueDecisions`, `fetchHierarchyOrgUnits`, `fetchHierarchyThemes`, `fetchHierarchyAggregationRules`, `fetchHierarchyDecisions`, `fetchHierarchyOverdueDecisions`                                                                                                                                                                                                                                                                                                                                                                                    |
| `lib/api/board.ts`            | quarterly board pack                                                            | `BoardPackSectionData`, `BoardPackSection`, `BoardPackContent`, `BoardPack`, `BOARD_PACK_SECTION_KEYS`, `BoardPackSectionInputs`, `listBoardPacks`, `generateBoardPack`, `getBoardPack`, `updateBoardPackSection`, `approveBoardPackSection`, `publishBoardPack`, `boardPackMarkdownURL`, `boardPackPdfURL`, `SessionMe`, `getSessionMe`                                                                                                                                                                                                                                                                                                                                                                                                                                                                         |
| `lib/api/calendar.ts`         | compliance calendar                                                             | `CalendarEventType`, `CalendarEvent`, `CalendarResponse`, `CalendarSubscriptionResponse`, `getCalendarEvents`, `postCalendarSubscription`, `fetchCalendarEvents`, `createCalendarSubscription`                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                   |
| `lib/api/risks.ts`            | risks list + create                                                             | `Risk`, `RisksListResponse`, `listRisks`, `fetchRisksList`, `RiskCreateInput`, `RiskCreatedResponse`, `createRisk`                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                               |
| `lib/api/audit-periods.ts`    | audits list                                                                     | `AuditPeriod`, `AuditPeriodsListResponse`, `fetchAuditPeriods`                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                   |
| `lib/api/evidence.ts`         | evidence list                                                                   | `EvidenceResultEnum`, `EvidenceRecord`, `EvidenceListResponse`, `EvidenceListFilters`, `fetchEvidenceList`                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                       |
| `lib/api/exceptions.ts`       | exceptions list                                                                 | `ExceptionStatus`, `Exception`, `ExceptionsListResponse`, `ExceptionsListFilters`, `fetchExceptionsList`                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                         |
| `lib/api/policies.ts`         | policies list                                                                   | `PolicyAckRate`, `Policy`, `PoliciesListResponse`, `listPolicies`, `fetchPoliciesList`                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                           |
| `lib/api/me.ts`               | /v1/me/\* profile + prefs + sessions                                            | `MeProfile`, `MePatchRequest`, `MePreferences`, `MeSession`, `MeSessionsResponse`, `getMe`, `patchMe`, `getMyPreferences`, `patchMyPreferences`, `listMySessions`, `revokeMySession`                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                             |

Note: the SSO/credentials/features/super-admins/tenants/demo domains are
co-located in one `admin.ts` (they share the `/v1/admin/*` surface and
the admin section consumes them together) rather than split into 6 tiny
files — per-domain granularity (D1), and the admin domain IS one domain.

`web/lib/api.ts` becomes a barrel that `export *`s from each of the 17
re-exported files (all except `_shared.ts`).

### D7 — coverage ratchet (slice 347 contract)

Splitting changes which files v8 measures. Procedure followed:

1. Run `npm run test -- --coverage` after the split.
2. `lib/api.ts` becomes a pure re-export barrel (no executable logic) →
   measures ~0% / no signal. Its old floor (16/88/15/16) is removed; a
   barrel that only re-exports has no meaningful per-line floor (the
   methodology omits 0%-signal files — `$omitted_zero_pct_rationale`).
3. For each NEW `lib/api/*.ts` file, seed a floor at
   `floor(measured - 2pp)`. Files measuring 0% are intentionally omitted
   (same methodology) — they carry no enforcement signal until a first
   test lands. `lib/api/base.ts` carries `apiBaseURL` whose coverage is
   driven by `lib/api.test.ts` (which imports it through the barrel; v8
   attributes coverage to the physical source file).
4. The aggregate bar is not lowered — coverage that lived under
   `lib/api.ts` now lives under the new files; the same lines execute.

Exact seeded floors are in the same commit's `coverage-thresholds.json`
diff.
