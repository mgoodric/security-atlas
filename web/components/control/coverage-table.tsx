"use client";

// Slice 041 — coverage-by-framework table (AC-2, AC-7).
// Slice 256 — Coverage column + chevron + footer, with the strength
// bar re-bound to coverage (not raw strength).
//
// One row per coverage requirement. Each row shows:
//   1. Framework requirement (name + natural-key suffix)
//   2. STRM relationship-type badge
//   3. Strength (numeric, two decimals)
//   4. Coverage (numeric, two decimals; "n/a" when null) — slice 256
//   5. Coverage bar (fills at coverage %, not strength %) — slice 256
//   6. Chevron affordance for per-row drill-down — slice 256
//
// Rows whose framework_version is out of scope for this control
// (effective scope is empty — slice 018) render dashed/greyed with an
// "out of FrameworkScope" note and an "n/a" coverage value — never
// hidden (anti-criterion P0-2; constitutional invariant 5).
//
// Slice 256 changes the framing of slice 041's "no client-side
// fabrication" deferral: instead of declining to render coverage at
// all, we render the BACKEND'S computed coverage (a first-class field
// on `GET /v1/controls/{id}/coverage`). The frontend never multiplies
// strength × effectiveness here — that would risk a number that
// disagrees with the backend (slice 041's original concern; slice 256
// P0-256-1). When the backend ships `coverage: null` we display "n/a"
// honestly; we do NOT fall back to client-computed strength × something.
//
// Per-row chevron is a JUDGMENT D2 decision (slice 256 docs/audit-log):
// we render the chevron visually but make it non-interactive in this
// slice, with a tooltip that names the follow-up. Anti-criterion
// P0-256-4 forbids shipping a 404 destination; rendering a visible
// chevron with a tooltip explains the affordance without breaking
// navigation.

import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { cn } from "@/lib/utils";
import type { CoverageRequirement } from "@/lib/api/control-detail";
import { strmStyle } from "@/components/control/strm";
import {
  coverageBarPercent,
  formatCoverage,
} from "@/components/control/coverage";
import { bandStyle, classifyBand } from "@/components/control/confidence-band";

