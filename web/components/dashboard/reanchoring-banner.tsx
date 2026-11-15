"use client";

// Issue #2176 — dashboard warning for controls that need re-anchoring.
//
// After a new SCF release, a control left on the previous release's anchor
// earns no framework credit until a person moves it (#1971 D3). The banner
// shows the count and links to /controls/needs-reanchoring, where each
// control's remap note is listed. It renders nothing while loading or when
// the count is zero, so the warning clears once every control is
// re-anchored. A 401/403 also renders nothing: a role that cannot read
// controls sees no banner, the same as the control list. Any other error
// renders inline (#2222), so a failed read never looks like "nothing to
// re-anchor".

import { useQuery } from "@tanstack/react-query";
import Link from "next/link";

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { reanchoringBannerText } from "@/components/dashboard/reanchoring-banner-text";
import { APIError } from "@/lib/api/base";
import { fetchControlsNeedingReanchoring } from "@/lib/api/reanchoring";

const NEEDS_REANCHORING_PATH = "/controls/needs-reanchoring";

function isAccessDenied(err: unknown): boolean {
  return err instanceof APIError && (err.status === 401 || err.status === 403);
}

export function ReanchoringBanner() {
  const q = useQuery({
    queryKey: ["controls", "needs-reanchoring"],
    queryFn: fetchControlsNeedingReanchoring,
    retry: false,
  });
  if (q.isError) {
    if (isAccessDenied(q.error)) return null;
    return (
      <Alert variant="destructive" data-testid="reanchoring-banner-error">
        <AlertTitle>
          Could not check for controls needing re-anchoring
        </AlertTitle>
        <AlertDescription>
          {q.error instanceof Error ? q.error.message : "Request failed"}
        </AlertDescription>
      </Alert>
    );
  }
  const text = q.data ? reanchoringBannerText(q.data) : null;
  if (!text) return null;
  return (
    // role="status": a standing condition on page load, not an
    // interruption, so it is announced politely.
    <Alert role="status" data-testid="reanchoring-banner">
      <AlertTitle data-testid="reanchoring-banner-title">
        {text.title}
      </AlertTitle>
      <AlertDescription>
        <p>
          {text.description}{" "}
          <Link
            href={NEEDS_REANCHORING_PATH}
            className="underline underline-offset-3"
            data-testid="reanchoring-banner-link"
          >
            Review the {text.count === 1 ? "control" : "controls"}
          </Link>
        </p>
      </AlertDescription>
    </Alert>
  );
}
