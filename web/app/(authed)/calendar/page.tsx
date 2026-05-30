"use client";

// Slice 094 — compliance calendar view (`/calendar`).
//
// Cross-business view of upcoming compliance dates. Two layouts:
//
//   - Agenda (default) — vertical list of events grouped by month
//     header. Each row: date + type icon + title + linked entity.
//   - Month grid — standard 7-col × 5/6-row calendar. Click on a date
//     opens a popover listing that day's events.
//
// Filter sidebar: event-type checkboxes (audit/exception/policy/control).
// Filter state persists in URL query string for shareable filtered views.
//
// "Subscribe in your calendar" button mints a per-user ICS URL token and
// copies it to clipboard. Calendar clients (Google / Apple / Outlook)
// subscribe to the URL and auto-refresh.
//
// Constitutional invariants honored:
// - Invariant 6 (RLS at DB layer): the BFF forwards the bearer cookie
//   to /v1/calendar; the platform enforces tenant isolation via slice
//   033 RLS. The UI does not pass tenant_id.
//
// Anti-criteria honored (P0):
// - P0-A6: NO calendar-library dependency (FullCalendar etc.). Both
//   views are hand-rolled with Tailwind. See decision D6.
// - P0-A1: ONLY 4 event types (audit/exception/policy/control).
// - P0-A5: per-event links fall back to placeholders for pages that
//   have not shipped yet.

import { useMutation, useQuery } from "@tanstack/react-query";
import { useRouter, useSearchParams } from "next/navigation";
import { Suspense, useMemo, useState } from "react";

import { AgendaView } from "@/components/calendar/agenda-view";
import { MonthGridView } from "@/components/calendar/month-grid-view";
import { TypeFilter } from "@/components/calendar/type-filter";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import {
  CalendarResponse,
  CalendarSubscriptionResponse,
  createCalendarSubscription,
  fetchCalendarEvents,
} from "@/lib/api/calendar";

const ALL_TYPES = ["audit", "exception", "policy", "control"] as const;
type AllowedType = (typeof ALL_TYPES)[number];

function isAllowedType(t: string): t is AllowedType {
  return (ALL_TYPES as readonly string[]).includes(t);
}

