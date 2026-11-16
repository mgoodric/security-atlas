// #2080 — PageShell: the one page frame every authenticated route uses.
//
// Title, subtitle, an actions slot, optional tabs, and two widths:
//
//   * `full` (default) — list and table pages. Spans the main column.
//   * `reading` — forms and settings. Caps the column at 760px and
//     centres it, so a settings form does not stretch across a 1,600px
//     monitor.
//
// The body carries `min-w-0` so a wide child (the risk register table is
// ~1,822px) cannot widen the page: the table scrolls inside its own
// container (`list-table-wrap`) instead of the page scrolling sideways.
//
// The document title is NOT set here. It is route metadata (`title` in
// a layout or page, rendered through the root layout's
// "%s · security-atlas" template), because a client page cannot export
// metadata and a second <title> in the body would race Next's own.
//
// No hooks, so this renders from server and client pages alike. The **?**
// help action (#2143) and the first-visit hint (#2145) are client
// components inside a client <HelpScope>, which reads the route and holds
// the panel's open state so the hint's link can open the same panel.

import type { ReactNode } from "react";

import { HelpButton, HelpScope } from "@/components/help/help-panel";
import { OnboardingHint } from "@/components/help/onboarding-hint";
import { cn } from "@/lib/utils";

export type PageShellWidth = "full" | "reading";

export type PageShellProps = {
  title: ReactNode;
  subtitle?: ReactNode;
  /** Rendered beside the H1 on the same baseline (e.g. a status tally). */
  titleAdornment?: ReactNode;
  /** Right-aligned page actions (e.g. "New risk", "Export CSV"). */
  actions?: ReactNode;
  /** Optional tab strip rendered under the header. */
  tabs?: ReactNode;
  width?: PageShellWidth;
  className?: string;
  /** Spacing between the body's children. Defaults to `space-y-6`. */
  bodyClassName?: string;
  /** Kept so pages can preserve the testids existing specs select on. */
  "data-testid"?: string;
  children?: ReactNode;
};

export const READING_MAX_WIDTH_CLASS = "max-w-[760px]";

export function PageShell({
  title,
  subtitle,
  titleAdornment,
  actions,
  tabs,
  width = "full",
  className,
  bodyClassName = "space-y-6",
  "data-testid": testId,
  children,
}: PageShellProps) {
  return (
    <HelpScope>
      <div
        data-page-shell=""
        data-width={width}
        data-testid={testId}
        className={cn(
          "w-full min-w-0 space-y-4",
          width === "reading" && cn("mx-auto", READING_MAX_WIDTH_CLASS),
          className,
        )}
      >
        <header className="flex flex-wrap items-start justify-between gap-3">
          <div className="min-w-0">
            <div className="flex flex-wrap items-baseline gap-3">
              <h1 className="text-2xl font-semibold tracking-tight">{title}</h1>
              {titleAdornment}
            </div>
            {subtitle ? (
              <div className="mt-0.5 text-sm text-muted-foreground">
                {subtitle}
              </div>
            ) : null}
          </div>
          {/* #2213 — the actions wrap onto as many rows as they need and
            never exceed the column, so every action stays reachable on
            a 390px phone. A page that groups actions in its own flex
            row must let that row wrap too (vendors' export buttons). */}
          {/* #2143 — the **?** help action sits after the page's own
            actions. It renders nothing on a page no module's help
            covers, so the wrapper is empty there. */}
          <div className="flex max-w-full min-w-0 flex-wrap items-center gap-2">
            {actions}
            <HelpButton />
          </div>
        </header>
        {/* #2145 — "New to <module>?" on a module's page until dismissed. */}
        <OnboardingHint />
        {tabs ? <div>{tabs}</div> : null}
        <div className={cn("min-w-0", bodyClassName)}>{children}</div>
      </div>
    </HelpScope>
  );
}
