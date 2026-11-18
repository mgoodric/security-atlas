"use client";

// #2135 — the dashboard's first section: the top 10 needs-attention items
// (GET /v1/needs-attention?limit=10, #2134), with the For you / Everyone
// toggle for callers who may see everyone, and See all opening the full
// /needs-attention page in the same view.

import Link from "next/link";

import { PanelCard, type PanelState } from "@/components/dashboard/panel-card";
import {
  NeedsAttentionList,
  ViewToggle,
  needsAttentionEmptyText,
} from "@/components/needs-attention/needs-attention-list";
import type {
  NeedsAttentionResponse,
  NeedsAttentionView,
} from "@/lib/api/needs-attention";

export function NeedsAttentionPanel({
  report,
  state,
  canSeeEveryone,
  onViewChange,
}: {
  report: NeedsAttentionResponse | undefined;
  state: PanelState;
  canSeeEveryone: boolean;
  onViewChange: (v: NeedsAttentionView) => void;
}) {
  const view = report?.for ?? "me";
  return (
    <PanelCard
      title="Needs attention"
      description={
        report
          ? `${report.total} ${
              report.total === 1 ? "item" : "items"
            }, most urgent first`
          : "Most urgent first"
      }
      action={
        <div className="flex items-center gap-3">
          <ViewToggle
            view={view}
            canSeeEveryone={canSeeEveryone}
            onChange={onViewChange}
          />
          <Link
            href={`/needs-attention?for=${view}`}
            className="text-xs text-primary hover:underline"
            data-testid="needs-attention-see-all"
          >
            See all →
          </Link>
        </div>
      }
      state={state}
      skeletonClassName="h-48 w-full"
      testid="needs-attention-panel"
    >
      {!report || report.items.length === 0 ? (
        <p
          className="py-6 text-sm text-muted-foreground"
          data-testid="needs-attention-empty"
        >
          {needsAttentionEmptyText(view)}
        </p>
      ) : (
        <NeedsAttentionList items={report.items} />
      )}
    </PanelCard>
  );
}
