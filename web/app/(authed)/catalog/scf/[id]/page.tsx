"use client";

import { useQuery } from "@tanstack/react-query";
import Link from "next/link";
import { useParams, useRouter } from "next/navigation";
import { useEffect, useMemo, useState } from "react";

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
import { Select } from "@/components/ui/select";
import { Skeleton } from "@/components/ui/skeleton";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { type AnchorDetail, type AnchorRequirement } from "@/lib/api/anchors";
import { APIError } from "@/lib/api/base";
import { isOrgMapping } from "@/lib/api/org-mapping";
import {
  ALL_CURRENT,
  buildVersionOptions,
  pinFor,
} from "@/lib/anchors/version-options";

export default function SCFAnchorDetailPage() {
  const router = useRouter();
  const { id } = useParams<{ id: string }>();

  // Slice 484 — the selected framework version. ALL_CURRENT (no pin) is the
  // default; selecting a specific version re-queries with the pin.
  const [selectedVersion, setSelectedVersion] = useState<string>(ALL_CURRENT);

  // The unpinned (current-versions) query — also drives the selector option
  // set, so the dropdown lists the versions actually present for this anchor.
  const base = useQuery<AnchorDetail>({
    queryKey: ["anchor", id, ALL_CURRENT],
    queryFn: () => fetchAnchorDetail(id),
    enabled: Boolean(id),
  });

  const pinned = useQuery<AnchorDetail>({
    queryKey: ["anchor", id, selectedVersion],
    queryFn: () => fetchAnchorDetail(id, pinFor(selectedVersion)),
    enabled: Boolean(id) && selectedVersion !== ALL_CURRENT,
  });

  const isPinned = selectedVersion !== ALL_CURRENT;
  const data = isPinned ? pinned.data : base.data;
  const isLoading = isPinned ? pinned.isLoading : base.isLoading;
  const error = isPinned ? pinned.error : base.error;

  const options = useMemo(() => buildVersionOptions(base.data), [base.data]);

  useEffect(() => {
    if (error instanceof APIError && error.status === 401) {
      router.push(`/login?from=/catalog/scf/${id}`);
    }
  }, [error, router, id]);

  // The anchor itself does not change with the version pin, so the header
  // reads from whichever query has resolved (the pinned one may still be
  // loading while the base one is already in cache).
  const anchor = (data ?? base.data)?.anchor;

  return (
    <PageShell
      title={anchor?.name ?? "SCF anchor"}
      subtitle={
        anchor ? (
          <>
            <div className="font-mono text-xs">
              {anchor.scf_id} · {anchor.family}
            </div>
            <p>{anchor.description}</p>
          </>
        ) : undefined
      }
      actions={
        <Link
          href="/catalog/scf"
          className="text-sm text-muted-foreground hover:underline"
        >
          ← All anchors
        </Link>
      }
    >
      {options.length > 1 ? (
        <div className="flex max-w-xs flex-col gap-1">
          <label
            htmlFor="framework-version-select"
            className="text-xs font-medium text-muted-foreground"
          >
            Framework version
          </label>
          <Select
            id="framework-version-select"
            data-testid="framework-version-select"
            value={selectedVersion}
            onChange={(e) => setSelectedVersion(e.target.value)}
          >
            {options.map((opt) => (
              <option key={opt.value} value={opt.value}>
                {opt.label}
              </option>
            ))}
          </Select>
        </div>
      ) : null}

      {isLoading ? <Skeleton className="h-32 w-full" /> : null}

      {error && !(error instanceof APIError && error.status === 401) ? (
        <Alert variant="destructive">
          <AlertTitle>Could not load anchor</AlertTitle>
          <AlertDescription>{(error as Error).message}</AlertDescription>
        </Alert>
      ) : null}

      {data ? <AnchorDetailView detail={data} /> : null}
    </PageShell>
  );
}

// #1800 — rows are the platform's flat requirementForAnchorWire. A row whose
// `origin` is "overlay" is the tenant's own org mapping: it is listed after
// the catalog rows of its framework version, labelled, with its rationale,
// source, author and date.
function AnchorDetailView({ detail }: { detail: AnchorDetail }) {
  const byFramework = new Map<
    string,
    { framework: string; version: string; rows: AnchorRequirement[] }
  >();
  for (const r of detail.requirements) {
    const key = r.framework_version_id;
    const existing = byFramework.get(key);
    if (existing) {
      existing.rows.push(r);
    } else {
      byFramework.set(key, {
        framework: r.framework_name,
        version: r.framework_version,
        rows: [r],
      });
    }
  }

  return (
    <div className="space-y-6">
      {[...byFramework.entries()].map(([fvID, group]) => {
        const rows = [
          ...group.rows.filter((r) => !isOrgMapping(r)),
          ...group.rows.filter((r) => isOrgMapping(r)),
        ];
        return (
          <Card key={fvID}>
            <CardHeader>
              <CardTitle>{group.framework}</CardTitle>
              <CardDescription>{group.version}</CardDescription>
            </CardHeader>
            <CardContent>
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead className="w-24">Code</TableHead>
                    <TableHead>Requirement</TableHead>
                    <TableHead className="w-32">STRM type</TableHead>
                    <TableHead className="w-24">Strength</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {rows.map((r) => {
                    const org = isOrgMapping(r);
                    return (
                      <TableRow
                        key={r.edge_id}
                        data-testid="anchor-requirement-row"
                        data-origin={org ? "overlay" : "catalog"}
                      >
                        <TableCell className="font-mono text-xs">
                          <Link
                            href={`/requirements/${encodeURIComponent(
                              r.requirement_id,
                            )}`}
                            className="hover:underline"
                          >
                            {r.code}
                          </Link>
                        </TableCell>
                        <TableCell className="text-sm">
                          <div>{r.title}</div>
                          {org ? (
                            <div className="mt-1">
                              <OrgMappingBadge />
                              <OrgMappingDetails
                                rationale={r.rationale}
                                overlay={r.overlay}
                              />
                            </div>
                          ) : null}
                        </TableCell>
                        <TableCell>
                          <Badge variant="secondary">
                            {r.relationship_type}
                          </Badge>
                        </TableCell>
                        <TableCell>{r.strength.toFixed(2)}</TableCell>
                      </TableRow>
                    );
                  })}
                </TableBody>
              </Table>
            </CardContent>
          </Card>
        );
      })}

      {detail.requirements.length === 0 ? (
        <Alert>
          <AlertTitle>No requirement mappings</AlertTitle>
          <AlertDescription>
            This anchor has no framework requirements in the starter catalog.
            Full framework mappings load when you run the catalog import.
          </AlertDescription>
        </Alert>
      ) : null}
    </div>
  );
}

async function fetchAnchorDetail(
  id: string,
  frameworkVersion?: string,
): Promise<AnchorDetail> {
  let url = `/api/anchors/${encodeURIComponent(id)}/requirements`;
  if (frameworkVersion) {
    url += `?framework_version=${encodeURIComponent(frameworkVersion)}`;
  }
  const res = await fetch(url);
  if (!res.ok) {
    throw new APIError(res.status, `${res.status} ${res.statusText}`);
  }
  return (await res.json()) as AnchorDetail;
}
