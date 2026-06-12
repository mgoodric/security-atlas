// Slice 125 — vitest cases for the audit-log typed client helpers.

import { describe, expect, test } from "vitest";

import {
  AUDIT_LOG_EXPORT_FORMATS,
  AUDIT_LOG_KINDS,
  AuditLogFetchError,
  MAX_WINDOW_DAYS,
  UnifiedEntry,
  buildAuditLogExportURL,
  buildUnifiedQuery,
  renderActorLabel,
  truncateActorId,
} from "./audit-log";

describe("buildUnifiedQuery", () => {
  test("emits the required from/to params verbatim", () => {
    const q = buildUnifiedQuery({
      from: "2026-05-11T00:00:00.000Z",
      to: "2026-05-18T00:00:00.000Z",
    });
    expect(q).toContain("from=2026-05-11T00%3A00%3A00.000Z");
    expect(q).toContain("to=2026-05-18T00%3A00%3A00.000Z");
    expect(q.startsWith("?")).toBe(true);
  });

  test("drops empty optional fields", () => {
    const q = buildUnifiedQuery({
      from: "2026-05-11T00:00:00.000Z",
      to: "2026-05-18T00:00:00.000Z",
      actor: "",
      kinds: [],
      cursor: "",
    });
    expect(q).not.toContain("actor");
    expect(q).not.toContain("kind=");
    expect(q).not.toContain("cursor");
    // Slice 669: include_reads omitted by default (business-events view).
    expect(q).not.toContain("include_reads");
  });

  test("joins kinds with commas (matches backend csv parser)", () => {
    const q = buildUnifiedQuery({
      from: "2026-05-11T00:00:00.000Z",
      to: "2026-05-18T00:00:00.000Z",
      kinds: ["evidence", "me", "walkthrough"],
    });
    expect(q).toContain("kind=evidence%2Cme%2Cwalkthrough");
  });

  test("trims actor input (defensive against UI whitespace)", () => {
    const q = buildUnifiedQuery({
      from: "2026-05-11T00:00:00.000Z",
      to: "2026-05-18T00:00:00.000Z",
      actor: "  00000000-0000-0000-0000-000000001111  ",
    });
    expect(q).toContain("actor=00000000-0000-0000-0000-000000001111");
  });

  test("passes cursor through when set", () => {
    const q = buildUnifiedQuery({
      from: "2026-05-11T00:00:00.000Z",
      to: "2026-05-18T00:00:00.000Z",
      cursor: "opaquebase64token",
    });
    expect(q).toContain("cursor=opaquebase64token");
  });

  // Slice 669 — read-telemetry opt-in is emitted ONLY when includeReads
  // is true (the Activity feed default excludes read-telemetry server-side).
  test("emits include_reads=true only when includeReads is set", () => {
    const base = {
      from: "2026-05-11T00:00:00.000Z",
      to: "2026-05-18T00:00:00.000Z",
    };
    expect(buildUnifiedQuery({ ...base, includeReads: true })).toContain(
      "include_reads=true",
    );
    expect(buildUnifiedQuery({ ...base, includeReads: false })).not.toContain(
      "include_reads",
    );
    expect(buildUnifiedQuery(base)).not.toContain("include_reads");
  });
});

describe("AUDIT_LOG_KINDS", () => {
  test("enumerates exactly the nine canonical kinds (matches platform unifiedlog.Kind)", () => {
    expect(AUDIT_LOG_KINDS).toEqual([
      "decision",
      "evidence",
      "exception",
      "sample",
      "audit_period",
      "aggregation_rule",
      "feature_flag",
      "me",
      "walkthrough",
    ]);
  });
});

describe("MAX_WINDOW_DAYS", () => {
  test("matches the backend's 90-day cap (defense-in-depth single source of truth)", () => {
    // Backend: internal/api/adminauditlog/unified.go const maxWindowDays = 90.
    expect(MAX_WINDOW_DAYS).toBe(90);
  });
});

describe("AuditLogFetchError", () => {
  test("carries the upstream status code for the UI to branch on", () => {
    const err = new AuditLogFetchError(
      403,
      "admin, auditor, or grc_engineer role required",
    );
    expect(err.status).toBe(403);
    expect(err.message).toMatch(/admin, auditor/);
    expect(err.name).toBe("AuditLogFetchError");
  });
});

// Helper: build a minimal UnifiedEntry varying only the fields the actor
// renderer cares about. The other fields are stubbed so the assertions
// stay focused on the cell-text contract.
function entry(actor_id: string, actor_name?: string | null): UnifiedEntry {
  return {
    occurred_at: "2026-05-18T00:00:00.000Z",
    actor_id,
    actor_name,
    tenant_id: "00000000-0000-0000-0000-000000000000",
    kind: "me",
    target_type: "user",
    target_id: "00000000-0000-0000-0000-000000000000",
    action: "profile.update",
    row_id: "00000000-0000-0000-0000-000000000000",
    payload_json: {},
  };
}

