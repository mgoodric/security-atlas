"use client";

// Slice 041 — control detail view (`/controls/[id]`).
// Slice 253 — re-pointed the four endpoint-pending placeholders.
// Slice 254 — re-foldered the page behind a sticky seven-tab strip
//   (Overview / Evidence / Mappings / Effective scope / Policies /
//   Risks / History). Tab state is URL-bound via `?tab=<name>` so
//   deep links resolve to the right tab; default is Overview when
//   the param is missing or unrecognised. The tab strip mirrors
//   `Plans/_archive/mockups/control.html` lines 139-152.
//
// Built per `Plans/_archive/mockups/control.html`. Renders, in mockup order:
//   - control header: CTRL id, SCF anchor pill, lifecycle badge, family,
//     title, owner/implementation/freshness-class meta
//   - KPI strip: effectiveness 30d, frameworks satisfied, evidence
//     records 30d (slice 253 — bound to GET /v1/evidence?control_id=…),
//     effective-scope cells
//   - tab strip: Overview / Evidence / Mappings / Effective scope /
//     Policies / Risks / History (slice 254)
//   - per-tab panels — see the Tabs / panel components below
//
// Slice 253 backstory: slice 041 shipped five "endpoint not on main yet"
// placeholders for the evidence-list KPI, the evidence-stream card, and
// the three right-rail Policies / Risks / Audit-log cards. Each named a
// specific upstream that DID land later (slice 106 for the evidence
// list; slice 064 for per-control history / policies / risks). The
// placeholders were not re-walked when the backends shipped, so the
// page denied real platform capability. Slice 253 binds all four to
// their live upstreams; empty states still render when the response is
// genuinely empty (fresh-install tenant, no linked policies, etc.) —
// the empty-state copy now reflects "no data" honestly, not "endpoint
// pending". No fabricated rows anywhere (anti-criterion P0-253-2).
//
// Slice 254 backstory: the page previously inlined every section on a
// single scroll. The mockup's information density assumes a tabbed
// view (each tab's content is page-sized once #253 wired real
// backends). The slice re-folders the rendered sections behind seven
// tabs without changing any data path or backend contract — anti-
// criterion P0-254-3: the Overview tab's data layout is preserved
// verbatim. All TanStack Query keys, fetchers, error branches, and
// data-testids from slices 152 / 253 / 255 / 256 / 257 are unchanged
// — slices that touched the page recently keep working because the
// re-folder is purely a render-shape change.
//
// Data sync: server values live ONLY in TanStack Query cache and are read
// during render — there is NO useEffect that seeds state from a server
// value (React 19 set-state-in-effect lint, slice 063 learned this). The
// single useEffect is the 401 -> /login redirect, matching the
// `catalog/scf/[id]` precedent exactly.

import { useQuery } from "@tanstack/react-query";
import Link from "next/link";

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";

import { Skeleton } from "@/components/ui/skeleton";

import {
  type ControlCoverage,
  type ControlHistoryResponse,
  type ControlLinkedPoliciesResponse,
  type ControlLinkedRisksResponse,
  type EffectiveScopeResponse,
} from "@/lib/api/control-detail";
import { type EvidenceListResponse } from "@/lib/api/evidence";

import { formatResidualScore } from "@/app/(authed)/risks/filters";

export function EvidenceStreamBody({
  id,
  evidenceQ,
}: {
  id: string;
  evidenceQ: ReturnType<typeof useQuery<EvidenceListResponse>>;
}) {
  if (evidenceQ.isLoading) return <Skeleton className="h-24 w-full" />;
  if (evidenceQ.error) {
    return (
      <Alert variant="destructive" data-testid="evidence-stream-error">
        <AlertTitle>Could not load evidence stream</AlertTitle>
        <AlertDescription>
          {(evidenceQ.error as Error).message}
        </AlertDescription>
      </Alert>
    );
  }
  if (evidenceQ.data && evidenceQ.data.evidence.length > 0) {
    return (
      <div
        className="divide-y divide-border"
        data-testid="evidence-stream-list"
      >
        {evidenceQ.data.evidence.map((rec) => (
          <EvidenceStreamRow
            key={rec.evidence_id}
            evidenceID={rec.evidence_id}
            observedAt={rec.observed_at}
            kind={rec.evidence_kind}
            source={rec.source}
            result={rec.result}
          />
        ))}
        {evidenceQ.data.next_cursor ? (
          <div className="pt-3 text-center text-xs text-muted-foreground">
            <Link
              href={`/evidence?control_id=${encodeURIComponent(id)}`}
              className="hover:underline"
              data-testid="evidence-stream-view-all"
            >
              View all records in last 30 days →
            </Link>
          </div>
        ) : null}
      </div>
    );
  }
  return (
    <p
      className="text-sm text-muted-foreground"
      data-testid="evidence-stream-empty"
    >
      No evidence records for this control in the last 30 days.
    </p>
  );
}

