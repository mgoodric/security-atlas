// Slice 042 — /audit/[controlId] per-control workspace page
// (AC-2, AC-3, AC-4, AC-5, AC-7).
//
// Resolves the auditor's assigned period server-side (same as the
// landing page) and renders the AuditShell with the deep-linked control
// selected. The shell mounts the AnnotationDraftProvider above both the
// ControlNav and the ControlWorkspace, so navigating between
// /audit/[controlId-A] and /audit/[controlId-B] keeps in-progress
// annotation drafts (AC-7 / P0-3) — the provider lives in the shell,
// which Next keeps mounted across sibling-route navigation within the
// /audit segment.
//
// P0-1: the page never accepts a period id from the URL — the period is
// always resolved from /v1/me/audit-period for the calling credential.
// The only URL-supplied value is the controlId, which is just the
// control the auditor wants to look at WITHIN their assigned period.

import { redirect } from "next/navigation";

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { AuditShell } from "@/components/audit/audit-shell";
import { resolveAuditPeriod } from "@/lib/api/audit-server";

export default async function ControlWorkspacePage({
  params,
}: {
  params: Promise<{ controlId: string }>;
}) {
  const { controlId } = await params;
  const resolution = await resolveAuditPeriod();

  if (resolution.kind === "unauthenticated") {
    redirect("/login?from=/audit");
  }

  if (resolution.kind === "no-period") {
    return (
      <div className="mx-auto max-w-xl py-12">
        <Alert>
          <AlertTitle>No audit period assigned</AlertTitle>
          <AlertDescription>
            You are signed in but not assigned to any audit period, so
            this control cannot be opened for testing.
          </AlertDescription>
        </Alert>
      </div>
    );
  }

  if (resolution.kind === "error") {
    return (
      <div className="mx-auto max-w-xl py-12">
        <Alert variant="destructive">
          <AlertTitle>Could not load the audit workspace</AlertTitle>
          <AlertDescription>
            The platform returned an unexpected status ({resolution.status}
            ) resolving your assigned audit period.
          </AlertDescription>
        </Alert>
      </div>
    );
  }

  return (
    <AuditShell
      period={resolution.period}
      selectedControlId={decodeURIComponent(controlId)}
    />
  );
}
