// Slice 223 — global ⌘K search input rendered in the shared shell.
//
// Closes AC-1..AC-6 + AC-10 (search input in the authed-layout header
// + ⌘K keyboard shortcut + popover results grouped by entity type +
// keyboard navigation + 250ms debounce + 50-row hard cap) and
// supersedes spillover slice 272. Mockup reference:
// `Plans/mockups/controls.html` lines 43-47.
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
const PER_TYPE_LIMIT = 15; // headroom under slice 268's MaxLimit=50

interface SearchHit {
  id: string;
  type: "controls" | "risks" | "evidence";
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
  controls: SearchHit[];
  risks: SearchHit[];
  evidence: SearchHit[];
}

/**
 * groupByType partitions a hits array by their `type` field. Pure;
 * exported for unit coverage.
 */
export function groupByType(hits: SearchHit[]): Grouped {
  const out: Grouped = { controls: [], risks: [], evidence: [] };
  for (const h of hits) {
    if (h.type === "controls") out.controls.push(h);
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
    () => [...grouped.controls, ...grouped.risks, ...grouped.evidence],
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
  // can highlight the active row when rendering grouped.
  const flatIndexFor = (type: keyof Grouped, idx: number): number => {
    let offset = 0;
    if (type === "risks") offset = grouped.controls.length;
    else if (type === "evidence")
      offset = grouped.controls.length + grouped.risks.length;
    return offset + idx;
  };

  const showPopover = open && query.trim().length >= 2;

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
          className="pl-8 pr-12 py-1.5 w-64 text-sm bg-muted/40 border border-border rounded-md focus:outline-none focus:ring-2 focus:ring-ring focus:bg-background"
        />
        <kbd
          aria-hidden
          className="absolute right-2 top-1/2 -translate-y-1/2 font-mono text-[10px] text-muted-foreground bg-background border border-border px-1 rounded"
        >
          ⌘K
        </kbd>
      </div>
      {showPopover ? (
        <div
          role="listbox"
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
              {(["controls", "risks", "evidence"] as const).map((type) => {
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
  type: "controls" | "risks" | "evidence";
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
          return (
            <li key={hit.id}>
              <Link
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

const GROUP_LABELS: Record<"controls" | "risks" | "evidence", string> = {
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
