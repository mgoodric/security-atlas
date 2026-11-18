"use client";

// Slice 040 — recent activity feed (AC-6) — REBOUND by slice 147, and by
// #2135.
//
// #2135: the panel reads the last 10 rows of the unified activity log
// (GET /v1/activity/unified?limit=10, the /activity page's source, which
// the platform filters by module, #2477) and renders each as a sentence,
// "<Person> approved exception …", through the same `describeEvent`
// templates the /activity page uses (#2093). Before #2135 it read the
// evidence-ingest archive (GET /v1/activity) and printed raw event kinds,
// which in a busy tenant was 40 identical evidence rows (#2072).

import Link from "next/link";

import { PanelCard, type PanelState } from "@/components/dashboard/panel-card";
import { Person } from "@/components/ui/person";
import { Timestamp } from "@/components/ui/timestamp";
import { describeEvent } from "@/lib/activity-sentences";
import type { UnifiedEntry, UnifiedListResponse } from "@/lib/api/audit-log";

function ActivityRow({ entry }: { entry: UnifiedEntry }) {
  const s = describeEvent(entry);
  const target = s.targetId ? s.targetId.slice(0, 8) : "";
  return (
    <li
      data-testid="activity-feed-row"
      data-kind={entry.kind}
      className="flex items-baseline justify-between gap-3 py-2.5 text-sm"
    >
      <p data-testid="activity-feed-sentence" className="min-w-0 flex-1">
        <Person id={entry.actor_id} className="font-medium" /> {s.phrase}
        {s.noun ? ` ${s.noun}` : ""}
        {target ? (
          <>
            {" "}
            {s.href ? (
              <Link
                href={s.href}
                className="font-mono text-xs underline underline-offset-2"
              >
                {target}
              </Link>
            ) : (
              <span className="font-mono text-xs" title={s.targetId}>
                {target}
              </span>
            )}
          </>
        ) : null}
        {s.suffix ? ` ${s.suffix}` : ""}
      </p>
      <Timestamp
        value={entry.occurred_at}
        className="shrink-0 text-xs text-muted-foreground"
        testId="activity-feed-row-ts"
      />
    </li>
  );
}

export function ActivityFeedPanel({
  report,
  state,
}: {
  report: UnifiedListResponse | undefined;
  state: PanelState;
}) {
  const entries = report?.entries ?? [];
  return (
    <PanelCard
      title="Recent activity"
      description="The last 10 changes across the program"
      action={
        <Link
          href="/activity"
          className="text-xs text-primary hover:underline"
          data-testid="activity-feed-ledger-link"
        >
          View all activity →
        </Link>
      }
      state={state}
      skeletonClassName="h-48 w-full"
      testid="activity-feed-panel"
    >
      {entries.length === 0 ? (
        <p
          className="py-6 text-sm text-muted-foreground"
          data-testid="activity-feed-empty"
        >
          No activity in the last 90 days.
        </p>
      ) : (
        <ul
          className="divide-y divide-foreground/5"
          data-testid="activity-feed-list"
        >
          {entries.map((entry) => (
            <ActivityRow key={`${entry.kind}:${entry.row_id}`} entry={entry} />
          ))}
        </ul>
      )}
    </PanelCard>
  );
}
