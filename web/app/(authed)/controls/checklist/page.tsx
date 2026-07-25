"use client";

// Slice 471 — Role-scoped control-implementation checklist generator v0 (cited,
// non-binding).
//
// The operator generates a cited DRAFT checklist for their in-scope control set,
// grouped by team role (infra / engineering / security + an honest "unassigned"
// bucket). The which-control -> which-role split is DETERMINISTIC server-side;
// the LLM only writes each control's task text. EVERY render decision (which
// sections are approvable, the non-binding disclosure, the export gate) lives in
// the node-testable view-model (web/lib/checklist/checklist-view.ts); this
// component is a thin renderer so the contract is unit-covered on vitest and the
// DOM is covered by Playwright.
//
// AI-assist boundary surfaced honestly: the draft is clearly labelled
// "AI-assisted draft — review before use", each section approves one click at a
// time, and the markdown export is DISABLED until at least one section is
// approved (P0-471-1).

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
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
import {
  type ChecklistResponse,
  approvedCount,
  aiSectionCount,
  buildSectionState,
  canExport,
  disclosureText,
  showCloudBanner,
} from "@/lib/checklist/checklist-view";

async function postJSON(path: string): Promise<ChecklistResponse> {
  const res = await fetch(path, { method: "POST" });
  if (!res.ok) {
    const detail = await res.json().catch(() => ({}));
    throw new Error(detail.error ?? `request failed (${res.status})`);
  }
  return (await res.json()) as ChecklistResponse;
}

export default function ChecklistPage() {
  const qc = useQueryClient();
  const [generationId, setGenerationId] = useState<string>("");

  const generate = useMutation({
    mutationFn: () => postJSON("/api/controls/checklist/generate"),
    onSuccess: (data) => {
      setGenerationId(data.generation_id);
      qc.setQueryData(["checklist", data.generation_id], data);
    },
  });

  // The generated checklist is held in the mutation result + the query cache so
  // a per-section approval can refresh it.
  const checklistQ = useQuery<ChecklistResponse>({
    queryKey: ["checklist", generationId],
    enabled: false, // we populate the cache directly from generate/approve
    initialData: generate.data,
  });
  const checklist = checklistQ.data ?? generate.data;

  const approve = useMutation({
    mutationFn: (sectionId: string) =>
      postJSON(
        `/api/controls/checklist/sections/${encodeURIComponent(
          sectionId,
        )}/approve`,
      ),
    onSuccess: () => {
      // Re-load the generation to reflect the now-approved section.
      if (generationId) {
        void fetch(
          `/api/controls/checklist/${encodeURIComponent(generationId)}`,
        )
          .then((r) => (r.ok ? r.json() : null))
          .then((data: ChecklistResponse | null) => {
            if (data) {
              qc.setQueryData(["checklist", generationId], data);
              generate.reset();
              setGenerationId(data.generation_id);
              // Force a re-render by writing the fresh data into the query cache.
              qc.setQueryData(["checklist", data.generation_id], data);
            }
          });
      }
    },
  });

  return (
    <div className="space-y-6" data-testid="checklist-page">
      <header className="space-y-2">
        <h1 className="text-2xl font-semibold">
          Role-scoped implementation checklist
        </h1>
        <p className="text-muted-foreground max-w-2xl text-sm">
          Generate a per-team to-do checklist from your in-scope controls. Each
          control is assigned to a team deterministically from its owner role;
          the AI drafts the concrete tasks and cites the source control for
          every one. Review and approve each team&apos;s section before
          exporting.
        </p>
        <Button
          onClick={() => generate.mutate()}
          disabled={generate.isPending}
          data-testid="generate-checklist"
        >
          {generate.isPending ? "Generating…" : "Generate checklist"}
        </Button>
      </header>

      {generate.isError ? (
        <Alert variant="destructive" data-testid="generate-error">
          <AlertTitle>Could not generate a checklist</AlertTitle>
          <AlertDescription>{String(generate.error)}</AlertDescription>
        </Alert>
      ) : null}

      {checklist ? (
        <ChecklistBody
          checklist={checklist}
          onApprove={(sectionId) => approve.mutate(sectionId)}
          approvingId={approve.isPending ? approve.variables ?? "" : ""}
        />
      ) : null}
    </div>
  );
}

