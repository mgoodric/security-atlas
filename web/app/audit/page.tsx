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

import { redirect } from "next/navigation";

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { AuditShell } from "@/components/audit/audit-shell";
import { resolveAuditPeriod } from "@/lib/api/audit-server";

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
      <div className="mx-auto max-w-xl py-12">
        <Alert variant="destructive">
          <AlertTitle>Could not load the audit workspace</AlertTitle>
          <AlertDescription>
            The platform returned an unexpected status ({resolution.status})
            resolving your assigned audit period. Try again shortly.
          </AlertDescription>
        </Alert>
      </div>
    );
  }

  return <AuditShell period={resolution.period} />;
}

function NoPeriodState() {
  return (
    <div
      data-testid="audit-no-period"
      className="mx-auto max-w-xl space-y-4 py-12"
    >
      <Alert>
        <AlertTitle>No audit period assigned</AlertTitle>
        <AlertDescription>
          Your account is signed in but is not assigned to any audit
          period. The audit workspace opens once a GRC engineer grants you
          an auditor assignment for a period. Until then there is nothing
          to test here.
        </AlertDescription>
      </Alert>
    </div>
  );
}
