"use client";

// useDebounced returns `value` once it has stopped changing for `ms`.
// Shared by the entity picker's server-side search and the bulk action
// plan picker (#2618).

import { useEffect, useState } from "react";

export function useDebounced<T>(value: T, ms: number): T {
  const [settled, setSettled] = useState(value);
  useEffect(() => {
    const t = setTimeout(() => setSettled(value), ms);
    return () => clearTimeout(t);
  }, [value, ms]);
  return settled;
}
