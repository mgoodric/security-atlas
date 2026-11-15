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

import type { Metadata } from "next";

import { PageShell } from "@/components/shell/page-shell";

import { AuditLogPageClient } from "./page-client";

export const metadata: Metadata = { title: "Audit log" };

export default function AuditLogPage() {
  return (
    <PageShell
      title="Audit log"
      subtitle="Every audit event across the tenant, in one paginated view. Filter by time window, actor, or event kind; expand a row to see its raw payload. Read-only."
      bodyClassName="space-y-4"
    >
      <AuditLogPageClient />
    </PageShell>
  );
}