export function EffectiveScopeRows({
  fvIds,
  scopeQueries,
  requirements,
}: {
  fvIds: string[];
  scopeQueries: ReadonlyArray<{
    data?: unknown;
    isLoading: boolean;
    error: unknown;
  }>;
  requirements: ControlCoverage["requirements"];
}) {
  if (fvIds.length === 0) {
    return (
      <p className="text-sm text-muted-foreground">
        No mapped frameworks, so no effective scope to compute.
      </p>
    );
  }
  return (
    <>
      {fvIds.map((fvId, i) => {
        const q = scopeQueries[i];
        const data = q?.data as EffectiveScopeResponse | undefined;
        const reqForFv = requirements.find(
          (r) => r.framework_version_id === fvId,
        );
        const label = reqForFv
          ? `${reqForFv.framework_name} ${reqForFv.framework_version}`
          : fvId;
        return (
          <div key={fvId} data-testid="effective-scope-row">
            <div className="flex items-center justify-between text-sm">
              <span className="text-foreground">{label}</span>
              <span className="font-mono text-xs">
                {q?.isLoading
                  ? "…"
                  : data
                    ? `${data.effective_scope_count} cells`
                    : "error"}
              </span>
            </div>
            {data && !data.in_scope ? (
              <div className="mt-0.5 text-[11px] text-muted-foreground">
                out of scope
                {data.out_of_scope_reason
                  ? ` — ${data.out_of_scope_reason}`
                  : ""}
              </div>
            ) : null}
          </div>
        );
      })}
    </>
  );
}

export function PoliciesBody({
  policiesQ,
}: {
  policiesQ: ReturnType<typeof useQuery<ControlLinkedPoliciesResponse>>;
}) {
  if (policiesQ.isLoading) return <Skeleton className="h-16 w-full" />;
  if (policiesQ.error) {
    return (
      <p className="text-sm text-destructive" data-testid="policies-error">
        Could not load linked policies: {(policiesQ.error as Error).message}
      </p>
    );
  }
  if (policiesQ.data && policiesQ.data.policies.length > 0) {
    return (
      <ul
        className="divide-y divide-border text-sm"
        data-testid="policies-list"
      >
        {policiesQ.data.policies.map((p) => (
          <li
            key={p.policy_id}
            className="flex items-center justify-between py-2"
            data-testid="policy-row"
          >
            <div className="min-w-0">
              <Link
                href={`/policies/${encodeURIComponent(p.policy_id)}`}
                className="text-foreground hover:underline"
              >
                {p.title}
              </Link>
              <div className="font-mono text-[11px] text-muted-foreground">
                {p.version}
              </div>
            </div>
            <Badge variant="secondary" className="capitalize">
              {p.status}
            </Badge>
          </li>
        ))}
      </ul>
    );
  }
  return (
    <p className="text-sm text-muted-foreground" data-testid="policies-empty">
      No policies are linked to this control.
    </p>
  );
}

export function RisksBody({
  risksQ,
}: {
  risksQ: ReturnType<typeof useQuery<ControlLinkedRisksResponse>>;
}) {
  if (risksQ.isLoading) return <Skeleton className="h-16 w-full" />;
  if (risksQ.error) {
    return (
      <p className="text-sm text-destructive" data-testid="risks-error">
        Could not load linked risks: {(risksQ.error as Error).message}
      </p>
    );
  }
  if (risksQ.data && risksQ.data.risks.length > 0) {
    return (
      <ul className="divide-y divide-border text-sm" data-testid="risks-list">
        {risksQ.data.risks.map((r) => (
          <li key={r.risk_id} className="py-2" data-testid="risk-row">
            <div className="flex items-center justify-between">
              <Link
                href={`/risks/${encodeURIComponent(r.risk_id)}`}
                className="min-w-0 text-foreground hover:underline"
              >
                {r.title}
              </Link>
              <span className="font-mono text-xs text-muted-foreground">
                {formatResidualScore(r.residual_score)} residual
              </span>
            </div>
            {typeof r.link_weight === "number" ? (
              <div className="font-mono text-[11px] text-muted-foreground">
                link weight {r.link_weight.toFixed(2)}
              </div>
            ) : null}
          </li>
        ))}
      </ul>
    );
  }
  return (
    <p className="text-sm text-muted-foreground" data-testid="risks-empty">
      No risks are linked to this control.
    </p>
  );
}

