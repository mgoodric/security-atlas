"use client";

// Slice 041 — coverage-by-framework table (AC-2, AC-7).
//
// One row per coverage requirement. Each row shows the STRM relationship
// type (badge), the numeric mapping strength, a strength bar, and the
// framework-version code. Rows whose framework_version is out of scope
// for this control (effective scope is empty — slice 018) render
// dashed/greyed with an "out of FrameworkScope" note and an n/a coverage
// value — never hidden (anti-criterion P0-2; constitutional invariant 5).
//
// `coverage` here is `strength` itself: the issue's AC-2 says "STRM types
// + strengths visible per row". The slice does NOT recompute
// strength × effectiveness per row — that weighted number is a
// framework-dashboard concern (slice 008/012 territory), and fabricating
// it here would risk a number that disagrees with the backend. The bar
// is the mapping strength, labeled as such.

import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { cn } from "@/lib/utils";
import type { CoverageRequirement } from "@/lib/api";
import { strmStyle } from "@/components/control/strm";

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
            <TableHead className="w-36">Strength bar</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {requirements.map((req) => {
            const style = strmStyle(req.relationship_type);
            const outOfScope = outOfScopeFvIds.has(req.framework_version_id);
            const pct = Math.round(
              Math.max(0, Math.min(1, req.strength)) * 100,
            );
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
                <TableCell>
                  <div
                    className="h-1.5 overflow-hidden rounded-full bg-muted"
                    role="presentation"
                  >
                    <div
                      className={cn(
                        "h-full",
                        outOfScope ? "bg-slate-300" : "bg-emerald-500",
                      )}
                      style={{ width: `${outOfScope ? 0 : pct}%` }}
                      data-testid="strength-bar"
                    />
                  </div>
                </TableCell>
              </TableRow>
            );
          })}
        </TableBody>
      </Table>
    </div>
  );
}
