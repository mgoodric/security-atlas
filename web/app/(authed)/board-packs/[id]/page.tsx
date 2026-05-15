"use client";

import { useQuery, useQueryClient } from "@tanstack/react-query";
import Link from "next/link";
import { use } from "react";

import { CoverageTrend } from "@/components/board-pack/coverage-trend";
import { ExportBar } from "@/components/board-pack/export-bar";
import {
  FindingRow,
  FindingsList,
} from "@/components/board-pack/findings-list";
import { InvestmentPanel } from "@/components/board-pack/investment-panel";
import { OperationalTiles } from "@/components/board-pack/operational-tiles";
import { PackHeader } from "@/components/board-pack/pack-header";
import {
  FrameworkPostureRow,
  PostureTiles,
} from "@/components/board-pack/posture-tiles";
import { PublishFooter } from "@/components/board-pack/publish-footer";
import { SectionCard } from "@/components/board-pack/section-card";
import {
  RiskAgingRow,
  TopRisksTable,
} from "@/components/board-pack/top-risks-table";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Skeleton } from "@/components/ui/skeleton";
import {
  APIError,
  BOARD_PACK_SECTION_KEYS,
  BoardPack,
  BoardPackSection,
  getBoardPack,
  getSessionMe,
} from "@/lib/api";

// Slice 043 — quarterly board pack preview/export view.
//
// The detail page walks the FIXED section keys in canonical order and
// composes a polished, mockup-faithful view (Plans/mockups/board-pack.html).
// For a DRAFT pack each section is editable + per-section approvable
// (role-gated by the slice-060 is_admin probe — decision D3). Publish is
// enabled only once every section is approved (slice 032 decision D6).
// A PUBLISHED pack renders read-only — every edit / approve / publish
// control is hidden (AC-7).
//
// The section-specific structured visuals (posture tiles, top-risks
// table, coverage trend, findings list, operational tiles, investment
// panel, asks list) are individual presentational components in
// `web/components/board-pack/`. The wire shapes mirror slice 032's
// pack.go Section.Data.

export default function BoardPackDetailPage({
  params,
}: {
  params: Promise<{ id: string }>;
}) {
  const { id } = use(params);
  const queryClient = useQueryClient();

  const packQuery = useQuery({
    queryKey: ["board-pack", id],
    queryFn: () => getBoardPack(id),
  });

  // Approver gate (AC-3). The platform always enforces its own publish
  // gate; this UI gate is defense-in-depth + a clearer affordance.
  const meQuery = useQuery({
    queryKey: ["session-me"],
    queryFn: getSessionMe,
    staleTime: 60_000,
  });
  const canApprove = meQuery.data?.is_admin === true;

  if (packQuery.isLoading) {
    return (
      <div className="mx-auto max-w-5xl space-y-4 p-8">
        <Skeleton className="h-10 w-72" />
        <Skeleton className="h-40 w-full" />
        <Skeleton className="h-40 w-full" />
      </div>
    );
  }
  if (packQuery.isError || !packQuery.data) {
    return (
      <div className="mx-auto max-w-5xl p-8">
        <Alert variant="destructive">
          <AlertTitle>Could not load the board pack</AlertTitle>
          <AlertDescription>
            {packQuery.error instanceof APIError
              ? packQuery.error.message
              : "Unexpected error."}
          </AlertDescription>
        </Alert>
        <Link
          href="/board-packs"
          className="mt-4 inline-block text-sm underline"
        >
          Back to board packs
        </Link>
      </div>
    );
  }

  const pack = packQuery.data;
  const isPublished = pack.status === "published";
  const { allApproved, unapprovedTitles } = approvalState(pack);

  const onMutated = (updated: BoardPack) =>
    queryClient.setQueryData(["board-pack", id], updated);

  // Approved sections count — used in the header progress badge.
  const approvedCount = BOARD_PACK_SECTION_KEYS.filter(
    (k) => pack.content.sections[k]?.approved,
  ).length;

  return (
    <div className="bg-slate-50 print:bg-white" data-testid="board-pack-view">
      <ExportBar packID={id} status={pack.status} canApprove={canApprove} />
      <main className="mx-auto max-w-5xl px-4 py-10 md:px-8">
        <PackHeader
          periodEnd={pack.period_end}
          status={pack.status}
          generatedAt={pack.content.generated_at}
          publishedBy={pack.published_by}
          approvedCount={approvedCount}
          totalSections={BOARD_PACK_SECTION_KEYS.length}
        />

        {BOARD_PACK_SECTION_KEYS.map((key, i) => {
          const section = pack.content.sections[key];
          if (!section) return null;
          return (
            <SectionCard
              key={key}
              index={i + 1}
              packID={id}
              section={section}
              isPublished={isPublished}
              canApprove={canApprove}
              onMutated={onMutated}
            >
              <SectionStructured section={section} />
            </SectionCard>
          );
        })}

        <div className="mt-8">
          <PublishFooter
            packID={id}
            isPublished={isPublished}
            allApproved={allApproved}
            canApprove={canApprove}
            publishedBy={pack.published_by}
            publishedAt={pack.published_at}
            unapprovedTitles={unapprovedTitles}
            onPublished={onMutated}
          />
        </div>

        <footer className="mt-10 space-y-1 text-xs text-slate-500">
          <div>
            Generated by security-atlas board-report engine · all metrics
            sourced from the evidence ledger and risk register.
          </div>
          <div>
            Narrative is templated only in v1 — no LLM. Publication freezes the
            pack as an immutable artifact.
          </div>
        </footer>
      </main>
    </div>
  );
}

