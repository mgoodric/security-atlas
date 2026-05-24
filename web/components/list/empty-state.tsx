"use client";

// Slice 098 — generic list-view empty state.
//
// Centered icon + title + body + primary CTA. Used by all five list-view
// slices (098/099/100/101/102) so the empty-state pattern stays consistent
// across `/controls`, `/evidence`, `/risks`, `/policies`, `/audits`.
//
// Design reference: `Plans/canvas/12-ui-fill-in-design-decisions.md` §2 —
// "centered illustration (16px-line heroicon, slate-300) + one-sentence
// cause + one-sentence next-step + one primary CTA button".
//
// This component is intentionally generic: NO controls-specific imports,
// types, or strings. The consuming page passes the icon (React node),
// title, body, and CTA props.
//
// Slice 242: `body` widened from `string` to `string | ReactNode` so the
// `/policies` empty-state can wrap its honesty-disclosure body text in a
// `<span data-testid="policies-scaffold-future">…</span>` for the slice
// 178 harness + Playwright (AC-7). String bodies still render
// unchanged — only the type widened.

import type { ReactNode } from "react";

import { Button } from "@/components/ui/button";

export type EmptyStateProps = {
  /** A heroicon-shaped SVG node (slate-300, w-12 h-12 recommended). */
  icon?: ReactNode;
  /** Single short sentence describing what the user is seeing. */
  title: string;
  /**
   * Single short sentence describing what the user can do about it.
   * Accepts either a plain string (the common case) or a ReactNode for
   * pages that need to attach a `data-testid` / honesty-disclosure
   * wrapper to the body content (slice 242 / `/policies`).
   */
  body?: ReactNode;
  /** Optional primary CTA. Click handler does whatever the page needs. */
  cta?: {
    label: string;
    onClick: () => void;
  };
};

export function EmptyState({ icon, title, body, cta }: EmptyStateProps) {
  return (
    <div
      data-testid="list-empty-state"
      className="rounded-xl border bg-card py-16 px-6 text-center"
    >
      {icon ? (
        <div className="mx-auto mb-3 text-muted-foreground">{icon}</div>
      ) : null}
      <div className="text-sm font-semibold text-foreground mb-1">{title}</div>
      {body ? (
        <div className="text-xs text-muted-foreground mb-4">{body}</div>
      ) : null}
      {cta ? (
        <Button
          size="sm"
          onClick={cta.onClick}
          data-testid="list-empty-state-cta"
        >
          {cta.label}
        </Button>
      ) : null}
    </div>
  );
}
