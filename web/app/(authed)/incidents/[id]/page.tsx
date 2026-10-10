"use client";

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import Link from "next/link";
import { notFound, useRouter } from "next/navigation";
import { FormEvent, use, useEffect, useState } from "react";

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button, buttonVariants } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Skeleton } from "@/components/ui/skeleton";
import { APIError } from "@/lib/api/base";
import {
  closeIncident,
  fetchIncidentDetail,
  linkIDs,
  transitionIncident,
  type IncidentDetail,
  type IncidentStatus,
} from "@/lib/api/incidents";

import {
  affectedSystemName,
  affectedSystemsList,
  chronologicalTimeline,
  dateTimeLabel,
  nextIncidentAction,
  SEVERITY_LABELS,
  STATUS_LABELS,
} from "../display";

export default function IncidentDetailPage({
  params,
}: {
  params: Promise<{ id: string }>;
}) {
  const { id } = use(params);
  const router = useRouter();
  const queryClient = useQueryClient();
  const [summary, setSummary] = useState("");
  const [postmortem, setPostmortem] = useState("");

  const { data, isLoading, error } = useQuery({
    queryKey: ["incidents", "detail", id],
    queryFn: () => fetchIncidentDetail(id),
    retry: (count, err) =>
      !(
        err instanceof APIError &&
        (err.status === 401 || err.status === 404)
      ) && count < 2,
  });

  useEffect(() => {
    if (error instanceof APIError && error.status === 401) {
      router.push(`/login?from=/incidents/${id}`);
    }
  }, [error, id, router]);

  const invalidate = async () => {
    await queryClient.invalidateQueries({ queryKey: ["incidents"] });
  };

  const transitionM = useMutation({
    mutationFn: (toState: Exclude<IncidentStatus, "detected">) =>
      transitionIncident(id, { to_state: toState, summary }),
    onSuccess: async () => {
      setSummary("");
      await invalidate();
    },
  });

  const closeM = useMutation({
    mutationFn: () =>
      closeIncident(id, {
        postmortem_summary: postmortem,
        observed_at: new Date().toISOString(),
      }),
    onSuccess: async () => {
      setPostmortem("");
      await invalidate();
    },
  });

  if (error instanceof APIError && error.status === 404) {
    notFound();
  }

  if (isLoading) {
    return (
      <div className="space-y-6" data-testid="incident-detail-loading">
        <Skeleton className="h-10 w-2/3" />
        <Skeleton className="h-28 w-full" />
        <Skeleton className="h-72 w-full" />
      </div>
    );
  }

  if (error && !(error instanceof APIError && error.status === 401)) {
    return (
      <div className="space-y-6">
        <BackLink />
        <Alert variant="destructive" data-testid="incident-detail-error">
          <AlertTitle>Could not load incident</AlertTitle>
          <AlertDescription>{(error as Error).message}</AlertDescription>
        </Alert>
      </div>
    );
  }

  const detail = data?.incident;
  if (!detail) return null;

  const incident = detail.record;
  const next = nextIncidentAction(incident.status);
  const systems = affectedSystemsList(incident.affected_systems);
  const timeline = chronologicalTimeline(detail.timeline);

  return (
    <div className="space-y-6" data-testid="incident-detail">
      <BackLink />
      <header className="flex flex-wrap items-start justify-between gap-4">
        <div className="space-y-2">
          <div className="flex flex-wrap items-center gap-2">
            <Badge variant="progress" data-testid="incident-detail-status">
              {STATUS_LABELS[incident.status]}
            </Badge>
            <Badge variant="warning" data-testid="incident-detail-severity">
              {SEVERITY_LABELS[incident.severity]}
            </Badge>
            {incident.affected_system_tier ? (
              <Badge variant="outline">
                {incident.affected_system_tier} system tier
              </Badge>
            ) : null}
          </div>
          <h1 className="text-2xl font-semibold tracking-tight">
            {incident.title}
          </h1>
          <p className="font-mono text-xs text-muted-foreground">
            {incident.id}
          </p>
        </div>
        {next ? (
          <LifecycleAction
            next={next}
            summary={summary}
            setSummary={setSummary}
            postmortem={postmortem}
            setPostmortem={setPostmortem}
            onTransition={() =>
              next.kind === "transition" && transitionM.mutate(next.toState)
            }
            onClose={() => closeM.mutate()}
            pending={transitionM.isPending || closeM.isPending}
            error={transitionM.error ?? closeM.error}
          />
        ) : null}
      </header>

      <div className="grid gap-4 lg:grid-cols-[1.1fr_0.9fr]">
        <Card data-testid="incident-detail-core">
          <CardHeader className="border-b">
            <CardTitle>Core fields</CardTitle>
          </CardHeader>
          <CardContent>
            <dl className="grid gap-x-8 gap-y-4 sm:grid-cols-2">
              <Field label="Detected" testid="incident-detail-detected-at">
                {dateTimeLabel(incident.detected_at)}
              </Field>
              <Field label="Detected by" testid="incident-detail-detected-by">
                {incident.detected_by}
              </Field>
              <Field label="Operator severity">
                {SEVERITY_LABELS[incident.operator_severity]}
              </Field>
              <Field label="Effective severity">
                {SEVERITY_LABELS[incident.severity]}
              </Field>
              <Field label="Closed" testid="incident-detail-closed-at">
                {dateTimeLabel(incident.closed_at)}
              </Field>
              <Field label="Closed by">{incident.closed_by ?? "-"}</Field>
            </dl>
            {incident.description.trim() ? (
              <p className="mt-4 whitespace-pre-wrap text-sm">
                {incident.description}
              </p>
            ) : null}
            {incident.postmortem_summary ? (
              <div className="mt-4 border-t pt-4">
                <div className="text-xs uppercase text-muted-foreground">
                  Postmortem
                </div>
                <p
                  className="mt-1 whitespace-pre-wrap text-sm"
                  data-testid="incident-detail-postmortem"
                >
                  {incident.postmortem_summary}
                </p>
              </div>
            ) : null}
          </CardContent>
        </Card>

        <Card data-testid="incident-detail-affected-systems">
          <CardHeader className="border-b">
            <CardTitle>Affected systems</CardTitle>
          </CardHeader>
          <CardContent>
            {systems.length === 0 ? (
              <p className="text-sm text-muted-foreground">
                No affected systems recorded.
              </p>
            ) : (
              <ul className="divide-y rounded-lg border">
                {systems.map((system, index) => (
                  <li key={index} className="p-3 text-sm">
                    <div className="font-medium">
                      {affectedSystemName(system)}
                    </div>
                    <div className="mt-1 flex flex-wrap gap-x-4 gap-y-1 text-xs text-muted-foreground">
                      {system.tier ? (
                        <span>tier {String(system.tier)}</span>
                      ) : null}
                      {system.environment ? (
                        <span>{String(system.environment)}</span>
                      ) : null}
                    </div>
                  </li>
                ))}
              </ul>
            )}
          </CardContent>
        </Card>
      </div>

      <LinkedEntities detail={detail} />

      <Card data-testid="incident-detail-timeline">
        <CardHeader className="border-b">
          <CardTitle>Timeline</CardTitle>
        </CardHeader>
        <CardContent>
          <ol className="space-y-3">
            {timeline.map((entry) => (
              <li
                key={entry.id}
                className="grid gap-2 border-b pb-3 last:border-0 last:pb-0 sm:grid-cols-[10rem_1fr]"
                data-testid="incident-timeline-entry"
              >
                <time className="font-mono text-xs text-muted-foreground">
                  {dateTimeLabel(entry.occurred_at)}
                </time>
                <div>
                  <div className="flex flex-wrap items-center gap-2">
                    <Badge variant="outline">{entry.action}</Badge>
                    <span className="text-sm">
                      {entry.from_state ? `${entry.from_state} -> ` : ""}
                      {entry.to_state}
                    </span>
                  </div>
                  <p className="mt-1 text-sm">{entry.summary}</p>
                  <p className="mt-1 font-mono text-xs text-muted-foreground">
                    {entry.actor}
                  </p>
                </div>
              </li>
            ))}
          </ol>
        </CardContent>
      </Card>
    </div>
  );
}

