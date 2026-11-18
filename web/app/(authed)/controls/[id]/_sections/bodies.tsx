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

import { DemoBadge } from "@/components/demo-badge";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Person } from "@/components/ui/person";

import { Skeleton } from "@/components/ui/skeleton";
import { Timestamp } from "@/components/ui/timestamp";

import {
  type ControlAnchorDecision,
  type ControlAnchorHistoryResponse,
  type ControlCoverage,
  type ControlHistoryResponse,
  type ControlLinkedPoliciesResponse,
  type ControlLinkedRisksResponse,
  type EffectiveScopeResponse,
} from "@/lib/api/control-detail";
import { type EvidenceListResponse } from "@/lib/api/evidence";
import { type ExceptionsListResponse } from "@/lib/api/exceptions";
import { enumLabel } from "@/lib/labels/enum-labels";
import { frameworkLabel } from "@/lib/labels/framework-label";
import { normaliseUserId } from "@/lib/person";

import { statusPillClass } from "@/app/(authed)/exceptions/lifecycle";
import { formatResidualScore } from "@/app/(authed)/risks/filters";

import { exceptionsForControl } from "../strip";

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
            demo={rec.demo === true}
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
          ? frameworkLabel(reqForFv.framework_name, reqForFv.framework_version)
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
            <Badge variant="secondary">
              {enumLabel("PolicyStatus", p.status)}
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
                {formatResidualScore(r.residual_score, r.methodology)} residual
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
            <Timestamp
              value={h.evaluated_at}
              className="text-muted-foreground"
            />{" "}
            ·{" "}
            <span className="text-foreground">
              evaluated {enumLabel("EvaluationState", h.computed_state)}
            </span>{" "}
            <span className="text-muted-foreground">
              ({h.evidence_count} evidence ·{" "}
              {enumLabel("FreshnessState", h.freshness_status)})
            </span>
            {h.trigger === "manual" && h.triggered_by ? (
              <>
                {" "}
                · run by{" "}
                <Person
                  id={h.triggered_by}
                  className="text-foreground"
                  testId="audit-log-triggered-by"
                />
              </>
            ) : null}
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

// #2392 — how a remap decision reads on the History tab.
const ANCHOR_DECISION_LABELS: Record<
  ControlAnchorDecision["decision"],
  string
> = {
  approved: "Re-anchored",
  rejected: "Remap rejected",
  kept_legacy: "Kept legacy anchor",
};

/** An SCF code with its release, as `code@release`. */
function anchorAt(code: string, release: string): string {
  return `${code}@${release}`;
}

