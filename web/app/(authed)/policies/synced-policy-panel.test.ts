// #2852 — a synced policy's detail page: the "Managed in Confluence"
// badge, the source link, the approval recorded in Confluence, the drift
// indicator, and no edit or lifecycle action.
//
// The vitest tier is node-only with no DOM (slice 069 P0-A3), so the panel
// renders to static markup with `createElement` (no JSX), as
// `components/detail/detail-header.test.ts` does.

import { createElement as h } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";

import type { Policy } from "@/lib/api/policies";

import { isDraft, primaryAction, syncedPolicyView } from "./lifecycle";
import { ManagedBySyncBadge, SyncedPolicyPanel } from "./synced-policy-panel";

const ID = "11111111-1111-4111-8111-111111111111";
const PAGE = "https://acme.atlassian.net/wiki/spaces/SEC/pages/1001";

type Synced = Pick<
  Policy,
  | "id"
  | "status"
  | "source_attribution"
  | "source_url"
  | "historical_approver_name"
  | "historical_approved_at"
  | "sync_drift"
  | "sync_property_drift"
  | "sync_source"
>;

function synced(over: Partial<Synced> = {}): Synced {
  return {
    id: ID,
    status: "published",
    source_attribution: "synced",
    source_url: PAGE,
    historical_approver_name: "Jane Doe",
    historical_approved_at: "2025-03-01",
    sync_drift: null,
    sync_property_drift: null,
    sync_source: "Confluence",
    ...over,
  };
}

function render(p: Synced): string {
  const view = syncedPolicyView(p);
  if (!view) throw new Error("not a synced view");
  return renderToStaticMarkup(h(SyncedPolicyPanel, { view }));
}

describe("synced policy", () => {
  it("shows the Managed in Confluence badge", () => {
    const html = renderToStaticMarkup(
      h(ManagedBySyncBadge, { source: "Confluence" }),
    );
    expect(html).toContain('data-testid="policy-detail-synced"');
    expect(html).toContain("Managed in Confluence");
  });

  // #2837 — a SharePoint policy names SharePoint, not Confluence.
  it("names SharePoint for a policy synced from SharePoint", () => {
    const view = syncedPolicyView(synced({ sync_source: "SharePoint" }));
    expect(view?.source).toBe("SharePoint");
    expect(view?.approval).toBe(
      "Approved in SharePoint by Jane Doe on 2025-03-01",
    );
    const badge = renderToStaticMarkup(
      h(ManagedBySyncBadge, { source: view!.source }),
    );
    expect(badge).toContain("Managed in SharePoint");
    const html = render(synced({ sync_source: "SharePoint" }));
    expect(html).toContain("This policy is synced from SharePoint.");
    expect(html).not.toContain("Confluence");
  });

  it("falls back to Confluence when the response has no sync_source", () => {
    expect(syncedPolicyView(synced({ sync_source: undefined }))?.source).toBe(
      "Confluence",
    );
  });

  it("links to the source page, opening it in a new tab", () => {
    const html = render(synced());
    expect(html).toContain('data-testid="policy-detail-source-link"');
    expect(html).toContain(`href="${PAGE}"`);
    expect(html).toContain('target="_blank"');
    expect(html).toContain('rel="noopener noreferrer"');
  });

  it("never links a non-http source URL", () => {
    const html = render(synced({ source_url: "javascript:alert(1)" }));
    expect(html).not.toContain("policy-detail-source-link");
    expect(html).not.toContain("javascript:");
  });

  it("labels the approver as approval recorded in Confluence", () => {
    const html = render(synced());
    expect(html).toContain('data-testid="policy-detail-synced-approval"');
    expect(html).toContain("Approved in Confluence by Jane Doe on 2025-03-01");
    expect(syncedPolicyView(synced())?.approval).toBe(
      "Approved in Confluence by Jane Doe on 2025-03-01",
    );
  });

  it("shows no drift indicator while the page is in step", () => {
    expect(render(synced())).not.toContain("policy-detail-sync-drift");
  });

  it("shows the drift indicator when the source has drifted", () => {
    const html = render(
      synced({
        sync_drift: {
          detected_at: "2026-11-20T08:00:00Z",
          source_body_sha256: "a".repeat(64),
        },
      }),
    );
    expect(html).toContain('data-testid="policy-detail-sync-drift"');
    expect(html).toContain("The source page has changed");
  });

  it("shows the property drift indicator, naming the keys", () => {
    const html = render(
      synced({
        sync_property_drift: {
          detected_at: "2026-11-21T08:00:00Z",
          keys: ["next_review_at", "owner_role"],
        },
      }),
    );
    expect(html).toContain('data-testid="policy-detail-sync-property-drift"');
    expect(html).toContain("next_review_at, owner_role");
    // Body drift is separate: not shown for a property-only change.
    expect(html).not.toContain('data-testid="policy-detail-sync-drift"');
    expect(render(synced())).not.toContain("policy-detail-sync-property-drift");
  });

  it("hides the edit and lifecycle actions", () => {
    // Published: no "Create new version".
    expect(primaryAction(synced())).toBeNull();
    // Every state a synced chain could hold: no submit, approve or publish.
    for (const status of ["draft", "under_review", "approved", "published"]) {
      expect(primaryAction(synced({ status }))).toBeNull();
    }
    // A synced row is never a draft, so no Edit or Discard.
    expect(isDraft(synced())).toBe(false);
  });

  it("leaves a policy authored in the tool alone", () => {
    const own = synced({ source_attribution: "tenant_authored" });
    expect(syncedPolicyView(own)).toBeNull();
    expect(primaryAction(own)?.kind).toBe("new-version");
  });
});
