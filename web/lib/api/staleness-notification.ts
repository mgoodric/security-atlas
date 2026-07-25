// Slice 439 — evidence-staleness notification presentation helper.
//
// The slice-439 backend rollup PRODUCES `evidence.staleness` notifications
// (one per-control "alert" subtype + a weekly "digest" subtype) into the
// slice-029 notifications store. The generic notifications BFF
// (web/app/api/audit/notifications/route.ts) forwards them with an opaque
// payload. This module is the typed presentation layer the in-app
// notifications surface uses to render the new kind correctly (AC-8) and to
// resolve the freshness-view deep-link (AC-9).
//
// HONEST-INTERVAL DISCIPLINE (P0-439-1 / AC-6): the cadence strings here MUST
// match the Go copy (internal/staleness/copy.go RecomputeIntervalText /
// DigestCadenceText) and MUST NOT use "continuous monitoring" / "real-time"
// framing — the canvas (§1.6) bans dressing polling up as real-time. The
// banned-phrase vitest guards this.
//
// Component-level rendering tests are out of scope for v1 (test-tier
// conventions Q-3 — vitest is the node-only module-logic tier; the Playwright
// e2e tier is the de facto component tier). This module is therefore a pure,
// node-testable presentation function; the component that consumes it is
// exercised by the e2e tier when an in-app notifications surface lands.

import type { Notification } from "@/lib/api/audit";

/** The load-bearing notification type string the backend writes (mirrors
 *  internal/audit/notifications.TypeEvidenceStaleness). */
export const EVIDENCE_STALENESS_TYPE = "evidence.staleness";

/** The freshness-view deep-link target (mirrors
 *  internal/staleness.FreshnessViewPath). AC-9: the digest/alert links here. */
export const FRESHNESS_VIEW_PATH = "/dashboard#evidence-freshness";

/** Honest cadence copy — kept consistent with the Go side. */
export const RECOMPUTE_INTERVAL_TEXT = "every 6 hours";
export const DIGEST_CADENCE_TEXT = "every Monday at 09:00 UTC";

export type StalenessSubtype = "alert" | "digest";

/** The view-model the notifications UI renders for one evidence.staleness row. */
export type StalenessNotificationView = {
  id: string;
  subtype: StalenessSubtype;
  /** Short label for the notification list item. */
  label: string;
  /** The plain, factual one-line body (carries the honest cadence). */
  message: string;
  /** Deep-link to the freshness view for the full stale list (AC-9). */
  freshnessViewUrl: string;
  /** Digest-only: counts surfaced as a compact badge. */
  staleCount?: number;
  approachingCount?: number;
  read: boolean;
};

function asString(v: unknown): string {
  return typeof v === "string" ? v : "";
}

function asNumber(v: unknown): number | undefined {
  return typeof v === "number" ? v : undefined;
}

/** Returns true when the notification is a slice-439 evidence-staleness row. */
export function isStalenessNotification(n: Notification): boolean {
  return n.type === EVIDENCE_STALENESS_TYPE;
}

/**
 * Build the presentation view-model for an evidence.staleness notification.
 * Falls back to safe defaults for a malformed payload (never throws) and
 * always resolves a non-empty freshness-view link so AC-9 holds even when the
 * payload omitted it. Returns null for a non-staleness notification.
 */
export function toStalenessView(
  n: Notification,
): StalenessNotificationView | null {
  if (!isStalenessNotification(n)) return null;
  const p = n.payload ?? {};
  const subtype: StalenessSubtype =
    asString(p.subtype) === "digest" ? "digest" : "alert";

  const freshnessViewUrl =
    asString(p.freshness_view_url) || FRESHNESS_VIEW_PATH;

  const label =
    subtype === "digest"
      ? "Weekly stale-evidence digest"
      : "Evidence went stale";

  const message =
    asString(p.message) ||
    (subtype === "digest"
      ? `Stale-evidence summary. Generated ${DIGEST_CADENCE_TEXT}.`
      : `Evidence for a control is now stale. Recomputed ${RECOMPUTE_INTERVAL_TEXT}.`);

  return {
    id: n.id,
    subtype,
    label,
    message,
    freshnessViewUrl,
    staleCount: asNumber(p.stale_count),
    approachingCount: asNumber(p.approaching_count),
    read: Boolean(n.read_at),
  };
}
