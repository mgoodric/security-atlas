"use client";

// Slice 255 — control-detail header actions + "last evaluated" timestamp.
//
// Mockup parity target: `Plans/mockups/control.html` lines 92-102. The
// top-right header well carries:
//
//   1. A row of three action buttons (in mockup order):
//        Run query · Edit YAML · Request exception
//   2. A sub-line: "last evaluated <relative-time>" with a status icon.
//
// JUDGMENT decisions (see docs/audit-log/255-header-actions-decisions.md):
//
//   D1: Run query + Edit YAML render as DISABLED buttons (shadcn outline
//       size=sm) with title/aria-label tooltips naming the canvas section
//       and the v2 status. They are NOT `<a href="#">` (slice 178
//       dead-link anti-pattern) and they are NOT links to "coming in v2"
//       routes (slice 152 empty-state route would 2× the slice size).
//       The slice 183/184 audit pattern (`title` + `aria-label` on a
//       static element, same line of copy) is the chosen analog.
//
//   D2: Request exception renders as a LINK (shadcn outline size=sm) to
//       `/exceptions?control_id=<id>`. That route exists on main
//       (`web/app/(authed)/exceptions/page.tsx`) and its `filters.ts`
//       accepts `?control_id=…` as a URL-driven filter — so the
//       destination is real, not a placeholder. This is the closest
//       honest behavior we can ship without a new endpoint
//       (anti-criterion P0-255-4). The button label still says
//       "Request exception" — the navigation lands on the tenant-wide
//       exception register filtered to this control, where the operator
//       sees the existing exception requests for the control. The
//       "create exception" affordance itself is a v2 surface (the
//       exception-request workflow per canvas §4.6).
//
//   D3: relative-time formatter lives in `web/lib/relative-time.ts` (new
//       — see that file's preamble). The freshness-clock's `humanizeSince`
//       is intentionally separate because it produces a compact ring
//       readout ("8m"), not the operator-facing sentence form ("8
//       minutes ago") the header sub-line needs per the mockup.
//
// Data binding:
//   The "last evaluated" timestamp reads `state.last_observed_at` from
//   the most recent scope cell (AC-1) — the same source the right-rail
//   freshness clock uses, so the two readouts agree by construction.
//   Aggregation rule: most-recent across all entries (the freshest
//   signal). When state is undefined we render "—"; when state exists
//   but has no `last_observed_at` we render "never".
//
// Anti-criteria honored:
//   - P0-255-1: no Run query execution path. Disabled button + tooltip.
//   - P0-255-2: no YAML editor. Disabled button + tooltip.
//   - P0-255-3: no `<a href="#">` anywhere. Request exception links to a
//     real, existing route; Run query + Edit YAML are <button disabled>.
//   - P0-255-4: no new API endpoint. /exceptions?control_id is an
//     existing URL-driven filter on a merged page.

import Link from "next/link";
import { useEffect, useState } from "react";

import { Button, buttonVariants } from "@/components/ui/button";
import type { ControlStateResponse, ControlStateEntry } from "@/lib/api";
import { relativeTimeOrNever } from "@/lib/relative-time";
import { cn } from "@/lib/utils";

// Tooltip / aria-label copy for the two placeholder buttons. Both
// inlined here as constants so the same string drives `title`,
// `aria-label`, and the test assertion — the slice 183 / slice 184
// pattern (visible copy + tooltip + aria-label all read the same line).
const RUN_QUERY_TOOLTIP =
  "Rule-DSL execution lands in a follow-up slice (canvas §4.5 — control-as-code)";
const EDIT_YAML_TOOLTIP =
  "Control-text editor lands in a follow-up slice (canvas §4.5 — control-as-code)";

// mostRecentObservedAt picks the freshest `last_observed_at` across all
// scope-cell entries — the same reduce-to-newest rule the freshness
// clock uses (`mostRecentObserved` in
// `web/components/control/freshness-clock.tsx`). Kept inline (not
// extracted to a shared helper) because the two readers want slightly
// different return shapes — the clock wants a `Date | null`, the
// header wants the raw ISO string so it can pass through to the
// `relativeTimeOrNever` formatter.
function mostRecentObservedAt(entries: ControlStateEntry[]): string | null {
  let latestISO: string | null = null;
  let latestMs = -Infinity;
  for (const e of entries) {
    if (!e.last_observed_at) continue;
    const ms = new Date(e.last_observed_at).getTime();
    if (Number.isNaN(ms)) continue;
    if (ms > latestMs) {
      latestMs = ms;
      latestISO = e.last_observed_at;
    }
  }
  return latestISO;
}