export function HistoryBody({
  historyQ,
}: {
  historyQ: ReturnType<typeof useQuery<ControlHistoryResponse>>;
}) {
  if (historyQ.isLoading) return <Skeleton className="h-16 w-full" />;
  if (historyQ.error) {
    return (
      <p className="text-sm text-destructive" data-testid="audit-log-error">
        Could not load audit log: {(historyQ.error as Error).message}
      </p>
    );
  }
  if (historyQ.data && historyQ.data.history.length > 0) {
    return (
      <ul className="space-y-1.5 text-xs" data-testid="audit-log-list">
        {historyQ.data.history.map((h, i) => (
          <li
            key={`${h.evaluated_at}-${i}`}
            className="text-muted-foreground"
            data-testid="audit-log-entry"
          >
            <span className="font-mono text-muted-foreground">
              {formatHistoryDate(h.evaluated_at)}
            </span>{" "}
            ·{" "}
            <span className="text-foreground">
              evaluated <span className="font-mono">{h.computed_state}</span>
            </span>{" "}
            <span className="text-muted-foreground">
              ({h.evidence_count} evidence ·{" "}
              <span className="font-mono">{h.freshness_status}</span>)
            </span>
          </li>
        ))}
      </ul>
    );
  }
  return (
    <p className="text-sm text-muted-foreground" data-testid="audit-log-empty">
      No evaluation history yet for this control.
    </p>
  );
}

// Slice 253 — one row of the evidence-stream card. Mirrors the mockup
// (`Plans/_archive/mockups/control.html` lines 389-440): a result dot, the
// observed_at timestamp, a summary line (the evidence_kind + the
// connector/actor tag), and a pass/fail/na/inconclusive badge. The
// `source` JSONB is rendered as a brief tag — `{actor_type}/{actor_id}`
// when present — rather than fabricated narrative copy.
export function EvidenceStreamRow({
  evidenceID,
  observedAt,
  kind,
  source,
  result,
}: {
  evidenceID: string;
  observedAt: string;
  kind: string | null;
  source: Record<string, unknown> | null;
  result: "pass" | "fail" | "na" | "inconclusive";
}) {
  const dotClass =
    result === "pass"
      ? "bg-emerald-500"
      : result === "fail"
        ? "bg-rose-500"
        : "bg-muted-foreground";
  const resultClass =
    result === "pass"
      ? "text-emerald-700"
      : result === "fail"
        ? "text-rose-600"
        : "text-muted-foreground";
  return (
    <div
      className="grid grid-cols-12 items-center gap-3 py-2.5"
      data-testid="evidence-stream-row"
    >
      <div className="col-span-1">
        <span className={`inline-block h-2 w-2 rounded-full ${dotClass}`} />
      </div>
      <div className="col-span-3 font-mono text-[11px] text-muted-foreground">
        {observedAt}
      </div>
      <div className="col-span-6 min-w-0">
        <div className="truncate text-sm text-foreground">
          {kind ?? evidenceID}
        </div>
        <div className="truncate font-mono text-[11px] text-muted-foreground">
          {formatEvidenceSource(source)}
        </div>
      </div>
      <div className={`col-span-2 text-right font-mono text-xs ${resultClass}`}>
        {result}
      </div>
    </div>
  );
}

// formatEvidenceSource renders the slice-013 provenance JSONB as a
// short "actor_type/actor_id" tag. Shape varies by connector; when the
// JSONB has no recognisable shape we fall back to "—" rather than
// stringifying arbitrary nested objects into the row.
export function formatEvidenceSource(
  source: Record<string, unknown> | null,
): string {
  if (!source || typeof source !== "object") return "—";
  const actorType = source.actor_type;
  const actorID = source.actor_id;
  if (typeof actorType === "string" && typeof actorID === "string") {
    return `${actorType}/${actorID}`;
  }
  if (typeof actorID === "string") return actorID;
  if (typeof actorType === "string") return actorType;
  return "—";
}

// formatHistoryDate renders the RFC3339 evaluated_at as the YYYY-MM-DD
// prefix used in the mockup's audit-log entries (lines 552-557). Falls
// back to the raw string when parsing fails so a backend-shape change
// surfaces as a visible date, not a silent error.
export function formatHistoryDate(iso: string): string {
  if (!iso) return "—";
  const t = iso.indexOf("T");
  return t > 0 ? iso.slice(0, t) : iso;
}
