import { describe, expect, it } from "vitest";

import type { Notification } from "@/lib/api/audit";
import {
  DIGEST_CADENCE_TEXT,
  EVIDENCE_STALENESS_TYPE,
  FRESHNESS_VIEW_PATH,
  RECOMPUTE_INTERVAL_TEXT,
  isStalenessNotification,
  toStalenessView,
} from "@/lib/api/staleness-notification";

function notif(type: string, payload: Record<string, unknown>): Notification {
  return {
    id: "n-1",
    recipient_user_id: "u-1",
    type,
    payload,
    created_at: "2026-06-01T09:00:00Z",
    read_at: null,
  };
}

describe("staleness notification presentation", () => {
  it("identifies evidence.staleness notifications", () => {
    expect(isStalenessNotification(notif(EVIDENCE_STALENESS_TYPE, {}))).toBe(
      true,
    );
    expect(isStalenessNotification(notif("audit_note.reply", {}))).toBe(false);
  });

  it("returns null for a non-staleness notification", () => {
    expect(toStalenessView(notif("control.drift", {}))).toBeNull();
  });

  it("renders an alert view with the control message (AC-8)", () => {
    const view = toStalenessView(
      notif(EVIDENCE_STALENESS_TYPE, {
        subtype: "alert",
        band: "stale",
        message: `Evidence is stale. Recomputed ${RECOMPUTE_INTERVAL_TEXT}.`,
        freshness_view_url: FRESHNESS_VIEW_PATH,
      }),
    );
    expect(view).not.toBeNull();
    expect(view!.subtype).toBe("alert");
    expect(view!.label).toBe("Evidence went stale");
    expect(view!.message).toContain(RECOMPUTE_INTERVAL_TEXT);
    expect(view!.freshnessViewUrl).toBe(FRESHNESS_VIEW_PATH);
  });

  it("renders a digest view with counts + freshness link (AC-9)", () => {
    const view = toStalenessView(
      notif(EVIDENCE_STALENESS_TYPE, {
        subtype: "digest",
        stale_count: 4,
        approaching_count: 7,
        message: `4 stale and 7 approaching-stale this week. Generated ${DIGEST_CADENCE_TEXT}.`,
        freshness_view_url: FRESHNESS_VIEW_PATH,
      }),
    );
    expect(view!.subtype).toBe("digest");
    expect(view!.label).toBe("Weekly stale-evidence digest");
    expect(view!.staleCount).toBe(4);
    expect(view!.approachingCount).toBe(7);
    expect(view!.freshnessViewUrl).toBe(FRESHNESS_VIEW_PATH);
  });

  it("falls back to a non-empty freshness link on a malformed payload (AC-9 holds)", () => {
    const view = toStalenessView(notif(EVIDENCE_STALENESS_TYPE, {}));
    expect(view!.freshnessViewUrl).toBe(FRESHNESS_VIEW_PATH);
    expect(view!.subtype).toBe("alert"); // safe default
    expect(view!.message.length).toBeGreaterThan(0);
  });

  it("honest-interval copy never uses banned real-time framing (P0-439-1)", () => {
    const alert = toStalenessView(
      notif(EVIDENCE_STALENESS_TYPE, { subtype: "alert" }),
    )!;
    const digest = toStalenessView(
      notif(EVIDENCE_STALENESS_TYPE, { subtype: "digest" }),
    )!;
    const banned = [
      "continuous monitoring",
      "real-time",
      "real time",
      "live monitoring",
    ];
    for (const text of [
      alert.message,
      digest.message,
      RECOMPUTE_INTERVAL_TEXT,
      DIGEST_CADENCE_TEXT,
    ]) {
      const low = text.toLowerCase();
      for (const b of banned) {
        expect(low).not.toContain(b);
      }
    }
    // and the honest interval IS named
    expect(alert.message.toLowerCase()).toContain("recomputed");
    expect(digest.message.toLowerCase()).toContain("monday");
  });
});
