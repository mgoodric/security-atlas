"use client";

// Slice 101 — /policies list view.
//
// Today `/policies` 404'd in the sidebar (audit finding F-4 in
// `Plans/canvas/13-ui-mockup-audit-2026-05-16.md`). This page ships
// the missing list view per the design captured in slice 093
// (`Plans/mockups/policies.html` + `Plans/canvas/12-ui-fill-in-design-
// decisions.md` §2/3/7/8).
//
// The page consumes the shared `web/components/list/*` shell from slice
// 098 — the reusable primitives that the other list-view slices
// (098/099/100/102) also consume.
//
// Data source resolution (slice 101 / 107):
//   `GET /v1/policies?include=ack_rate` (slice 022 `policyWire` +
//   slice 107 joined ack-rate cell) is the row source. The BFF
//   hard-codes the include param in `web/lib/api.ts` (mirrors slice
//   104's hard-coded `?include=state` for anchors). Published rows
//   carry a populated `ack_rate` cell; non-published rows carry
//   `ack_rate: null` and the cell renders an em-dash.
//
//   Per-row fan-out remains explicitly forbidden by P0-A2 — the
//   joined endpoint is the only ack-rate source for the list view.
//
// Constitutional invariants honored:
//   - Invariant 6 (tenant isolation): the BFF at /api/policies forwards
//     the bearer cookie to /v1/policies; the platform enforces tenant
//     isolation via RLS. The UI does not pass tenant_id.
//
// Anti-criteria honored (P0):
//   - P0-A1: NO auto-narration of ack-rate trends.
//   - P0-A2: NO client-side per-row fan-out — em-dash placeholder
//     instead until backend extension lands.
//   - P0-A3: NO invented columns — every column derives from
//     `policyWire` (title, version, status, owner_role, published_at,
//     updated_at) + the joined PolicyAckRate cell when available.
//   - P0-A4: NO policy-create UI bundled. Slice 242 closes the
//     forward-looking-UI claim by replacing the lying CTA
//     ("Scaffold five foundational policies" → /admin/credentials)
//     with a label-honest body disclosure naming the platform API
//     endpoint (`POST /v1/policies`) as the operator's concrete
//     next action. Slice 100's "link the lie at /admin/credentials"
//     precedent is explicitly retired for this empty-state — the
//     anti-criterion P0-242-4 forbids moving the lie to another
//     unrelated admin page; the only honest fix is to ship the
//     destination or update the copy. See slice 242 D1 in
//     `docs/audit-log/242-decisions.md` for the path-(b) rationale.
//   - P0-A5: neutral test-* tokens only.

import { useQuery } from "@tanstack/react-query";
import Link from "next/link";
import { useRouter, useSearchParams } from "next/navigation";
import { Suspense, useMemo } from "react";

import {
  EmptyState,
  FilterPills,
  ListLoadingSkeleton,
  ListPage,
  ListTable,
  type FilterPill,
  type ListColumn,
} from "@/components/list";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button, buttonVariants } from "@/components/ui/button";
import { Progress } from "@/components/ui/progress";
import {
  fetchPoliciesList,
  type PoliciesListResponse,
  type Policy,
} from "@/lib/api";

import {
  ackRateAriaLabel,
  ackRateBand,
  ackRateColor,
  ackRateTextColor,
  formatAckRate,
} from "./ack-rate";
import {
  ALL,
  applyFilters,
  clearFilters,
  DEFAULT_FILTERS,
  isDefault,
  setFilter,
  uniqueOwners,
  type PolicyFilters,
} from "./filters";
import { statusCountsLabel } from "./header-counts";
import {
  POLICIES_SCAFFOLD_FUTURE_BODY,
  POLICIES_SCAFFOLD_FUTURE_TESTID,
} from "./scaffold-future";

const FILTER_KEYS: (keyof PolicyFilters)[] = ["status", "owner_role"];

const STATUS_OPTIONS: { value: string; label: string }[] = [
  { value: ALL, label: "All statuses" },
  { value: "published", label: "published" },
  { value: "draft", label: "draft" },
  { value: "under_review", label: "under_review" },
  { value: "approved", label: "approved" },
  { value: "retired", label: "retired" },
];

