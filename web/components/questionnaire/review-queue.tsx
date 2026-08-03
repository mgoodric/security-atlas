// Slice 757 — the batch review queue: a focus-mode stepper over every
// unapproved AI draft (JUDGMENT call: stepper over a single-pane table —
// "work through 200 drafts" is a one-at-a-time review act; the decisions log
// records the call).
//
// Constitutional surface:
//   * Approval is PER ANSWER: the only approve control acts on the single
//     draft in focus and submits exactly one answer id (AC-5/AC-10,
//     P0-757-1). There is NO select-all, NO approve-all, and the keyboard
//     shortcuts (j/k / arrows) only NAVIGATE — approval is always an explicit
//     per-answer click.
//   * A draft always renders WITH its citations (linked) and its model
//     provenance; a draft without resolved citations is not approvable
//     (P0-757-2).
//   * The cloud-routing banner renders config-driven (slice 499) AND
//     per-draft from the recorded model_provider (AC-4, P0-757-3).
//   * Non-draft outcomes are their own filterable work lists: insufficient →
//     answer manually; skipped_needs_mapping → the slice-755 mapping
//     affordance; suppressed → fixed reason code only (P0-757-5).
//
// The queue is server-derived (unapproved drafts from the questionnaire
// detail; outcome lists from the run record), so a reload mid-review loses
// nothing (AC-7).

"use client";

import Link from "next/link";
import { useEffect, useMemo, useState } from "react";

import { CloudRoutingBanner } from "@/components/llm/cloud-routing-banner";
import type { Question } from "@/components/questionnaire/types";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import {
  type AnswerRunDetail,
  type OutcomeFilter,
  approveRequestBody,
  outcomeRows,
  pendingDrafts,
  rejectRequestBody,
  runProgress,
} from "@/lib/questionnaire/review-queue";

interface ReviewQueueProps {
  questionnaireID: string;
  questions: Question[];
  runDetail: AnswerRunDetail | null;
  // Called after an approve/reject lands so the parent refetches the detail
  // (the acted-on draft leaves the queue server-side).
  onChanged: () => void;
  // Routes the operator back to the authoring editor for a question (the
  // manual-authoring and mapping affordances).
  onSelectQuestion: (questionId: string) => void;
}

type Tab = "drafts" | OutcomeFilter;

