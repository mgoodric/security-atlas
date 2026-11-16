import { describe, expect, it } from "vitest";

import { AUDIT_LOG_KINDS } from "@/lib/api/audit-log";

import {
  GENERIC,
  KIND_TEMPLATES,
  describeEvent,
  groupByDay,
  targetHref,
  verbed,
} from "./activity-sentences";

const row = (
  kind: string,
  action: string,
  target_type = kind,
  target_id = "t-1",
) => ({
  kind,
  action,
  target_type,
  target_id,
});

describe("KIND_TEMPLATES (#2093)", () => {
  it("every event kind has a template or an explicit generic mapping", () => {
    const missing = AUDIT_LOG_KINDS.filter((k) => {
      const t = (KIND_TEMPLATES as Record<string, unknown>)[k];
      return !(t === GENERIC || typeof t === "function");
    });
    expect(missing).toEqual([]);
  });

  it("maps no kind the platform does not emit", () => {
    const extra = Object.keys(KIND_TEMPLATES).filter(
      (k) => !(AUDIT_LOG_KINDS as readonly string[]).includes(k),
    );
    expect(extra).toEqual([]);
  });
});

describe("describeEvent", () => {
  it("reads a lifecycle action in the past tense", () => {
    expect(describeEvent(row("exception", "approved"))).toMatchObject({
      phrase: "approved",
      noun: "exception",
      targetId: "t-1",
      href: null,
    });
    expect(describeEvent(row("framework_scope", "submit")).phrase).toBe(
      "submitted",
    );
    expect(describeEvent(row("control_import", "approve")).phrase).toBe(
      "approved",
    );
  });

  it("keeps a dotted action's prefix as a qualifier", () => {
    const s = describeEvent(row("framework_scope", "predicate.update"));
    expect(s.phrase).toBe("updated");
    expect(s.suffix).toBe("(predicate)");
  });

  it("names risk and feature-flag changes", () => {
    expect(describeEvent(row("risk", "placement_changed")).phrase).toBe(
      "moved",
    );
    expect(
      describeEvent(row("feature_flag", "feature_flag.disable")).phrase,
    ).toBe("disabled");
  });

  it("names no record for an account event", () => {
    const s = describeEvent(row("me", "session.revoke", "user", "u-1"));
    expect(s.phrase).toBe("revoked one of their sessions");
    expect(s.targetId).toBe("");
  });

  it("falls back to the generic sentence for a decision and an unknown kind", () => {
    expect(describeEvent(row("decision", "read", "controls"))).toMatchObject({
      phrase: 'recorded "read" on',
      noun: "controls",
    });
    expect(
      describeEvent(row("brand_new_kind", "update", "widget")),
    ).toMatchObject({
      phrase: 'recorded "update" on',
      noun: "widget",
    });
  });

  it("never reads the payload", () => {
    const withPayload = {
      ...row("risk", "fields_changed"),
      payload_json: null,
    };
    expect(() => describeEvent(withPayload)).not.toThrow();
  });
});

describe("verbed", () => {
  it("handles past-tense and irregular actions", () => {
    expect(verbed("requested").phrase).toBe("requested");
    expect(verbed("carry_over").phrase).toBe("carried over");
    expect(verbed("archive").phrase).toBe("archived");
    expect(verbed("").phrase).toBe("changed");
  });
});

describe("targetHref", () => {
  it("links controls only", () => {
    expect(targetHref("control", "abc")).toBe("/controls/abc");
    expect(targetHref("controls", "a/b")).toBe("/controls/a%2Fb");
    expect(targetHref("risk", "abc")).toBeNull();
    expect(targetHref("exception", "abc")).toBeNull();
    expect(targetHref("control", "")).toBeNull();
  });
});

describe("groupByDay", () => {
  const now = new Date(2026, 10, 16, 12, 0, 0);
  const at = (d: number, h = 9) => new Date(2026, 10, d, h).toISOString();

  it("buckets by local day, keeping input order, with Today and Yesterday labels", () => {
    const groups = groupByDay(
      [
        { id: "a", occurred_at: at(16, 11) },
        { id: "b", occurred_at: at(16, 8) },
        { id: "c", occurred_at: at(15) },
        { id: "d", occurred_at: at(10) },
      ],
      now,
      "en-US",
    );
    expect(groups.map((g) => g.label)).toEqual([
      "Today",
      "Yesterday",
      "Tuesday, November 10, 2026",
    ]);
    expect(groups[0].rows.map((r) => r.id)).toEqual(["a", "b"]);
  });

  it("puts an unparsable timestamp in its own group", () => {
    const groups = groupByDay([{ occurred_at: "nope" }], now);
    expect(groups[0].label).toBe("Unknown date");
  });
});
