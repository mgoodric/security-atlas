// Slice 125 — /audit-log server-component shell.
//
// AC-1. The shell stays server-rendered so the page chrome (title +
// subtitle + outer layout container) lands in the initial HTML payload.
// All interactivity (URL-state-driven filters, TanStack-Query-driven
// infinite scroll, row-expand) lives in the page-client island below.
//
// The route is guarded by /audit-log/layout.tsx (admin OR future-auditor
// preflight) BEFORE this component renders. By the time this component
// runs, the caller has cleared the route-level guard.

import { AuditLogPageClient } from "./page-client";

export default function AuditLogPage() {
  return (
    <div className="mx-auto max-w-screen-2xl space-y-4 p-4 sm:p-6">
      <div>
        <h1 className="text-2xl font-semibold tracking-tight">Audit log</h1>
        <p className="text-sm text-muted-foreground mt-0.5">
          Every audit event across the tenant, in one paginated view. Filter by
          time window, actor, or event kind; expand a row to see its raw
          payload. Read-only.
        </p>
      </div>
      <AuditLogPageClient />
    </div>
  );
}
