"use client";

// Issue #2176 — dashboard warning for controls that need re-anchoring.
//
// After a new SCF release, a control left on the previous release's anchor
// earns no framework credit until a person moves it (#1971 D3). The banner
// shows the count and links to /controls/needs-reanchoring, where each
// control's remap note is listed. It renders nothing while loading, on an
// error (the read is optional: a role that cannot read controls gets a 403
// and sees no banner, the same as the control list), or when the count is
// zero, so the warning clears once every control is re-anchored.

import { useQuery } from "@tanstack/react-query";
import Link from "next/link";

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { reanchoringBannerText } from "@/components/dashboard/reanchoring-banner-text";
import { fetchControlsNeedingReanchoring } from "@/lib/api/reanchoring";

export const NEEDS_REANCHORING_PATH = "/controls/needs-reanchoring";

export function ReanchoringBanner() {
  const q = useQuery({
    queryKey: ["controls", "needs-reanchoring"],
    queryFn: fetchControlsNeedingReanchoring,
    retry: false,
  });
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
