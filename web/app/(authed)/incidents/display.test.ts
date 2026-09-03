import { describe, expect, test } from "vitest";

import type { IncidentTimelineEntry } from "@/lib/api/incidents";

import {
  affectedSystemsSummary,
  chronologicalTimeline,
  dateTimeLabel,
  nextIncidentAction,
} from "./display";

describe("incident display helpers", () => {
  test("exposes only the valid next primary lifecycle action", () => {
    expect(nextIncidentAction("detected")).toEqual({
      kind: "transition",
      toState: "triaged",
    });
    expect(nextIncidentAction("triaged")).toEqual({
      kind: "transition",
      toState: "contained",
    });
    expect(nextIncidentAction("contained")).toEqual({
      kind: "transition",
      toState: "resolved",
    });
    expect(nextIncidentAction("resolved")).toEqual({
      kind: "close",
      toState: "closed",
    });
    expect(nextIncidentAction("closed")).toBeNull();
  });

  test("sorts the append-only timeline chronologically", () => {
    const entries = [
      entry("b", "2026-08-04T10:05:00Z"),
      entry("a", "2026-08-04T10:00:00Z"),
      entry("c", "2026-08-04T10:10:00Z"),
    ];
    expect(chronologicalTimeline(entries).map((e) => e.id)).toEqual([
      "a",
      "b",
      "c",
    ]);
  });

  test("summarizes affected systems without crashing on malformed values", () => {
    expect(affectedSystemsSummary(undefined)).toBe(
      "No affected systems recorded",
    );
    expect(
      affectedSystemsSummary([
        { name: "api" },
        { service: "billing" },
        { system: "warehouse" },
      ]),
    ).toBe("api, billing +1");
  });

  test("formats timestamps defensively", () => {
    expect(dateTimeLabel("2026-08-04T10:05:22Z")).toBe("2026-08-04 10:05Z");
    expect(dateTimeLabel("not-a-date")).toBe("not-a-date");
  });
});

function entry(id: string, occurredAt: string): IncidentTimelineEntry {
  return {
    id,
    action: "transitioned",
    actor: "operator",
    from_state: "detected",
    to_state: "triaged",
    summary: id,
    detail: {},
    occurred_at: occurredAt,
  };
}
