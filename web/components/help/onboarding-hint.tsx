"use client";

// #2145 — the first-visit hint: one dismissible line at the top of a
// module's page, "New to <module>? See how this works", where the link
// opens the module's "How this works" panel (help-panel.tsx).
//
// Dismissals live in the user's server-side preferences
// (GET/PATCH /v1/me/preferences, `dismissed_hints`), so a dismissal holds
// on every browser the user signs in from. Settings' "Show onboarding
// hints again" clears them.
//
// Nothing renders until the preferences read answers, so a dismissed hint
// never flashes in. A failed read renders nothing: an API key or OAuth
// client has no user, and /v1/me/preferences answers 404 for it.
//
// The module name is the help module's `title` (web/help/modules/<id>.md),
// the same label the panel and its **?** button use.

import { useEffect, useState } from "react";

import { dismissHint, getMyDismissedHints } from "@/lib/api/me";
import { cn } from "@/lib/utils";

import { useHelp } from "./help-panel";

type HintState = "loading" | "show" | "hidden";

export function OnboardingHint() {
  const { mod, setOpen } = useHelp();
  const moduleId = mod?.id;
  const [state, setState] = useState<HintState>("loading");

  useEffect(() => {
    if (!moduleId) return;
    let live = true;
    getMyDismissedHints()
      .then((dismissed) => {
        if (live) setState(dismissed.includes(moduleId) ? "hidden" : "show");
      })
      .catch(() => {
        if (live) setState("hidden");
      });
    return () => {
      live = false;
    };
  }, [moduleId]);

  if (!mod || state !== "show") return null;

  function dismiss() {
    setState("hidden");
    // A failed write leaves the hint to show again on the next visit,
    // which is the safe direction.
    dismissHint(mod!.id).catch(() => {});
  }

  return (
    <div
      data-testid="onboarding-hint"
      className="flex items-center justify-between gap-3 rounded-md border border-border bg-muted/40 px-3 py-2 text-sm"
    >
      <p>
        New to {mod.title}?{" "}
        <button
          type="button"
          data-testid="onboarding-hint-open"
          onClick={() => setOpen(true)}
          className={cn(
            "rounded-sm font-medium text-primary underline underline-offset-2 hover:no-underline",
            "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring",
          )}
        >
          See how this works
        </button>
      </p>
      <button
        type="button"
        data-testid="onboarding-hint-dismiss"
        aria-label={`Dismiss the ${mod.title} hint`}
        onClick={dismiss}
        className="inline-flex size-7 shrink-0 items-center justify-center rounded-md text-base text-muted-foreground hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-3 focus-visible:ring-ring/50"
      >
        <span aria-hidden="true">×</span>
      </button>
    </div>
  );
}
