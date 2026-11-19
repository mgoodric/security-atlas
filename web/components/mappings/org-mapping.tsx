// #1800 — an overlay edge shown as an "org mapping": the organisation's own
// requirement -> SCF anchor mapping, labelled apart from catalog edges
// wherever crosswalk edges appear (requirement detail, anchor detail,
// control coverage).
//
// Callers decide a row is an org mapping from the platform's `origin` field
// only (isOrgMapping). Rationale and source are organisation-entered text:
// they render as React text children, never as HTML. The author renders
// through <Person> (#2385), like every other actor on the platform.

import { Term } from "@/components/help/term";
import { formatAddedOn } from "@/components/mappings/format";
import { Person } from "@/components/ui/person";
import type { OverlayProvenance } from "@/lib/api/org-mapping";

export function OrgMappingBadge() {
  return (
    <span
      className="inline-flex items-center rounded bg-violet-100 px-2 py-0.5 text-[10px] font-semibold text-violet-900 dark:bg-violet-950 dark:text-violet-200"
      data-testid="org-mapping-badge"
    >
      <Term id="org-mapping">org mapping</Term>
    </span>
  );
}

export function OrgMappingDetails({
  rationale,
  overlay,
}: {
  rationale?: string;
  overlay?: OverlayProvenance;
}) {
  return (
    <dl
      className="mt-1 grid grid-cols-[auto_1fr] gap-x-2 gap-y-0.5 text-xs text-muted-foreground"
      data-testid="org-mapping-details"
    >
      <dt className="font-medium">Rationale</dt>
      <dd className="break-words" data-testid="org-mapping-rationale">
        {rationale || "—"}
      </dd>
      <dt className="font-medium">Source</dt>
      <dd className="break-words" data-testid="org-mapping-source">
        {overlay?.source_ref || "—"}
      </dd>
      {overlay ? (
        <>
          <dt className="font-medium">Added</dt>
          <dd data-testid="org-mapping-added">
            {formatAddedOn(overlay.created_at)} by{" "}
            <Person id={overlay.created_by} />
          </dd>
        </>
      ) : null}
    </dl>
  );
}
