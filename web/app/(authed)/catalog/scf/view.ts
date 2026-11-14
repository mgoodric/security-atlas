// Issue #2066 — pure helpers for the SCF catalog page. The page walks the
// whole catalog (`fetchControlsCatalog`), then searches and pages it in
// the browser, the same way /controls does.

import type { AnchorWithState } from "@/lib/api/anchors";

/** Rows per page of the catalog table. */
export const CATALOG_PAGE_SIZE = 100;

/**
 * Anchors whose SCF code, title or family contain every word of `query`,
 * ignoring case. A blank query keeps every anchor.
 */
export function searchAnchors<T extends AnchorWithState>(
  anchors: readonly T[],
  query: string,
): T[] {
  const words = query.toLowerCase().split(/\s+/).filter(Boolean);
  if (words.length === 0) return [...anchors];
  return anchors.filter((a) => {
    const hay = `${a.scf_id} ${a.name} ${a.family}`.toLowerCase();
    return words.every((w) => hay.includes(w));
  });
}

/** True when at least one of the tenant's controls anchors to `a`. */
export function anchorHasControl(a: AnchorWithState): boolean {
  return (a.control_count ?? 0) > 0 || Boolean(a.control_id);
}

/**
 * The card title: the catalog's real size, from the server's `total`
 * when it sent one and the rows loaded otherwise.
 */
export function catalogCountLabel(loaded: number, total?: number): string {
  const count = total ?? loaded;
  return `${count.toLocaleString("en-US")} ${
    count === 1 ? "anchor" : "anchors"
  }`;
}
