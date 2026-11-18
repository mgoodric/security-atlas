"use client";

// #2135 — the full needs-attention list, the dashboard's "See all".
//
// It reads the same endpoint as the dashboard (GET /v1/needs-attention,
// #2134) a page of NEEDS_ATTENTION_PAGE_SIZE at a time, with type filters
// the platform applies (`?type=`), so paging and totals stay right. The
// For you / Everyone toggle shows only when /v1/me says the caller may
// read everyone. The URL carries the view, the types and the page, so a
// filtered list can be linked.

import { useQuery } from "@tanstack/react-query";
import { useRouter, useSearchParams } from "next/navigation";
import { Suspense, useEffect } from "react";

import { ListLoadingSkeleton } from "@/components/list/loading-skeleton";
import {
  NeedsAttentionList,
  ViewToggle,
  needsAttentionEmptyText,
} from "@/components/needs-attention/needs-attention-list";
import { PageShell } from "@/components/shell/page-shell";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { APIError } from "@/lib/api/base";
import { getMe } from "@/lib/api/me";
import {
  NEEDS_ATTENTION_PAGE_SIZE,
  NEEDS_ATTENTION_TYPES,
  fetchNeedsAttention,
  needsAttentionTypeLabel,
} from "@/lib/api/needs-attention";

import {
  type NeedsAttentionSearch,
  needsAttentionSearchString,
  parseNeedsAttentionSearch,
  toggleType,
} from "./search";
import { Term } from "@/components/help/term";

function NeedsAttentionPageInner() {
  const router = useRouter();
  const sp = useSearchParams();
  const search = parseNeedsAttentionSearch(new URLSearchParams(sp.toString()));
  const go = (next: NeedsAttentionSearch) =>
    router.replace(`/needs-attention${needsAttentionSearchString(next)}`);

  const q = useQuery({
    queryKey: [
      "needs-attention",
      search.view ?? "default",
      search.types.join(","),
      search.offset,
    ],
    queryFn: () =>
      fetchNeedsAttention({
        view: search.view,
        types: search.types,
        limit: NEEDS_ATTENTION_PAGE_SIZE,
        offset: search.offset,
      }),
  });
  const meQ = useQuery({ queryKey: ["dashboard", "me"], queryFn: getMe });

  useEffect(() => {
    if (q.error instanceof APIError && q.error.status === 401) {
      router.push("/login?from=/needs-attention");
    }
  }, [q.error, router]);

  const data = q.data;
  const view = data?.for ?? search.view ?? "me";
  const typeCounts = data?.type_counts ?? {};
  const first = data && data.total > 0 ? search.offset + 1 : 0;
  const last = data ? search.offset + data.items.length : 0;

  return (
    <PageShell
      title="Needs attention"
      subtitle={
        <>
          Failing controls, approvals waiting for you, overdue and due-soon
          reviews, and <Term id="owner">ownership</Term> gaps, most urgent
          first.
        </>
      }
      actions={
        <ViewToggle
          view={view}
          canSeeEveryone={meQ.data?.is_approver ?? false}
          onChange={(v) => go({ ...search, view: v, offset: 0 })}
        />
      }
      data-testid="needs-attention-page"
    >
      <div
        role="group"
        aria-label="Filter by type"
        className="flex flex-wrap gap-2"
        data-testid="needs-attention-type-filters"
      >
        {NEEDS_ATTENTION_TYPES.map((t) => {
          const on = search.types.includes(t);
          return (
            <button
              key={t}
              type="button"
              aria-pressed={on}
              onClick={() => go(toggleType(search, t))}
              data-testid={`needs-attention-type-filter-${t}`}
              className={`rounded-full border px-3 py-1 text-xs ${
                on
                  ? "border-primary bg-primary text-primary-foreground"
                  : "text-muted-foreground hover:text-foreground"
              }`}
            >
              {needsAttentionTypeLabel(t)}
              {data ? ` (${typeCounts[t] ?? 0})` : ""}
            </button>
          );
        })}
      </div>

      <div className="rounded-xl border bg-card px-4 py-2">
        {q.isLoading ? (
          <ListLoadingSkeleton />
        ) : q.isError ? (
          <Alert variant="destructive" data-testid="needs-attention-error">
            <AlertTitle>Could not load the list</AlertTitle>
            <AlertDescription>
              {(q.error as Error)?.message ?? "Unknown error"}
              <div className="mt-2">
                <Button
                  variant="outline"
                  size="sm"
                  onClick={() => void q.refetch()}
                  data-testid="needs-attention-retry"
                >
                  Retry
                </Button>
              </div>
            </AlertDescription>
          </Alert>
        ) : !data || data.items.length === 0 ? (
          <p
            className="py-10 text-center text-sm text-muted-foreground"
            data-testid="needs-attention-empty"
          >
            {search.types.length > 0
              ? "Nothing of these types needs attention."
              : needsAttentionEmptyText(view)}
          </p>
        ) : (
          <NeedsAttentionList items={data.items} />
        )}
      </div>

      {data && data.total > 0 ? (
        <div
          className="flex items-center justify-between text-xs text-muted-foreground"
          data-testid="needs-attention-paging"
        >
          <span data-testid="needs-attention-range">
            {first}–{last} of {data.total}
          </span>
          <div className="flex gap-2">
            <Button
              variant="outline"
              size="sm"
              disabled={search.offset === 0}
              onClick={() =>
                go({
                  ...search,
                  offset: Math.max(
                    0,
                    search.offset - NEEDS_ATTENTION_PAGE_SIZE,
                  ),
                })
              }
              data-testid="needs-attention-prev"
            >
              Previous
            </Button>
            <Button
              variant="outline"
              size="sm"
              disabled={last >= data.total}
              onClick={() =>
                go({
                  ...search,
                  offset: search.offset + NEEDS_ATTENTION_PAGE_SIZE,
                })
              }
              data-testid="needs-attention-next"
            >
              Next
            </Button>
          </div>
        </div>
      ) : null}
    </PageShell>
  );
}

export default function NeedsAttentionPage() {
  // useSearchParams must be wrapped in Suspense in App Router.
  return (
    <Suspense fallback={<ListLoadingSkeleton />}>
      <NeedsAttentionPageInner />
    </Suspense>
  );
}