// #2392 — the control's SCF anchor history: every remap decision on any
// revision of its bundle, newest first. Codes read as code@release and the
// decider through <Person> (a system principal reads "System"); no ids.
export function AnchorHistoryBody({
  anchorHistoryQ,
}: {
  anchorHistoryQ: ReturnType<typeof useQuery<ControlAnchorHistoryResponse>>;
}) {
  if (anchorHistoryQ.isLoading) return <Skeleton className="h-16 w-full" />;
  if (anchorHistoryQ.error) {
    return (
      <p
        className="text-sm text-destructive"
        data-testid="anchor-history-error"
      >
        Could not load anchor history: {(anchorHistoryQ.error as Error).message}
      </p>
    );
  }
  const decisions = anchorHistoryQ.data?.decisions ?? [];
  if (decisions.length === 0) {
    return (
      <p
        className="text-sm text-muted-foreground"
        data-testid="anchor-history-empty"
      >
        No SCF remap decisions for this control yet.
      </p>
    );
  }
  return (
    <ul className="space-y-2 text-xs" data-testid="anchor-history-list">
      {decisions.map((d) => (
        <li
          key={d.id}
          className="text-muted-foreground"
          data-testid="anchor-history-entry"
          data-decision={d.decision}
        >
          <Timestamp value={d.decided_at} className="text-muted-foreground" /> ·{" "}
          <span className="text-foreground">
            {ANCHOR_DECISION_LABELS[d.decision] ?? d.decision}
          </span>{" "}
          <span className="font-mono" data-testid="anchor-history-legacy">
            {anchorAt(d.legacy_scf_id, d.legacy_release)}
          </span>
          {d.new_scf_id && d.new_release ? (
            <>
              {" → "}
              <span className="font-mono" data-testid="anchor-history-new">
                {anchorAt(d.new_scf_id, d.new_release)}
              </span>
              {d.new_version !== null ? ` (revision ${d.new_version})` : null}
            </>
          ) : null}
          {d.bulk ? " · bulk approval" : null} · by{" "}
          <Person
            id={d.decided_by}
            className="text-foreground"
            testId="anchor-history-decided-by"
          />
          {d.reason ? (
            <span className="block pl-4" data-testid="anchor-history-reason">
              {d.reason}
            </span>
          ) : null}
        </li>
      ))}
    </ul>
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
  demo = false,
}: {
  evidenceID: string;
  observedAt: string;
  kind: string | null;
  source: Record<string, unknown> | null;
  result: "pass" | "fail" | "na" | "inconclusive";
  /** #2043 — the in-place demo seed added this record. */
  demo?: boolean;
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
  // #2137 — the row opens the evidence detail page.
  return (
    <Link
      href={`/evidence/${encodeURIComponent(evidenceID)}`}
      className="grid grid-cols-12 items-center gap-3 py-2.5 hover:bg-muted/50"
      data-testid="evidence-stream-row"
    >
      <div className="col-span-1">
        <span className={`inline-block h-2 w-2 rounded-full ${dotClass}`} />
      </div>
      <div className="col-span-3 font-mono text-[11px] text-muted-foreground">
        <Timestamp value={observedAt} />
      </div>
      <div className="col-span-6 min-w-0">
        <div className="truncate text-sm text-foreground">
          {kind ?? evidenceID}
          {demo ? <DemoBadge testId="evidence-stream-row-demo" /> : null}
        </div>
        <div className="truncate text-[11px] text-muted-foreground">
          <EvidenceSource source={source} />
        </div>
      </div>
      <div
        className={`col-span-2 text-right text-xs ${resultClass}`}
        data-testid="evidence-stream-row-result"
      >
        {enumLabel("EvidenceResult", result)}
      </div>
    </Link>
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

// EvidenceSource (#2140) names who or what observed a record: a person or
// key by name through <Person>, never their id, and a connector's own
// actor tag as written.
function EvidenceSource({
  source,
}: {
  source: Record<string, unknown> | null;
}) {
  const actorID =
    source && typeof source.actor_id === "string" ? source.actor_id : "";
  if (actorID && (normaliseUserId(actorID) || actorID.startsWith("system"))) {
    return <Person id={actorID} testId="evidence-stream-row-source" />;
  }
  return (
    <span data-testid="evidence-stream-row-source">
      {formatEvidenceSource(source)}
    </span>
  );
}

// EXCEPTION_STATUS_LABEL is each exception status in words (#2140).
const EXCEPTION_STATUS_LABEL: Record<string, string> = {
  requested: "Requested",
  approved: "Approved",
  denied: "Denied",
  active: "Active",
  expired: "Expired",
  revoked: "Revoked",
};

// ExceptionsBody (#2140) lists the exceptions filed against the control,
// read through GET /v1/exceptions?control_id=. Each row opens the
// exception; the justification is shown as a one-line preview, as the
// exceptions list shows it (slice 138 P0-A-Ledger-3). Rows on any other
// control are dropped as well, so a list the server did not narrow never
// shows another control's exception.
export function ExceptionsBody({
  controlId,
  exceptionsQ,
}: {
  controlId: string;
  exceptionsQ: ReturnType<typeof useQuery<ExceptionsListResponse>>;
}) {
  if (exceptionsQ.isLoading) return <Skeleton className="h-16 w-full" />;
  if (exceptionsQ.error) {
    return (
      <p className="text-sm text-destructive" data-testid="exceptions-error">
        Could not load exceptions: {(exceptionsQ.error as Error).message}
      </p>
    );
  }
  const rows = exceptionsForControl(exceptionsQ.data?.exceptions, controlId);
  if (rows.length === 0) {
    return (
      <p
        className="text-sm text-muted-foreground"
        data-testid="exceptions-empty"
      >
        No exceptions have been filed against this control.
      </p>
    );
  }
  return (
    <ul
      className="divide-y divide-border text-sm"
      data-testid="exceptions-list"
    >
      {rows.map((e) => (
        <li key={e.id} className="py-2" data-testid="exception-row">
          <div className="flex items-center justify-between gap-3">
            <Link
              href={`/exceptions/${encodeURIComponent(e.id)}`}
              className="min-w-0 truncate text-foreground hover:underline"
              title={e.justification}
              data-testid="exception-row-link"
            >
              {e.justification.trim() || "Exception"}
            </Link>
            <span
              className={`shrink-0 rounded px-2 py-0.5 text-xs font-medium ${statusPillClass(
                e.status,
              )}`}
              data-testid="exception-row-status"
            >
              {EXCEPTION_STATUS_LABEL[e.status] ?? "Unknown"}
            </span>
          </div>
          <div className="text-[11px] text-muted-foreground">
            Expires <Timestamp value={e.expires_at} style="date" />
          </div>
        </li>
      ))}
    </ul>
  );
}
