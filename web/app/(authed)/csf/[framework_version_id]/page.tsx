"use client";

// Slice 515 frontend — NIST CSF 2.0 Tier / Profile assessment surface keyed to
// a single CSF framework_version.
//
// AC-5: the Profile editor + gap view. This page renders:
//   * the tenant's current Tier rating (1-4), or a "not yet rated" state;
//   * the Current-vs-Target gap table derived from the two profiles — one row
//     per Subcategory with the current outcome, target outcome, and a
//     per-Subcategory gap delta (target above current = a gap to close).
//
// The gap view READS the existing CSF crosswalk traversal server-side
// (invariant #1) — the page never re-stores the Subcategory↔SCF-anchor
// mapping; it deep-links each row to the requirement coverage read.

import { useQuery } from "@tanstack/react-query";
import { useParams } from "next/navigation";

import { Badge } from "@/components/ui/badge";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { Skeleton } from "@/components/ui/skeleton";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import type { CsfGapView, CsfTier } from "@/lib/api/csf";

const TIER_LABEL: Record<CsfTier, string> = {
  tier1_partial: "Tier 1 — Partial",
  tier2_risk_informed: "Tier 2 — Risk Informed",
  tier3_repeatable: "Tier 3 — Repeatable",
  tier4_adaptive: "Tier 4 — Adaptive",
};

const OUTCOME_LABEL: Record<string, string> = {
  not_targeted: "Not targeted",
  partial: "Partial",
  largely: "Largely",
  fully: "Fully",
};

async function fetchGap(fv: string): Promise<CsfGapView> {
  const res = await fetch(
    `/api/csf/gap?framework_version=${encodeURIComponent(fv)}`,
  );
  if (!res.ok) {
    throw new Error(`gap fetch failed: ${res.status}`);
  }
  return (await res.json()) as CsfGapView;
}

export default function CsfAssessmentPage() {
  const params = useParams<{ framework_version_id: string }>();
  const fv = params.framework_version_id;

  const { data, isLoading, isError } = useQuery({
    queryKey: ["csf-gap", fv],
    queryFn: () => fetchGap(fv),
    enabled: !!fv,
  });

  return (
    <div className="space-y-6" data-testid="csf-assessment">
      <div>
        <h1 className="text-2xl font-semibold">CSF 2.0 Assessment</h1>
        <p className="text-muted-foreground text-sm">
          Tier rating and Current-vs-Target profile gap for this CSF version.
        </p>
      </div>

      <Card data-testid="csf-tier-card">
        <CardHeader>
          <CardTitle>Tier rating</CardTitle>
          <CardDescription>
            How rigorous and risk-informed this program&apos;s governance is
            (Partial → Adaptive).
          </CardDescription>
        </CardHeader>
        <CardContent>
          {isLoading ? (
            <Skeleton className="h-6 w-48" />
          ) : data?.tier_rating ? (
            <Badge data-testid="csf-tier-value" variant="secondary">
              {TIER_LABEL[data.tier_rating.tier]}
            </Badge>
          ) : (
            <span
              className="text-muted-foreground text-sm"
              data-testid="csf-tier-empty"
            >
              Not yet rated.
            </span>
          )}
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Current vs Target gap</CardTitle>
          <CardDescription>
            One row per Subcategory in either profile. A positive gap means the
            target outcome is above the current outcome.
          </CardDescription>
        </CardHeader>
        <CardContent>
          {isLoading ? (
            <Skeleton className="h-32 w-full" />
          ) : isError ? (
            <p className="text-destructive text-sm" data-testid="csf-gap-error">
              Failed to load the gap view.
            </p>
          ) : !data || data.gap.length === 0 ? (
            <p
              className="text-muted-foreground text-sm"
              data-testid="csf-gap-empty"
            >
              No Subcategory selections yet. Build a Current and Target profile
              to populate the gap view.
            </p>
          ) : (
            <Table data-testid="csf-gap-table">
              <TableHeader>
                <TableRow>
                  <TableHead>Subcategory</TableHead>
                  <TableHead>Current</TableHead>
                  <TableHead>Target</TableHead>
                  <TableHead>Gap</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {data.gap.map((row) => (
                  <TableRow
                    key={row.subcategory_code}
                    data-testid={`csf-gap-row-${row.subcategory_code}`}
                  >
                    <TableCell className="font-mono text-xs">
                      <span className="font-semibold">
                        {row.subcategory_code}
                      </span>
                      <span className="text-muted-foreground ml-2">
                        {row.subcategory_title}
                      </span>
                    </TableCell>
                    <TableCell>
                      {OUTCOME_LABEL[row.current_outcome] ??
                        row.current_outcome}
                    </TableCell>
                    <TableCell>
                      {OUTCOME_LABEL[row.target_outcome] ?? row.target_outcome}
                    </TableCell>
                    <TableCell>
                      {row.met ? (
                        <Badge variant="outline">Met</Badge>
                      ) : (
                        <Badge variant="destructive">+{row.gap_delta}</Badge>
                      )}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </CardContent>
      </Card>
    </div>
  );
}
