"use client";

// #2116 — `/policies/[id]/publish`: publish an approved policy.
//
// The detail page's header links here once the policy is approved. The
// form takes the effective date (today by default) and the next review
// date; the version is the one the draft was created with. The platform
// owns every refusal: the approver role (403), a policy that is not
// approved (409), and a policy with no linked control (409). Publishing a
// new version supersedes the version it revises. On success the form
// returns to the policy with a toast.

import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useParams, useRouter } from "next/navigation";
import { useState } from "react";

import { PageShell } from "@/components/shell/page-shell";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Skeleton } from "@/components/ui/skeleton";
import { useToast } from "@/components/ui/toast";
import {
  fetchPolicyDetail,
  transitionPolicy,
  type Policy,
} from "@/lib/api/policies";

import { NO_CONTROLS_WARNING, needsControlsWarning } from "../../lifecycle";

export default function PolicyPublishPage() {
  const { id } = useParams<{ id: string }>();
  const q = useQuery({
    queryKey: ["policy", id],
    queryFn: () => fetchPolicyDetail(id),
    enabled: Boolean(id),
  });
  const policy = q.data?.policy;

  let body: React.ReactNode;
  if (q.isLoading) {
    body = <Skeleton className="h-40 w-full" />;
  } else if (q.isError || !policy) {
    body = (
      <Alert variant="destructive" data-testid="policy-publish-load-error">
        <AlertTitle>Could not load policy</AlertTitle>
        <AlertDescription>
          {(q.error as Error | null)?.message ?? "Policy not found."}
        </AlertDescription>
      </Alert>
    );
  } else {
    body = <PublishForm policy={policy} />;
  }

  return (
    <PageShell
      width="reading"
      title="Publish policy"
      subtitle="Publishing makes this version the policy in force, from its effective date, and starts its acknowledgment window."
      data-testid="policy-publish"
    >
      {body}
    </PageShell>
  );
}

function today(): string {
  return new Date().toISOString().slice(0, 10);
}

function PublishForm({ policy }: { policy: Policy }) {
  const router = useRouter();
  const qc = useQueryClient();
  const { toast } = useToast();
  const [effective, setEffective] = useState(today);
  const [nextReview, setNextReview] = useState("");
  const [err, setErr] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const detailHref = `/policies/${encodeURIComponent(policy.id)}`;

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setErr(null);
    setBusy(true);
    try {
      await transitionPolicy(policy.id, "publish", {
        new_version: policy.version,
        ...(effective ? { effective_date: effective } : {}),
        ...(nextReview ? { next_review_at: nextReview } : {}),
      });
      await qc.invalidateQueries({ queryKey: ["policy", policy.id] });
      if (policy.predecessor_id) {
        void qc.invalidateQueries({
          queryKey: ["policy", policy.predecessor_id],
        });
      }
      void qc.invalidateQueries({ queryKey: ["policies"] });
      toast("Policy published");
      router.push(detailHref);
    } catch (e2) {
      setErr((e2 as Error).message);
      setBusy(false);
    }
  };

  return (
    <form
      onSubmit={submit}
      noValidate
      className="space-y-4"
      data-testid="policy-publish-form"
    >
      <p className="text-sm">
        <span className="font-medium">{policy.title}</span>{" "}
        <span
          className="font-mono text-muted-foreground"
          data-testid="policy-publish-version"
        >
          {policy.version}
        </span>
        {policy.predecessor_id ? (
          <span className="text-muted-foreground">
            {" "}
            — supersedes the published version on publish.
          </span>
        ) : null}
      </p>
      {policy.status !== "approved" ? (
        <Alert data-testid="policy-publish-not-approved">
          <AlertTitle>Not approved yet</AlertTitle>
          <AlertDescription>
            Only an approved policy can be published. This one is{" "}
            {policy.status.replace("_", " ")}.
          </AlertDescription>
        </Alert>
      ) : null}
      {needsControlsWarning(policy) ? (
        <Alert data-testid="policy-publish-no-controls-warning">
          <AlertTitle>No linked controls</AlertTitle>
          <AlertDescription>{NO_CONTROLS_WARNING}</AlertDescription>
        </Alert>
      ) : null}
      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
        <div className="space-y-1">
          <label htmlFor="policy-effective" className="text-sm font-medium">
            Effective date
          </label>
          <Input
            id="policy-effective"
            type="date"
            value={effective}
            onChange={(e) => setEffective(e.target.value)}
            data-testid="policy-publish-effective"
          />
        </div>
        <div className="space-y-1">
          <label htmlFor="policy-next-review" className="text-sm font-medium">
            Next review
          </label>
          <Input
            id="policy-next-review"
            type="date"
            value={nextReview}
            onChange={(e) => setNextReview(e.target.value)}
            data-testid="policy-publish-next-review"
          />
        </div>
      </div>
      {err ? (
        <Alert variant="destructive" data-testid="policy-publish-error">
          <AlertTitle>Could not publish policy</AlertTitle>
          <AlertDescription>{err}</AlertDescription>
        </Alert>
      ) : null}
      <Button type="submit" disabled={busy} data-testid="policy-publish-submit">
        {busy ? "Publishing…" : "Publish"}
      </Button>
    </form>
  );
}