function LifecycleAction({
  next,
  summary,
  setSummary,
  postmortem,
  setPostmortem,
  onTransition,
  onClose,
  pending,
  error,
}: {
  next: NonNullable<ReturnType<typeof nextIncidentAction>>;
  summary: string;
  setSummary: (value: string) => void;
  postmortem: string;
  setPostmortem: (value: string) => void;
  onTransition: () => void;
  onClose: () => void;
  pending: boolean;
  error: unknown;
}) {
  const submit = (event: FormEvent) => {
    event.preventDefault();
    if (next.kind === "close") {
      onClose();
    } else {
      onTransition();
    }
  };
  return (
    <form className="w-full max-w-md space-y-2" onSubmit={submit}>
      {next.kind === "close" ? (
        <textarea
          className="min-h-24 w-full rounded-lg border bg-transparent px-3 py-2 text-sm outline-none focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50"
          value={postmortem}
          onChange={(e) => setPostmortem(e.target.value)}
          placeholder="Postmortem summary"
          aria-label="Postmortem summary"
          data-testid="incident-close-postmortem"
          required
        />
      ) : (
        <textarea
          className="min-h-16 w-full rounded-lg border bg-transparent px-3 py-2 text-sm outline-none focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50"
          value={summary}
          onChange={(e) => setSummary(e.target.value)}
          placeholder={`Summary for ${next.toState}`}
          aria-label="Transition summary"
          data-testid="incident-transition-summary"
        />
      )}
      <div className="flex justify-end">
        <Button
          type="submit"
          disabled={pending}
          data-testid={
            next.kind === "close"
              ? "incident-close-submit"
              : "incident-transition-submit"
          }
        >
          {next.kind === "close"
            ? "Close incident"
            : `Mark ${STATUS_LABELS[next.toState]}`}
        </Button>
      </div>
      {error ? (
        <p
          className="text-xs text-destructive"
          data-testid="incident-action-error"
        >
          {(error as Error).message}
        </p>
      ) : null}
    </form>
  );
}

