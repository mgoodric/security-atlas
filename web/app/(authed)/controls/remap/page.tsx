"use client";

// #1778 — SCF remap review page.
//
// After a new SCF release becomes current, every control left on an older
// release's anchor carries a remap proposal (#1775). This page lists the
// ones a reviewer can still decide (open, or rejected and revisitable) in
// one tab per class, with a count on each. Unchanged proposals can be
// approved in one call to the platform's bulk endpoint. Every decision goes
// through #1776's endpoints, which admit admin and grc_engineer people only.
//
// Crosswalk-edge proposals (#1777) are global catalog data, and the
// platform answers them for super-admins only. A 403 from that read hides
// the edge section; the platform, not this page, is the gate.

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import Link from "next/link";
import { useState } from "react";

import { PageShell } from "@/components/shell/page-shell";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { APIError } from "@/lib/api/base";
import {
  REMAP_CLASSES,
  bulkApproveUnchanged,
  fetchEdgeRemapProposals,
  fetchRemapProposals,
  type RemapClass,
} from "@/lib/api/remap";
import {
  REMAP_CLASS_LABELS,
  edgesFor,
  pendingByClass,
} from "@/lib/remap-review";

import { ProposalCard } from "./proposal-card";

export default function RemapReviewPage() {
  const qc = useQueryClient();
  const [tab, setTab] = useState<RemapClass>("unchanged");

  const proposalsQ = useQuery({
    queryKey: ["remap-proposals"],
    queryFn: fetchRemapProposals,
    retry: false,
  });
  const edgesQ = useQuery({
    queryKey: ["remap-proposals", "edges"],
    queryFn: fetchEdgeRemapProposals,
    retry: false,
  });
  // Edges show only when the platform answered the read (super-admin).
  const edges = edgesQ.isSuccess ? edgesQ.data.proposals : null;
  const edgesFailed =
    edgesQ.isError &&
    !(edgesQ.error instanceof APIError && edgesQ.error.status === 403);

  const bulk = useMutation({
    mutationFn: bulkApproveUnchanged,
    onSuccess: () => qc.invalidateQueries({ queryKey: ["remap-proposals"] }),
  });

  const groups = proposalsQ.data
    ? pendingByClass(proposalsQ.data.proposals)
    : null;
  const active = groups?.[tab] ?? [];

  return (
    <PageShell
      title="SCF remap review"
      subtitle="Controls still on an anchor from an earlier SCF release. Approve a successor anchor, reject for now, or keep the legacy anchor."
      data-testid="remap-review-page"
      actions={
        <Link
          href="/controls/needs-reanchoring"
          className="text-sm underline underline-offset-3"
        >
          Controls needing re-anchoring
        </Link>
      }
    >
      {proposalsQ.isLoading ? (
        <Skeleton className="h-40 w-full" />
      ) : proposalsQ.isError ? (
        <Alert variant="destructive" data-testid="remap-review-error">
          <AlertTitle>Could not load remap proposals</AlertTitle>
          <AlertDescription>
            {proposalsQ.error instanceof Error
              ? proposalsQ.error.message
              : "Request failed"}
          </AlertDescription>
        </Alert>
      ) : groups ? (
        <>
          <div
            role="tablist"
            aria-label="Remap classes"
            className="flex flex-wrap gap-1 border-b"
          >
            {REMAP_CLASSES.map((c) => (
              <button
                key={c}
                type="button"
                role="tab"
                aria-selected={tab === c}
                data-testid={`remap-tab-${c}`}
                className={`-mb-px border-b-2 px-3 py-2 text-sm ${
                  tab === c
                    ? "border-primary font-medium"
                    : "border-transparent text-muted-foreground"
                }`}
                onClick={() => setTab(c)}
              >
                {REMAP_CLASS_LABELS[c]}{" "}
                <span data-testid={`remap-tab-count-${c}`}>
                  {groups[c].length}
                </span>
              </button>
            ))}
          </div>

          {edgesFailed ? (
            <Alert variant="destructive" data-testid="remap-edges-error">
              <AlertDescription>
                Could not load crosswalk edge proposals:{" "}
                {(edgesQ.error as Error).message}
              </AlertDescription>
            </Alert>
          ) : null}

          <div role="tabpanel" className="space-y-4">
            {tab === "unchanged" && active.length > 0 ? (
              <div className="flex items-center gap-3">
                <Button
                  data-testid="remap-bulk-approve"
                  disabled={bulk.isPending}
                  onClick={() => bulk.mutate()}
                >
                  Approve all unchanged ({active.length})
                </Button>
                <span className="text-xs text-muted-foreground">
                  Same code, same title in the new release.
                </span>
              </div>
            ) : null}
            {bulk.isError ? (
              <Alert variant="destructive" data-testid="remap-bulk-error">
                <AlertDescription>
                  {(bulk.error as Error).message}
                </AlertDescription>
              </Alert>
            ) : null}
            {bulk.data && bulk.data.skipped.length > 0 ? (
              <Alert data-testid="remap-bulk-skipped">
                <AlertDescription>
                  {bulk.data.skipped.length} left open:{" "}
                  {bulk.data.skipped
                    .map((s) => `${s.bundle_id} (${s.reason})`)
                    .join("; ")}
                </AlertDescription>
              </Alert>
            ) : null}

            {active.length === 0 ? (
              <p
                className="text-sm text-muted-foreground"
                data-testid="remap-tab-empty"
              >
                Nothing to review in this class.
              </p>
            ) : (
              active.map((p) => (
                <ProposalCard
                  key={p.id}
                  proposal={p}
                  edges={edges ? edgesFor(p, edges) : null}
                />
              ))
            )}
          </div>
        </>
      ) : null}
    </PageShell>
  );
}
