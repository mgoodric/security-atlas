// Slice 218 — board-pack detail breadcrumb component.
//
// Renders the breadcrumb chain at the left edge of the sticky export
// bar. Replaces the slice-043 `← All packs` link (which is semantically
// the same as the breadcrumb's parent segment — keeping both would be
// the redundancy AC-2 warns against; slice 218 D2).
//
// Mockup parity target: Plans/_archive/mockups/board-pack.html lines 27-33.
// Anti-parity: we drop the mockup's fabricated `Sentinel Labs` +
// `Board reports` segments (no session-bound tenant name + no parent
// route on main). See pack-breadcrumb.ts for the honesty rationale.
//
// Render shape (Tailwind, matches the mockup's `text-xs text-slate-500`
// styling with the trailing segment as `text-slate-900 font-medium`):
//
//   Board packs  ›  Q1 2026
//   <link>       <chev>  <plain text>

"use client";

import Link from "next/link";

import { packBreadcrumbSegments } from "./pack-breadcrumb-segments";

type PackBreadcrumbProps = {
  periodEnd: string;
};

export function PackBreadcrumb({ periodEnd }: PackBreadcrumbProps) {
  const segments = packBreadcrumbSegments(periodEnd);
  return (
    <nav
      aria-label="breadcrumb"
      data-testid="pack-breadcrumb"
      className="flex items-center gap-1 text-xs text-slate-500"
    >
      {segments.map((seg, i) => {
        const isLast = i === segments.length - 1;
        return (
          <span key={seg.testId} className="flex items-center gap-1">
            {seg.href ? (
              <Link
                href={seg.href}
                data-testid={seg.testId}
                className="hover:text-slate-700"
              >
                {seg.label}
              </Link>
            ) : (
              <span
                data-testid={seg.testId}
                className="font-medium text-slate-900"
                aria-current={isLast ? "page" : undefined}
              >
                {seg.label}
              </span>
            )}
            {!isLast && (
              <ChevronRight
                aria-hidden="true"
                data-testid="pack-breadcrumb-sep"
              />
            )}
          </span>
        );
      })}
    </nav>
  );
}

function ChevronRight({
  "aria-hidden": ariaHidden,
  "data-testid": testId,
}: {
  "aria-hidden"?: boolean | "true" | "false";
  "data-testid"?: string;
}) {
  // Inline SVG matches the mockup chevron (Plans/_archive/mockups/board-pack.html
  // lines 29 + 31) to keep the visual identical without adding a new
  // dependency. Heroicons / lucide-react aren't in the export-bar's
  // dependency surface today, and pulling them in for one chevron is
  // the same anti-pattern slice 217 D2 rejected for shadcn Popover.
  return (
    <svg
      aria-hidden={ariaHidden}
      data-testid={testId}
      className="h-3 w-3"
      viewBox="0 0 20 20"
      fill="currentColor"
    >
      <path d="M7.293 14.707a1 1 0 010-1.414L10.586 10 7.293 6.707a1 1 0 011.414-1.414l4 4a1 1 0 010 1.414l-4 4a1 1 0 01-1.414 0z" />
    </svg>
  );
}
