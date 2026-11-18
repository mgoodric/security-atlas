"use client";

// #2137 — evidence detail view (`/evidence/[id]`).
//
// Opened from an evidence row on the evidence list and on the control
// page. Reads its own BFF (`/api/evidence/{id}`) through TanStack Query:
// loading skeleton, 401 -> /login, 404 -> in-shell `notFound()` (the
// platform answers 404 for both an unknown id and a record the caller may
// not read), and a destructive Alert for anything else.
//
// The payload arrives redacted from the platform and renders as served.
// A manual attestation also shows its attestor, statement and supporting
// link. The statement renders through the shared Markdown component
// (#2094, #2316); every other value renders as text, and the supporting
// link is an <a> only when it is absolute http(s).

import { useQuery } from "@tanstack/react-query";
import Link from "next/link";
import { notFound, useRouter } from "next/navigation";
import { use, useEffect, type ReactNode } from "react";

import { DemoBadge } from "@/components/demo-badge";
import { Markdown } from "@/components/markdown";
import { PageShell } from "@/components/shell/page-shell";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Skeleton } from "@/components/ui/skeleton";
import { Timestamp } from "@/components/ui/timestamp";
import { APIError } from "@/lib/api/base";
import { fetchEvidenceDetail, type EvidenceDetail } from "@/lib/api/evidence";

import { enumLabel } from "@/lib/labels/enum-labels";
import { prettyJSON, scopeLabel, sourceSummary } from "../format";
import {
  attestationFields,
  isManualAttestation,
  resultBadgeVariant,
  safeHttpUrl,
} from "./view";

export default function EvidenceDetailPage({
  params,
}: {
  params: Promise<{ id: string }>;
}) {
  const { id } = use(params);
  const router = useRouter();

  const { data, isLoading, error } = useQuery({
    queryKey: ["evidence", id],
    queryFn: () => fetchEvidenceDetail(id),
    retry: (count, err) =>
      !(
        err instanceof APIError &&
        (err.status === 404 || err.status === 401)
      ) && count < 2,
  });

  useEffect(() => {
    if (error instanceof APIError && error.status === 401) {
      router.push(`/login?from=/evidence/${id}`);
    }
  }, [error, id, router]);

  if (error instanceof APIError && error.status === 404) {
    notFound();
  }

  if (isLoading) {
    return (
      <PageShell
        title={
          <Skeleton className="inline-block h-8 w-96 max-w-full align-middle" />
        }
        data-testid="evidence-detail-loading"
      >
        <Skeleton className="h-24 w-full" />
        <Skeleton className="h-64 w-full" />
      </PageShell>
    );
  }

  if (error && !(error instanceof APIError && error.status === 401)) {
    return (
      <PageShell title="Evidence record" actions={<BackLink />}>
        <Alert variant="destructive" data-testid="evidence-detail-error">
          <AlertTitle>Could not load evidence</AlertTitle>
          <AlertDescription>{(error as Error).message}</AlertDescription>
        </Alert>
      </PageShell>
    );
  }

  if (!data) return null;
  const ev = data.evidence;

  return (
    <PageShell
      title={
        <span data-testid="evidence-detail-title">
          {ev.evidence_kind ?? "Evidence record"}
        </span>
      }
      titleAdornment={
        <div className="flex flex-wrap items-center gap-2">
          <Badge
            variant={resultBadgeVariant(ev.result)}
            data-testid="evidence-detail-result"
          >
            {enumLabel("EvidenceResult", ev.result)}
          </Badge>
          {ev.demo ? <DemoBadge testId="evidence-detail-demo" /> : null}
        </div>
      }
      subtitle={
        <>
          Observed <Timestamp value={ev.observed_at} style="datetime" />
        </>
      }
      actions={<BackLink />}
      data-testid="evidence-detail"
    >
      {isManualAttestation(ev.evidence_kind) ? (
        <AttestationCard evidence={ev} />
      ) : null}
      <RecordCard evidence={ev} />
      <Card>
        <CardHeader>
          <CardTitle>Payload</CardTitle>
        </CardHeader>
        <CardContent>
          <pre
            className="max-h-[480px] overflow-auto rounded-md bg-muted p-3 font-mono text-xs"
            data-testid="evidence-detail-payload"
          >
            {prettyJSON(ev.payload)}
          </pre>
        </CardContent>
      </Card>
    </PageShell>
  );
}

