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
import {
  APIError,
  BoardPack,
  generateBoardPack,
  listBoardPacks,
} from "@/lib/api";

// Slice 032 — quarterly board pack list + generate.
//
// The list shows every pack for the tenant, newest report-date first. The
// generate form takes a quarter-end date and POSTs a DRAFT pack — the
// operator then reviews + approves it section-by-section on the detail
// page. Published packs are read-only (the detail page renders them frozen).

function StatusBadge({ status }: { status: string }) {
  if (status === "published") {
    return (
      <Badge className="bg-emerald-100 text-emerald-800 hover:bg-emerald-100">
        published
      </Badge>
    );
  }
  return (
    <Badge className="bg-amber-100 text-amber-800 hover:bg-amber-100">
      draft
    </Badge>
  );
}

export default function BoardPacksPage() {
  const queryClient = useQueryClient();
  const [periodEnd, setPeriodEnd] = useState("");

  const packsQuery = useQuery({
    queryKey: ["board-packs"],
    queryFn: listBoardPacks,
  });

  const generate = useMutation({
    mutationFn: (date: string) => generateBoardPack(date),
    onSuccess: () => {
      setPeriodEnd("");
      queryClient.invalidateQueries({ queryKey: ["board-packs"] });
    },
  });

  return (
    <div className="mx-auto max-w-4xl space-y-6 p-8">
      <div>
        <h1 className="text-3xl font-semibold tracking-tight">Board packs</h1>
        <p className="mt-1 text-slate-600">
          Quarterly board-meeting reports — posture, top risks, coverage trend,
          open findings, operational metrics, investment vs coverage, and asks
          of the board. Each pack is reviewed and approved section-by-section,
          then published as a frozen artifact.
        </p>
      </div>

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
            className="flex items-end gap-3"
            onSubmit={(e) => {
              e.preventDefault();
              if (periodEnd) generate.mutate(periodEnd);
            }}
          >
            <div className="space-y-1">
              <label
                htmlFor="period-end"
                className="text-sm font-medium text-slate-700"
              >
                Quarter end
              </label>
              <Input
                id="period-end"
                type="date"
                value={periodEnd}
                onChange={(e) => setPeriodEnd(e.target.value)}
                className="w-48"
              />
            </div>
            <Button type="submit" disabled={!periodEnd || generate.isPending}>
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
            <p className="text-sm text-slate-500">
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
                    <TableCell className="text-slate-600">
                      {pack.published_by || "—"}
                    </TableCell>
                    <TableCell className="text-right">
                      <Link
                        href={`/board-packs/${pack.id}`}
                        className="text-sm font-medium text-slate-900 underline"
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
    </div>
  );
}
