"use client";

// Slice 102 — /audits list view (the plural period index).
//
// Today `/audits` (plural) 404'd in the sidebar (audit finding F-4 in
// `Plans/canvas/13-ui-mockup-audit-2026-05-16.md`). This page ships the
// missing list view per the design captured in slice 093
// (`Plans/_archive/mockups/audits.html` + `Plans/canvas/12-ui-fill-in-design-
// decisions.md` §2/3/6/7/8).
//
// Disambiguation per design doc §6:
//   /audits           (plural)   — this page. Period index. List of
//                                  audit_periods (the lifecycle artifact).
//   /audit/[controlId] (singular) — slice 042. Per-control auditor walk-
//                                   through inside one open/frozen period.
// Different routes, different files, different goals. No collision.
//
// Row source resolution (slice 102 D1):
//   periodWire from `internal/api/auditperiods/handlers.go` via
//   `GET /v1/audit-periods` (canonical per design doc §7). Tenant-scoped
//   at the platform via the bearer-derived tenant context + RLS.
//
// Constitutional invariants honored:
//   - Invariant 6 (tenant isolation): the BFF at /api/audits forwards
//     the bearer cookie to /v1/audit-periods; the platform enforces
//     tenant isolation via RLS. The UI does not pass tenant_id.
//   - Invariant 10 (audit-period freezing): frozen periods are visually
//     distinct (lock icon + sky pill + tooltip). The list itself is
//     read-only — editing frozen periods requires the period-detail
//     page's unfreeze workflow (out of scope per P0-A2).
//
// Anti-criteria honored (P0):
//   - P0-A1: NO collision with /audit/[controlId] (different file,
//            different route segment — Next.js routes /audits and
//            /audit/[id] independently).
//   - P0-A2: NO editing frozen periods from the list (read-only render).
//   - P0-A3: (originally) NO period-create UI — the "New audit period"
//            CTA was a placeholder link to the existing admin flow.
//            UPDATED slice 149: P0-A3 is superseded — slice 149 ships the
//            create flow at `/audits/new`. Both the toolbar CTA and the
//            true-zero empty-state CTA now route there.
//   - P0-A4: NO invented columns — every column is derived from
//            periodWire (name, framework_version_id, period_start,
//            period_end, status, frozen_at, frozen_by, created_by).
//            Mockup shows a "Sample size" column but periodWire does
//            NOT carry it — we OMIT the column rather than invent.
//   - P0-A5: neutral test-* tokens only in tests.

import { useQuery } from "@tanstack/react-query";
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
import {
  fetchAuditPeriods,
  type AuditPeriod,
  type AuditPeriodsListResponse,
} from "@/lib/api/audit-periods";

import {
  ALL,
  applyFilters,
  clearFilters,
  DEFAULT_FILTERS,
  isDefault,
  setFilter,
  uniqueYears,
  type AuditFilters,
} from "./filters";
import {
  OSCAL_EXPORT_DOWNLOAD_LABEL,
  OSCAL_EXPORT_DOWNLOAD_TESTID,
  OSCAL_EXPORT_TOOLBAR_NOTE,
  OSCAL_EXPORT_TOOLBAR_TESTID,
  oscalExportDownloadFilename,
  oscalExportDownloadURL,
} from "./oscal-export";
import {
  daysUntilEnd,
  daysUntilEndLabel,
  frameworkVersionLabel,
  frozenMetaLabel,
  frozenTooltip,
  isFrozen,
  isInProgressUrgent,
  periodRangeLabel,
  statusDotClass,
  statusPillClass,
  statusTallyLabel,
} from "./format";

const FILTER_KEYS: (keyof AuditFilters)[] = ["framework", "status", "year"];

// The status pill option set enumerates the broader forward-looking
// status vocabulary from the slice text. The DB CHECK constraint only
// allows {'open','frozen'} in v1; the additional options become live
// when the backend lifts the constraint, with no page rework.
const STATUS_OPTIONS: { value: string; label: string }[] = [
  { value: ALL, label: "All statuses" },
  { value: "open", label: "open" },
  { value: "in_progress", label: "in_progress" },
  { value: "frozen", label: "frozen" },
  { value: "closed", label: "closed" },
  { value: "planned", label: "planned" },
];

// Framework pill is a no-op in v1 (periodWire has only the UUID — no
// label endpoint exists yet). The pill still renders so the UI shape
// stays stable across slices.
const FRAMEWORK_OPTIONS: { value: string; label: string }[] = [
  { value: ALL, label: "All frameworks" },
  { value: "soc2", label: "SOC 2" },
  { value: "iso27001", label: "ISO 27001" },
  { value: "nist_csf", label: "NIST CSF" },
  { value: "pci_dss", label: "PCI DSS" },
  { value: "hipaa", label: "HIPAA" },
  { value: "gdpr", label: "GDPR" },
];