// SectionStructured chooses the structured visual for one section based
// on its key. Wire shapes mirror slice 032 pack.go SectionData.
function SectionStructured({ section }: { section: BoardPackSection }) {
  const data = section.data ?? {};
  switch (section.key) {
    case "posture":
      return (
        <PostureTiles
          frameworks={(data.frameworks as FrameworkPostureRow[]) ?? []}
        />
      );
    case "top_risks":
      return <TopRisksTable risks={(data.top_risks as RiskAgingRow[]) ?? []} />;
    case "coverage_trend":
      return (
        <CoverageTrend
          baseline={data.baseline_coverage_pct ?? 0}
          current={data.coverage_pct ?? 0}
          delta={data.coverage_delta ?? 0}
        />
      );
    case "open_findings":
      return (
        <FindingsList
          findings={(data.findings as FindingRow[]) ?? []}
          count={data.findings_count ?? 0}
        />
      );
    case "operational_metrics":
      return (
        <OperationalTiles
          phishingPassRatePct={data.phishing_pass_rate_pct}
          p1PatchMedianDays={data.p1_patch_median_days}
          incidentCount={data.incident_count}
          vendorReviewsOnTime={data.vendor_reviews_on_time}
          vendorReviewsTotal={data.vendor_reviews_total}
        />
      );
    case "investment":
      return (
        <InvestmentPanel
          spendUSD={data.spend_usd ?? 0}
          coverageDelta={data.coverage_delta ?? 0}
          costPerCoveragePoint={data.cost_per_coverage_point ?? 0}
        />
      );
    case "asks":
      return null; // freeform — the narrative textarea IS the section
    default:
      return null;
  }
}

// approvalState computes whether every fixed section is approved (the
// publish gate) and the human-readable titles of the unapproved ones
// (for the "not ready to publish" alert).
function approvalState(pack: BoardPack): {
  allApproved: boolean;
  unapprovedTitles: string[];
} {
  const unapproved: string[] = [];
  for (const key of BOARD_PACK_SECTION_KEYS) {
    const section = pack.content.sections[key];
    if (!section) {
      unapproved.push(key);
      continue;
    }
    if (!section.approved) unapproved.push(section.title || key);
  }
  return { allApproved: unapproved.length === 0, unapprovedTitles: unapproved };
}