export function ReviewQueue({
  questionnaireID,
  questions,
  runDetail,
  onChanged,
  onSelectQuestion,
}: ReviewQueueProps) {
  const [tab, setTab] = useState<Tab>("drafts");
  const [index, setIndex] = useState(0);
  const [editDraft, setEditDraft] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const drafts = useMemo(() => pendingDrafts(questions), [questions]);
  const clamped = drafts.length === 0 ? 0 : Math.min(index, drafts.length - 1);
  const current = drafts[clamped] ?? null;

  // Reset the inline edit buffer whenever the focused draft changes. The
  // setTimeout(0) defers setState out of the effect body (slice 223 + 192
  // react-hooks/set-state-in-effect pattern).
  const currentAnswerId = current?.answerId ?? "";
  useEffect(() => {
    const t = setTimeout(() => setEditDraft(null), 0);
    return () => clearTimeout(t);
  }, [currentAnswerId]);

  const insufficient = useMemo(
    () => outcomeRows(runDetail, questions, "insufficient_evidence"),
    [runDetail, questions],
  );
  const needsMapping = useMemo(
    () => outcomeRows(runDetail, questions, "skipped_needs_mapping"),
    [runDetail, questions],
  );
  const suppressed = useMemo(
    () => outcomeRows(runDetail, questions, "suppressed"),
    [runDetail, questions],
  );

  function step(delta: number): void {
    if (drafts.length === 0) return;
    setIndex((i) => Math.min(Math.max(i + delta, 0), drafts.length - 1));
  }

  // Keyboard flow: NAVIGATION ONLY. Approve/reject are deliberately not
  // keyboard-bound — each approval stays an explicit per-answer click.
  function onKeyDown(e: React.KeyboardEvent<HTMLDivElement>): void {
    if (e.target instanceof HTMLTextAreaElement) return;
    if (e.key === "j" || e.key === "ArrowRight") {
      e.preventDefault();
      step(1);
    } else if (e.key === "k" || e.key === "ArrowLeft") {
      e.preventDefault();
      step(-1);
    }
  }

  async function submitOne(
    kind: "approve" | "reject",
    answerId: string,
    questionId: string,
  ): Promise<void> {
    setBusy(true);
    setError(null);
    try {
      const path =
        kind === "approve"
          ? `/api/questionnaires/${questionnaireID}/answers/${questionId}/ai-approve`
          : `/api/questionnaires/${questionnaireID}/answers/${questionId}/ai-reject`;
      const body =
        kind === "approve"
          ? approveRequestBody(answerId, editDraft ?? current?.narrative ?? "")
          : rejectRequestBody(answerId);
      const res = await fetch(path, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(body),
      });
      if (!res.ok) {
        let msg = `${res.status} ${res.statusText}`;
        try {
          const j = (await res.json()) as { error?: string };
          if (j.error) msg = j.error;
        } catch {
          // body not JSON
        }
        setError(msg);
        return;
      }
      onChanged();
    } finally {
      setBusy(false);
    }
  }

  const activeRows =
    tab === "insufficient_evidence"
      ? insufficient
      : tab === "skipped_needs_mapping"
        ? needsMapping
        : suppressed;

  const tabs: { key: Tab; label: string; count: number }[] = [
    { key: "drafts", label: "Drafts to review", count: drafts.length },
    {
      key: "insufficient_evidence",
      label: "Insufficient evidence",
      count: insufficient.length,
    },
    {
      key: "skipped_needs_mapping",
      label: "Needs mapping",
      count: needsMapping.length,
    },
    { key: "suppressed", label: "Suppressed", count: suppressed.length },
  ];

  return (
    <div
      data-testid="review-queue"
      className="flex flex-col gap-3 flex-1 overflow-y-auto"
      onKeyDown={onKeyDown}
    >
      {/* Config-driven routing banner (slice 499) — renders only on a cloud
          tenant config. */}
      <CloudRoutingBanner />

      {runDetail ? (
        <div
          data-testid="run-progress"
          className="rounded-md border border-border bg-muted/20 px-3 py-2 flex flex-wrap items-center gap-x-4 gap-y-1 text-xs"
        >
          <span className="font-medium">
            Run {runDetail.run.status}
            {runDetail.run.failure_reason
              ? ` — ${runDetail.run.failure_reason}`
              : ""}
          </span>
          {runProgress(runDetail.run).map((p) => (
            <span
              key={p.key}
              data-testid={`run-progress-${p.key}`}
              className="text-muted-foreground"
            >
              {p.label}: <span className="font-mono">{p.count}</span>
            </span>
          ))}
        </div>
      ) : null}

      {/* Outcome work-list tabs (AC-6). */}
      <div className="flex flex-wrap gap-1.5" role="tablist">
        {tabs.map((t) => (
          <button
            key={t.key}
            type="button"
            role="tab"
            aria-selected={tab === t.key}
            data-testid={`review-tab-${t.key}`}
            onClick={() => setTab(t.key)}
            className={`text-xs px-2.5 py-1 rounded-md border ${
              tab === t.key
                ? "border-primary bg-primary/10 text-primary font-medium"
                : "border-border bg-card text-muted-foreground hover:bg-muted/40"
            }`}
          >
            {t.label} ({t.count})
          </button>
        ))}
      </div>

      {error ? (
        <Alert variant="destructive" data-testid="review-error">
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      ) : null}

      {tab === "drafts" ? (
        current ? (
          <div
            data-testid="review-stepper"
            tabIndex={0}
            className="rounded-xl border border-border bg-card p-4 space-y-3 focus:outline-none focus:ring-2 focus:ring-ring"
          >
            <div className="flex items-center justify-between gap-2">
              <span
                data-testid="review-position"
                className="text-xs text-muted-foreground font-mono"
              >
                {clamped + 1} / {drafts.length}
              </span>
              <div className="flex items-center gap-1.5">
                <button
                  type="button"
                  data-testid="review-prev"
                  disabled={clamped === 0}
                  onClick={() => step(-1)}
                  className="text-xs px-2 py-1 rounded-md border border-border bg-card hover:bg-muted/40 disabled:opacity-50"
                >
                  ← Prev
                </button>
                <button
                  type="button"
                  data-testid="review-next"
                  disabled={clamped >= drafts.length - 1}
                  onClick={() => step(1)}
                  className="text-xs px-2 py-1 rounded-md border border-border bg-card hover:bg-muted/40 disabled:opacity-50"
                >
                  Next →
                </button>
                <span className="text-[10px] text-muted-foreground hidden md:inline">
                  j/k to navigate
                </span>
              </div>
            </div>

            <div>
              <div className="text-xs font-mono text-muted-foreground">
                {current.questionCode}
              </div>
              <div
                className="text-sm font-medium"
                data-testid="review-question"
              >
                {current.questionText}
              </div>
            </div>

            {/* Per-draft cloud flag from the recorded provider (AC-4). */}
            {current.cloudRouted ? (
              <Alert data-testid="review-cloud-banner">
                <AlertTitle>Cloud LLM routing</AlertTitle>
                <AlertDescription>
                  This draft was generated by a cloud LLM, not the local model.
                  Tenant data left the deployment for this request.
                </AlertDescription>
              </Alert>
            ) : null}

            <div className="flex items-center gap-2 text-[11px]">
              <span className="px-1.5 py-0.5 rounded bg-amber-500/15 text-amber-700 font-semibold">
                AI DRAFT · NOT APPROVED
              </span>
              <span
                data-testid="review-provenance"
                className="text-muted-foreground font-mono"
              >
                {current.modelLabel}
                {current.promptVersion ? ` · ${current.promptVersion}` : ""}
              </span>
            </div>

            {/* Inline edit: the edited text is what approval stores (AC-5). */}
            <textarea
              data-testid="review-draft-text"
              value={editDraft ?? current.narrative}
              onChange={(e) => setEditDraft(e.target.value)}
              className="w-full min-h-32 p-3 text-sm border border-border rounded-md bg-card focus:outline-none focus:ring-2 focus:ring-ring"
            />

            <div
              data-testid="review-citations"
              className="flex flex-wrap gap-1.5"
            >
              {current.citations.map((c) => (
                <Link
                  key={c.label}
                  href={c.href}
                  data-testid="review-citation-link"
                  className="text-[11px] font-mono px-1.5 py-0.5 rounded bg-primary/10 text-primary hover:underline"
                >
                  {c.label}
                </Link>
              ))}
            </div>

            <div className="flex items-center gap-2">
              {/* One click approves THIS answer only (AC-5/AC-10). */}
              <button
                type="button"
                data-testid="review-approve"
                disabled={!current.canApprove || busy}
                onClick={() => {
                  void submitOne(
                    "approve",
                    current.answerId,
                    current.questionId,
                  );
                }}
                className="text-xs px-3 py-1 rounded-md border border-primary bg-primary/10 text-primary font-medium hover:bg-primary/20 disabled:opacity-50"
              >
                Approve answer
              </button>
              <button
                type="button"
                data-testid="review-reject"
                disabled={busy}
                onClick={() => {
                  void submitOne(
                    "reject",
                    current.answerId,
                    current.questionId,
                  );
                }}
                className="text-xs px-3 py-1 rounded-md border border-destructive/50 text-destructive font-medium hover:bg-destructive/10 disabled:opacity-50"
              >
                Reject draft
              </button>
              <span className="text-xs text-muted-foreground">
                Review before approving — approval is per answer.
              </span>
            </div>
          </div>
        ) : (
          <div
            data-testid="review-empty"
            className="rounded-xl border border-border bg-card p-6 text-sm text-muted-foreground text-center"
          >
            No unreviewed drafts. Start a run or switch to an outcome list.
          </div>
        )
      ) : (
        <div className="rounded-xl border border-border bg-card divide-y divide-border">
          {activeRows.map((row) => (
            <div
              key={row.questionId}
              data-testid={`outcome-row-${tab}`}
              className="px-4 py-3 flex items-start justify-between gap-3"
            >
              <div>
                <div className="text-xs font-mono text-muted-foreground">
                  {row.questionCode}
                </div>
                <div className="text-sm">{row.questionText}</div>
                {tab === "suppressed" ? (
                  <div
                    className="text-xs text-muted-foreground mt-0.5"
                    data-testid="outcome-suppressed-reason"
                  >
                    {row.reasonLabel}
                  </div>
                ) : null}
              </div>
              {tab === "insufficient_evidence" ? (
                <button
                  type="button"
                  data-testid="outcome-answer-manually"
                  onClick={() => onSelectQuestion(row.questionId)}
                  className="text-xs px-2.5 py-1 rounded-md border border-border bg-card hover:bg-muted/40 shrink-0"
                >
                  Answer manually
                </button>
              ) : null}
              {tab === "skipped_needs_mapping" ? (
                <button
                  type="button"
                  data-testid="outcome-map-question"
                  onClick={() => onSelectQuestion(row.questionId)}
                  className="text-xs px-2.5 py-1 rounded-md border border-border bg-card hover:bg-muted/40 shrink-0"
                >
                  Map question
                </button>
              ) : null}
            </div>
          ))}
          {activeRows.length === 0 ? (
            <div className="px-4 py-6 text-sm text-muted-foreground text-center">
              Nothing in this list.
            </div>
          ) : null}
        </div>
      )}
    </div>
  );
}
