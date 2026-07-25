"use client";

// Slice 056 — decision timeline panel for the hierarchical risk
// dashboard.
//
// FULLY bound — slice 055 (Decision Log CRUD) is merged. The panel
// renders decisions from GET /v1/decisions as a vertical list sorted by
// `decided_at` descending, and cross-references GET /v1/decisions/overdue
// to mark rows whose revisit date has passed with an amber border + a
// "Revisit overdue" pill (AC-6).
//
// Filter bar (AC-7): `status` multi-select, `constraints[]` multi-select,
// `decision_maker` typeahead, and a `revisit_by` date range. Filter state
// is persisted in URL query params (deep-linkable) by the parent page;
// this component is a controlled view over a `filters` prop + an
// `onFiltersChange` callback.
//
// The upstream ListDecisions handler exposes only `?status=` and
// `?revisit_due_within_days=`, so `status` drives the server query while
// `constraints`, `decision_maker`, and the date range are applied
// client-side over the returned rows — honest, no fabricated server
// capability.
//
// Overdue pills are NEVER auto-acknowledged: the amber pill stays until a
// human acts on the decision upstream (anti-criterion P0-3). This panel
// is read-only — it surfaces data, it does not interpret or comment on it
// (anti-criterion: no LLM-generated commentary).

import Link from "next/link";
import { useMemo } from "react";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { PanelCard, type PanelState } from "@/components/dashboard/panel-card";
import type { Decision } from "@/lib/api/risk-hierarchy";
import { cn } from "@/lib/utils";

// The Decision Log status vocabulary (canvas §6.7).
export const DECISION_STATUSES = [
  "active",
  "revisited",
  "superseded",
  "expired",
] as const;

// Structured constraint tags (canvas §6.7). This is the documented
// starter set; tenant data may carry others, so the constraints filter
// also unions in whatever appears on the returned rows.
export const KNOWN_CONSTRAINTS = [
  "time-pressure",
  "cost",
  "dependency-blocked",
  "risk-accepted",
] as const;

export type TimelineFilters = {
  statuses: string[];
  constraints: string[];
  decisionMaker: string;
  revisitFrom: string; // YYYY-MM-DD or ""
  revisitTo: string; // YYYY-MM-DD or ""
};

function toggleIn(list: string[], value: string): string[] {
  return list.includes(value)
    ? list.filter((v) => v !== value)
    : [...list, value];
}

function FilterBar({
  filters,
  constraintOptions,
  onChange,
}: {
  filters: TimelineFilters;
  constraintOptions: string[];
  onChange: (next: TimelineFilters) => void;
}) {
  return (
    <div
      className="mb-4 flex flex-col gap-3 rounded-md border bg-muted/20 p-3"
      data-testid="timeline-filter-bar"
    >
      <div>
        <span className="text-xs font-medium text-muted-foreground">
          Status
        </span>
        <div className="mt-1 flex flex-wrap gap-1" data-testid="filter-status">
          {DECISION_STATUSES.map((s) => {
            const on = filters.statuses.includes(s);
            return (
              <Button
                key={s}
                type="button"
                size="sm"
                variant={on ? "default" : "outline"}
                onClick={() =>
                  onChange({
                    ...filters,
                    statuses: toggleIn(filters.statuses, s),
                  })
                }
                data-testid={`filter-status-${s}`}
                aria-pressed={on}
                className="h-6 px-2 text-xs"
              >
                {s}
              </Button>
            );
          })}
        </div>
      </div>
      <div>
        <span className="text-xs font-medium text-muted-foreground">
          Constraints
        </span>
        <div
          className="mt-1 flex flex-wrap gap-1"
          data-testid="filter-constraints"
        >
          {constraintOptions.map((c) => {
            const on = filters.constraints.includes(c);
            return (
              <Button
                key={c}
                type="button"
                size="sm"
                variant={on ? "default" : "outline"}
                onClick={() =>
                  onChange({
                    ...filters,
                    constraints: toggleIn(filters.constraints, c),
                  })
                }
                data-testid={`filter-constraint-${c}`}
                aria-pressed={on}
                className="h-6 px-2 text-xs"
              >
                {c}
              </Button>
            );
          })}
        </div>
      </div>
      <div className="flex flex-wrap gap-3">
        <label className="flex flex-col gap-1 text-xs font-medium text-muted-foreground">
          Decision maker
          <Input
            type="text"
            value={filters.decisionMaker}
            placeholder="filter by name"
            onChange={(e) =>
              onChange({ ...filters, decisionMaker: e.target.value })
            }
            data-testid="filter-decision-maker"
            className="h-7 w-44 text-xs"
          />
        </label>
        <label className="flex flex-col gap-1 text-xs font-medium text-muted-foreground">
          Revisit from
          <Input
            type="date"
            value={filters.revisitFrom}
            onChange={(e) =>
              onChange({ ...filters, revisitFrom: e.target.value })
            }
            data-testid="filter-revisit-from"
            className="h-7 w-36 text-xs"
          />
        </label>
        <label className="flex flex-col gap-1 text-xs font-medium text-muted-foreground">
          Revisit to
          <Input
            type="date"
            value={filters.revisitTo}
            onChange={(e) =>
              onChange({ ...filters, revisitTo: e.target.value })
            }
            data-testid="filter-revisit-to"
            className="h-7 w-36 text-xs"
          />
        </label>
      </div>
    </div>
  );
}

