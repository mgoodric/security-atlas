// Slice 223 — global ⌘K search input rendered in the shared shell.
//
// Closes AC-1..AC-6 + AC-10 (search input in the authed-layout header
// + ⌘K keyboard shortcut + popover results grouped by entity type +
// keyboard navigation + 250ms debounce + 50-row hard cap) and
// supersedes spillover slice 272. Mockup reference:
// `Plans/_archive/mockups/controls.html` lines 43-47.
//
// Slice 361 — WCAG 4.1.2 Name/Role/Value combobox ARIA wiring layered
// on top of the slice 223 surface. The input is now wired as a
// `role="combobox"` (with `aria-haspopup="listbox"`, `aria-expanded`,
// `aria-controls`, `aria-activedescendant`); the popover carries a
// stable `id="global-search-listbox"`; each row carries a stable
// `id="global-search-option-{type}-{id}"`; and a visually-hidden
// `aria-live="polite"` region announces the result count whenever it
// updates. ARIA + ids only — no visual / interaction change.
//
// Path 1 (Link-keep, see docs/audit-log/361-... D1) was chosen over
// Path 2 (replace `<Link>` with `<li role="option">` + click handler):
// preserves the native cmd-click / right-click semantics of `<Link>`,
// honors P0-361-1 (no interaction shape change), and WAI-ARIA does not
// forbid `role="option"` on a focusable interactive descendant in a
// one-shot popover usage.
//
// Backend: slice 268's unified `GET /v1/search` endpoint (merged on
// main at d9d8e69b). Wire shape:
//
//   IN:  ?q=<query>&types=controls,risks,evidence&limit=N
//   OUT: { hits: [...], count: N, partial_types: [...] }
//
// The component is a single-purpose client component. Visible on
// every authed page via the shared topbar.
//
// Constitutional invariants:
//   * Invariant 6 (tenant isolation): every fetch goes through the
//     BFF `/api/search` route which forwards the bearer cookie;
//     upstream `/v1/search` runs each per-type query under the tenant
//     GUC via tenancy.ApplyTenant. No client-supplied tenant scope.
//     (P0-223-1 — no RLS bypass at this layer.)
//   * Invariant 5 (FrameworkScope intersection): inherited from the
//     upstream's per-type queries which already respect the same
//     applicability + framework_scope predicate as the detail-view
//     read path (AC-5 — no separate "search authz" surface).
//
// UX:
//   * Input always visible in the topbar (mockup shape; matches the
//     Linear / Stripe / Vercel pattern of "search is a first-class
//     affordance, not hidden behind a button").
//   * ⌘K (or Ctrl+K on non-mac) focuses the input. Esc blurs and
//     closes the popover.
//   * Typing debounces by 250ms before firing the BFF call (AC-6).
//     The upstream caps `limit` at 50; we request 15 per type so
//     three types × top-K stays well under the cap with comfortable
//     headroom.
//   * Results render in a popover below the input, grouped by entity
//     type. Each row is a `<Link>` to the entity's detail page (or
//     the established alias when no per-row detail page exists yet:
//     risks → /risks/hierarchy?focus=<id>, evidence → /evidence).
//   * Arrow keys move selection; Enter follows the active link; Esc
//     closes the popover.

"use client";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";

const DEBOUNCE_MS = 250; // AC-6
// Slice 661 added a fourth result type (`anchors`). Four types × 12 = 48
// stays under slice 268's MaxLimit=50 with headroom.
const PER_TYPE_LIMIT = 12; // headroom under slice 268's MaxLimit=50

// SearchHitType is the discriminator union. Slice 661 added `anchors` —
// the bundled SCF anchor catalog — so an empty tenant (zero instantiated
// controls) can still find controls by their SCF code (CRY-04) or name
// (encryption). See web/app/api/search/route.ts.
type SearchHitType = "anchors" | "controls" | "risks" | "evidence";

