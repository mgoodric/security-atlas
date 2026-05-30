// Slice 370 — backward-compat barrel for the per-domain api client split.
//
// `web/lib/api.ts` was a 2901-LOC, 219-export god-file (slice 328 audit
// finding H-2). Slice 370 split it into per-domain modules under
// `web/lib/api/*.ts` matching the convention already established next
// door (`bff.ts`, `audit-server.ts`, `metrics.ts`, `*-export.ts`).
//
// This file is now a transition shim: every symbol the god-file exported
// is re-exported here so the 176 existing `@/lib/api` import sites resolve
// unchanged (zero import-site churn in this PR). The import-site migration
// to the per-domain paths is Phase 2 (a separate, mechanical PR); deleting
// this shim is Phase 3. Both are filed as spillover slices — the shim is a
// transition aid, not a permanent layer (slice 370 P0-370-3).
//
// NOTE: `./api/_shared` is intentionally NOT re-exported — `apiFetch` and
// `bffControlFetch` were private helpers in the god-file and must stay
// internal to the api package; re-exporting them would widen the public
// surface beyond the original 219 symbols.

export * from "./api/base";
export * from "./api/anchors";
export * from "./api/controls-list";
export * from "./api/vendors";
export * from "./api/framework-scopes";
export * from "./api/attest";
export * from "./api/admin";
export * from "./api/control-detail";
export * from "./api/dashboard";
export * from "./api/risk-hierarchy";
export * from "./api/board";
export * from "./api/calendar";
export * from "./api/risks";
export * from "./api/audit-periods";
export * from "./api/evidence";
export * from "./api/exceptions";
export * from "./api/policies";
export * from "./api/me";