function CalendarPageInner() {
  const router = useRouter();
  const search = useSearchParams();

  // URL-driven filter state. Parse once per render. Default = all four.
  const selectedTypes = useMemo(() => {
    const raw = search.get("types");
    if (!raw) return [...ALL_TYPES];
    const parts = raw
      .split(",")
      .map((s) => s.trim())
      .filter((s): s is AllowedType => isAllowedType(s));
    return parts.length === 0 ? [...ALL_TYPES] : parts;
  }, [search]);

  const view = (search.get("view") ?? "agenda") as "agenda" | "month";

  // Month-grid current month state. Default = current month.
  const [monthAnchor, setMonthAnchor] = useState<Date>(() => {
    const now = new Date();
    return new Date(now.getFullYear(), now.getMonth(), 1);
  });

  // Event query — keyed on the type filter so a checkbox toggle re-fetches.
  const typesParam =
    selectedTypes.length === ALL_TYPES.length ? "" : selectedTypes.join(",");
  const eventsQ = useQuery<CalendarResponse>({
    queryKey: ["calendar", typesParam, view, monthAnchor.toISOString()],
    queryFn: () => {
      if (view === "month") {
        // Month view: fetch the full month + a 1-week pad on either side
        // so events on the first/last visible cells render.
        const from = new Date(monthAnchor);
        from.setDate(from.getDate() - 7);
        const to = new Date(monthAnchor);
        to.setMonth(to.getMonth() + 1);
        to.setDate(to.getDate() + 7);
        return fetchCalendarEvents({
          from: from.toISOString().slice(0, 10),
          to: to.toISOString().slice(0, 10),
          types: typesParam || undefined,
        });
      }
      return fetchCalendarEvents({
        types: typesParam || undefined,
      });
    },
  });

  // Subscription mutation — POST mints + returns URL; we copy to clipboard.
  const [subscribeMsg, setSubscribeMsg] = useState<string | null>(null);
  const subscribeM = useMutation<CalendarSubscriptionResponse>({
    mutationFn: createCalendarSubscription,
    onSuccess: async (data) => {
      try {
        const absolute = data.url.startsWith("http")
          ? data.url
          : `${window.location.origin}${data.url}`;
        await navigator.clipboard.writeText(absolute);
        setSubscribeMsg(
          "URL copied. Paste into Google/Outlook/Apple Calendar's `Add by URL` feature.",
        );
      } catch {
        setSubscribeMsg(`URL: ${data.url}`);
      }
      setTimeout(() => setSubscribeMsg(null), 6000);
    },
    onError: () => {
      setSubscribeMsg("Failed to create subscription URL — try again.");
      setTimeout(() => setSubscribeMsg(null), 6000);
    },
  });

  const toggleType = (t: AllowedType) => {
    const next = new Set<AllowedType>(selectedTypes);
    if (next.has(t)) next.delete(t);
    else next.add(t);
    const nextArr = Array.from(next);
    const sp = new URLSearchParams(search.toString());
    if (nextArr.length === 0 || nextArr.length === ALL_TYPES.length) {
      sp.delete("types");
    } else {
      sp.set("types", nextArr.join(","));
    }
    router.replace(`/calendar?${sp.toString()}`);
  };

  const switchView = (v: "agenda" | "month") => {
    const sp = new URLSearchParams(search.toString());
    sp.set("view", v);
    router.replace(`/calendar?${sp.toString()}`);
  };

  return (
    <div className="space-y-6">
      <div className="flex flex-wrap items-start justify-between gap-4">
        <div>
          <h1 className="text-2xl font-semibold">Compliance calendar</h1>
          <p className="text-sm text-muted-foreground">
            Upcoming audits, exception expirations, policy reviews, and periodic
            control reviews — one place for the whole team.
          </p>
        </div>
        <div className="flex flex-col items-end gap-2">
          <div className="flex gap-2">
            <Button
              variant={view === "agenda" ? "default" : "outline"}
              size="sm"
              onClick={() => switchView("agenda")}
            >
              Agenda
            </Button>
            <Button
              variant={view === "month" ? "default" : "outline"}
              size="sm"
              onClick={() => switchView("month")}
            >
              Month
            </Button>
            <Button
              variant="secondary"
              size="sm"
              onClick={() => subscribeM.mutate()}
              disabled={subscribeM.isPending}
              title="Subscribe in Google / Apple / Outlook Calendar"
            >
              {subscribeM.isPending ? "Generating…" : "Subscribe in calendar"}
            </Button>
          </div>
          {subscribeMsg && (
            <p
              role="status"
              className="text-xs text-muted-foreground max-w-xs text-right"
            >
              {subscribeMsg}
            </p>
          )}
        </div>
      </div>

      <div className="grid grid-cols-12 gap-6">
        <aside className="col-span-12 md:col-span-3">
          <TypeFilter selected={selectedTypes} onToggle={toggleType} />
        </aside>
        <section className="col-span-12 md:col-span-9">
          {eventsQ.isLoading ? (
            <div className="space-y-3">
              <Skeleton className="h-12 w-full" />
              <Skeleton className="h-12 w-full" />
              <Skeleton className="h-12 w-full" />
            </div>
          ) : eventsQ.isError ? (
            <div className="rounded-md border border-destructive/40 bg-destructive/5 p-4 text-sm">
              Failed to load calendar events. Refresh to try again.
            </div>
          ) : view === "month" ? (
            <MonthGridView
              events={eventsQ.data?.events ?? []}
              anchor={monthAnchor}
              onPrev={() =>
                setMonthAnchor(
                  new Date(
                    monthAnchor.getFullYear(),
                    monthAnchor.getMonth() - 1,
                    1,
                  ),
                )
              }
              onNext={() =>
                setMonthAnchor(
                  new Date(
                    monthAnchor.getFullYear(),
                    monthAnchor.getMonth() + 1,
                    1,
                  ),
                )
              }
            />
          ) : (
            <AgendaView
              events={eventsQ.data?.events ?? []}
              truncated={eventsQ.data?.truncated ?? false}
            />
          )}
        </section>
      </div>
    </div>
  );
}

export default function CalendarPage() {
  // useSearchParams must be wrapped in Suspense in App Router (Next 16
  // strict-mode requirement). The fallback is a one-row skeleton so the
  // page still shows something during the brief client boot.
  return (
    <Suspense fallback={<Skeleton className="h-32 w-full" />}>
      <CalendarPageInner />
    </Suspense>
  );
}
