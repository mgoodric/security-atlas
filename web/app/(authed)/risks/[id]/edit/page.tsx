"use client";

// #2104 — `/risks/[id]/edit`: edit a risk's record fields.
//
// The risk page's shared header (DetailHeader) links here. The form is
// the create form in edit mode (RiskForm with `initial`): it sends only
// the changed record fields to `PATCH /api/risks/{id}`, and on success
// returns to the risk page with a toast. Treatment and acceptance are not
// on this form; the platform refuses them on PATCH. A 400 renders in the
// form with the user's input kept, as on create.
//
// #2311: the PATCH carries If-Match with the version the form opened on.
// When someone else saved the risk in between, the platform refuses it
// (412) and the page offers to reload; reloading reopens the form on the
// latest version (the form is keyed by updated_at), dropping these edits.

import { useQuery, useQueryClient } from "@tanstack/react-query";
import Link from "next/link";
import { notFound, useRouter } from "next/navigation";
import { use, useEffect, useState } from "react";

import { PageShell } from "@/components/shell/page-shell";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { useToast } from "@/components/ui/toast";
import { APIError } from "@/lib/api/base";
import { fetchRiskDetail, riskIfMatch, updateRisk } from "@/lib/api/risks";

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
  const [stale, setStale] = useState(false);

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
      {stale ? (
        <Alert variant="destructive" data-testid="risk-edit-stale">
          <AlertTitle>This risk changed since you opened it</AlertTitle>
          <AlertDescription>
            <p>
              Someone else saved it first, so your edits were not saved. Reload
              to see the latest version, then make your changes again.
            </p>
            <Button
              type="button"
              variant="outline"
              size="sm"
              className="mt-2"
              data-testid="risk-edit-stale-reload"
              onClick={async () => {
                await qc.refetchQueries({ queryKey: ["risk", id] });
                setStale(false);
              }}
            >
              Reload
            </Button>
          </AlertDescription>
        </Alert>
      ) : null}
      {data ? (
        <RiskForm
          key={data.risk.updated_at}
          initial={data.risk}
          onCancelHref={detailHref}
          onSave={async (body) => {
            if (Object.keys(body).length === 0) {
              toast("No changes to save");
              router.push(detailHref);
              return;
            }
            try {
              await updateRisk(id, body, { ifMatch: riskIfMatch(data.risk) });
            } catch (err) {
              if (err instanceof APIError && err.status === 412) {
                setStale(true);
                return;
              }
              throw err;
            }
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
