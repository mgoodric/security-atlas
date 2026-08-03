// Slice 263 — Stage C two-pane authoring view.
//
// Closes ISC-20..ISC-25 + ISC-37 + AC-8..AC-11 + AC-19..AC-20:
//
//   - Two-pane layout at >= md: left = scrollable question list,
//     right = answer editor for the selected question.
//   - Mobile (< md): collapses to a single column; pane state is
//     URL-bound (`?pane=list|edit`) so the back-button works on
//     mobile — slice 277 mobile-responsive composition.
//   - Page header: Export PDF button + title from the questionnaire.
//   - Loading skeleton + error alert.
//
// Slice 757 adds the batch flow: a "Draft all answers" header button starts a
// slice-756 run (request-scoped — the POST returns the completed run), and a
// URL-bound review view (`?view=review&run=<id>`) renders the per-answer
// review queue. Both the view and the run id live in the URL so a reload
// mid-review resumes with nothing lost (AC-7).

"use client";

import { useQuery, useQueryClient } from "@tanstack/react-query";
import Link from "next/link";
import { useRouter, useSearchParams } from "next/navigation";
import { use, useEffect, useMemo, useState } from "react";

import { AnswerEditor } from "@/components/questionnaire/answer-editor";
import { QuestionList } from "@/components/questionnaire/question-list";
import { ReviewQueue } from "@/components/questionnaire/review-queue";
import type { QuestionnaireDetail } from "@/components/questionnaire/types";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import {
  type AnswerRunDetail,
  pendingDrafts,
} from "@/lib/questionnaire/review-queue";

async function fetchDetail(id: string): Promise<QuestionnaireDetail> {
  const res = await fetch(`/api/questionnaires/${id}`, {
    cache: "no-store",
  });
  if (!res.ok) {
    let msg = `${res.status} ${res.statusText}`;
    try {
      const j = (await res.json()) as { error?: string };
      if (j.error) msg = j.error;
    } catch {
      // body not JSON
    }
    throw new Error(msg);
  }
  return (await res.json()) as QuestionnaireDetail;
}

async function exportPDF(id: string, name: string): Promise<void> {
  const res = await fetch(`/api/questionnaires/${id}/export-pdf`, {
    method: "POST",
  });
  if (!res.ok) {
    let msg = `${res.status} ${res.statusText}`;
    try {
      const j = (await res.json()) as { error?: string };
      if (j.error) msg = j.error;
    } catch {
      // body not JSON
    }
    throw new Error(msg);
  }
  const blob = await res.blob();
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = `${name.replace(/[^a-z0-9-]/gi, "-").toLowerCase()}.pdf`;
  document.body.appendChild(a);
  a.click();
  document.body.removeChild(a);
  URL.revokeObjectURL(url);
}

async function fetchRunDetail(
  id: string,
  runId: string,
): Promise<AnswerRunDetail> {
  const res = await fetch(`/api/questionnaires/${id}/answer-runs/${runId}`, {
    cache: "no-store",
  });
  if (!res.ok) {
    let msg = `${res.status} ${res.statusText}`;
    try {
      const j = (await res.json()) as { error?: string };
      if (j.error) msg = j.error;
    } catch {
      // body not JSON
    }
    throw new Error(msg);
  }
  return (await res.json()) as AnswerRunDetail;
}

