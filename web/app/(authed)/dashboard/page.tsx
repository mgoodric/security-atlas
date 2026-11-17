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
// #2135 (front door, #2072): the page leads with four sections, in this
// order, each a `dashboard-section` with its `data-section` name:
//   1. needs-attention: the top 10 from GET /v1/needs-attention, For you
//      / Everyone (the toggle shows when /v1/me says is_approver), See
//      all opening /needs-attention;
//   2. posture: framework tiles plus the top-risks card (5 open,
//      non-roll-up risks by residual);
//   3. coming-up: the calendar's next 14 days;
//   4. recent-activity: the last 10 activity rows as sentences.
// The older panels (evidence freshness, drift, the portfolio summary)
// follow, unchanged. Each panel still owns its query, so one failing
// section never blanks the page.
//
// Data sync: server values live ONLY in the TanStack Query cache and
// are read during render — there is NO useEffect that seeds state from
// a server value (React 19 set-state-in-effect lint, slice 063 learned
// this). The single useEffect is the 401 -> /login redirect, matching
// the `controls/[id]` precedent exactly.

import { useQuery } from "@tanstack/react-query";
import { useRouter } from "next/navigation";
import { useEffect, useState, type ReactNode } from "react";

import { PageShell } from "@/components/shell/page-shell";
import { ActivityFeedPanel } from "@/components/dashboard/activity-feed-panel";
import {
  DashboardHeaderSubtitle,
  TenantContext,
} from "@/components/dashboard/dashboard-header-subtitle";
import { EvidenceFreshnessPanel } from "@/components/dashboard/evidence-freshness-panel";
import { FrameworkPosturePanel } from "@/components/dashboard/framework-posture-panel";
import { NeedsAttentionPanel } from "@/components/dashboard/needs-attention-panel";
import { PortfolioSummaryPanel } from "@/components/dashboard/portfolio-summary-panel";
import { ReanchoringBanner } from "@/components/dashboard/reanchoring-banner";
import { RecentDriftPanel } from "@/components/dashboard/recent-drift-panel";
import { TopRisksPanel } from "@/components/dashboard/top-risks-panel";
import { UpcomingPanel } from "@/components/dashboard/upcoming-panel";
import { APIError } from "@/lib/api/base";
import {
  fetchDashboardDrift,
  fetchDashboardFrameworkPosture,
  fetchDashboardFreshness,
  fetchDashboardRecentActivity,
  fetchDashboardRisks,
  fetchDashboardComingUp,
} from "@/lib/api/dashboard";
import { getMe } from "@/lib/api/me";
import {
  DASHBOARD_NEEDS_ATTENTION_LIMIT,
  fetchNeedsAttention,
  type NeedsAttentionView,
} from "@/lib/api/needs-attention";
import { pageTitle } from "@/lib/page-titles";

// DashboardSection names one of the page's ordered sections.
function DashboardSection({
  name,
  label,
  children,
}: {
  name: string;
  label: string;
  children: ReactNode;
}) {
  return (
    <section
      data-testid="dashboard-section"
      data-section={name}
      aria-label={label}
      className="space-y-4"
    >
      {children}
    </section>
  );
}