describe("truncateActorId (slice 129 fallback)", () => {
  test("empty actor_id renders '(none)'", () => {
    expect(truncateActorId("")).toBe("(none)");
  });
  test("short ids pass through unchanged", () => {
    expect(truncateActorId("abc")).toBe("abc");
    expect(truncateActorId("12345678")).toBe("12345678");
  });
  test("UUID-length ids truncate to first 8 chars + ellipsis", () => {
    expect(truncateActorId("00000000-0000-0000-0000-000000001111")).toBe(
      "00000000…",
    );
  });
});

describe("renderActorLabel (slice 129)", () => {
  test("prefers actor_name when the backend resolved one", () => {
    expect(
      renderActorLabel(
        entry("00000000-0000-0000-0000-000000001111", "Alice Example"),
      ),
    ).toBe("Alice Example");
  });

  test("falls back to truncated actor_id when actor_name is null (no users row matches)", () => {
    expect(
      renderActorLabel(entry("00000000-0000-0000-0000-000000001111", null)),
    ).toBe("00000000…");
  });

  test("falls back to truncated actor_id when actor_name is undefined (P0-A6 — older deployment)", () => {
    // Older deployments whose backend predates slice 129 do NOT serve
    // actor_name. The wire shape is then `undefined` (field absent).
    // The page MUST gracefully degrade rather than render "undefined"
    // or throw.
    expect(
      renderActorLabel(entry("00000000-0000-0000-0000-000000001111")),
    ).toBe("00000000…");
  });

  test("treats empty-string actor_name as a no-resolve and falls back", () => {
    // The schema-level default `users.display_name` is `''`. A user row
    // that was never edited would surface as `""` here; rendering an
    // empty string in the cell would be operator-hostile, so the
    // renderer treats it as 'no-resolve' and falls back.
    expect(
      renderActorLabel(entry("00000000-0000-0000-0000-000000001111", "")),
    ).toBe("00000000…");
  });

  test("non-UUID actor_id with no actor_name renders short literal verbatim", () => {
    // Credential-only / system actors carry literals like 'key_xxx' or
    // 'seeder' as actor_id. truncateActorId returns the literal when
    // its length is <= 8.
    expect(renderActorLabel(entry("seeder", null))).toBe("seeder");
  });
});

describe("buildAuditLogExportURL (slice 135)", () => {
  test("produces the canonical BFF export URL with format + window", () => {
    const url = buildAuditLogExportURL("csv", {
      from: "2026-05-11T00:00:00.000Z",
      to: "2026-05-18T00:00:00.000Z",
    });
    expect(url.startsWith("/api/audit-log/export?")).toBe(true);
    expect(url).toContain("format=csv");
    expect(url).toContain("from=2026-05-11T00%3A00%3A00.000Z");
    expect(url).toContain("to=2026-05-18T00%3A00%3A00.000Z");
  });

  test("drops empty optional filters but keeps non-empty ones", () => {
    const url = buildAuditLogExportURL("json", {
      from: "2026-05-11T00:00:00.000Z",
      to: "2026-05-18T00:00:00.000Z",
      actor: "",
      kinds: [],
    });
    expect(url).not.toContain("actor=");
    expect(url).not.toContain("kind=");

    const withFilters = buildAuditLogExportURL("xlsx", {
      from: "2026-05-11T00:00:00.000Z",
      to: "2026-05-18T00:00:00.000Z",
      actor: "user-alice",
      kinds: ["evidence", "me"],
    });
    expect(withFilters).toContain("actor=user-alice");
    expect(withFilters).toContain("kind=evidence%2Cme");
    expect(withFilters).toContain("format=xlsx");
  });

  test("AUDIT_LOG_EXPORT_FORMATS enumerates exactly csv|json|xlsx (slice 135 P0-A11)", () => {
    // Triple-locked: order matters for the dropdown UX; PDF MUST NOT
    // appear (slice 135 P0-A11).
    expect(AUDIT_LOG_EXPORT_FORMATS).toEqual(["csv", "json", "xlsx"]);
    expect(AUDIT_LOG_EXPORT_FORMATS).not.toContain("pdf" as never);
  });
});

describe("UnifiedEntry shape (slice 129)", () => {
  test("actor_name is typed as optional string | null (forward-compat with older backends)", () => {
    // Compile-time assertion: all three forms must satisfy the type.
    const withName: UnifiedEntry = entry("uuid", "Alice");
    const withNullName: UnifiedEntry = entry("uuid", null);
    const withoutField: UnifiedEntry = entry("uuid");
    expect(withName.actor_name).toBe("Alice");
    expect(withNullName.actor_name).toBeNull();
    expect(withoutField.actor_name).toBeUndefined();
  });
});