export default function QuestionnaireDetailPage(props: {
  params: Promise<{ id: string }>;
}) {
  const { id } = use(props.params);
  const router = useRouter();
  const queryClient = useQueryClient();
  const search = useSearchParams();
  const pane = (search.get("pane") ?? "list") as "list" | "edit";
  const view = search.get("view") === "review" ? "review" : "author";
  const runId = search.get("run");
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [pdfError, setPdfError] = useState<string | null>(null);
  const [drafting, setDrafting] = useState(false);
  const [draftError, setDraftError] = useState<string | null>(null);

  const detailQ = useQuery({
    queryKey: ["questionnaire", id],
    queryFn: () => fetchDetail(id),
  });

  // The slice-756 run record referenced by the URL (`?run=<id>`), if any —
  // URL-bound so a reload mid-review restores the outcome lists (AC-7).
  const runQ = useQuery({
    queryKey: ["questionnaire-run", id, runId],
    queryFn: () => fetchRunDetail(id, runId ?? ""),
    enabled: runId !== null && runId !== "",
  });

  // "Draft all answers": starts a slice-756 run. Execution is request-scoped
  // and sequential server-side, so the POST resolves with the completed run;
  // the button is disabled while the request is in flight and a concurrent
  // run elsewhere surfaces as the platform's 409.
  async function draftAll(): Promise<void> {
    setDrafting(true);
    setDraftError(null);
    try {
      const res = await fetch(`/api/questionnaires/${id}/answer-runs`, {
        method: "POST",
      });
      if (!res.ok) {
        let msg = `${res.status} ${res.statusText}`;
        try {
          const j = (await res.json()) as { error?: string };
          if (j.error) msg = j.error;
        } catch {
          // body not JSON
        }
        setDraftError(msg);
        return;
      }
      const detail = (await res.json()) as AnswerRunDetail;
      await queryClient.invalidateQueries({ queryKey: ["questionnaire", id] });
      queryClient.setQueryData(
        ["questionnaire-run", id, detail.run.id],
        detail,
      );
      router.replace(`/questionnaires/${id}?view=review&run=${detail.run.id}`, {
        scroll: false,
      });
    } finally {
      setDrafting(false);
    }
  }

  // Default-select the first question once data lands. setState via
  // setTimeout(0) per react-hooks/set-state-in-effect (slice 223 +
  // 192 pattern).
  useEffect(() => {
    if (selectedId) return;
    const first = detailQ.data?.questions?.[0]?.id;
    if (!first) return;
    const t = setTimeout(() => setSelectedId(first), 0);
    return () => clearTimeout(t);
  }, [detailQ.data, selectedId]);

  const selected = useMemo(() => {
    if (!detailQ.data || !selectedId) return null;
    return detailQ.data.questions.find((q) => q.id === selectedId) ?? null;
  }, [detailQ.data, selectedId]);

  function selectQuestion(qid: string): void {
    setSelectedId(qid);
    // On mobile, navigate to the editor pane via URL so the
    // back-button returns to the list.
    if (
      typeof window !== "undefined" &&
      window.matchMedia("(max-width: 767px)").matches
    ) {
      router.replace(`/questionnaires/${id}?pane=edit`, { scroll: false });
    }
  }

  function backToList(): void {
    router.replace(`/questionnaires/${id}?pane=list`, { scroll: false });
  }

  if (detailQ.isLoading) {
    return (
      <div className="space-y-4" data-testid="questionnaire-detail-loading">
        <Skeleton className="h-8 w-1/2" />
        <Skeleton className="h-96 w-full" />
      </div>
    );
  }

  if (detailQ.error) {
    return (
      <Alert variant="destructive" data-testid="questionnaire-detail-error">
        <AlertTitle>Could not load questionnaire</AlertTitle>
        <AlertDescription>{(detailQ.error as Error).message}</AlertDescription>
      </Alert>
    );
  }

  if (!detailQ.data) return null;

  const { questionnaire, questions } = detailQ.data;
  const draftCount = pendingDrafts(questions).length;

  return (
    <div
      className="flex flex-col h-full overflow-hidden"
      data-testid="questionnaire-detail"
    >
      {/* Page header */}
      <div className="flex items-start justify-between gap-4 pb-4 border-b border-border">
        <div>
          <div className="flex items-center gap-2 text-xs text-muted-foreground mb-1">
            <Link href="/questionnaires" className="hover:underline">
              Questionnaires
            </Link>
            <span>·</span>
            <span className="font-mono">{questionnaire.status}</span>
          </div>
          <h1 className="text-xl font-semibold tracking-tight">
            {questionnaire.name}
          </h1>
          {questionnaire.source_filename ? (
            <div className="text-xs text-muted-foreground font-mono mt-0.5">
              {questionnaire.source_filename}
            </div>
          ) : null}
        </div>
        <div className="flex items-center gap-2">
          {/* Slice 757: start a batch drafting run (disabled while one is in
              flight). The run persists UNAPPROVED drafts only — review +
              per-answer approval happen in the queue. */}
          <Button
            variant="outline"
            size="sm"
            data-testid="questionnaire-draft-all"
            disabled={drafting}
            onClick={() => {
              void draftAll();
            }}
          >
            {drafting ? "Drafting…" : "Draft all answers"}
          </Button>
          {view === "review" ? (
            <Button
              variant="outline"
              size="sm"
              data-testid="questionnaire-back-to-authoring"
              onClick={() =>
                router.replace(`/questionnaires/${id}`, { scroll: false })
              }
            >
              Back to authoring
            </Button>
          ) : (
            <Button
              variant="outline"
              size="sm"
              data-testid="questionnaire-open-review"
              onClick={() =>
                router.replace(
                  runId
                    ? `/questionnaires/${id}?view=review&run=${runId}`
                    : `/questionnaires/${id}?view=review`,
                  { scroll: false },
                )
              }
            >
              Review drafts ({draftCount})
            </Button>
          )}
          <Button
            variant="outline"
            size="sm"
            data-testid="questionnaire-export-pdf"
            onClick={async () => {
              setPdfError(null);
              try {
                await exportPDF(id, questionnaire.name);
              } catch (err) {
                setPdfError((err as Error).message);
              }
            }}
          >
            Export PDF
          </Button>
        </div>
      </div>

      {draftError ? (
        <Alert
          variant="destructive"
          className="mt-3"
          data-testid="questionnaire-draft-all-error"
        >
          <AlertTitle>Batch drafting failed</AlertTitle>
          <AlertDescription>{draftError}</AlertDescription>
        </Alert>
      ) : null}

      {pdfError ? (
        <Alert
          variant="destructive"
          className="mt-3"
          data-testid="questionnaire-pdf-error"
        >
          <AlertTitle>PDF export failed</AlertTitle>
          <AlertDescription>{pdfError}</AlertDescription>
        </Alert>
      ) : null}

      {/* Slice 757 review view: the per-answer batch review queue. */}
      {view === "review" ? (
        <div className="flex-1 overflow-hidden mt-4 flex flex-col">
          <ReviewQueue
            questionnaireID={id}
            questions={questions}
            runDetail={runQ.data ?? null}
            onChanged={() => {
              void queryClient.invalidateQueries({
                queryKey: ["questionnaire", id],
              });
            }}
            onSelectQuestion={(qid) => {
              setSelectedId(qid);
              router.replace(`/questionnaires/${id}?pane=edit`, {
                scroll: false,
              });
            }}
          />
        </div>
      ) : (
        <div className="grid grid-cols-1 md:grid-cols-12 gap-4 flex-1 overflow-hidden mt-4">
          <aside
            data-testid="question-list-pane"
            className={`md:col-span-4 bg-card border border-border rounded-xl overflow-hidden flex flex-col ${
              pane === "list" ? "block" : "hidden md:flex"
            }`}
          >
            <div className="border-b border-border px-3 py-2 text-xs text-muted-foreground">
              {questions.length} question
              {questions.length === 1 ? "" : "s"}
            </div>
            <QuestionList
              questions={questions}
              selectedId={selectedId}
              onSelect={selectQuestion}
            />
          </aside>

          <section
            data-testid="answer-editor-pane"
            className={`md:col-span-8 bg-card border border-border rounded-xl overflow-hidden flex flex-col ${
              pane === "edit" ? "block" : "hidden md:flex"
            }`}
          >
            {/* Mobile-only back button */}
            <div className="md:hidden border-b border-border px-3 py-2">
              <button
                type="button"
                data-testid="answer-editor-back-mobile"
                onClick={backToList}
                className="text-xs text-primary hover:underline font-medium"
              >
                ← Back to questions
              </button>
            </div>
            {selected ? (
              <AnswerEditor questionnaireID={id} question={selected} />
            ) : (
              <div
                className="flex-1 flex items-center justify-center text-sm text-muted-foreground"
                data-testid="answer-editor-no-selection"
              >
                {questions.length === 0
                  ? "No questions imported yet."
                  : "Select a question to begin."}
              </div>
            )}
          </section>
        </div>
      )}
    </div>
  );
}
