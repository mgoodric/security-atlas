"use client";

// Slice 589 — /oscal/component-definitions list view.
//
// Lists the tenant's imported vendor component-definitions (OSCAL
// component-definition import, slice 512). Each row links to the per-import
// claims view where the operator dispositions individual vendor claims.
//
// A vendor claim is an ASSERTION, not platform-verified evidence — the list
// surfaces the import provenance (vendor label, source hash, claim count) so
// the operator knows what they are about to review.
//
// Data source: GET /api/oscal/component-definitions (BFF -> upstream
// GET /v1/oscal/component-definitions, tenant-scoped via RLS).

import { useQuery } from "@tanstack/react-query";
import Link from "next/link";

import { Alert } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Skeleton } from "@/components/ui/skeleton";
import { bffControlFetch } from "@/lib/api/_shared";
import type { ComponentDefinitionList } from "@/lib/api/oscal-components";

export default function ComponentDefinitionsPage() {
  const q = useQuery({
    queryKey: ["oscal", "component-definitions"],
    queryFn: () =>
      bffControlFetch<ComponentDefinitionList>(
        "/api/oscal/component-definitions",
      ),
  });

  return (
    <div className="space-y-6" data-testid="component-definitions-page">
      <header className="space-y-1">
        <h1 className="text-2xl font-semibold">Vendor Claims</h1>
        <p className="text-sm text-muted-foreground">
          Imported vendor component-definitions. Each claim is a vendor
          assertion, not platform-verified evidence — accept, reject, or ask for
          more information.
        </p>
      </header>

      {q.isLoading && (
        <div className="space-y-3" data-testid="component-definitions-loading">
          <Skeleton className="h-12 w-full" />
          <Skeleton className="h-12 w-full" />
          <Skeleton className="h-12 w-full" />
        </div>
      )}

      {q.isError && (
        <Alert variant="destructive" data-testid="component-definitions-error">
          Could not load imported component-definitions.
        </Alert>
      )}

      {q.data && q.data.count === 0 && (
        <div
          className="rounded-lg border border-dashed p-8 text-center text-sm text-muted-foreground"
          data-testid="component-definitions-empty"
        >
          No vendor component-definitions imported yet. Import an OSCAL
          component-definition to review its claims here.
        </div>
      )}

      {q.data && q.data.count > 0 && (
        <ul className="space-y-2" data-testid="component-definitions-list">
          {q.data.component_definitions.map((d) => (
            <li key={d.id}>
              <Link
                href={`/oscal/component-definitions/${d.id}`}
                className="flex items-center justify-between rounded-lg border p-4 hover:bg-muted/50"
                data-testid="component-definition-row"
              >
                <div className="space-y-1">
                  <div className="font-medium" data-testid="cd-source-label">
                    {d.source_label || d.catalog_title || d.id}
                  </div>
                  <div className="text-xs text-muted-foreground">
                    {d.catalog_title} · OSCAL {d.oscal_version} · imported by{" "}
                    {d.imported_by}
                  </div>
                </div>
                <Badge variant="secondary" data-testid="cd-claim-count">
                  {d.claim_count} claim{d.claim_count === 1 ? "" : "s"}
                </Badge>
              </Link>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
