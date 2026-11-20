"use client";

// useHydrated is false during the server render and the hydrating pass,
// and true from the first client render after. A component that shows
// something only the browser knows (the viewer's timezone, locale or
// clock) renders a fixed form until this is true, so the hydrating pass
// matches the server HTML.

import { useSyncExternalStore } from "react";

const noopSubscribe = () => () => {};

export function useHydrated(): boolean {
  return useSyncExternalStore(
    noopSubscribe,
    () => true,
    () => false,
  );
}
