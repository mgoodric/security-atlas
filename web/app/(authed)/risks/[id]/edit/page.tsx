"use client";

// #2104 — `/risks/[id]/edit`: edit a risk's record fields.
//
// The risk page's shared header (DetailHeader) links here. The form is
// the create form in edit mode (RiskForm with `initial`): it sends only
// the changed record fields to `PATCH /api/risks/{id}`, and on success
// returns to the risk page with a toast. Treatment and acceptance are not
// on this form; the platform refuses them on PATCH. A 400 renders in the
// form with the user's input kept, as on create.

import { useQuery, useQueryClient } from "@tanstack/react-query";
import Link from "next/link";
import { notFound, useRouter } from "next/navigation";
import { use, useEffect } from "react";

import { PageShell } from "@/components/shell/page-shell";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Skeleton } from "@/components/ui/skeleton";
import { useToast } from "@/components/ui/toast";
import { APIError } from "@/lib/api/base";
import { fetchRiskDetail, updateRisk } from "@/lib/api/risks";

import { RiskForm } from "../../new/risk-form";

export default function RiskEditPage({
  params,
}: {
  params: Promise<{ id: string }>;
}) {
  const { id } = use(params);
  const router = useRouter();
  const qc = useQueryClient();
  const { toast } = useToast();
  const detailHref = `/risks/${encodeURIComponent(id)}`;

  // Shares the detail page's ["risk", id] key, so the form opens on the
  // risk the operator was just reading.
  const { data, isLoading, error } = useQuery({
    queryKey: ["risk", id],
    queryFn: () => fetchRiskDetail(id),
    retry: (count, err) =>
      !(
        err instanceof APIError &&
        (err.status === 404 || err.status === 401)
      ) && count < 2,
  });

  useEffect(() => {
    if (error instanceof APIError && error.status === 401) {
      router.push(`/login?from=/risks/${id}/edit`);
    }
  }, [error, id, router]);

  if (error instanceof APIError && error.status === 404) {
    notFound();
  }

  return (
    <PageShell
      title="Edit risk"
      subtitle="Change the risk's record fields. Treatment and acceptance are recorded separately. Every change is kept in the risk's history."
      width="reading"
      data-testid="risk-edit"
      actions={
        <Link
          href={detailHref}
          className="text-sm text-muted-foreground hover:underline"
          data-testid="risk-edit-back"
        >
          ← Risk
        </Link>
      }
    >
      {isLoading ? <Skeleton className="h-96 w-full" /> : null}
      {error && !(error instanceof APIError && error.status === 401) ? (
        <Alert variant="destructive">
          <AlertTitle>Could not load risk</AlertTitle>
          <AlertDescription>{(error as Error).message}</AlertDescription>
        </Alert>
      ) : null}
      {data ? (
        <RiskForm
          initial={data.risk}
          onCancelHref={detailHref}
          onSave={async (body) => {
            if (Object.keys(body).length === 0) {
              toast("No changes to save");
              router.push(detailHref);
              return;
            }
            await updateRisk(id, body);
            // Refetch the detail and its history before navigating, so the
            // risk page paints the saved record, not the cached one.
            await Promise.all([
              qc.invalidateQueries({ queryKey: ["risk", id] }),
              qc.invalidateQueries({ queryKey: ["risk-audit-log", id] }),
            ]);
            void qc.invalidateQueries({ queryKey: ["risks", "list"] });
            toast("Risk updated");
            router.push(detailHref);
          }}
        />
      ) : null}
    </PageShell>
  );
}