function statusPillClass(status: string): string {
  switch (status) {
    case "published":
      return "bg-emerald-50 text-emerald-700";
    case "under_review":
    case "approved":
      return "bg-amber-50 text-amber-700";
    case "retired":
      return "bg-rose-50 text-rose-700";
    case "draft":
    default:
      return "bg-muted text-muted-foreground";
  }
}

function formatDate(iso: string | null | undefined): string {
  if (!iso) return "";
  // Take YYYY-MM-DD prefix only (matches mockup mono-spaced "2026-01-15"
  // shape). Avoids any locale drift from new Date().toLocaleDateString.
  return iso.slice(0, 10);
}

function PoliciesPageInner() {
  const router = useRouter();
  const search = useSearchParams();

  // URL-driven filter state — mirrors the slice 098/100 controls/risks
  // pattern so the filter set is shareable / bookmarkable. Default =
  // ALL on every pill.
  const filters: PolicyFilters = useMemo(() => {
    const out = { ...DEFAULT_FILTERS };
    for (const k of FILTER_KEYS) {
      const v = search.get(k);
      if (v) out[k] = v;
    }
    return out;
  }, [search]);

  const updateFilter = (key: keyof PolicyFilters, value: string) => {
    const next = setFilter(filters, key, value);
    const sp = new URLSearchParams(search.toString());
    if (next[key] === ALL) {
      sp.delete(key);
    } else {
      sp.set(key, next[key]);
    }
    router.replace(`/policies?${sp.toString()}`);
  };

  const clearAll = () => {
    const cleared = clearFilters();
    const sp = new URLSearchParams(search.toString());
    for (const k of FILTER_KEYS) {
      if (cleared[k] === ALL) sp.delete(k);
    }
    router.replace(`/policies?${sp.toString()}`);
  };

  const policiesQ = useQuery<PoliciesListResponse>({
    queryKey: ["policies", "list"],
    queryFn: fetchPoliciesList,
  });

  const rows: Policy[] = useMemo(
    () => policiesQ.data?.policies ?? [],
    [policiesQ.data],
  );

  const visible = useMemo(() => applyFilters(rows, filters), [rows, filters]);
  const ownerOptions: { value: string; label: string }[] = useMemo(() => {
    const owners = uniqueOwners(rows);
    return [
      { value: ALL, label: "All roles" },
      ...owners.map((o) => ({ value: o, label: o })),
    ];
  }, [rows]);

  const pills: FilterPill[] = [
    {
      id: "status",
      label: "Status",
      value: filters.status,
      options: STATUS_OPTIONS,
    },
    {
      id: "owner_role",
      label: "Owner role",
      value: filters.owner_role,
      options: ownerOptions,
    },
  ];

  const meta = (
    <span>
      Showing{" "}
      <span className="text-foreground font-medium">{visible.length}</span> of{" "}
      <span className="font-mono">{rows.length}</span> policies
    </span>
  );

  // Slice 239 — derive the header status-count tally from the FULL rows
  // list (NOT `visible`), per AC-1. The tally is the one-glance "this
  // is the right tenant" check the operator runs BEFORE they touch a
  // filter; making it filter-sensitive would shadow the "Showing X of
  // Y" meta-text in the filter row that already plays that role
  // (P0-239-2). Empty-string sentinel from `statusCountsLabel` cleanly
  // suppresses rendering for the zero-row case (AC-3).
  const headerCounts = useMemo(() => statusCountsLabel(rows), [rows]);
  const titleAdornment = headerCounts ? (
    <span
      data-testid="policies-status-counts"
      aria-label="policy status counts"
      className="text-sm text-muted-foreground"
    >
      {headerCounts}
    </span>
  ) : null;

  const columns: ListColumn<Policy>[] = [
    {
      id: "title",
      header: "Title",
      cell: (row) => (
        <Link
          href={`/policies/${encodeURIComponent(row.id)}`}
          className="text-foreground font-medium hover:text-primary"
          data-testid="policies-row-title"
          onClick={(e) => e.stopPropagation()}
        >
          {row.title}
        </Link>
      ),
    },
    {
      id: "version",
      header: "Version",
      cell: (row) => (
        <span className="font-mono text-xs text-foreground">{row.version}</span>
      ),
    },
    {
      id: "status",
      header: "Status",
      cell: (row) => (
        <span
          data-testid="policies-row-status"
          className={
            "inline-flex items-center px-2 py-0.5 text-[11px] font-medium rounded-md " +
            statusPillClass(row.status)
          }
        >
          {row.status}
        </span>
      ),
    },
    {
      id: "owner_role",
      header: "Owner role",
      cell: (row) => (
        <span className="text-xs text-muted-foreground">{row.owner_role}</span>
      ),
    },
    {
      id: "published_at",
      header: "Published",
      cell: (row) =>
        row.published_at ? (
          <span className="font-mono text-xs text-muted-foreground">
            {formatDate(row.published_at)}
          </span>
        ) : (
          <span className="text-xs text-muted-foreground italic">—</span>
        ),
    },
    {
      id: "ack_rate",
      header: "Acknowledgment",
      cell: (row) => {
        // Slice 107: the backend now joins the ack-rate cell via
        // `?include=ack_rate`. Non-published rows return `ack_rate:
        // null`; published rows with zero denominator (no required-role
        // users) return a cell with `percent: null`. Both cases render
        // em-dash honestly (slice 098 D1 precedent — labelled empty,
        // not fabricated).
        const rate = row.ack_rate ?? null;
        if (rate == null || rate.percent == null) {
          return (
            <span
              className="text-xs text-muted-foreground italic"
              data-testid="policies-ack-rate-missing"
            >
              {rate == null ? "—" : "no required-role users"}
            </span>
          );
        }
        const band = ackRateBand(rate.percent);
        return (
          <div
            className="flex items-center gap-2"
            data-testid="policies-ack-rate-cell"
          >
            <Progress
              value={rate.percent}
              aria-label={ackRateAriaLabel(rate)}
              indicatorClassName={ackRateColor(band)}
            />
            <span
              className={"font-mono text-xs " + ackRateTextColor(band)}
              data-testid="policies-ack-rate-caption"
            >
              {formatAckRate(rate)}
            </span>
          </div>
        );
      },
    },
    {
      id: "updated_at",
      header: "Updated",
      align: "right",
      cell: (row) => (
        <span className="font-mono text-xs text-muted-foreground">
          {formatDate(row.updated_at)}
        </span>
      ),
    },
  ];

  // Slice 138 — three Export links to the slice 138 policies BFF
  // (`/api/admin/policies/export?format=...`). Full policy row set
  // (including body_md) — operators need it for audit prep. RLS is
  // the only mitigation.
  const actions = (
    <>
      <div
        className="flex items-center gap-1"
        data-testid="policies-export-buttons"
      >
        <span className="text-xs text-muted-foreground">Export:</span>
        {(["csv", "json", "xlsx"] as const).map((fmt) => (
          <a
            key={fmt}
            href={`/api/admin/policies/export?format=${fmt}`}
            className={buttonVariants({ variant: "outline", size: "sm" })}
            data-testid={`policies-export-${fmt}`}
          >
            {fmt.toUpperCase()}
          </a>
        ))}
      </div>
      <Button variant="outline" size="sm" disabled>
        Acknowledgment report
      </Button>
      <Button size="sm" disabled>
        New policy
      </Button>
    </>
  );

  if (policiesQ.isLoading) {
    return (
      <ListPage
        title="Policy library"
        subtitle="Versioned policies · acknowledgment tracked against the current version"
        actions={actions}
        filterRow={
          <FilterPills pills={pills} onChange={() => {}} meta={meta} />
        }
      >
        <ListLoadingSkeleton />
      </ListPage>
    );
  }

  if (policiesQ.isError) {
    return (
      <ListPage
        title="Policy library"
        subtitle="Versioned policies · acknowledgment tracked against the current version"
        actions={actions}
      >
        <Alert variant="destructive" data-testid="policies-load-error">
          <AlertTitle>Could not load policies</AlertTitle>
          <AlertDescription>
            {(policiesQ.error as Error).message}
          </AlertDescription>
        </Alert>
      </ListPage>
    );
  }

  // Empty-state branch — two cases:
  //
  //   * zeroState (no rows in the tenant at all): slice 242 closed
  //     the slice 101 P0-A4 honesty-gap. The empty-state previously
  //     rendered a primary CTA "Scaffold five foundational policies"
  //     whose `onClick` pointed at `/admin/credentials` — an
  //     unrelated admin surface (slice 100's "land somewhere usable"
  //     placeholder pattern). Slice 242 retired the lying CTA: the
  //     `cta` prop is dropped and the disclosure is folded into the
  //     `body` text, which names the operator's concrete next action
  //     (drafting policies via `POST /v1/policies` on the platform
  //     API). The disclosure copy is the single source of truth in
  //     `./scaffold-future.ts` so vitest can pin its invariants
  //     (sentence-shape, future-tense framing, capability-named) and
  //     Playwright can assert on the load-bearing substring. The
  //     `data-testid` on the body wrapper composes with the slice 178
  //     honesty harness — `captureComingSoonButtons` looks for
  //     `button[disabled]` matching coming-soon copy; a body
  //     paragraph is invisible to that heuristic, which is the
  //     correct behavior because the disclosure IS the affordance.
  //
  //   * filter-narrowed (rows exist but none match the active
  //     filters): the slice 098 "Clear filters" affordance is the
  //     working action and remains untouched by slice 242 — the
  //     destination genuinely exists (it's the same page with
  //     filters cleared), so the CTA is honest.
  const zeroState = rows.length === 0;
  const emptyStateBody = zeroState ? (
    <span data-testid={POLICIES_SCAFFOLD_FUTURE_TESTID}>
      {POLICIES_SCAFFOLD_FUTURE_BODY}
    </span>
  ) : (
    "Try widening the status or owner-role filters."
  );
  const emptyState = (
    <EmptyState
      icon={
        <svg
          className="w-12 h-12 mx-auto"
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          strokeWidth="1.5"
          aria-hidden
        >
          <path
            d="M19.5 14.25v-2.625a3.375 3.375 0 00-3.375-3.375h-1.5A1.125 1.125 0 0113.5 7.125v-1.5a3.375 3.375 0 00-3.375-3.375H8.25m0 12.75h7.5m-7.5 3H12M10.5 2.25H5.625c-.621 0-1.125.504-1.125 1.125v17.25c0 .621.504 1.125 1.125 1.125h12.75c.621 0 1.125-.504 1.125-1.125V11.25a9 9 0 00-9-9z"
            strokeLinecap="round"
            strokeLinejoin="round"
          />
        </svg>
      }
      title={
        zeroState
          ? "No policies published yet"
          : "No policies match these filters"
      }
      body={emptyStateBody}
      cta={
        zeroState
          ? undefined
          : isDefault(filters)
            ? undefined
            : { label: "Clear filters", onClick: clearAll }
      }
    />
  );

  return (
    <ListPage
      title="Policy library"
      titleAdornment={titleAdornment}
      subtitle="Versioned policies · acknowledgment tracked against the current version"
      actions={actions}
      filterRow={
        <FilterPills
          pills={pills}
          onChange={(id, v) => updateFilter(id as keyof PolicyFilters, v)}
          meta={meta}
        />
      }
    >
      <ListTable<Policy>
        columns={columns}
        rows={visible}
        rowKey={(row) => row.id}
        onRowClick={(row) =>
          router.push(`/policies/${encodeURIComponent(row.id)}`)
        }
        emptyFallback={emptyState}
      />
    </ListPage>
  );
}

export default function PoliciesListPage() {
  // useSearchParams must be wrapped in Suspense in App Router (Next 16
  // strict-mode requirement). The fallback is the same skeleton the
  // query-pending state shows, so the perceived layout is stable.
  return (
    <Suspense fallback={<ListLoadingSkeleton />}>
      <PoliciesPageInner />
    </Suspense>
  );
}
