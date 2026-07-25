"use client";

// Slice 040 — recent activity feed (AC-6) — REBOUND by slice 147.
//
// Binds to `GET /v1/activity` via the dashboard BFF (slice 066 backend
// reads). Per `docs/audit-log/066-...-decisions.md` D1 the endpoint
// reads slice-062's `admin_audit_log_v` view filtered to the
// `evidence_audit_log` source table (slice 015 ingestion's durable
// append-only archive), keyed newest-first by `received_at`.
//
// Slice 040 originally shipped this as a `MissingEndpointPanel` with
// disabled filter chips. Slice 147 bound the panel to real data but left
// the filter chips visually present and inert. Slice 667 removed them:
// the dashboard endpoint surfaces only the evidence branch and takes no
// kind/source filter, so the chips had nothing to bind to and carried a
// developer-facing placeholder tooltip. Wiring real filtering requires a
// backend slice (widen the dashboard source + add a `?kind=` param) and
// is tracked as a follow-up — see the slice 667 decisions log.

import { PanelCard, type PanelState } from "@/components/dashboard/panel-card";
import type { ActivityEvent, ActivityFeedResponse } from "@/lib/api/dashboard";

// relativeTime renders an RFC3339Nano timestamp as a short human-readable
// "Nm ago" / "Nh ago" / "Nd ago" string. Future timestamps degrade to
// "just now" (a server clock skew shouldn't break the feed render).
function relativeTime(iso: string): string {
  const t = new Date(iso).getTime();
  if (Number.isNaN(t)) return iso;
  const seconds = Math.max(0, Math.floor((Date.now() - t) / 1000));
  if (seconds < 60) return "just now";
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return `${minutes}m ago`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours}h ago`;
  const days = Math.floor(hours / 24);
  if (days < 30) return `${days}d ago`;
  const months = Math.floor(days / 30);
  return `${months}mo ago`;
}

function eventTypeBadgeClass(eventType: string): string {
  const lower = eventType.toLowerCase();
  if (lower.includes("reject") || lower.includes("fail")) {
    return "text-destructive";
  }
  if (lower.includes("dedup")) return "text-muted-foreground";
  return "text-emerald-600 dark:text-emerald-400";
}

function ActivityRow({ event }: { event: ActivityEvent }) {
  return (
    <li data-testid="activity-feed-row" className="py-3 text-sm">
      <div className="flex items-baseline justify-between gap-3">
        <span
          className={`font-mono text-xs ${eventTypeBadgeClass(
            event.event_type,
          )}`}
          data-testid="activity-feed-row-event-type"
        >
          {event.event_type}
        </span>
        <span
          className="shrink-0 text-xs text-muted-foreground"
          data-testid="activity-feed-row-ts"
        >
          {relativeTime(event.ts)}
        </span>
      </div>
      <div className="mt-1 text-xs text-muted-foreground">
        <span className="font-mono">{event.resource_type}</span>
        {event.resource_id ? (
          <>
            {" · "}
            <span className="font-mono">{event.resource_id.slice(0, 12)}</span>
          </>
        ) : null}
        {event.actor ? (
          <>
            {" · by "}
            <span className="font-mono">{event.actor}</span>
          </>
        ) : null}
      </div>
    </li>
  );
}

export function ActivityFeedPanel({
  report,
  state,
}: {
  report: ActivityFeedResponse | undefined;
  state: PanelState;
}) {
  return (
    <PanelCard
      title="Recent activity"
      description="Evidence ingest, control state changes, approvals"
      state={state}
      skeletonClassName="h-48 w-full"
      testid="activity-feed-panel"
    >
      {!report || report.activity.length === 0 ? (
        <p
          className="py-6 text-sm text-muted-foreground"
          data-testid="activity-feed-empty"
        >
          No evidence-ingest activity yet. Push evidence via a connector or the
          CLI to populate this feed.
        </p>
      ) : (
        <ul
          className="divide-y divide-foreground/5"
          data-testid="activity-feed-list"
        >
          {report.activity.map((event, idx) => (
            <ActivityRow
              key={`${event.ts}:${event.resource_id}:${idx}`}
              event={event}
            />
          ))}
        </ul>
      )}
    </PanelCard>
  );
}
