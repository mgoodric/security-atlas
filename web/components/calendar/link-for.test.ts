// Slice 183 — unit test for the calendar `linkFor` helper.
//
// AC-6 coverage: assert the exception + policy branches return the
// no-link sentinel value, and that no branch ever returns `"#"`.
//
// The default branch is exercised via a deliberate `as never` cast —
// TypeScript can't reach it at compile time (the union is closed), but
// the runtime fallback (assertNever throwing) is the safety net the
// slice 178 audit cares about.

import { describe, expect, it } from "vitest";

import type { CalendarEvent } from "@/lib/api/calendar";

import { linkFor } from "./link-for";

function makeEvent(overrides: Partial<CalendarEvent>): CalendarEvent {
  return {
    id: "ev-1",
    type: "audit",
    title: "Event",
    starts_at: "2026-01-01T00:00:00Z",
    related_entity_id: "ent-1",
    related_entity_kind: "audit_period",
    summary: "",
    status: "scheduled",
    ...overrides,
  };
}

describe("linkFor", () => {
  it("audit event returns a link to the audits detail route", () => {
    const result = linkFor(
      makeEvent({ type: "audit", related_entity_id: "ap-42" }),
    );
    expect(result).toEqual({ kind: "link", href: "/audits/ap-42" });
  });

  it("control event returns a link to the control detail route", () => {
    const result = linkFor(
      makeEvent({ type: "control", related_entity_id: "ctl-9" }),
    );
    expect(result).toEqual({ kind: "link", href: "/controls/ctl-9" });
  });

  it("vendor event returns a link to the vendor detail route (slice 675)", () => {
    const result = linkFor(
      makeEvent({ type: "vendor", related_entity_id: "vnd-7" }),
    );
    expect(result).toEqual({ kind: "link", href: "/vendors/vnd-7" });
  });

  it("exception event returns a static (no-link) result with an explanatory reason", () => {
    const result = linkFor(makeEvent({ type: "exception" }));
    expect(result.kind).toBe("static");
    if (result.kind === "static") {
      expect(result.reason).toMatch(/exception/i);
      expect(result.reason).toMatch(/future slice/i);
    }
  });

  it("policy event returns a static (no-link) result with an explanatory reason", () => {
    const result = linkFor(makeEvent({ type: "policy" }));
    expect(result.kind).toBe("static");
    if (result.kind === "static") {
      expect(result.reason).toMatch(/policy/i);
      expect(result.reason).toMatch(/future slice/i);
    }
  });

  it("does NOT return a `#` href for any of the event types (AC-1)", () => {
    const types: CalendarEvent["type"][] = [
      "audit",
      "exception",
      "policy",
      "vendor",
      "control",
    ];
    for (const t of types) {
      const result = linkFor(makeEvent({ type: t }));
      if (result.kind === "link") {
        expect(result.href).not.toBe("#");
        expect(result.href.startsWith("/")).toBe(true);
      }
    }
  });

  it("static results never carry an href string (defense against future regressions)", () => {
    const result = linkFor(makeEvent({ type: "exception" }));
    // TypeScript narrows away `href` on the static branch; runtime
    // checks the property is genuinely absent so an accidental
    // string-typed return won't slip through.
    expect("href" in result).toBe(false);
  });

  it("throws assertNever on an unexpected runtime event type (AC-1 default branch)", () => {
    // Cast through `unknown` to bypass the closed union — the
    // assertNever path is unreachable in well-typed code, but the
    // runtime safety net is the load-bearing claim the slice 178 audit
    // tests against.
    const rogue = makeEvent({}) as unknown as CalendarEvent;
    (rogue as { type: string }).type = "vendor_assessment";
    expect(() => linkFor(rogue)).toThrow(/unreachable/i);
  });
});