interface SearchHit {
  id: string;
  type: SearchHitType;
  title: string;
  snippet: string;
  relevance_score: number;
}

interface SearchResponse {
  hits?: SearchHit[];
  count?: number;
  partial_types?: string[];
}

interface Grouped {
  anchors: SearchHit[];
  controls: SearchHit[];
  risks: SearchHit[];
  evidence: SearchHit[];
}

/**
 * GROUP_ORDER is the canonical render + arrow-navigation order for the
 * grouped popover. Anchors render first so an empty tenant's
 * SCF-catalog matches (the slice 661 fix) are the most prominent group.
 * The flat navigation order and the JSX both consume this single source
 * of truth so they can never drift.
 */
export const GROUP_ORDER: readonly SearchHitType[] = [
  "anchors",
  "controls",
  "risks",
  "evidence",
] as const;

/**
 * groupByType partitions a hits array by their `type` field. Pure;
 * exported for unit coverage.
 */
export function groupByType(hits: SearchHit[]): Grouped {
  const out: Grouped = { anchors: [], controls: [], risks: [], evidence: [] };
  for (const h of hits) {
    if (h.type === "anchors") out.anchors.push(h);
    else if (h.type === "controls") out.controls.push(h);
    else if (h.type === "risks") out.risks.push(h);
    else if (h.type === "evidence") out.evidence.push(h);
  }
  return out;
}

/**
 * hrefForHit returns the destination URL for a result row. Controls
 * have a per-id detail page; risks and evidence do not yet so we
 * route to the established alias surfaces (matches the slice 100
 * /risks list-view convention of `?focus=<id>` deep-link into the
 * hierarchy view; evidence has no per-row detail at all, so the list
 * page is the honest destination). Exported for unit coverage.
 */
export function hrefForHit(hit: SearchHit): string {
  switch (hit.type) {
    case "anchors":
      // Slice 661 — the anchor hit id is the SCF anchor UUID; the
      // catalog detail page at /catalog/scf/[id] resolves it (the
      // route accepts the UUID, matching the catalog list view's
      // `/catalog/scf/${anchor.id}` link).
      return `/catalog/scf/${encodeURIComponent(hit.id)}`;
    case "controls":
      return `/controls/${encodeURIComponent(hit.id)}`;
    case "risks":
      return `/risks/hierarchy?focus=${encodeURIComponent(hit.id)}`;
    case "evidence":
      return `/evidence`;
  }
}

/**
 * isMacShortcutTrigger returns true for ⌘K (mac) or Ctrl+K (non-mac).
 * `navigator.platform` reads as deprecated in newer browsers; the
 * `metaKey || ctrlKey` shape is the practical contract every web app
 * implementing ⌘K uses. Exported for unit coverage.
 */
export function isShortcutTrigger(e: {
  key: string;
  metaKey: boolean;
  ctrlKey: boolean;
}): boolean {
  return e.key.toLowerCase() === "k" && (e.metaKey || e.ctrlKey);
}

/**
 * LISTBOX_ID is the stable id the input's `aria-controls` resolves to
 * and the popover's `id` attribute. Constant because there is only one
 * global search in the shell at any time. Exported for spec coverage.
 */
export const LISTBOX_ID = "global-search-listbox";

/**
 * optionIdFor returns the stable DOM id for a result row, used both as
 * each option's `id` and as the input's `aria-activedescendant` when
 * that row is the active highlight. The id encodes the row type so the
 * three groups can never collide on the same upstream id. Exported for
 * unit coverage.
 *
 * Slice 361 (WCAG 4.1.2): the combobox-listbox pattern requires every
 * option to carry a stable id so `aria-activedescendant` on the input
 * can name the currently-highlighted row programmatically.
 */
export function optionIdFor(type: SearchHitType, id: string): string {
  return `global-search-option-${type}-${id}`;
}