// Small lock SVG. Heroicons mini lock-closed. Inlined to avoid pulling
// a new dependency for one icon.
function LockIcon({ className }: { className?: string }) {
  return (
    <svg
      aria-hidden
      className={className}
      viewBox="0 0 20 20"
      fill="currentColor"
      data-testid="audits-row-lock-icon"
    >
      <path
        fillRule="evenodd"
        d="M10 1a4.5 4.5 0 00-4.5 4.5V9H5a2 2 0 00-2 2v6a2 2 0 002 2h10a2 2 0 002-2v-6a2 2 0 00-2-2h-.5V5.5A4.5 4.5 0 0010 1zm3 8V5.5a3 3 0 10-6 0V9h6z"
        clipRule="evenodd"
      />
    </svg>
  );
}

function AuditsPageInner() {
  const router = useRouter();
  const search = useSearchParams();

  // URL-driven filter state — mirrors the slice 098 controls pattern so
  // the filter set is shareable / bookmarkable. Default = ALL on every
  // pill.
  const filters: AuditFilters = useMemo(() => {
    const out = { ...DEFAULT_FILTERS };
    for (const k of FILTER_KEYS) {
      const v = search.get(k);
      if (v) out[k] = v;
    }
    return out;
  }, [search]);

  const updateFilter = (key: keyof AuditFilters, value: string) => {
    const next = setFilter(filters, key, value);
    const sp = new URLSearchParams(search.toString());
    if (next[key] === ALL) {
      sp.delete(key);
    } else {
      sp.set(key, next[key]);
    }
    router.replace(`/audits?${sp.toString()}`);
  };

  const clearAll = () => {
    const cleared = clearFilters();
    const sp = new URLSearchParams(search.toString());
    for (const k of FILTER_KEYS) {
      if (cleared[k] === ALL) sp.delete(k);
    }
    router.replace(`/audits?${sp.toString()}`);
  };

  const periodsQ = useQuery<AuditPeriodsListResponse>({
    queryKey: ["audits", "list"],
    queryFn: fetchAuditPeriods,
  });

  const periods: AuditPeriod[] = useMemo(
    () => periodsQ.data?.audit_periods ?? [],
    [periodsQ.data],
  );

  const visible = useMemo(
    () => applyFilters(periods, filters),
    [periods, filters],
  );

  // Slice 215 — derive the status tally from the same TanStack Query
  // cache the table reads (P0-215-3: no new endpoint, no second
  // network call). Tally counts the FULL period set, not the
  // filter-narrowed `visible` set, so the operator's "this is the
  // right tenant" check is stable as they fiddle with filters.
  // Empty-string sentinel from the formatter (AC-2) cleanly skips
  // rendering below.
  const tally = useMemo(() => statusTallyLabel(periods), [periods]);
  const titleAdornment = tally ? (
    <span
      data-testid="audits-status-tally"
      aria-label="audit period status tally"
      className="text-sm text-muted-foreground"
    >
      {tally}
    </span>
  ) : null;

  const yearOptions: { value: string; label: string }[] = useMemo(() => {
    const years = uniqueYears(periods);
    return [
      { value: ALL, label: "All years" },
      ...years.map((y) => ({ value: y, label: y })),
    ];
  }, [periods]);

  const pills: FilterPill[] = [
    {
      id: "framework",
      label: "Framework",
      value: filters.framework,
      options: FRAMEWORK_OPTIONS,
    },
    {
      id: "status",
      label: "Status",
      value: filters.status,
      options: STATUS_OPTIONS,
    },
    {
      id: "year",
      label: "Year",
      value: filters.year,
      options: yearOptions,
    },
  ];

  const meta = (
    <span>
      Showing{" "}
      <span className="text-foreground font-medium">{visible.length}</span> of{" "}
      <span className="font-mono">{periods.length}</span> periods
    </span>
  );

  const columns: ListColumn<AuditPeriod>[] = [
    {
      id: "name",
      header: "Name",
      cell: (p) => (
        <span
          className="text-foreground font-medium"
          data-testid="audits-row-name"
        >
          {p.name}
        </span>
      ),
    },
    {
      id: "framework_version",
      header: "Framework version",
      cell: (p) => {
        // Slice 680 / ATLAS-033: render the readable catalog label
        // ("SCF 2025.2") when the LIST path resolved one; otherwise fall
        // back to the truncated framework_version_id UUID in mono so the
        // user can still copy the identifier for a support ticket. The
        // title attribute always carries the full UUID for copy.
        const fw = frameworkVersionLabel(p);
        return (
          <span
            className={
              fw.readable
                ? "text-xs text-muted-foreground"
                : "font-mono text-xs text-muted-foreground"
            }
            title={p.framework_version_id}
            data-testid="audits-row-framework-version"
          >
            {fw.text}
          </span>
        );
      },
    },
    {
      id: "period",
      header: "Period",
      cell: (p) => (
        <span
          className="font-mono text-xs text-muted-foreground"
          data-testid="audits-row-period"
        >
          {periodRangeLabel(p)}
        </span>
      ),
    },
    {
      id: "status",
      header: "Status",
      cell: (p) => {
        const urgent = isInProgressUrgent(p);
        const days = daysUntilEnd(p);
        return (
          <span
            className="inline-flex items-center gap-1.5"
            data-testid="audits-row-status-cell"
          >
            <span
              className={`inline-flex items-center gap-1.5 px-2 py-0.5 text-[11px] font-medium rounded-md ${statusPillClass(
                p.status,
              )}`}
              data-testid="audits-row-status-pill"
            >
              <span
                className={`w-1.5 h-1.5 rounded-full ${statusDotClass(
                  p.status,
                )}`}
              />
              {p.status}
            </span>
            {isFrozen(p) ? (
              <span
                title={frozenTooltip(p)}
                className="text-sky-700"
                data-testid="audits-row-lock"
              >
                <LockIcon className="w-3.5 h-3.5" />
              </span>
            ) : null}
            {urgent ? (
              <span
                title={`ends in ${days}d — start fieldwork soon`}
                className="inline-flex items-center"
                data-testid="audits-row-urgent-cue"
              >
                <span className="w-1.5 h-1.5 rounded-full bg-amber-500 animate-pulse" />
                <span className="ml-1 text-[11px] text-amber-700">
                  {daysUntilEndLabel(days)}
                </span>
              </span>
            ) : null}
          </span>
        );
      },
    },
    {
      id: "frozen",
      header: "Frozen",
      cell: (p) =>
        isFrozen(p) ? (
          <span className="inline-flex flex-col gap-1">
            <span
              className="font-mono text-xs text-muted-foreground"
              data-testid="audits-row-frozen-meta"
            >
              {frozenMetaLabel(p)}
            </span>
            {/* Slice 457 — per-frozen-period OSCAL signed-bundle download.
                Only frozen periods are exportable (invariant #10), so the
                link renders only on frozen rows. A native `<a download>`
                GET so the browser's file-save dialog handles the save and
                a Playwright `waitForEvent("download")` fires (AC-3). The
                BFF forwards the bearer to the platform :download verb,
                which serves the bundle tenant-scoped via RLS (invariant
                #6).

                The `download` attribute carries the deterministic
                filename VALUE (not a bare attribute): for a same-origin
                download the anchor's `download` value takes precedence
                over the server `Content-Disposition` filename, and a BARE
                `download` would make the browser derive the name from the
                URL last segment (`oscal-export` → `oscal-export.txt`).
                Pinning the value keeps the suggested filename
                deterministic and in lock-step with the BFF header
                (AC-2/AC-3). */}
            <a
              href={oscalExportDownloadURL(p.id)}
              download={oscalExportDownloadFilename(p.id, p.frozen_at)}
              className={buttonVariants({ variant: "outline", size: "sm" })}
              data-testid={OSCAL_EXPORT_DOWNLOAD_TESTID}
            >
              {OSCAL_EXPORT_DOWNLOAD_LABEL}
            </a>
          </span>
        ) : (
          <span
            className="text-xs text-muted-foreground italic"
            data-testid="audits-row-frozen-meta-empty"
          >
            —
          </span>
        ),
    },
    {
      id: "created_by",
      header: "Created by",
      align: "right",
      cell: (p) => (
        <span
          className="text-xs text-muted-foreground"
          data-testid="audits-row-created-by"
        >
          {p.created_by}
        </span>
      ),
    },
  ];

  const actions = (
    <>
      {/* Slice 457 — supersedes the slice-217 future-state disclosure.
          The disclosure `<span>` ("OSCAL bundle export ships with the
          per-period detail view") signposted a capability that did not
          exist as a download surface. Slice 457 ships it: every FROZEN
          period row carries a working "Export OSCAL bundle" download link
          (see the Frozen column below) pointing at the BFF download route
          (`/api/audits/{id}/oscal-export`), which streams the signed
          bundle as a `Content-Disposition: attachment` artifact. This
          toolbar note tells the operator where the now-working action
          lives — the honesty property migrates from the disclosure to the
          live affordance (AC-5). */}
      <span
        title={OSCAL_EXPORT_TOOLBAR_NOTE}
        aria-label={OSCAL_EXPORT_TOOLBAR_NOTE}
        data-testid={OSCAL_EXPORT_TOOLBAR_TESTID}
        className="inline-flex items-center px-2.5 text-[0.8rem] text-muted-foreground italic"
      >
        {OSCAL_EXPORT_TOOLBAR_NOTE}
      </span>
      {/* Slice 139 — audit-periods data export (CSV / JSON / XLSX).
          Distinct from the slice 030 OSCAL bundle (which is the
          cosigned audit-binding artifact). This export is the
          operator-facing data dump including freeze metadata —
          frozen_at / frozen_by / frozen_hash columns. Cosigned
          bundle bytes are NOT in this export's column set
          (P0-A-AP-1). */}
      <AuditPeriodsExportButtons />
      {/* Slice 138 — samples data export (CSV / JSON / XLSX). Row
          cap raised to 250K because sample populations at
          multi-product orgs can be voluminous. INCLUDES audit_period_id
          link (via populations.audit_period_id) so downstream
          consumers correlate samples to a specific frozen period. */}
      <SamplesExportButtons />
      {/* Slice 149: re-wired from disabled placeholder to a working
          link. Routes to /audits/new (slice 149 create form) which
          posts to /v1/audit-periods via the BFF. */}
      <Button
        size="sm"
        data-testid="audits-create-cta"
        onClick={() => router.push("/audits/new")}
      >
        New audit period
      </Button>
    </>
  );

  if (periodsQ.isLoading) {
    return (
      <ListPage
        title="Audit periods"
        subtitle="Period-level index — open a period for the per-control walk-through"
        actions={actions}
        filterRow={
          <FilterPills pills={pills} onChange={() => {}} meta={meta} />
        }
      >
        <ListLoadingSkeleton
          columns={["w-40", "w-24", "w-36", "w-20", "w-24", "w-20"]}
        />
      </ListPage>
    );
  }

  if (periodsQ.isError) {
    return (
      <ListPage
        title="Audit periods"
        subtitle="Period-level index — open a period for the per-control walk-through"
        actions={actions}
      >
        <Alert variant="destructive" data-testid="audits-load-error">
          <AlertTitle>Could not load audit periods</AlertTitle>
          <AlertDescription>
            {(periodsQ.error as Error).message}
          </AlertDescription>
        </Alert>
      </ListPage>
    );
  }

  // Empty-state copy diverges by reason:
  //   - true zero-state (no periods at all) → design doc §2 audits row:
  //     "No audit periods yet" + Create CTA.
  //   - filter-induced empty → "No periods match these filters" + Clear.
  const emptyState =
    periods.length === 0 ? (
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
              d="M6.75 3v2.25M17.25 3v2.25M3 18.75V7.5a2.25 2.25 0 012.25-2.25h13.5A2.25 2.25 0 0121 7.5v11.25m-18 0A2.25 2.25 0 005.25 21h13.5A2.25 2.25 0 0021 18.75m-18 0v-7.5A2.25 2.25 0 015.25 9h13.5A2.25 2.25 0 0121 11.25v7.5"
              strokeLinecap="round"
              strokeLinejoin="round"
            />
          </svg>
        }
        title="No audit periods yet"
        body="Create your first period when you start an external audit — sample populations will draw from evidence captured during the period."
        cta={{
          label: "Create audit period",
          // Slice 149: re-wired from /admin placeholder to the working
          // create flow at /audits/new. This was the operator-reported
          // bug (v1.10.0: clicking the CTA bounced to /admin).
          onClick: () => router.push("/audits/new"),
        }}
      />
    ) : (
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
              d="M6.75 3v2.25M17.25 3v2.25M3 18.75V7.5a2.25 2.25 0 012.25-2.25h13.5A2.25 2.25 0 0121 7.5v11.25"
              strokeLinecap="round"
              strokeLinejoin="round"
            />
          </svg>
        }
        title="No periods match these filters"
        body="Try widening the framework, status, or year filters."
        cta={
          isDefault(filters)
            ? undefined
            : { label: "Clear filters", onClick: clearAll }
        }
      />
    );

  return (
    <ListPage
      title="Audit periods"
      titleAdornment={titleAdornment}
      subtitle="Period-level index — open a period for the per-control walk-through"
      actions={actions}
      filterRow={
        <FilterPills
          pills={pills}
          onChange={(id, v) => updateFilter(id as keyof AuditFilters, v)}
          meta={meta}
        />
      }
    >
      {/* Slice 184 — per-period detail honesty banner.
          Surfaces the same disclosure that lives in code comments to
          the user, since the user cannot read code comments. Closes
          the slice-178 first-pass F-178-4 HONESTY-GAP finding by
          replacing the 404-on-click row affordance (removed below)
          with an explicit "future slice" notice above the table.
          When the detail page lands, this banner is deleted and the
          onRowClick is restored. */}
      <Alert data-testid="audits-detail-coming-soon-banner" className="mb-3">
        <AlertTitle>Per-period detail view is not available yet</AlertTitle>
        <AlertDescription>
          Rows in this table are not clickable yet. The per-audit-period detail
          page (frozen-status meta, sample-population summary, OSCAL export
          controls) is tracked separately. Today this page is the period-level
          index; for the per-control walk-through inside an open period, use the
          audit workspace.
        </AlertDescription>
      </Alert>
      <ListTable<AuditPeriod>
        columns={columns}
        rows={visible}
        rowKey={(p) => p.id}
        // Slice 184 — onRowClick intentionally omitted.
        //
        // Previously: row click pushed `/audits/${id}` which 404'd with
        // the standard Next.js not-found UI. The slice-178 first-pass
        // UI-honesty audit categorized this as a HONESTY-GAP (F-178-4):
        // the LIVE UI does not say "detail page coming soon" — it just
        // presents clickable rows that 404. Filed as spillover slice
        // #184 per slice-178 AC-17 one-slice-per-fix discipline.
        //
        // Resolution (Option A per slice 184): remove the click
        // affordance entirely until the detail page ships. ListTable
        // (web/components/list/list-table.tsx) drops `cursor-pointer`
        // and the click handler when `onRowClick` is undefined — see
        // lines 93-94 of that file. The banner above the table carries
        // the disclosure to the user.
        //
        // When the per-period detail page ships, restore the prop:
        //   onRowClick={(p) => router.push(`/audits/${encodeURIComponent(p.id)}`)}
        // and delete the banner above.
        emptyFallback={emptyState}
      />
    </ListPage>
  );
}

