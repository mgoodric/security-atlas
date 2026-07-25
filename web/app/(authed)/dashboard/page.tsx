"use client";

// Slice 040 — program dashboard view (`/dashboard`).
//
// Built per `Plans/_archive/mockups/dashboard.html`, ported via shadcn/ui
// primitives — NOT a verbatim HTML copy (anti-criterion P0-3). This is
// the solo-security-leader persona's morning home screen.
//
// Six panels, each owning its own TanStack Query so a slow or failing
// endpoint degrades only that panel — the page never blocks on a single
// API (AC-7, anti-criterion P0-2). Every panel binds to a real backend
// endpoint via a BFF proxy under `/api/dashboard/**`. Slice 040 shipped
// four bound panels + two `MissingEndpointPanel` placeholders (framework
// posture, activity feed); slice 066 shipped the backend endpoints;
// slice 147 closed the loop by re-pointing the placeholders; and slice
// 157 closed the loop on the remaining two slice-066 follow-on panels
// (upcoming + top-risks residual,age sort). See
// `docs/audit-log/147-dashboard-placeholders-decisions.md` +
// `docs/audit-log/157-dashboard-upcoming-and-top-risks-decisions.md`.
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
import {
  DashboardHeaderSubtitle,
  TenantContext,
} from "@/components/dashboard/dashboard-header-subtitle";
import { EvidenceFreshnessPanel } from "@/components/dashboard/evidence-freshness-panel";
import { FrameworkPosturePanel } from "@/components/dashboard/framework-posture-panel";
import { PortfolioSummaryPanel } from "@/components/dashboard/portfolio-summary-panel";
import { RecentDriftPanel } from "@/components/dashboard/recent-drift-panel";
import { TopRisksPanel } from "@/components/dashboard/top-risks-panel";
import { UpcomingPanel } from "@/components/dashboard/upcoming-panel";
import { APIError } from "@/lib/api/base";
import {
  fetchDashboardActivity,
  fetchDashboardDrift,
  fetchDashboardFrameworkPosture,
  fetchDashboardFreshness,
  fetchDashboardRisks,
  fetchDashboardUpcoming,
} from "@/lib/api/dashboard";

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
  const postureQ = useQuery({
    queryKey: ["dashboard", "framework-posture"],
    queryFn: fetchDashboardFrameworkPosture,
  });
  const activityQ = useQuery({
    queryKey: ["dashboard", "activity"],
    queryFn: fetchDashboardActivity,
  });

  // 401 from any bound panel query -> the cookie expired mid-session;
  // bounce to /login. The (authed) layout guards the initial load; this
  // covers token expiry while the page is open.
  const firstError =
    driftQ.error ??
    freshnessQ.error ??
    risksQ.error ??
    upcomingQ.error ??
    postureQ.error ??
    activityQ.error ??
    null;
  useEffect(() => {
    if (firstError instanceof APIError && firstError.status === 401) {
      router.push("/login?from=/dashboard");
    }
  }, [firstError, router]);

  return (
    <div className="space-y-6" data-testid="program-dashboard">
      {/* ============ HEADER ============ */}
      {/*
        Slice 229 — the H1 row carries the active tenant name (AC-1)
        and the subtitle binds to the freshness pct / empty / error
        state (AC-2, AC-3, AC-4, AC-5). The prior generic marketing
        copy ("The home screen for the security program …") was
        decoration that did not communicate which tenant the operator
        was viewing nor the aggregate freshness posture; the mockup
        (`Plans/_archive/mockups/dashboard.html` lines 117-120) encodes the
        right design intent (operator orientation in one line).
        Snapshot timestamp is OMITTED until the FreshnessReport wire
        shape exposes a `received_at` — see slice header comment in
        `dashboard-header-subtitle.tsx` for the JUDGMENT note.
      */}
      <header className="flex items-baseline justify-between">
        <div>
          <div className="flex items-baseline gap-3">
            <h1 className="text-2xl font-semibold tracking-tight">Program</h1>
            <TenantContext />
          </div>
          <DashboardHeaderSubtitle />
        </div>
      </header>

      {/* ============ FRAMEWORK POSTURE TILES ============ */}
      <FrameworkPosturePanel
        report={postureQ.data}
        state={{
          isLoading: postureQ.isLoading,
          isError: postureQ.isError,
          error: postureQ.error,
          refetch: () => void postureQ.refetch(),
        }}
      />

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
        <div className="lg:col-span-1" id="evidence-freshness">
          {/* Anchor target for the slice-439 staleness digest/alert deep-link
              (staleness.FreshnessViewPath = /dashboard#evidence-freshness). */}
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

      {/* ============ PORTFOLIO AI EVIDENCE SUMMARY (slice 750) ============ */}
      {/*
        Non-binding, cited, local-default AI summary of current live evidence
        across a bounded set of controls (whole program by default). The
        deterministic two-level-bounded rollup renders always; the summary
        degrades gracefully and never blocks the dashboard (AC-7). No
        approve/publish/export affordance (AC-5).
      */}
      <PortfolioSummaryPanel />

      {/* ============ ACTIVITY FEED ============ */}
      <ActivityFeedPanel
        report={activityQ.data}
        state={{
          isLoading: activityQ.isLoading,
          isError: activityQ.isError,
          error: activityQ.error,
          refetch: () => void activityQ.refetch(),
        }}
      />
    </div>
  );
}
