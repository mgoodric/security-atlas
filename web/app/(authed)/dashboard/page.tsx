"use client";

// Slice 040 — program dashboard view (`/dashboard`).
//
// Built per `Plans/mockups/dashboard.html`, ported via shadcn/ui
// primitives — NOT a verbatim HTML copy (anti-criterion P0-3). This is
// the solo-security-leader persona's morning home screen.
//
// Six panels, each owning its own TanStack Query so a slow or failing
// endpoint degrades only that panel — the page never blocks on a single
// API (AC-7, anti-criterion P0-2). Every panel binds to a real backend
// endpoint via a BFF proxy under `/api/dashboard/**`; the two panels
// whose endpoints do not exist on main yet (framework posture, activity
// feed) render endpoint-naming placeholders rather than fabricating
// data (anti-criterion P0-1). See the slice 040 decisions log for the
// full missing-endpoint gap inventory.
//
// Data sync: server values live ONLY in the TanStack Query cache and
// are read during render — there is NO useEffect that seeds state from
// a server value (React 19 set-state-in-effect lint, slice 063 learned
// this). The single useEffect is the 401 -> /login redirect, matching
// the `controls/[id]` precedent exactly.

import { useQuery } from "@tanstack/react-query";
import { useRouter } from "next/navigation";
import { useEffect } from "react";

import { ActivityFeedPanel } from "@/components/dashboard/activity-feed-panel";
import { EvidenceFreshnessPanel } from "@/components/dashboard/evidence-freshness-panel";
import { FrameworkPosturePanel } from "@/components/dashboard/framework-posture-panel";
import { RecentDriftPanel } from "@/components/dashboard/recent-drift-panel";
import { TopRisksPanel } from "@/components/dashboard/top-risks-panel";
import { UpcomingPanel } from "@/components/dashboard/upcoming-panel";
import {
  APIError,
  fetchDashboardDrift,
  fetchDashboardFreshness,
  fetchDashboardRisks,
  fetchDashboardUpcoming,
} from "@/lib/api";

export default function DashboardPage() {
  const router = useRouter();

  const driftQ = useQuery({
    queryKey: ["dashboard", "drift"],
    queryFn: fetchDashboardDrift,
  });
  const freshnessQ = useQuery({
    queryKey: ["dashboard", "freshness"],
    queryFn: fetchDashboardFreshness,
  });
  const risksQ = useQuery({
    queryKey: ["dashboard", "risks"],
    queryFn: fetchDashboardRisks,
  });
  const upcomingQ = useQuery({
    queryKey: ["dashboard", "upcoming"],
    queryFn: fetchDashboardUpcoming,
  });

  // 401 from any bound panel query -> the cookie expired mid-session;
  // bounce to /login. The (authed) layout guards the initial load; this
  // covers token expiry while the page is open.
  const firstError =
    driftQ.error ?? freshnessQ.error ?? risksQ.error ?? upcomingQ.error ?? null;
  useEffect(() => {
    if (firstError instanceof APIError && firstError.status === 401) {
      router.push("/login?from=/dashboard");
    }
  }, [firstError, router]);

  return (
    <div className="space-y-6" data-testid="program-dashboard">
      {/* ============ HEADER ============ */}
      <header className="flex items-baseline justify-between">
        <div>
          <h1 className="text-2xl font-semibold tracking-tight">Program</h1>
          <p className="mt-0.5 text-sm text-muted-foreground">
            The home screen for the security program — live posture, drift,
            risk, and what is coming up.
          </p>
        </div>
      </header>

      {/* ============ FRAMEWORK POSTURE TILES ============ */}
      <FrameworkPosturePanel />

      {/* ============ SECONDARY GRID: risks + freshness ============ */}
      <div className="grid grid-cols-1 gap-4 lg:grid-cols-3">
        <div className="lg:col-span-2">
          <TopRisksPanel
            risks={risksQ.data}
            state={{
              isLoading: risksQ.isLoading,
              isError: risksQ.isError,
              error: risksQ.error,
              refetch: () => void risksQ.refetch(),
            }}
          />
        </div>
        <div className="lg:col-span-1">
          <EvidenceFreshnessPanel
            report={freshnessQ.data}
            state={{
              isLoading: freshnessQ.isLoading,
              isError: freshnessQ.isError,
              error: freshnessQ.error,
              refetch: () => void freshnessQ.refetch(),
            }}
          />
        </div>
      </div>

      {/* ============ TERTIARY GRID: drift + upcoming ============ */}
      <div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
        <RecentDriftPanel
          report={driftQ.data}
          state={{
            isLoading: driftQ.isLoading,
            isError: driftQ.isError,
            error: driftQ.error,
            refetch: () => void driftQ.refetch(),
          }}
        />
        <UpcomingPanel
          report={upcomingQ.data}
          state={{
            isLoading: upcomingQ.isLoading,
            isError: upcomingQ.isError,
            error: upcomingQ.error,
            refetch: () => void upcomingQ.refetch(),
          }}
        />
      </div>

      {/* ============ ACTIVITY FEED ============ */}
      <ActivityFeedPanel />
    </div>
  );
}
