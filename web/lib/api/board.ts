// Slice 370 — quarterly board pack (slice 032 + slice 043), extracted
// from the former `web/lib/api.ts` god-file. Includes the slice-043
// export-URL helpers and the approver-role probe (`getSessionMe`).

import { APIError } from "./base";

// ----- Slice 032: quarterly board pack -----
//
// The quarterly board pack has a draft -> published lifecycle. The
// `content` is a structured map of fixed sections; each section carries a
// templated narrative, an optional operator override, an approval flag,
// and structured data. Publish is gated on every section being approved
// (decision D6). The wire shapes mirror internal/api/board/pack_handlers.go.

export type BoardPackSectionData = {
  // posture
  frameworks?: {
    slug: string;
    name: string;
    coverage_pct: number;
    freshness_pct: number;
    trend_arrow: string;
    delta: number;
    state: string;
  }[];
  // top_risks
  top_risks?: {
    id: string;
    title: string;
    category: string;
    treatment: string;
    residual_severity: number;
    age_days: number;
  }[];
  // coverage_trend
  coverage_pct?: number;
  baseline_coverage_pct?: number;
  coverage_delta?: number;
  // open_findings
  findings?: {
    evaluation_id: string;
    control_id: string;
    scope_cell_id: string;
    evaluated_at: string;
    freshness_status: string;
  }[];
  findings_count?: number;
  // vendor_burndown (generated — slice 273). Mirrors the
  // internal/board/pack.go SectionData `vendor_burndown_*` JSON tags.
  vendor_burndown_total?: number;
  vendor_burndown_on_time?: number;
  vendor_burndown_past_due?: number;
  vendor_burndown_on_time_pct?: number;
  vendor_burndown_on_time_fraction?: number;
  // operational_metrics (operator-entered)
  phishing_pass_rate_pct?: number | null;
  p1_patch_median_days?: number | null;
  incident_count?: number | null;
  vendor_reviews_on_time?: number | null;
  vendor_reviews_total?: number | null;
  // investment (operator-entered)
  spend_usd?: number;
  cost_per_coverage_point?: number;
};

export type BoardPackSection = {
  key: string;
  title: string;
  templated_text: string;
  override_text: string;
  approved: boolean;
  data: BoardPackSectionData;
};

export type BoardPackContent = {
  period_end: string;
  generated_at: string;
  status: string;
  sections: Record<string, BoardPackSection>;
};

export type BoardPack = {
  id: string;
  period_end: string;
  status: string;
  content: BoardPackContent;
  narrative_md: string;
  published_by?: string;
  published_at?: string;
  created_at: string;
  updated_at: string;
};

// The fixed, ordered section keys (decision D6) — the single source of
// truth in the UI for "what sections exist and in what order". Mirrors
// internal/board/pack.go SectionKeys.
//
// Slice 273 added `vendor_burndown` in slot §05 (between `open_findings`
// and `operational_metrics`). The mirror is the *only* FE change in this
// slice — no dedicated <VendorBurndown /> component ships here; the page
// renders the section's chrome (title, approve button, templated
// narrative) via the default fallback in SectionStructured, and the
// publish-gate math stays correct (totalSections === BOARD_PACK_SECTION_KEYS.length).
// A dedicated component lands in a follow-on FE slice. See
// docs/audit-log/273-decisions.md D6.
export const BOARD_PACK_SECTION_KEYS: string[] = [
  "posture",
  "top_risks",
  "coverage_trend",
  "open_findings",
  "vendor_burndown",
  "operational_metrics",
  "investment",
  "asks",
];

// SECTION_TITLES maps each fixed section key to its board-facing human
// label. Mirrors internal/board/pack.go `sectionTitles` (all eight keys).
// This is the FE source of truth for a section's display name so the UI
// NEVER shows a raw internal key (slice 662 AC-2) — not even when the
// served section is missing or carries an empty `title` (an incomplete
// stored pack, e.g. an older demo-seed row). Resolution order is
// SECTION_TITLES[key] -> served section.title -> key (the last is a
// defensive floor for an unknown key, which the fixed set never contains).
export const SECTION_TITLES: Record<string, string> = {
  posture: "Posture summary",
  top_risks: "Top risks aging",
  coverage_trend: "Coverage trend",
  open_findings: "Open findings",
  vendor_burndown: "Vendor risk burndown",
  operational_metrics: "Operational metrics",
  investment: "Investment vs coverage",
  asks: "Asks of the board",
};

