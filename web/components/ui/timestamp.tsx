"use client";

// #2084 — renders a timestamp through the one shared formatter
// (`formatTimestamp`, `lib/relative-time.ts`). The visible text follows
// the style; the tooltip is the exact instant in UTC.
//
// The viewer's locale, timezone and clock exist only in the browser. The
// server render (and the hydrating pass) therefore formats a fixed
// absolute form in UTC, and the first client render after hydration
// swaps in the viewer's form. Rendering the viewer's form during
// hydration would mismatch the server HTML.

import { useSyncExternalStore } from "react";

import { formatTimestamp, type TimestampStyle } from "@/lib/relative-time";

const noopSubscribe = () => () => {};

function useHydrated(): boolean {
  return useSyncExternalStore(
    noopSubscribe,
    () => true,
    () => false,
  );
}

export function Timestamp({
  value,
  style = "recency",
  fallback = "—",
  className,
  testId,
}: {
  value: string | null | undefined;
  style?: TimestampStyle;
  // Text for a null, empty or unparsable value.
  fallback?: string;
  className?: string;
  testId?: string;
}) {
  const hydrated = useHydrated();
  const f = hydrated
    ? formatTimestamp(value, { style })
    : formatTimestamp(value, {
        style: style === "recency" ? "datetime" : style,
        locale: "en-GB",
        timeZone: "UTC",
      });
  if (!f.dateTime) {
    return (
      <span className={className} data-testid={testId}>
        {fallback}
      </span>
    );
  }
  return (
    <time
      dateTime={f.dateTime}
      title={f.title}
      className={className}
      data-testid={testId}
    >
      {f.text}
    </time>
  );
}