/**
 * resultCountAnnouncement returns the text a screen reader will hear
 * when results update. Used inside the visually-hidden
 * `aria-live="polite"` region. Singular vs plural matters for SR voice
 * naturalness. Exported for unit coverage.
 *
 * The "No results" branch is only emitted when the user has actually
 * typed enough to trigger a search (the popover is open); the caller
 * is responsible for not rendering the region when the popover is
 * closed, otherwise an SR would hear "No results" on every page load.
 */
export function resultCountAnnouncement(count: number): string {
  if (count === 0) return "No results";
  if (count === 1) return "1 result";
  return `${count} results`;
}

export function GlobalSearch() {
  const router = useRouter();
  const [query, setQuery] = useState("");
  const [results, setResults] = useState<SearchHit[]>([]);
  const [partialTypes, setPartialTypes] = useState<string[]>([]);
  const [activeIndex, setActiveIndex] = useState(0);
  const [open, setOpen] = useState(false);
  const [loading, setLoading] = useState(false);
  const inputRef = useRef<HTMLInputElement | null>(null);
  const containerRef = useRef<HTMLDivElement | null>(null);

  // ⌘K / Ctrl+K global focus shortcut.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (isShortcutTrigger(e)) {
        e.preventDefault();
        inputRef.current?.focus();
        inputRef.current?.select();
        setOpen(true);
      }
    };
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("keydown", onKey);
    };
  }, []);

  // Close on outside click.
  useEffect(() => {
    const onDown = (e: MouseEvent) => {
      if (!containerRef.current) return;
      if (!containerRef.current.contains(e.target as Node)) setOpen(false);
    };
    document.addEventListener("mousedown", onDown);
    return () => {
      document.removeEventListener("mousedown", onDown);
    };
  }, []);

  // Debounced fetch. The DEBOUNCE_MS window is the AC-6 contract.
  //
  // The effect body itself does NOT call setState directly — every
  // state update happens inside either a setTimeout or async fetch
  // callback (both are external subscriptions per the
  // react-hooks/set-state-in-effect rule). The "below-min-length"
  // branch sets state via a synchronous setTimeout(0) so the same
  // discipline applies uniformly. Mirrors the slice 192
  // TenantSwitcher's queueMicrotask pattern.
  useEffect(() => {
    let cancelled = false;
    const trimmed = query.trim();

    if (trimmed.length < 2) {
      const microTimer = setTimeout(() => {
        if (cancelled) return;
        setResults([]);
        setPartialTypes([]);
        setLoading(false);
      }, 0);
      return () => {
        cancelled = true;
        clearTimeout(microTimer);
      };
    }

    const showLoadingTimer = setTimeout(() => {
      if (cancelled) return;
      setLoading(true);
    }, 0);

    const fetchTimer = setTimeout(async () => {
      try {
        const resp = await fetch(
          `/api/search?q=${encodeURIComponent(trimmed)}&limit=${
            PER_TYPE_LIMIT * 3
          }`,
          {
            cache: "no-store",
            credentials: "include",
          },
        );
        if (cancelled) return;
        if (!resp.ok) {
          setResults([]);
          setPartialTypes([]);
          setLoading(false);
          return;
        }
        const data = (await resp.json()) as SearchResponse;
        if (cancelled) return;
        setResults(Array.isArray(data?.hits) ? data.hits : []);
        setPartialTypes(
          Array.isArray(data?.partial_types) ? data.partial_types : [],
        );
        setActiveIndex(0);
        setLoading(false);
      } catch {
        if (cancelled) return;
        setResults([]);
        setPartialTypes([]);
        setLoading(false);
      }
    }, DEBOUNCE_MS);

    return () => {
      cancelled = true;
      clearTimeout(showLoadingTimer);
      clearTimeout(fetchTimer);
    };
  }, [query]);

  const grouped = useMemo(() => groupByType(results), [results]);

  // Flattened list in render order — the same order the UI lists,
  // used for arrow-key navigation. Stays in lockstep with the JSX
  // below because the same grouping is consumed in both places.
  const flatHits = useMemo(
    () => GROUP_ORDER.flatMap((type) => grouped[type]),
    [grouped],
  );

  const onKeyDown = useCallback(
    (e: React.KeyboardEvent<HTMLInputElement>) => {
      if (e.key === "Escape") {
        setOpen(false);
        inputRef.current?.blur();
        return;
      }
      if (e.key === "ArrowDown") {
        e.preventDefault();
        if (flatHits.length === 0) return;
        setActiveIndex((i) => Math.min(i + 1, flatHits.length - 1));
        return;
      }
      if (e.key === "ArrowUp") {
        e.preventDefault();
        if (flatHits.length === 0) return;
        setActiveIndex((i) => Math.max(i - 1, 0));
        return;
      }
      if (e.key === "Enter") {
        if (flatHits.length === 0) return;
        e.preventDefault();
        const hit = flatHits[activeIndex] ?? flatHits[0];
        if (!hit) return;
        const href = hrefForHit(hit);
        setOpen(false);
        setQuery("");
        router.push(href);
      }
    },
    [activeIndex, flatHits, router],
  );

  // Stable flat index for each (type, idx-within-type) tuple so we
  // can highlight the active row when rendering grouped. Computed by
  // summing the sizes of every group that precedes `type` in
  // GROUP_ORDER — the same order flatHits flattens, so the two can
  // never drift (slice 661 added a fourth group).
  const flatIndexFor = (type: keyof Grouped, idx: number): number => {
    let offset = 0;
    for (const t of GROUP_ORDER) {
      if (t === type) break;
      offset += grouped[t].length;
    }
    return offset + idx;
  };

  const showPopover = open && query.trim().length >= 2;

  // Slice 361 — derive the active option's stable DOM id for
  // `aria-activedescendant`. Empty string when no row is active so the
  // input renders the attribute as absent (React drops empty string for
  // aria-activedescendant). Computed unconditionally so the input
  // attribute stays stable across renders.
  const activeHit = flatHits[activeIndex];
  const activeOptionId =
    showPopover && activeHit ? optionIdFor(activeHit.type, activeHit.id) : "";

  return (
    <div ref={containerRef} data-testid="global-search" className="relative">
      <div className="relative">
        <SearchIcon />
        <input
          ref={inputRef}
          type="text"
          placeholder="Search controls, evidence, risks…"
          value={query}
          onChange={(e) => {
            setQuery(e.target.value);
            setOpen(true);
          }}
          onFocus={() => setOpen(true)}
          onKeyDown={onKeyDown}
          data-testid="global-search-input"
          aria-label="Global search"
          role="combobox"
          aria-haspopup="listbox"
          aria-expanded={showPopover}
          aria-controls={LISTBOX_ID}
          aria-activedescendant={activeOptionId || undefined}
          aria-autocomplete="list"
          className="pl-8 pr-12 py-1.5 w-64 text-sm bg-muted/40 border border-border rounded-md focus:outline-none focus:ring-2 focus:ring-ring focus:bg-background"
        />
        <kbd
          aria-hidden
          className="absolute right-2 top-1/2 -translate-y-1/2 font-mono text-[10px] text-muted-foreground bg-background border border-border px-1 rounded"
        >
          ⌘K
        </kbd>
      </div>
      {/*
        Slice 361 — visually-hidden aria-live=polite region announcing
        the result count whenever it updates. Only mounted when the
        popover is open + not in the loading flash; otherwise an SR
        would hear "No results" on every page load. The region is
        OUTSIDE the popover so SR users hear the count without having
        to focus into the listbox. WCAG 4.1.3 Status Messages.
      */}
      {showPopover && !loading ? (
        <div
          role="status"
          aria-live="polite"
          aria-atomic="true"
          data-testid="global-search-live-region"
          className="sr-only"
        >
          {resultCountAnnouncement(flatHits.length)}
        </div>
      ) : null}
      {showPopover ? (
        <div
          id={LISTBOX_ID}
          role="listbox"
          aria-label="Search results"
          data-testid="global-search-popover"
          className="absolute right-0 top-full mt-1 w-96 max-h-96 overflow-y-auto rounded-md border border-border bg-popover text-popover-foreground shadow-lg z-50"
        >
          {loading ? (
            <div className="px-3 py-2 text-xs text-muted-foreground">
              Searching…
            </div>
          ) : flatHits.length === 0 ? (
            <div className="px-3 py-2 text-xs text-muted-foreground">
              No matches
            </div>
          ) : (
            <>
              {GROUP_ORDER.map((type) => {
                const rows = grouped[type];
                if (rows.length === 0) return null;
                return (
                  <ResultGroup
                    key={type}
                    type={type}
                    rows={rows}
                    activeIndex={activeIndex}
                    flatIndexFor={(idx) => flatIndexFor(type, idx)}
                    onRowClick={() => {
                      setOpen(false);
                      setQuery("");
                    }}
                  />
                );
              })}
              {partialTypes.length > 0 ? (
                <div
                  data-testid="global-search-partial"
                  className="px-3 py-2 text-[10px] text-muted-foreground border-t border-border"
                >
                  Some result types are hidden by your role:{" "}
                  {partialTypes.join(", ")}
                </div>
              ) : null}
            </>
          )}
        </div>
      ) : null}
    </div>
  );
}

