"use client";

// #2116 — /policies/new: create a policy draft (parent #2071).
//
// Opened from the list's "New policy", or from a published policy's
// "Create new version" with `?predecessor=<id>`: then the form starts from
// that policy with its version bumped, and the draft is created as its
// next version. The published policy is never edited; publishing the new
// draft supersedes it. On success it lands on the new draft with a toast
// (#2089).

import { useQuery, useQueryClient } from "@tanstack/react-query";
import Link from "next/link";
import { useRouter, useSearchParams } from "next/navigation";
import { Suspense } from "react";

import { PageShell } from "@/components/shell/page-shell";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { useToast } from "@/components/ui/toast";
import { createPolicy, fetchPolicyDetail } from "@/lib/api/policies";

import { createInput, EMPTY_FORM, formFromPolicy } from "../lifecycle";
import { PolicyForm } from "../policy-form";

export default function NewPolicyPage() {
  return (
    <Suspense fallback={null}>
      <NewPolicy />
    </Suspense>
  );
}

function NewPolicy() {
  const params = useSearchParams();
  const predecessor = params.get("predecessor") ?? "";
  const router = useRouter();
  const qc = useQueryClient();
  const { toast } = useToast();

  const predQ = useQuery({
    queryKey: ["policy", predecessor],
    queryFn: () => fetchPolicyDetail(predecessor),
    enabled: Boolean(predecessor),
  });
  const from = predQ.data?.policy;

  const title = predecessor ? "New policy version" : "New policy";
  const subtitle = from ? (
    <>
      The next version of{" "}
      <Link
        href={`/policies/${encodeURIComponent(from.id)}`}
        className="text-primary hover:underline"
        data-testid="policy-new-predecessor"
      >
        {from.title} {from.version}
      </Link>
      . It is a draft until it is approved and published; publishing it
      supersedes the current version.
    </>
  ) : (
    "A draft policy. Submit it for review when it is ready; an approver approves and publishes it."
  );

  let body: React.ReactNode;
  if (predecessor && predQ.isLoading) {
    body = <p className="text-sm text-muted-foreground">Loading…</p>;
  } else if (predecessor && (predQ.isError || !from)) {
    body = (
      <Alert variant="destructive" data-testid="policy-new-predecessor-error">
        <AlertTitle>Could not load the policy to revise</AlertTitle>
        <AlertDescription>
          {(predQ.error as Error | null)?.message ?? "Policy not found."}
        </AlertDescription>
      </Alert>
    );
  } else {
    body = (
      <PolicyForm
        key={from?.id ?? "new"}
        mode="create"
        initial={from ? formFromPolicy(from, "new-version") : EMPTY_FORM}
        submitLabel={from ? "Create draft version" : "Create policy"}
        submittingLabel="Creating…"
        errorTitle="Could not create policy"
        onSubmit={async (v) => {
          const created = await createPolicy(
            createInput(v, from ? from.id : undefined),
          );
          await qc.invalidateQueries({ queryKey: ["policies"] });
          toast(from ? "Draft version created" : "Policy created");
          router.push(`/policies/${encodeURIComponent(created.id)}`);
        }}
      />
    );
  }

  return (
    <PageShell
      width="reading"
      title={title}
      subtitle={subtitle}
      data-testid="policy-new"
    >
      {body}
    </PageShell>
  );
}