// sectionLabel resolves the human-readable label for a section key. It
// prefers the canonical FE label, then the served title, then the raw key
// as a last-resort floor (slice 662 AC-2 — a label always renders).
export function sectionLabel(key: string, section?: BoardPackSection): string {
  return SECTION_TITLES[key] ?? (section?.title || key);
}

// Operator-entered structured inputs for the PUT section endpoint
// (decisions D3 + D5). All fields optional — only populated ones apply.
export type BoardPackSectionInputs = {
  phishing_pass_rate_pct?: number;
  p1_patch_median_days?: number;
  incident_count?: number;
  vendor_reviews_on_time?: number;
  vendor_reviews_total?: number;
  spend_usd?: number;
  baseline_coverage_pct?: number;
};

async function boardPackJSON<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(path, init);
  if (!res.ok) {
    let msg = `${res.status} ${res.statusText}`;
    try {
      const body = (await res.json()) as { error?: string };
      if (body.error) msg = body.error;
    } catch {
      // keep the status-line message
    }
    throw new APIError(res.status, msg);
  }
  return (await res.json()) as T;
}

export function listBoardPacks(): Promise<BoardPack[]> {
  return boardPackJSON<{ packs: BoardPack[] }>("/api/board-packs").then(
    (b) => b.packs ?? [],
  );
}

export function generateBoardPack(periodEnd: string): Promise<BoardPack> {
  return boardPackJSON<BoardPack>("/api/board-packs", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ period_end: periodEnd }),
  });
}

export function getBoardPack(id: string): Promise<BoardPack> {
  return boardPackJSON<BoardPack>(`/api/board-packs/${encodeURIComponent(id)}`);
}

export function updateBoardPackSection(
  id: string,
  key: string,
  payload: { override_text?: string; inputs?: BoardPackSectionInputs },
): Promise<BoardPack> {
  return boardPackJSON<BoardPack>(
    `/api/board-packs/${encodeURIComponent(id)}/sections/${encodeURIComponent(
      key,
    )}`,
    {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(payload),
    },
  );
}

export function approveBoardPackSection(
  id: string,
  key: string,
): Promise<BoardPack> {
  return boardPackJSON<BoardPack>(
    `/api/board-packs/${encodeURIComponent(id)}/sections/${encodeURIComponent(
      key,
    )}/approve`,
    { method: "POST" },
  );
}

export function publishBoardPack(
  id: string,
  publishedBy: string,
): Promise<BoardPack> {
  return boardPackJSON<BoardPack>(
    `/api/board-packs/${encodeURIComponent(id)}/publish`,
    {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ published_by: publishedBy }),
    },
  );
}

// ----- Slice 043: export URLs + approver-role probe -----
//
// boardPackMarkdownURL / boardPackPdfURL point at the slice-043 BFF
// passthrough routes, NOT the raw /v1/board-packs/...md and .../pdf
// endpoints (which require an Authorization header the browser cannot
// attach to a plain link). The BFF routes read the bearer cookie
// server-side and stream the binary bytes back.

export function boardPackMarkdownURL(id: string): string {
  return `/api/board-packs/${encodeURIComponent(id)}/markdown`;
}

export function boardPackPdfURL(id: string): string {
  return `/api/board-packs/${encodeURIComponent(id)}/pdf`;
}

// The approver gate (AC-3) — the UI hides approve + publish controls
// unless the current bearer is an admin credential. The platform always
// enforces its own publish gate (every section approved + published_by
// required); the UI gate is defense-in-depth + clearer affordance.
//
// Decision D3 of slice 043: there is no `is_board_approver` role on
// main; we reuse the slice-060 /api/admin/me probe (is_admin boolean).
// A finer role is a documented follow-up.

export type SessionMe = {
  is_admin: boolean;
};

export async function getSessionMe(): Promise<SessionMe> {
  const res = await fetch("/api/admin/me", { cache: "no-store" });
  if (res.status === 401) {
    return { is_admin: false };
  }
  if (!res.ok) {
    // Don't throw — degrade to "not approver" so the UI stays usable.
    return { is_admin: false };
  }
  const body = (await res.json()) as { is_admin?: boolean };
  return { is_admin: body.is_admin === true };
}