export function CoverageTable({
  requirements,
  outOfScopeFvIds,
}: {
  requirements: CoverageRequirement[];
  outOfScopeFvIds: ReadonlySet<string>;
}) {
  if (requirements.length === 0) {
    return (
      <p className="text-sm text-muted-foreground" data-testid="coverage-empty">
        This control has no mapped framework requirements. It is anchored but
        the SCF anchor has no STRM edges to framework requirements yet.
      </p>
    );
  }

  return (
    <div className="overflow-x-auto">
      <Table data-testid="coverage-table">
        <TableHeader>
          <TableRow>
            <TableHead>Framework requirement</TableHead>
            <TableHead className="w-32">STRM</TableHead>
            <TableHead className="w-20 text-right">Strength</TableHead>
            <TableHead className="w-20 text-right">Coverage</TableHead>
            <TableHead className="w-24">Confidence</TableHead>
            <TableHead className="w-32">Coverage bar</TableHead>
            <TableHead className="w-8 sr-only">Drill-down</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {requirements.map((req) => {
            const style = strmStyle(req.relationship_type);
            const outOfScope = outOfScopeFvIds.has(req.framework_version_id);
            const coverageDisplay = formatCoverage(
              outOfScope ? null : req.coverage,
            );
            const barPct = coverageBarPercent(req.coverage, outOfScope);
            const coverageIsNumeric = coverageDisplay !== "n/a";
            // Slice 482 — confidence band for this requirement row. The
            // band reflects the EFFECTIVE per-row coverage (null when out
            // of scope), so an out-of-scope or no-data row reads
            // "uncovered" rather than borrowing the raw strength's band.
            // Thresholds mirror the backend rollup (rollup.go) so the
            // per-row label and the requirement-level rollup never
            // disagree.
            const band = classifyBand(outOfScope ? null : req.coverage);
            const bStyle = bandStyle(band);
            return (
              <TableRow
                key={req.edge_id}
                data-testid="coverage-row"
                data-out-of-scope={outOfScope ? "true" : "false"}
                className={cn(
                  outOfScope &&
                    "border-dashed text-muted-foreground opacity-60",
                )}
              >
                <TableCell>
                  <div
                    className={cn(
                      "text-sm font-medium",
                      outOfScope ? "text-muted-foreground" : "text-foreground",
                    )}
                  >
                    {req.framework_name} · {req.code} — {req.title}
                  </div>
                  <div className="mt-0.5 font-mono text-[11px] text-muted-foreground">
                    {req.framework_slug}:{req.framework_version}:{req.code}
                    {outOfScope ? " · out of FrameworkScope" : ""}
                  </div>
                </TableCell>
                <TableCell>
                  <span
                    className={cn(
                      "inline-flex items-center rounded px-2 py-0.5 font-mono text-[10px] font-semibold",
                      style.badge,
                    )}
                    data-strm={req.relationship_type}
                    title={style.label}
                  >
                    {req.relationship_type}
                  </span>
                </TableCell>
                <TableCell className="text-right font-mono text-sm">
                  {req.strength.toFixed(2)}
                </TableCell>
                <TableCell
                  className={cn(
                    "text-right font-mono text-sm",
                    coverageIsNumeric
                      ? "font-semibold text-foreground"
                      : "text-muted-foreground",
                  )}
                  data-testid="coverage-cell"
                  data-coverage-state={
                    outOfScope
                      ? "out-of-scope"
                      : req.coverage === null
                        ? "no-data"
                        : "numeric"
                  }
                >
                  {coverageDisplay}
                </TableCell>
                <TableCell>
                  <span
                    className={cn(
                      "inline-flex items-center rounded px-2 py-0.5 text-[10px] font-semibold capitalize",
                      bStyle.badge,
                    )}
                    data-testid="confidence-band"
                    data-band={band}
                    title={bStyle.label}
                  >
                    {band}
                  </span>
                </TableCell>
                <TableCell>
                  <div
                    className="h-1.5 overflow-hidden rounded-full bg-muted"
                    role="presentation"
                  >
                    <div
                      className={cn(
                        "h-full",
                        outOfScope || req.coverage === null
                          ? "bg-slate-300"
                          : "bg-emerald-500",
                      )}
                      style={{ width: `${barPct}%` }}
                      data-testid="strength-bar"
                      data-coverage-percent={barPct}
                    />
                  </div>
                </TableCell>
                <TableCell className="text-right">
                  {/* Slice 256 D2 — chevron is a non-interactive
                      affordance. The per-row drill-down destination
                      (per-edge inspector / mappings-tab jump) ships in
                      a follow-up slice; rendering a clickable 404 here
                      is the anti-pattern slice 178 introduced and
                      slice 256 P0-256-4 forbids. The tooltip names the
                      next step honestly. */}
                  <span
                    data-testid="coverage-row-chevron"
                    aria-disabled="true"
                    title="Per-requirement inspector lands in a follow-up slice"
                    className="inline-flex items-center justify-end text-muted-foreground/40"
                  >
                    <svg
                      className="h-4 w-4"
                      viewBox="0 0 20 20"
                      fill="currentColor"
                      aria-hidden
                    >
                      <path d="M7.293 14.707a1 1 0 010-1.414L10.586 10 7.293 6.707a1 1 0 011.414-1.414l4 4a1 1 0 010 1.414l-4 4a1 1 0 01-1.414 0z" />
                    </svg>
                  </span>
                </TableCell>
              </TableRow>
            );
          })}
        </TableBody>
      </Table>
      <p
        className="mt-3 border-t border-border bg-muted/30 px-3 py-2 text-xs text-muted-foreground"
        data-testid="coverage-footer"
      >
        <span className="font-semibold text-foreground">Reading this:</span>{" "}
        coverage is strength × 30-day effectiveness, intersected with the
        framework&apos;s scope predicate. Where the framework is out of scope,
        coverage is n/a. The confidence band buckets that coverage:{" "}
        <span className="font-medium">strong</span> (≥ 0.80),{" "}
        <span className="font-medium">partial</span> (0.50–0.79),{" "}
        <span className="font-medium">weak</span> (&lt; 0.50), and{" "}
        <span className="font-medium">uncovered</span> (no in-scope evidence).
      </p>
    </div>
  );
}
