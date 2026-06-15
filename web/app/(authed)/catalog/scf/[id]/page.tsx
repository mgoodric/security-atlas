"use client";

import { useQuery } from "@tanstack/react-query";
import Link from "next/link";
import { useParams, useRouter } from "next/navigation";
import { useEffect, useMemo, useState } from "react";

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
import { type AnchorDetail } from "@/lib/api/anchors";
import { APIError } from "@/lib/api/base";
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

  return (
    <div className="space-y-6">
      <div className="text-sm">
        <Link
          href="/catalog/scf"
          className="text-muted-foreground hover:underline"
        >
          ← All anchors
        </Link>
      </div>

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
    </div>
  );
}

function AnchorDetailView({ detail }: { detail: AnchorDetail }) {
  const byFramework = new Map<
    string,
    { framework: string; version: string; rows: typeof detail.requirements }
  >();
  for (const r of detail.requirements) {
    const key = r.framework_version.id;
    const existing = byFramework.get(key);
    if (existing) {
      existing.rows.push(r);
    } else {
      byFramework.set(key, {
        framework: r.framework_version.framework,
        version: r.framework_version.version,
        rows: [r],
      });
    }
  }

  return (
    <div className="space-y-6">
      <div>
        <div className="font-mono text-xs text-muted-foreground">
          {detail.anchor.scf_id} · {detail.anchor.family}
        </div>
        <h1 className="text-2xl font-semibold tracking-tight">
          {detail.anchor.name}
        </h1>
        <p className="text-sm text-muted-foreground">
          {detail.anchor.description}
        </p>
      </div>

      {[...byFramework.values()].map((group) => (
        <Card key={group.framework + group.version}>
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
                {group.rows.map((r) => (
                  <TableRow key={r.requirement.id}>
                    <TableCell className="font-mono text-xs">
                      {r.requirement.code}
                    </TableCell>
                    <TableCell className="text-sm">
                      {r.requirement.text}
                    </TableCell>
                    <TableCell>
                      <Badge variant="secondary">{r.strm_type}</Badge>
                    </TableCell>
                    <TableCell>{r.strength.toFixed(2)}</TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </CardContent>
        </Card>
      ))}

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
