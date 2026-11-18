// Slice 270 — /activity server-component shell. Since #2093 it is the
// one audit-log page: `/audit-log` and `/admin/audit` redirect here.
//
// The shell stays server-rendered so the page chrome lands in the
// initial HTML payload. All interactivity (URL-state-driven filters,
// infinite scroll, the sentence / raw-ledger views, row-expand) lives in
// the page-client island below.
//
// The route lives under `(authed)` and is reachable to every signed-in
// tenant member (slice 270 D4). The backend at `/v1/activity/unified` is
// the authoritative gate (slice 270 D1), and its SQL row-visibility
// predicate restricts non-privileged callers to tenant-public events
// plus their own me-rows; for admin / auditor / grc_engineer it returns
// the same rows `/v1/admin/audit-log/unified` does.
//
// The one thing that differs by role is export: `/v1/admin/audit-log/export`
// admits fewer callers, so the bar renders only when `canReachAuditLog`
// (the predicate the retired /audit-log layout used) admits the caller.
// It fails closed: any /v1/me failure hides the bar.

import { cookies } from "next/headers";

import { PageShell } from "@/components/shell/page-shell";
import { canReachAuditLog } from "@/lib/api/audit-log-access";
import { fetchAdminMe } from "@/lib/api/self.server";
import { ATLAS_JWT_COOKIE } from "@/lib/auth";
import { pageTitle } from "@/lib/page-titles";

import { ActivityPageClient } from "./page-client";
import { Term } from "@/components/help/term";

export default async function ActivityPage() {
  const jar = await cookies();
  const bearer = jar.get(ATLAS_JWT_COOKIE)?.value;
  const canExport = bearer
    ? canReachAuditLog(await fetchAdminMe(bearer))
    : false;
  return (
    <PageShell
      title={pageTitle("/activity")}
      subtitle={
        <>
          The <Term id="activity-log">activity log</Term>: what happened in this{" "}
          <Term id="tenant">tenant</Term> — state changes, evidence ingestion,
          audit milestones, and your own account activity. Filter by time
          window, actor, or event kind; switch to the raw ledger for kinds,
          targets and IDs. Read-only.
        </>
      }
      bodyClassName="space-y-4"
    >
      <ActivityPageClient canExport={canExport} />
    </PageShell>
  );
}