interface ResultGroupProps {
  type: SearchHitType;
  rows: SearchHit[];
  activeIndex: number;
  flatIndexFor: (idx: number) => number;
  onRowClick: () => void;
}

function ResultGroup({
  type,
  rows,
  activeIndex,
  flatIndexFor,
  onRowClick,
}: ResultGroupProps) {
  return (
    <div
      data-testid={`global-search-group-${type}`}
      className="border-b border-border last:border-b-0"
    >
      <div className="px-3 pt-2 pb-1 text-[10px] uppercase tracking-wide text-muted-foreground">
        {GROUP_LABELS[type]}
      </div>
      <ul className="pb-1">
        {rows.map((hit, idx) => {
          const flatIdx = flatIndexFor(idx);
          const isActive = flatIdx === activeIndex;
          // Slice 361 — stable id so the input's
          // `aria-activedescendant` can name this row when active.
          const optionId = optionIdFor(type, hit.id);
          return (
            <li key={hit.id}>
              <Link
                id={optionId}
                href={hrefForHit(hit)}
                onClick={onRowClick}
                data-testid={`global-search-row-${type}`}
                role="option"
                aria-selected={isActive}
                className={`block px-3 py-1.5 text-xs ${
                  isActive
                    ? "bg-accent text-accent-foreground"
                    : "hover:bg-accent/50"
                }`}
              >
                <div className="font-medium truncate">{hit.title}</div>
                {hit.snippet && hit.snippet !== hit.title ? (
                  <div className="text-[10px] text-muted-foreground truncate">
                    {hit.snippet}
                  </div>
                ) : null}
              </Link>
            </li>
          );
        })}
      </ul>
    </div>
  );
}

const GROUP_LABELS: Record<SearchHitType, string> = {
  anchors: "SCF anchors",
  controls: "Controls",
  risks: "Risks",
  evidence: "Evidence",
};

function SearchIcon() {
  return (
    <svg
      className="absolute left-2.5 top-1/2 -translate-y-1/2 w-3.5 h-3.5 text-muted-foreground"
      viewBox="0 0 20 20"
      fill="currentColor"
      aria-hidden
    >
      <path
        fillRule="evenodd"
        d="M8 4a4 4 0 100 8 4 4 0 000-8zM2 8a6 6 0 1110.89 3.476l4.817 4.817a1 1 0 01-1.414 1.414l-4.816-4.816A6 6 0 012 8z"
        clipRule="evenodd"
      />
    </svg>
  );
}
