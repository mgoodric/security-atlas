// #2852 — what a synced policy's detail page says about its source: a
// "Managed in Confluence" badge, the link to its page (where it is edited),
// the approval the page records, and a drift notice when the page's body
// has changed without a version bump (#2851), and another when its page
// properties have (#2877). Presentational only, so the
// node vitest tier renders it to static markup.
//
// The approver is source-asserted text (historical_approver_name /
// historical_approved_at; approved_by is empty), so it is labelled as an
// approval recorded in Confluence, never as an approval made in this app.

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Timestamp } from "@/components/ui/timestamp";

import type { SyncedPolicyView } from "./lifecycle";

export function ManagedInConfluenceBadge() {
  return (
    <Badge variant="outline" data-testid="policy-detail-synced">
      Managed in Confluence
    </Badge>
  );
}

export function SyncedPolicyPanel({ view }: { view: SyncedPolicyView }) {
  return (
    <div className="space-y-2" data-testid="policy-detail-sync">
      <p className="text-sm text-muted-foreground">
        This policy is synced from Confluence. Its text, version and review date
        are edited on its page
        {view.sourceHref ? (
          <>
            :{" "}
            <a
              href={view.sourceHref}
              className="text-primary hover:underline break-all"
              target="_blank"
              rel="noopener noreferrer"
              data-testid="policy-detail-source-link"
            >
              {view.sourceHref}
            </a>
          </>
        ) : null}
        . Control links and acknowledgments are kept here.
      </p>
      {view.approval ? (
        <p className="text-sm" data-testid="policy-detail-synced-approval">
          {view.approval}
        </p>
      ) : null}
      {view.drift ? (
        <Alert data-testid="policy-detail-sync-drift">
          <AlertTitle>The source page has changed</AlertTitle>
          <AlertDescription>
            <span>
              Since <Timestamp value={view.drift.detectedAt} style="date" />,
              the page&apos;s text differs from this published version, but its
              version was not bumped, so nothing was synced. Bump the
              page&apos;s version to publish the change, or revert the edit.
            </span>
          </AlertDescription>
        </Alert>
      ) : null}
      {view.propertyDrift ? (
        <Alert data-testid="policy-detail-sync-property-drift">
          <AlertTitle>
            The source page&apos;s properties have changed
          </AlertTitle>
          <AlertDescription>
            <span>
              Since{" "}
              <Timestamp value={view.propertyDrift.detectedAt} style="date" />,
              the page&apos;s {view.propertyDrift.keys.join(", ")} differ from
              this published version, but its version was not bumped, so nothing
              was synced. Bump the page&apos;s version to publish the change, or
              revert the edit.
            </span>
          </AlertDescription>
        </Alert>
      ) : null}
    </div>
  );
}
