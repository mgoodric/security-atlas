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

import { PageShell } from "@/components/shell/page-shell";

import { ActivityPageClient } from "./page-client";
import { pageTitle } from "@/lib/page-titles";

export default function ActivityPage() {
  return (
    <PageShell
      title={pageTitle("/activity")}
      subtitle="Tenant activity ledger: state changes, evidence ingestion, audit milestones, and your own audit trail. Filter by time window, actor, or event kind; expand a row to see its raw payload. Read-only."
      bodyClassName="space-y-4"
    >
      <ActivityPageClient />
    </PageShell>
  );
}