export default function AuditsListPage() {
  // useSearchParams must be wrapped in Suspense in App Router (Next 16
  // strict-mode requirement). The fallback is the same skeleton the
  // query-pending state shows, so the perceived layout is stable.
  return (
    <Suspense
      fallback={
        <ListLoadingSkeleton
          columns={["w-40", "w-24", "w-36", "w-20", "w-24", "w-20"]}
        />
      }
    >
      <AuditsPageInner />
    </Suspense>
  );
}

// AuditPeriodsExportButtons renders three direct-download links to the
// slice-139 audit-periods export BFF — one per format. Each is an
// `<a>` (not a fetch) so the browser's native file-save dialog
// handles the download. The BFF streams the platform response back
// unchanged. The row cap + concurrency cap + role gate live
// server-side.
//
// `data-testid` tokens are stable contract points for the Playwright
// e2e spec (`web/e2e/audit-periods-export.spec.ts`).
function AuditPeriodsExportButtons() {
  return (
    <span
      className="inline-flex items-center gap-1"
      data-testid="audit-periods-export-buttons"
    >
      <span className="text-xs text-muted-foreground">Export:</span>
      {(["csv", "json", "xlsx"] as const).map((fmt) => (
        <a
          key={fmt}
          href={`/api/admin/audit-periods/export?format=${fmt}`}
          className={buttonVariants({ variant: "outline", size: "sm" })}
          data-testid={`audit-periods-export-${fmt}`}
        >
          {fmt.toUpperCase()}
        </a>
      ))}
    </span>
  );
}

// SamplesExportButtons renders three direct-download links to the
// slice 138 samples export BFF — one per format. Sibling of
// AuditPeriodsExportButtons above; row cap is 250K at v1.
function SamplesExportButtons() {
  return (
    <span
      className="inline-flex items-center gap-1"
      data-testid="samples-export-buttons"
    >
      <span className="text-xs text-muted-foreground">Samples export:</span>
      {(["csv", "json", "xlsx"] as const).map((fmt) => (
        <a
          key={fmt}
          href={`/api/admin/samples/export?format=${fmt}`}
          className={buttonVariants({ variant: "outline", size: "sm" })}
          data-testid={`samples-export-${fmt}`}
        >
          {fmt.toUpperCase()}
        </a>
      ))}
    </span>
  );
}
