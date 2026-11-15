"use client";

// Issue #2176 — controls that need re-anchoring after a new SCF release.
//
// Lists the tenant's controls still on an anchor of an earlier SCF release
// that the #1971 D3 rule flags for a person (the code is gone, or now names
// a different control). Each row carries its remap note, which says where
// the anchor's concept went. A control is re-anchored by re-uploading its
// bundle on a current-release anchor; it then leaves this list and the
// dashboard warning clears. Pinned bundles (a declared scf_release) are
// bound to their release on purpose and are not listed.

import { useQuery } from "@tanstack/react-query";
import Link from "next/link";

import { PageShell } from "@/components/shell/page-shell";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Skeleton } from "@/components/ui/skeleton";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { fetchControlsNeedingReanchoring } from "@/lib/api/reanchoring";

export default function NeedsReanchoringPage() {
  const q = useQuery({
    queryKey: ["controls", "needs-reanchoring"],
    queryFn: fetchControlsNeedingReanchoring,
  });

  return (
    <PageShell
      title="Controls needing re-anchoring"
      subtitle="Controls still on an anchor from an earlier SCF release. Re-upload each bundle on a current-release anchor; the note says where its anchor's concept went."
      data-testid="needs-reanchoring-page"
    >
      {q.isLoading ? (
        <Skeleton className="h-40 w-full" />
      ) : q.isError ? (
        <Alert variant="destructive" data-testid="needs-reanchoring-error">
          <AlertTitle>Could not load the list</AlertTitle>
          <AlertDescription>
            {q.error instanceof Error ? q.error.message : "Request failed"}
          </AlertDescription>
        </Alert>
      ) : !q.data || q.data.controls.length === 0 ? (
        <p
          className="text-sm text-muted-foreground"
          data-testid="needs-reanchoring-empty"
        >
          No controls need re-anchoring.
        </p>
      ) : (
        <div className="list-table-wrap">
          <Table data-testid="needs-reanchoring-table">
            <TableHeader>
              <TableRow>
                <TableHead>Control</TableHead>
                <TableHead>Bundle</TableHead>
                <TableHead>Anchor</TableHead>
                <TableHead>Remap note</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {q.data.controls.map((c) => (
                <TableRow key={c.id} data-testid="needs-reanchoring-row">
                  <TableCell>
                    <Link
                      href={`/controls/${encodeURIComponent(c.id)}`}
                      className="underline underline-offset-3"
                    >
                      {c.title}
                    </Link>
                  </TableCell>
                  <TableCell className="font-mono text-xs">
                    {c.bundle_id}
                  </TableCell>
                  <TableCell>
                    <span className="font-mono text-xs">
                      {c.legacy_scf_id}@{c.legacy_release}
                    </span>
                    <span className="block text-xs text-muted-foreground">
                      {c.legacy_anchor_title}
                    </span>
                  </TableCell>
                  <TableCell data-testid="needs-reanchoring-note">
                    {c.note}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      )}
    </PageShell>
  );
}
