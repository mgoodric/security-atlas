"use client";

// #2116 — `/policies/[id]/edit`: edit a draft policy.
//
// The detail page's header links here while the policy is a draft. Every
// editable field is sent whole to `PATCH /api/policies/{id}`; the version
// is fixed at create. The platform refuses an edit once the policy has
// left draft (409); a published policy changes through "Create new
// version". On success the form returns to the policy with a toast.

import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useParams, useRouter } from "next/navigation";

import { PageShell } from "@/components/shell/page-shell";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Skeleton } from "@/components/ui/skeleton";
import { useToast } from "@/components/ui/toast";
import { fetchPolicyDetail, updatePolicyDraft } from "@/lib/api/policies";

import { draftInput, formFromPolicy, isDraft } from "../../lifecycle";
import { PolicyForm } from "../../policy-form";

export default function PolicyEditPage() {
  const { id } = useParams<{ id: string }>();
  const router = useRouter();
  const qc = useQueryClient();
  const { toast } = useToast();
  const detailHref = `/policies/${encodeURIComponent(id)}`;

  // Shares the detail page's key, so the form opens on the policy the
  // operator was just reading.
  const q = useQuery({
    queryKey: ["policy", id],
    queryFn: () => fetchPolicyDetail(id),
    enabled: Boolean(id),
  });
  const policy = q.data?.policy;

  let body: React.ReactNode;
  if (q.isLoading) {
    body = <Skeleton className="h-64 w-full" />;
  } else if (q.isError || !policy) {
    body = (
      <Alert variant="destructive" data-testid="policy-edit-error">
        <AlertTitle>Could not load policy</AlertTitle>
        <AlertDescription>
          {(q.error as Error | null)?.message ?? "Policy not found."}
        </AlertDescription>
      </Alert>
    );
  } else if (!isDraft(policy)) {
    body = (
      <Alert data-testid="policy-edit-not-draft">
        <AlertTitle>Only a draft can be edited</AlertTitle>
        <AlertDescription>
          This policy is {policy.status.replace("_", " ")}. A published policy
          changes through a new version.
        </AlertDescription>
      </Alert>
    );
  } else {
    body = (
      <PolicyForm
        mode="edit"
        initial={formFromPolicy(policy, "edit")}
        submitLabel="Save draft"
        submittingLabel="Saving…"
        errorTitle="Could not save policy"
        onSubmit={async (v) => {
          await updatePolicyDraft(id, draftInput(v));
          await qc.invalidateQueries({ queryKey: ["policy", id] });
          void qc.invalidateQueries({ queryKey: ["policies"] });
          toast("Policy saved");
          router.push(detailHref);
        }}
      />
    );
  }

  return (
    <PageShell
      width="reading"
      title="Edit policy"
      subtitle="Change the draft's text, roles and linked controls. Its status moves through the actions on the policy's page."
      data-testid="policy-edit"
    >
      {body}
    </PageShell>
  );
}