function ChecklistBody({
  checklist,
  onApprove,
  approvingId,
}: {
  checklist: ChecklistResponse;
  onApprove: (sectionId: string) => void;
  approvingId: string;
}) {
  const exportable = canExport(checklist);
  return (
    <div className="space-y-6" data-testid="checklist-body">
      <Alert data-testid="non-binding-disclosure">
        <AlertTitle>AI-assisted draft</AlertTitle>
        <AlertDescription>{disclosureText(checklist)}</AlertDescription>
      </Alert>

      {showCloudBanner(checklist) ? (
        <Alert variant="destructive" data-testid="cloud-banner">
          <AlertTitle>Cloud model routing</AlertTitle>
          <AlertDescription>
            This draft was generated by a cloud LLM. Your control text left the
            deployment.
          </AlertDescription>
        </Alert>
      ) : null}

      <div className="flex items-center justify-between">
        <span
          className="text-muted-foreground text-sm"
          data-testid="approval-progress"
        >
          {approvedCount(checklist)} of {aiSectionCount(checklist)} sections
          approved
        </span>
        <a
          href={
            exportable
              ? `/api/controls/checklist/${encodeURIComponent(
                  checklist.generation_id,
                )}/export`
              : undefined
          }
        >
          <Button
            variant="outline"
            disabled={!exportable}
            data-testid="export-markdown"
            title={
              exportable
                ? "Download approved sections as markdown"
                : "Approve at least one section before exporting"
            }
          >
            Export approved (markdown)
          </Button>
        </a>
      </div>

      {checklist.sections.map((s) => {
        const st = buildSectionState(s);
        return (
          <Card
            key={st.sectionId || st.roleLabel}
            data-testid={`section-${s.role}`}
          >
            <CardHeader>
              <div className="flex items-center justify-between">
                <CardTitle className="flex items-center gap-2">
                  {st.roleLabel}
                  {st.approved ? (
                    <Badge data-testid={`approved-badge-${s.role}`}>
                      Approved
                    </Badge>
                  ) : null}
                </CardTitle>
                {st.approvable ? (
                  <Button
                    size="sm"
                    onClick={() => onApprove(st.sectionId)}
                    disabled={approvingId === st.sectionId}
                    data-testid={`approve-${s.role}`}
                  >
                    {approvingId === st.sectionId
                      ? "Approving…"
                      : "Approve section"}
                  </Button>
                ) : null}
              </div>
              {st.modelDisclosure ? (
                <CardDescription>{st.modelDisclosure}</CardDescription>
              ) : null}
              {st.approved && st.approver ? (
                <CardDescription>Approved by {st.approver}</CardDescription>
              ) : null}
            </CardHeader>
            <CardContent>
              {st.suppressed ? (
                <p
                  className="text-muted-foreground text-sm"
                  data-testid={`suppressed-${s.role}`}
                >
                  {st.note}
                </p>
              ) : (
                <ul className="space-y-2">
                  {st.items.map((it, i) => (
                    <li
                      key={i}
                      className="text-sm"
                      data-testid={`item-${s.role}-${i}`}
                    >
                      <span>{it.task}</span>
                      {it.no_evidence ? (
                        <Badge
                          variant="outline"
                          className="ml-2"
                          data-testid={`no-evidence-${s.role}-${i}`}
                        >
                          no evidence yet
                        </Badge>
                      ) : null}
                    </li>
                  ))}
                </ul>
              )}
            </CardContent>
          </Card>
        );
      })}
    </div>
  );
}