function LinkedEntities({ detail }: { detail: IncidentDetail }) {
  return (
    <div
      className="grid gap-4 md:grid-cols-2 xl:grid-cols-4"
      data-testid="incident-detail-links"
    >
      <LinkCard
        title="Controls"
        ids={linkIDs(detail.links, "controls")}
        hrefBase="/controls"
      />
      <LinkCard
        title="Risks"
        ids={linkIDs(detail.links, "risks")}
        hrefBase="/risks"
      />
      <LinkCard
        title="Vendors"
        ids={linkIDs(detail.links, "vendors")}
        hrefBase="/vendors"
      />
      <LinkCard
        title="Evidence"
        ids={linkIDs(detail.links, "evidence")}
        hrefBase="/evidence"
        queryKey="focus"
      />
    </div>
  );
}

function LinkCard({
  title,
  ids,
  hrefBase,
  queryKey,
}: {
  title: string;
  ids: string[];
  hrefBase: string;
  queryKey?: string;
}) {
  return (
    <Card>
      <CardHeader className="border-b">
        <CardTitle>
          {title}{" "}
          <span className="font-mono text-muted-foreground">{ids.length}</span>
        </CardTitle>
      </CardHeader>
      <CardContent>
        {ids.length === 0 ? (
          <p className="text-sm text-muted-foreground">None linked</p>
        ) : (
          <ul className="space-y-1">
            {ids.map((id) => (
              <li key={id}>
                <Link
                  href={
                    queryKey
                      ? `${hrefBase}?${queryKey}=${encodeURIComponent(id)}`
                      : `${hrefBase}/${encodeURIComponent(id)}`
                  }
                  className="font-mono text-xs text-primary hover:underline"
                >
                  {id.slice(0, 8)}...
                </Link>
              </li>
            ))}
          </ul>
        )}
      </CardContent>
    </Card>
  );
}

function Field({
  label,
  children,
  testid,
}: {
  label: string;
  children: React.ReactNode;
  testid?: string;
}) {
  return (
    <div>
      <dt className="text-xs uppercase text-muted-foreground">{label}</dt>
      <dd className="mt-1 text-sm" data-testid={testid}>
        {children}
      </dd>
    </div>
  );
}

function BackLink() {
  return (
    <Link
      href="/incidents"
      className={buttonVariants({ variant: "ghost", size: "sm" })}
    >
      Back to incidents
    </Link>
  );
}
