// Slice 043 — sticky top export bar (per Plans/mockups/board-pack.html).
//
// Renders Export PDF + Copy Markdown + Approve & publish. The export
// links point at the slice-043 BFF passthrough routes (NOT the raw
// /v1/... endpoints — a plain <a href> cannot attach the Authorization
// header). The approve-and-publish button is a scroll-to-publish-card
// affordance; the actual publish happens in PublishFooter.

"use client";

import Link from "next/link";

import { Button } from "@/components/ui/button";
import { boardPackMarkdownURL, boardPackPdfURL } from "@/lib/api";
import { cn } from "@/lib/utils";

// Tailwind utility set matching the shadcn Button "outline" + "sm" variant —
// we render export links as anchors (not <Button asChild>) because the
// repo's shadcn button.tsx does not include the asChild slot prop.
const linkButtonClasses =
  "inline-flex h-9 items-center justify-center rounded-md border border-slate-300 bg-white px-3 text-sm font-medium text-slate-700 shadow-xs transition-colors hover:bg-slate-50 hover:text-slate-900 focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-slate-400";

type ExportBarProps = {
  packID: string;
  status: string;
  canApprove: boolean;
};

export function ExportBar({ packID, status, canApprove }: ExportBarProps) {
  const isPublished = status === "published";
  return (
    <div
      className={cn(
        "sticky top-0 z-20 flex items-center gap-2 border-b border-slate-200 bg-white px-4 py-2 print:hidden",
      )}
      data-testid="export-bar"
    >
      <Link
        href="/board-packs"
        className="text-sm text-slate-500 underline hover:text-slate-700"
      >
        ← All packs
      </Link>
      <div className="ml-auto flex items-center gap-2">
        <a
          href={boardPackPdfURL(packID)}
          target="_blank"
          rel="noopener"
          className={linkButtonClasses}
          data-testid="export-pdf-link"
        >
          Export PDF
        </a>
        <a
          href={boardPackMarkdownURL(packID)}
          className={linkButtonClasses}
          data-testid="export-markdown-link"
        >
          Copy Markdown
        </a>
        {!isPublished && canApprove && (
          <Button
            size="sm"
            data-testid="scroll-to-publish"
            onClick={() => {
              const el = document.getElementById("publish-footer");
              if (el) el.scrollIntoView({ behavior: "smooth" });
            }}
          >
            Approve & publish
          </Button>
        )}
      </div>
    </div>
  );
}