function AttestationCard({ evidence }: { evidence: EvidenceDetail }) {
  const a = attestationFields(evidence.payload);
  const href = safeHttpUrl(a.supportingUri);
  return (
    <Card data-testid="evidence-detail-attestation">
      <CardHeader>
        <CardTitle>Attestation</CardTitle>
      </CardHeader>
      <CardContent>
        <dl className="grid grid-cols-1 gap-3 text-sm sm:grid-cols-[10rem_1fr]">
          <Field label="Attestor" testId="evidence-detail-attestor">
            {a.attestor ?? "—"}
          </Field>
          <Field label="Statement" testId="evidence-detail-statement">
            {a.statement ? <Markdown source={a.statement} /> : "—"}
          </Field>
          <Field label="Supporting link" testId="evidence-detail-supporting">
            {href ? (
              <a
                href={href}
                target="_blank"
                rel="noopener noreferrer"
                className="break-all underline hover:text-foreground"
              >
                {a.supportingUri}
              </a>
            ) : (
              <span className="break-all">{a.supportingUri ?? "—"}</span>
            )}
          </Field>
        </dl>
      </CardContent>
    </Card>
  );
}

function RecordCard({ evidence: ev }: { evidence: EvidenceDetail }) {
  const observer =
    sourceSummary(ev.source_attribution) !== "—"
      ? sourceSummary(ev.source_attribution)
      : ev.credential_id ?? "—";
  return (
    <Card>
      <CardHeader>
        <CardTitle>Record</CardTitle>
      </CardHeader>
      <CardContent>
        <dl className="grid grid-cols-1 gap-3 text-sm sm:grid-cols-[10rem_1fr]">
          <Field label="Control" testId="evidence-detail-control">
            {ev.control_id ? (
              <Link
                href={`/controls/${encodeURIComponent(ev.control_id)}`}
                className="font-mono underline hover:text-foreground"
              >
                {ev.control_ref || ev.control_id}
              </Link>
            ) : (
              <span className="font-mono">{ev.control_ref || "—"}</span>
            )}
          </Field>
          <Field label="Observed" testId="evidence-detail-observed-at">
            <Timestamp value={ev.observed_at} style="datetime" />
          </Field>
          <Field label="Ingested">
            <Timestamp value={ev.ingested_at} style="datetime" />
          </Field>
          <Field label="Observer" testId="evidence-detail-observer">
            <span className="font-mono">{observer}</span>
          </Field>
          <Field label="Source" testId="evidence-detail-source">
            <span className="font-mono">
              {sourceSummary(ev.source)} · {ev.ingestion_path}
            </span>
          </Field>
          <Field label="Scope cell" testId="evidence-detail-scope">
            <span className="font-mono">{scopeLabel(ev.scope_cell)}</span>
          </Field>
          <Field label="Schema">
            <span className="font-mono">
              {ev.evidence_kind ?? "—"}
              {ev.schema_version ? ` @ ${ev.schema_version}` : ""}
            </span>
          </Field>
          <Field label="Content hash" testId="evidence-detail-hash">
            <span className="break-all font-mono text-xs">
              {ev.content_hash}
            </span>
          </Field>
        </dl>
      </CardContent>
    </Card>
  );
}

function Field({
  label,
  testId,
  children,
}: {
  label: string;
  testId?: string;
  children: ReactNode;
}) {
  return (
    <>
      <dt className="text-muted-foreground">{label}</dt>
      <dd className="min-w-0 text-foreground" data-testid={testId}>
        {children}
      </dd>
    </>
  );
}

function BackLink() {
  return (
    <div className="text-sm">
      <Link
        href="/evidence"
        className="text-muted-foreground hover:underline"
        data-testid="evidence-detail-back"
      >
        ← Evidence ledger
      </Link>
    </div>
  );
}
