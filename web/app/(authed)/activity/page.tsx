// Slice 270 — non-admin /activity ledger surface (server-component shell).
//
// AC-1. The shell stays server-rendered so the page chrome (title +
// subtitle + outer layout container) lands in the initial HTML payload.
// All interactivity (URL-state-driven filters, TanStack-Query-driven
// infinite scroll, row-expand) lives in the page-client island below.
//
// The route lives under `(authed)` and is reachable to every signed-in
// tenant member (slice 270 D4 — sidebar entry renders for all authed
// users, no role gate). The backend at `/v1/activity/unified` is the
// authoritative gate (slice 270 D1 — five-role OPA admit via the
// existing `"activity"` resource type), and the SQL-layer row-visibility
// predicate restricts non-privileged callers to tenant-public events
// plus their own me-rows.

import { ActivityPageClient } from "./page-client";

export default function ActivityPage() {
  return (
    <div className="mx-auto max-w-screen-2xl space-y-4 p-4 sm:p-6">
      <div>
        <h1 className="text-2xl font-semibold tracking-tight">Activity</h1>
        <p className="text-sm text-muted-foreground mt-0.5">
          Tenant activity ledger: state changes, evidence ingestion, audit
          milestones, and your own audit trail. Filter by time window, actor,
          or event kind; expand a row to see its raw payload. Read-only.
        </p>
      </div>
      <ActivityPageClient />
    </div>
  );
}