export default function DashboardPage() {
  const router = useRouter();

  // #2135: undefined until the user picks a view, so the first load asks
  // for the server's default (For you, or Everyone for a user who owns
  // nothing and may see it) under the key the layout prefetches.
  const [view, setView] = useState<NeedsAttentionView | undefined>(undefined);
  const needsAttentionQ = useQuery({
    queryKey: ["dashboard", "needs-attention", view ?? "default"],
    queryFn: () =>
      fetchNeedsAttention({ view, limit: DASHBOARD_NEEDS_ATTENTION_LIMIT }),
  });
  // The For you / Everyone toggle shows only for callers the platform
  // lets read for=all (is_approver is authz.IsTenantApprover).
  const meQ = useQuery({ queryKey: ["dashboard", "me"], queryFn: getMe });

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
  // #2130: "Coming up" reads the compliance calendar for the next 14 days.
  const upcomingQ = useQuery({
    queryKey: ["dashboard", "coming-up"],
    queryFn: fetchDashboardComingUp,
  });
  const postureQ = useQuery({
    queryKey: ["dashboard", "framework-posture"],
    queryFn: fetchDashboardFrameworkPosture,
  });
  const activityQ = useQuery({
    queryKey: ["dashboard", "activity"],
    queryFn: fetchDashboardRecentActivity,
  });

  // 401 from any bound panel query -> the cookie expired mid-session;
  // bounce to /login. The (authed) layout guards the initial load; this
  // covers token expiry while the page is open.
  const firstError =
    needsAttentionQ.error ??
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
    // ============ HEADER ============
    // Slice 229 — the H1 row carries the active tenant name (AC-1)
    // and the subtitle binds to the freshness pct / empty / error
    // state (AC-2, AC-3, AC-4, AC-5). The prior generic marketing
    // copy ("The home screen for the security program …") was
    // decoration that did not communicate which tenant the operator
    // was viewing nor the aggregate freshness posture; the mockup
    // (`Plans/_archive/mockups/dashboard.html` lines 117-120) encodes the
    // right design intent (operator orientation in one line).
    // Snapshot timestamp is OMITTED until the FreshnessReport wire
    // shape exposes a `received_at` — see slice header comment in
    // `dashboard-header-subtitle.tsx` for the JUDGMENT note.
    <PageShell
      title={pageTitle("/dashboard")}
      titleAdornment={<TenantContext />}
      subtitle={<DashboardHeaderSubtitle />}
      data-testid="program-dashboard"
    >
      {/* ============ SCF RE-ANCHORING WARNING (#2176) ============ */}
      <ReanchoringBanner />

      {/* ============ 1. NEEDS ATTENTION (#2135) ============ */}
      <DashboardSection name="needs-attention" label="Needs attention">
        <NeedsAttentionPanel
          report={needsAttentionQ.data}
          canSeeEveryone={meQ.data?.is_approver ?? false}
          onViewChange={setView}
          state={{
            isLoading: needsAttentionQ.isLoading,
            isError: needsAttentionQ.isError,
            error: needsAttentionQ.error,
            refetch: () => void needsAttentionQ.refetch(),
          }}
        />
      </DashboardSection>

      {/* ============ 2. POSTURE: tiles + top risks ============ */}
      <DashboardSection name="posture" label="Posture">
        <FrameworkPosturePanel
          report={postureQ.data}
          state={{
            isLoading: postureQ.isLoading,
            isError: postureQ.isError,
            error: postureQ.error,
            refetch: () => void postureQ.refetch(),
          }}
        />
        <TopRisksPanel
          risks={risksQ.data}
          state={{
            isLoading: risksQ.isLoading,
            isError: risksQ.isError,
            error: risksQ.error,
            refetch: () => void risksQ.refetch(),
          }}
        />
      </DashboardSection>

      {/* ============ 3. COMING UP: calendar, next 14 days ============ */}
      <DashboardSection name="coming-up" label="Coming up">
        <UpcomingPanel
          report={upcomingQ.data}
          state={{
            isLoading: upcomingQ.isLoading,
            isError: upcomingQ.isError,
            error: upcomingQ.error,
            refetch: () => void upcomingQ.refetch(),
          }}
        />
      </DashboardSection>

      {/* ============ 4. RECENT ACTIVITY: 10 sentences ============ */}
      <DashboardSection name="recent-activity" label="Recent activity">
        <ActivityFeedPanel
          report={activityQ.data}
          state={{
            isLoading: activityQ.isLoading,
            isError: activityQ.isError,
            error: activityQ.error,
            refetch: () => void activityQ.refetch(),
          }}
        />
      </DashboardSection>

      {/* ============ MORE: freshness + drift ============ */}
      <div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
        <div id="evidence-freshness">
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
        <RecentDriftPanel
          report={driftQ.data}
          state={{
            isLoading: driftQ.isLoading,
            isError: driftQ.isError,
            error: driftQ.error,
            refetch: () => void driftQ.refetch(),
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
    </PageShell>
  );
}
