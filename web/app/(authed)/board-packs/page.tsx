"use client";

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import Link from "next/link";
import { useState } from "react";

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Skeleton } from "@/components/ui/skeleton";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { TONE_CHIP } from "@/components/board-pack/tone";
import { PageShell } from "@/components/shell/page-shell";
import { APIError } from "@/lib/api/base";
import { BoardPack, generateBoardPack, listBoardPacks } from "@/lib/api/board";

import {
  GenerateFieldErrors,
  hasErrors,
  validateGenerateForm,
} from "./validate";
import { pageTitle } from "@/lib/page-titles";

// Slice 032 — quarterly board pack list + generate.
//
// The list shows every pack for the tenant, newest report-date first. The
// generate form takes a quarter-end date and POSTs a DRAFT pack — the
// operator then reviews + approves it section-by-section on the detail
// page. Published packs are read-only (the detail page renders them frozen).

function StatusBadge({ status }: { status: string }) {
  if (status === "published") {
    return (
      <Badge variant="outline" className={TONE_CHIP.pass}>
        published
      </Badge>
    );
  }
  return (
    <Badge variant="outline" className={TONE_CHIP.warning}>
      draft
    </Badge>
  );
}

export default function BoardPacksPage() {
  const queryClient = useQueryClient();
  const [periodEnd, setPeriodEnd] = useState("");
  // Slice 665: client-side field errors for the generate-draft form. Empty
  // until the operator attempts a submit (so we don't pre-shame an empty
  // form). After a failed attempt, validation re-runs on every change so the
  // error clears as the date is filled in — the risks/new form precedent.
  const [fieldErrors, setFieldErrors] = useState<GenerateFieldErrors>({});
  const [submitAttempted, setSubmitAttempted] = useState(false);

  const packsQuery = useQuery({
    queryKey: ["board-packs"],
    queryFn: listBoardPacks,
  });

  const generate = useMutation({
    mutationFn: (date: string) => generateBoardPack(date),
    onSuccess: () => {
      setPeriodEnd("");
      setFieldErrors({});
      setSubmitAttempted(false);
      queryClient.invalidateQueries({ queryKey: ["board-packs"] });
    },
  });

  function onPeriodEndChange(value: string) {
    setPeriodEnd(value);
    // Re-validate live after the first submit attempt so the error clears
    // immediately as the operator fills in a valid date.
    if (submitAttempted) {
      setFieldErrors(validateGenerateForm({ periodEnd: value }));
    }
  }

  function onGenerateSubmit(e: React.FormEvent) {
    e.preventDefault();
    setSubmitAttempted(true);
    const errs = validateGenerateForm({ periodEnd });
    setFieldErrors(errs);
    if (hasErrors(errs)) {
      // Surface the inline field error and stop — no silent no-op, and the
      // server is never contacted with an empty date (AC-1 + AC-2).
      return;
    }
    generate.mutate(periodEnd);
  }

  // The `board.reporting` flag is checked server-side by FeatureGate in
  // ./layout.tsx (#2092); this page only mounts when the module is on.
  return (
    <PageShell
      width="reading"
      data-testid="board-packs-page"
      title={pageTitle("/board-packs")}
      subtitle="Quarterly board-meeting reports — posture, top risks, coverage trend, open findings, operational metrics, investment vs coverage, and asks of the board. Each pack is reviewed and approved section-by-section, then published as a frozen artifact."
    >
      <Card>
        <CardHeader>
          <CardTitle>Generate a quarterly pack</CardTitle>
          <CardDescription>
            Enter the quarter-end date. The generated pack is a DRAFT — the
            generated narrative is templated (no AI), and you review and approve
            each section before publishing.
          </CardDescription>
        </CardHeader>
        <CardContent>
          <form
            className="flex items-start gap-3"
            onSubmit={onGenerateSubmit}
            data-testid="board-pack-generate-form"
          >
            <div className="space-y-1">
              <label
                htmlFor="period-end"
                className="text-sm font-medium text-foreground"
              >
                Quarter end
              </label>
              <Input
                id="period-end"
                type="date"
                value={periodEnd}
                onChange={(e) => onPeriodEndChange(e.target.value)}
                className="w-48"
                aria-invalid={fieldErrors.periodEnd ? true : undefined}
                aria-describedby={
                  fieldErrors.periodEnd ? "period-end-error" : undefined
                }
                data-testid="board-pack-period-end"
              />
              {fieldErrors.periodEnd && (
                <p
                  id="period-end-error"
                  role="alert"
                  className="text-sm text-destructive"
                  data-testid="board-pack-period-end-error"
                >
                  {fieldErrors.periodEnd}
                </p>
              )}
            </div>
            <Button
              type="submit"
              disabled={generate.isPending}
              className="mt-6"
              data-testid="board-pack-generate-submit"
            >
              {generate.isPending ? "Generating…" : "Generate draft"}
            </Button>
          </form>
          {generate.isError && (
            <Alert variant="destructive" className="mt-4">
              <AlertTitle>Could not generate the pack</AlertTitle>
              <AlertDescription>
                {generate.error instanceof APIError
                  ? generate.error.message
                  : "Unexpected error."}
              </AlertDescription>
            </Alert>
          )}
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>All packs</CardTitle>
        </CardHeader>
        <CardContent>
          {packsQuery.isLoading && (
            <div className="space-y-2">
              <Skeleton className="h-10 w-full" />
              <Skeleton className="h-10 w-full" />
            </div>
          )}
          {packsQuery.isError && (
            <Alert variant="destructive">
              <AlertTitle>Could not load board packs</AlertTitle>
              <AlertDescription>
                {packsQuery.error instanceof APIError
                  ? packsQuery.error.message
                  : "Unexpected error."}
              </AlertDescription>
            </Alert>
          )}
          {packsQuery.data && packsQuery.data.length === 0 && (
            <p className="text-sm text-muted-foreground">
              No board packs yet. Generate one above.
            </p>
          )}
          {packsQuery.data && packsQuery.data.length > 0 && (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Quarter end</TableHead>
                  <TableHead>Status</TableHead>
                  <TableHead>Published by</TableHead>
                  <TableHead className="text-right">Open</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {packsQuery.data.map((pack: BoardPack) => (
                  <TableRow key={pack.id}>
                    <TableCell className="font-medium">
                      {pack.period_end}
                    </TableCell>
                    <TableCell>
                      <StatusBadge status={pack.status} />
                    </TableCell>
                    <TableCell className="text-muted-foreground">
                      {pack.published_by || "—"}
                    </TableCell>
                    <TableCell className="text-right">
                      <Link
                        href={`/board-packs/${pack.id}`}
                        className="text-sm font-medium text-foreground underline"
                      >
                        Review
                      </Link>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </CardContent>
      </Card>
    </PageShell>
  );
}
