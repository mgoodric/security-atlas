// Slice 042 — /audit landing page (AC-1, AC-6).
//
// AC-1: lands the auditor in their assigned AuditPeriod context. The
// page resolves the period server-side and renders the AuditShell (which
// renders the AuditPeriodBar). When the caller has no assigned period it
// renders a documented empty-state instead of erroring.
//
// AC-6: when the session cookie is missing the layout already redirected
// to /login?from=/audit; if the platform returns 401 here (stale cookie)
// the page redirects too. A signed-in non-auditor gets the "no period"
// empty-state.

import type { Metadata } from "next";
import { redirect } from "next/navigation";

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { AuditShell } from "@/components/audit/audit-shell";
import { PageShell } from "@/components/shell/page-shell";
import { resolveAuditPeriod } from "@/lib/api/audit-server";

export const metadata: Metadata = { title: "Audit workspace" };

export default async function AuditLandingPage() {
  const resolution = await resolveAuditPeriod();

  if (resolution.kind === "unauthenticated") {
    redirect("/login?from=/audit");
  }

  if (resolution.kind === "no-period") {
    return <NoPeriodState />;
  }

  if (resolution.kind === "error") {
    return (
      <div className="overflow-y-auto p-4 sm:p-6">
        <PageShell title="Audit workspace" width="reading">
          <Alert variant="destructive">
            <AlertTitle>Could not load the audit workspace</AlertTitle>
            <AlertDescription>
              The platform returned an unexpected status ({resolution.status})
              resolving your assigned audit period. Try again shortly.
            </AlertDescription>
          </Alert>
        </PageShell>
      </div>
    );
  }

  return <AuditShell period={resolution.period} />;
}

function NoPeriodState() {
  return (
    <div className="overflow-y-auto p-4 sm:p-6">
      <PageShell
        title="Audit workspace"
        width="reading"
        data-testid="audit-no-period"
      >
        <Alert>
          <AlertTitle>No audit period assigned</AlertTitle>
          <AlertDescription>
            Your account is signed in but is not assigned to any audit period.
            The audit workspace opens once an admin assigns you to a period, on
            that period&apos;s page. Until then there is nothing to test here.
          </AlertDescription>
        </Alert>
      </PageShell>
    </div>
  );
}
