"use client";

// Slice 072 — VersionFooter.
//
// Renders the build version in low-chrome muted text at the bottom-right
// of every page. Click on the trigger expands a small build-info panel
// showing commit, build_time, and go_version.
//
// Anti-criteria honored:
//   * P0-A2 — `print:hidden` Tailwind class hides the footer on the
//     board-pack print stylesheet and any other print context.
//   * P0-A3 — the build-info panel contains read-only text only. No
//     external network links; no "check for updates" buttons.
//   * P0-A5 — single fetch per session via useVersion() (24h staleTime /
//     7d gcTime in web/lib/version.ts).
//
// The build-info panel is implemented as an aria-controlled toggle div
// (not a Popover) so we don't pull in @base-ui/react/popover purely for
// this surface. The trigger is a native <button> with `aria-expanded`
// and `aria-controls`; the panel has `role="region"` and the same
// `aria-label` as the trigger. Click-outside closes via a document-
// level mousedown listener wired in a useEffect.

import { useEffect, useRef, useState } from "react";

import { cn } from "@/lib/utils";
import { useVersion, type VersionInfo, UNKNOWN_VERSION } from "@/lib/version";

const SHORT_COMMIT_LEN = 7;

function shortCommit(commit: string): string {
  if (!commit || commit === "none" || commit === "unknown") {
    return "";
  }
  return commit.slice(0, SHORT_COMMIT_LEN);
}

function renderLabel(info: VersionInfo): string {
  const short = shortCommit(info.commit);
  if (short) {
    return `v${info.version} · ${short}`;
  }
  return `v${info.version}`;
}

export function VersionFooter() {
  const query = useVersion();
  const info: VersionInfo = query.data ?? UNKNOWN_VERSION;
  const isUnknown = !query.data;
  const [open, setOpen] = useState(false);
  const rootRef = useRef<HTMLDivElement | null>(null);

  // Close on click outside. The panel is tiny enough that an Esc-only
  // close would surprise users who click elsewhere expecting it to
  // dismiss.
  useEffect(() => {
    if (!open) return;
    function onMouseDown(e: MouseEvent) {
      if (!rootRef.current) return;
      if (!rootRef.current.contains(e.target as Node)) {
        setOpen(false);
      }
    }
    function onEscape(e: KeyboardEvent) {
      if (e.key === "Escape") {
        setOpen(false);
      }
    }
    document.addEventListener("mousedown", onMouseDown);
    document.addEventListener("keydown", onEscape);
    return () => {
      document.removeEventListener("mousedown", onMouseDown);
      document.removeEventListener("keydown", onEscape);
    };
  }, [open]);

  return (
    <div
      ref={rootRef}
      className={cn(
        "fixed bottom-2 right-3 z-40",
        // P0-A2: never render on print contexts (board-pack export,
        // browser print). The board-pack print CSS uses `print:hidden`
        // on similar utility chrome.
        "print:hidden",
      )}
    >
      {open ? (
        <div
          id="version-footer-panel"
          role="region"
          aria-label="Show build info"
          className="mb-2 w-64 rounded-md border border-border bg-popover p-3 text-xs shadow-md"
        >
          <dl className="space-y-1">
            <div className="flex justify-between gap-2">
              <dt className="text-muted-foreground">version</dt>
              <dd className="font-mono">{info.version || "unknown"}</dd>
            </div>
            <div className="flex justify-between gap-2">
              <dt className="text-muted-foreground">commit</dt>
              <dd className="font-mono">{info.commit || "unknown"}</dd>
            </div>
            <div className="flex justify-between gap-2">
              <dt className="text-muted-foreground">build_time</dt>
              <dd className="font-mono">{info.build_time || "unknown"}</dd>
            </div>
            <div className="flex justify-between gap-2">
              <dt className="text-muted-foreground">go_version</dt>
              <dd className="font-mono">{info.go_version || "unknown"}</dd>
            </div>
          </dl>
        </div>
      ) : null}
      <button
        type="button"
        aria-label="Show build info"
        aria-expanded={open}
        aria-controls="version-footer-panel"
        onClick={() => setOpen((v) => !v)}
        className={cn(
          "text-muted-foreground hover:text-foreground",
          "text-xs font-mono",
          "rounded px-1.5 py-0.5",
          "transition-colors",
          // Make the trigger focusable + keyboard-operable. Outline
          // colors picked to match shadcn defaults.
          "focus:outline-none focus-visible:ring-2 focus-visible:ring-ring",
        )}
      >
        {isUnknown && query.isFetching ? "v?" : renderLabel(info)}
      </button>
    </div>
  );
}