// useNow gives the page a client-side clock that refreshes every minute
// so the "8 minutes ago" text re-renders without a page reload.
//
// React 19 set-state-in-effect lint discipline (slice 063 — recorded in
// the page-level preamble): we do NOT call setState synchronously
// inside an effect. The hook returns `null` on the server / first
// client render and a real number after the first mount tick, and the
// caller is responsible for rendering a stable placeholder when `now`
// is `null`. This avoids the SSR / client-hydration drift in a way the
// lint can verify — the server-rendered HTML carries the placeholder,
// the client hydrates to the placeholder (identical), and the FIRST
// post-mount setState (inside `setInterval` — not synchronous in the
// effect body) flips to the real clock.
//
// The trade-off: there is a one-tick window where the relative-time
// reads "—" instead of "8 minutes ago". With `intervalMs = 60_000`
// the worst case is a one-minute warmup; we accept that to keep the
// lint clean (set-state-in-effect is a known React-19 footgun the
// codebase has been bitten by before — see slice 063's note).
function useNow(intervalMs = 60_000): number | null {
  const [now, setNow] = useState<number | null>(null);
  useEffect(() => {
    // First-tick: run the interval body once via a 0ms timeout so the
    // clock seeds without a synchronous setState in the effect body.
    // The 0ms timeout is microtask-ordered after the current render
    // commits — React-19-safe.
    const seed = setTimeout(() => setNow(Date.now()), 0);
    const id = setInterval(() => setNow(Date.now()), intervalMs);
    return () => {
      clearTimeout(seed);
      clearInterval(id);
    };
  }, [intervalMs]);
  return now;
}

export interface ControlHeaderActionsProps {
  controlID: string;
  // The state response may be undefined while the query is loading or if
  // it errored. The header sub-line renders "—" in that case rather
  // than a flash of "never".
  state: ControlStateResponse | undefined;
}

export function ControlHeaderActions({
  controlID,
  state,
}: ControlHeaderActionsProps) {
  const now = useNow();

  // Resolve the freshest `last_observed_at` across cells. Three branches:
  //   1. state is undefined        → undefined  → "—"
  //   2. state exists, no entries  → null       → "never"
  //   3. state exists, has entries → ISO string → "8 minutes ago"
  const latestISO: string | null | undefined = state
    ? mostRecentObservedAt(state.states)
    : undefined;

  // Render "—" until the client clock has seeded (`now === null`).
  // After the first 0ms timeout in `useNow` fires, `now` becomes a
  // real ms value and the relative-time string renders honestly.
  const display = now === null ? "—" : relativeTimeOrNever(latestISO, now);

  return (
    <div
      className="flex flex-col items-end gap-2"
      data-testid="control-header-actions"
    >
      <div
        className="flex items-center gap-2"
        data-testid="control-header-action-buttons"
      >
        <Button
          variant="outline"
          size="sm"
          disabled
          title={RUN_QUERY_TOOLTIP}
          aria-label={RUN_QUERY_TOOLTIP}
          data-testid="control-action-run-query"
        >
          Run query
        </Button>
        <Button
          variant="outline"
          size="sm"
          disabled
          title={EDIT_YAML_TOOLTIP}
          aria-label={EDIT_YAML_TOOLTIP}
          data-testid="control-action-edit-yaml"
        >
          Edit YAML
        </Button>
        <Link
          href={`/exceptions?control_id=${encodeURIComponent(controlID)}`}
          className={cn(buttonVariants({ variant: "outline", size: "sm" }))}
          data-testid="control-action-request-exception"
        >
          Request exception
        </Link>
      </div>

      <div
        className="flex items-center gap-1.5 text-xs text-muted-foreground"
        data-testid="control-last-evaluated"
      >
        <svg
          className="h-3.5 w-3.5"
          viewBox="0 0 20 20"
          fill="currentColor"
          aria-hidden="true"
        >
          <path
            fillRule="evenodd"
            d="M10 18a8 8 0 100-16 8 8 0 000 16zm.93-12.518A1 1 0 0010 4a1 1 0 00-.93.518L5.4 11.482A1 1 0 006.291 13H8v3a1 1 0 102 0v-3h1.71a1 1 0 00.89-1.518L10.93 5.482z"
            clipRule="evenodd"
          />
        </svg>
        <span>
          last evaluated{" "}
          <span
            className="font-mono text-foreground"
            data-testid="control-last-evaluated-value"
          >
            {display}
          </span>
        </span>
      </div>
    </div>
  );
}
