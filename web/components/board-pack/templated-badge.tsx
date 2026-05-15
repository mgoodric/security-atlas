// Slice 043 — templated-narrative provenance badge.
//
// The mockup (Plans/mockups/board-pack.html) ships a violet "AI-drafted ·
// llama3.1-8b · approved" badge depicting a v3+ future state where the
// platform optionally polishes the templated narrative with a local LLM.
// v1 is templated-only — slice 032's pack_narrative.go imports no
// inference client. Labeling content "AI-drafted" when no model ran
// would itself violate the AI-assist boundary in CLAUDE.md (the boundary
// forbids fabricating AI provenance just as it forbids fabricating
// coverage). The badge is therefore "Templated v1".
//
// Decision D1 of slice 043.

import { cn } from "@/lib/utils";

export function TemplatedBadge({ className }: { className?: string }) {
  return (
    <span
      className={cn(
        "inline-flex items-center gap-1.5 rounded bg-slate-100 px-2 py-0.5 font-mono text-[10px] font-semibold uppercase tracking-wider text-slate-700",
        className,
      )}
      data-testid="templated-badge"
    >
      <svg
        className="h-3 w-3"
        viewBox="0 0 20 20"
        fill="currentColor"
        aria-hidden
      >
        <path
          fillRule="evenodd"
          d="M4 4a2 2 0 012-2h8a2 2 0 012 2v12a2 2 0 01-2 2H6a2 2 0 01-2-2V4zm3 1h6v2H7V5zm0 4h6v2H7V9zm0 4h4v2H7v-2z"
          clipRule="evenodd"
        />
      </svg>
      Templated v1
    </span>
  );
}

// HumanAuthoredBadge marks the asks-of-the-board section, which has no
// templated or AI provenance — the operator types it freeform.
export function HumanAuthoredBadge({ className }: { className?: string }) {
  return (
    <span
      className={cn(
        "inline-flex items-center gap-1.5 rounded bg-slate-100 px-2 py-0.5 font-mono text-[10px] font-semibold uppercase tracking-wider text-slate-600",
        className,
      )}
      data-testid="human-authored-badge"
    >
      human-authored only
    </span>
  );
}