function statusVariant(
  status: string,
): "default" | "secondary" | "outline" | "ghost" {
  switch (status) {
    case "active":
      return "default";
    case "revisited":
      return "secondary";
    case "superseded":
      return "ghost";
    case "expired":
      return "outline";
    default:
      return "ghost";
  }
}

function formatDate(iso: string | undefined): string {
  if (!iso) return "—";
  // Render the date portion only; the timeline is day-granular.
  return iso.slice(0, 10);
}

export function DecisionTimelinePanel({
  decisions,
  overdueIds,
  filters,
  onFiltersChange,
  state,
}: {
  decisions: Decision[] | undefined;
  overdueIds: Set<string>;
  filters: TimelineFilters;
  onFiltersChange: (next: TimelineFilters) => void;
  state: PanelState;
}) {
  // The constraint filter options union the known starter set with
  // whatever constraints actually appear on the returned rows.
  const constraintOptions = useMemo(() => {
    const set = new Set<string>(KNOWN_CONSTRAINTS);
    for (const d of decisions ?? []) {
      for (const c of d.constraints ?? []) set.add(c);
    }
    return [...set].sort();
  }, [decisions]);

  // Client-side filtering for the dimensions the upstream handler does
  // not expose (constraints, decision_maker, revisit range). `status`
  // is also re-checked here so a multi-status selection (the server
  // takes one) still works.
  const visible = useMemo(() => {
    const rows = [...(decisions ?? [])];
    rows.sort((a, b) => b.decided_at.localeCompare(a.decided_at));
    return rows.filter((d) => {
      if (filters.statuses.length > 0 && !filters.statuses.includes(d.status)) {
        return false;
      }
      if (
        filters.constraints.length > 0 &&
        !filters.constraints.some((c) => (d.constraints ?? []).includes(c))
      ) {
        return false;
      }
      if (
        filters.decisionMaker.trim() !== "" &&
        !d.decision_maker
          .toLowerCase()
          .includes(filters.decisionMaker.trim().toLowerCase())
      ) {
        return false;
      }
      if (
        filters.revisitFrom &&
        (!d.revisit_by || d.revisit_by.slice(0, 10) < filters.revisitFrom)
      ) {
        return false;
      }
      if (
        filters.revisitTo &&
        (!d.revisit_by || d.revisit_by.slice(0, 10) > filters.revisitTo)
      ) {
        return false;
      }
      return true;
    });
  }, [decisions, filters]);

  const isEmptyData =
    !state.isLoading && !state.isError && (decisions ?? []).length === 0;

  return (
    <PanelCard
      title="Decision timeline"
      description="Decision Log with revisit-due markers"
      state={state}
      testid="decision-timeline-panel"
      skeletonClassName="h-64 w-full"
    >
      {isEmptyData ? (
        <div
          className="flex flex-col items-center gap-3 py-10 text-center"
          data-testid="decision-timeline-empty"
        >
          <p className="text-sm text-muted-foreground">
            No decisions recorded yet. The Decision Log captures tradeoffs and
            deferred best practices.
          </p>
          <Button
            variant="outline"
            size="sm"
            render={<Link href="/risks" />}
            data-testid="decision-timeline-empty-action"
          >
            Record your first decision
          </Button>
        </div>
      ) : (
        <>
          <FilterBar
            filters={filters}
            constraintOptions={constraintOptions}
            onChange={onFiltersChange}
          />
          {visible.length === 0 ? (
            <p
              className="py-8 text-center text-sm text-muted-foreground"
              data-testid="decision-timeline-no-match"
            >
              No decisions match the current filters.
            </p>
          ) : (
            <ol
              className="flex flex-col gap-2"
              data-testid="decision-timeline-list"
            >
              {visible.map((d) => {
                const overdue = overdueIds.has(d.id);
                return (
                  <li
                    key={d.id}
                    data-testid="decision-timeline-row"
                    data-overdue={overdue ? "true" : "false"}
                    className={cn(
                      "rounded-md border p-3",
                      overdue
                        ? "border-amber-500 bg-amber-50 dark:bg-amber-950/30"
                        : "border-border",
                    )}
                  >
                    <div className="flex items-start justify-between gap-2">
                      <div className="min-w-0">
                        <div className="flex items-center gap-2">
                          <span className="font-mono text-xs text-muted-foreground">
                            {d.decision_id}
                          </span>
                          <Badge variant={statusVariant(d.status)}>
                            {d.status}
                          </Badge>
                          {overdue ? (
                            <Badge
                              variant="outline"
                              className="border-amber-500 text-amber-700 dark:text-amber-400"
                              data-testid="decision-overdue-pill"
                            >
                              Revisit overdue
                            </Badge>
                          ) : null}
                        </div>
                        <p className="mt-0.5 truncate text-sm font-medium">
                          {d.title}
                        </p>
                        <p className="mt-0.5 text-xs text-muted-foreground">
                          {d.decision_maker} · decided{" "}
                          {formatDate(d.decided_at)} · revisit{" "}
                          {formatDate(d.revisit_by)}
                        </p>
                        {(d.constraints ?? []).length > 0 ? (
                          <div className="mt-1 flex flex-wrap gap-1">
                            {d.constraints.map((c) => (
                              <Badge
                                key={c}
                                variant="outline"
                                className="h-4 px-1 text-[10px]"
                              >
                                {c}
                              </Badge>
                            ))}
                          </div>
                        ) : null}
                      </div>
                    </div>
                  </li>
                );
              })}
            </ol>
          )}
        </>
      )}
    </PanelCard>
  );
}
