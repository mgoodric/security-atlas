"use client";

// #1800 — requirement detail: one framework requirement, the SCF anchors it
// maps to, and the organisation's controls behind them.
//
// Binds GET /v1/requirements/{id}/coverage through the BFF
// (/api/requirements/{id}/coverage). The catalog's crosswalk edges and the
// tenant's own overlay edges are listed apart: an overlay edge is an
// "org mapping", shown with its rationale, source, author and date. A
// control reached only through an org mapping is marked, so the share of
// coverage resting on the organisation's own mappings is visible.
//
// Whether a row is an org mapping is the platform's `origin` field only
// (isOrgMapping). The coverage score and band are the platform's numbers,
// rendered verbatim (P0-482-4).

import { useQuery } from "@tanstack/react-query";
import Link from "next/link";
import { useParams, useRouter } from "next/navigation";
import { useEffect } from "react";

import {
  OrgMappingBadge,
  OrgMappingDetails,
} from "@/components/mappings/org-mapping";
import { PageShell } from "@/components/shell/page-shell";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { Skeleton } from "@/components/ui/skeleton";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { APIError } from "@/lib/api/base";
import { isOrgMapping } from "@/lib/api/org-mapping";
import {
  fetchRequirementCoverage,
  type RequirementAnchorWire,
  type RequirementCoverage,
} from "@/lib/api/requirement-coverage";

export default function RequirementDetailPage() {
  const router = useRouter();
  const { id } = useParams<{ id: string }>();

  const q = useQuery<RequirementCoverage>({
    queryKey: ["requirement", id, "coverage"],
    queryFn: () => fetchRequirementCoverage(id),
    enabled: Boolean(id),
  });

  useEffect(() => {
    if (q.error instanceof APIError && q.error.status === 401) {
      router.push(`/login?from=/requirements/${id}`);
    }
  }, [q.error, router, id]);

  const req = q.data?.requirement;

  return (
    <PageShell
      title={req ? `${req.code} — ${req.title}` : "Requirement"}
      subtitle={req?.body ? <p>{req.body}</p> : undefined}
    >
      {q.isLoading ? <Skeleton className="h-32 w-full" /> : null}

      {q.error && !(q.error instanceof APIError && q.error.status === 401) ? (
        <Alert variant="destructive">
          <AlertTitle>Could not load requirement</AlertTitle>
          <AlertDescription>{(q.error as Error).message}</AlertDescription>
        </Alert>
      ) : null}

      {q.data ? <RequirementDetailView detail={q.data} /> : null}
    </PageShell>
  );
}

function RequirementDetailView({ detail }: { detail: RequirementCoverage }) {
  const catalog = detail.anchors.filter((a) => !isOrgMapping(a));
  const org = detail.anchors.filter((a) => isOrgMapping(a));

  // A control is credited only through an org mapping when its anchor is
  // reached by an overlay edge and by no catalog edge.
  const catalogAnchorIDs = new Set(catalog.map((a) => a.id));
  const orgAnchorIDs = new Set(org.map((a) => a.id));
  const orgOnly = (anchorID?: string) =>
    Boolean(anchorID) &&
    orgAnchorIDs.has(anchorID!) &&
    !catalogAnchorIDs.has(anchorID!);

  return (
    <div className="space-y-6">
      <Card>
        <CardHeader>
          <CardTitle>Coverage</CardTitle>
          <CardDescription>
            Best-satisfying path over the anchors below and your evaluated
            controls.
          </CardDescription>
        </CardHeader>
        <CardContent className="flex items-center gap-3">
          <span
            className="font-mono text-lg font-semibold"
            data-testid="requirement-coverage-strength"
          >
            {detail.coverage_strength.toFixed(2)}
          </span>
          <Badge variant="secondary" data-testid="requirement-confidence-band">
            {detail.confidence_band}
          </Badge>
          {detail.exclusion ? (
            <span className="text-sm text-muted-foreground">
              Excluded by your framework scope: {detail.exclusion.justification}
            </span>
          ) : null}
        </CardContent>
      </Card>

      <Card data-testid="requirement-catalog-mappings">
        <CardHeader>
          <CardTitle>Catalog mappings</CardTitle>
          <CardDescription>
            The published crosswalk from this requirement to SCF anchors.
          </CardDescription>
        </CardHeader>
        <CardContent>
          {catalog.length === 0 ? (
            <p className="text-sm text-muted-foreground">
              The catalog maps this requirement to no SCF anchor.
            </p>
          ) : (
            <AnchorTable anchors={catalog} />
          )}
        </CardContent>
      </Card>

      {org.length > 0 ? (
        <Card data-testid="requirement-org-mappings">
          <CardHeader>
            <CardTitle>Org mappings</CardTitle>
            <CardDescription>
              Your organisation&apos;s own mappings from this requirement to SCF
              anchors. They count toward your coverage only.
            </CardDescription>
          </CardHeader>
          <CardContent>
            <AnchorTable anchors={org} />
          </CardContent>
        </Card>
      ) : null}

      <Card>
        <CardHeader>
          <CardTitle>Controls</CardTitle>
          <CardDescription>
            Your controls anchored on the SCF anchors above.
          </CardDescription>
        </CardHeader>
        <CardContent>
          {detail.controls.length === 0 ? (
            <p className="text-sm text-muted-foreground">
              No control of yours sits behind this requirement.
            </p>
          ) : (
            <ul className="space-y-2">
              {detail.controls.map((c) => (
                <li
                  key={c.id}
                  className="flex flex-wrap items-center gap-2 text-sm"
                  data-testid="requirement-control"
                  data-org-only={orgOnly(c.scf_anchor_id) ? "true" : "false"}
                >
                  <Link href={`/controls/${c.id}`} className="hover:underline">
                    {c.title}
                  </Link>
                  <span className="font-mono text-xs text-muted-foreground">
                    {c.scf_id}
                  </span>
                  {orgOnly(c.scf_anchor_id) ? (
                    <>
                      <OrgMappingBadge />
                      <span className="text-xs text-muted-foreground">
                        Credited through your org mapping only
                      </span>
                    </>
                  ) : null}
                </li>
              ))}
            </ul>
          )}
        </CardContent>
      </Card>
    </div>
  );
}

function AnchorTable({ anchors }: { anchors: RequirementAnchorWire[] }) {
  return (
    <div className="overflow-x-auto">
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead className="w-28">SCF anchor</TableHead>
            <TableHead>Anchor</TableHead>
            <TableHead className="w-32">STRM type</TableHead>
            <TableHead className="w-24">Strength</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {anchors.map((a) => {
            const org = isOrgMapping(a);
            return (
              <TableRow
                key={a.edge_id ?? a.id}
                data-testid="requirement-anchor-row"
                data-origin={org ? "overlay" : "catalog"}
              >
                <TableCell className="font-mono text-xs">
                  <Link
                    href={`/catalog/scf/${encodeURIComponent(a.id)}`}
                    className="hover:underline"
                  >
                    {a.scf_id}
                  </Link>
                </TableCell>
                <TableCell className="text-sm">
                  <div>{a.name}</div>
                  {org ? (
                    <div className="mt-1">
                      <OrgMappingBadge />
                      <OrgMappingDetails
                        rationale={a.rationale}
                        overlay={a.overlay}
                      />
                    </div>
                  ) : null}
                </TableCell>
                <TableCell>
                  <Badge variant="secondary">{a.relationship_type}</Badge>
                </TableCell>
                <TableCell>{(a.strength ?? 0).toFixed(2)}</TableCell>
              </TableRow>
            );
          })}
        </TableBody>
      </Table>
    </div>
  );
}
