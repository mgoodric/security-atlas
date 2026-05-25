// Slice 263 — Stage C left-pane question list.
//
// Closes ISC-22 + AC-9 (slice 263):
//   - Renders one row per question grouped by domain (matches the
//     mockup at Plans/mockups/questionnaire.html lines 156-159).
//   - Each row shows: question code, truncated question text (clamped
//     to 2 lines), answer status chip (Unanswered / Draft / Final),
//     and the SCF anchor ID.
//   - Selected row is highlighted with a left border + tinted bg.
//
// Status derivation rules (D-internal):
//   - answer absent          -> "Unanswered"
//   - answer present, has narrative or answer_value -> "Draft"
//   - status field "final"   -> "Final" (questionnaire-level concept
//     not yet on per-answer model; reserved for v2)

"use client";

import { useMemo } from "react";

import type { Question } from "./types";

export type AnswerStatus = "Unanswered" | "Draft" | "Final";

export function statusForQuestion(q: Question): AnswerStatus {
  if (!q.answer) return "Unanswered";
  if (q.answer.answer_value || q.answer.narrative) return "Draft";
  return "Unanswered";
}

interface Group {
  domain: string;
  questions: Question[];
}

export function groupByDomain(questions: Question[]): Group[] {
  const map = new Map<string, Question[]>();
  for (const q of questions) {
    const d = q.domain || "Other";
    const arr = map.get(d) ?? [];
    arr.push(q);
    map.set(d, arr);
  }
  return [...map.entries()].map(([domain, qs]) => ({
    domain,
    questions: qs.slice().sort((a, b) => a.sort_order - b.sort_order),
  }));
}

interface QuestionListProps {
  questions: Question[];
  selectedId: string | null;
  onSelect: (id: string) => void;
}

export function QuestionList({
  questions,
  selectedId,
  onSelect,
}: QuestionListProps) {
  const groups = useMemo(() => groupByDomain(questions), [questions]);

  if (questions.length === 0) {
    return (
      <div
        className="px-3 py-12 text-center text-sm text-muted-foreground"
        data-testid="question-list-empty"
      >
        No questions yet. Upload an Excel workbook to import.
      </div>
    );
  }

  return (
    <div data-testid="question-list" className="overflow-y-auto">
      {groups.map((g) => (
        <div key={g.domain}>
          <div className="px-3 py-1.5 text-[10px] uppercase tracking-wider font-medium text-muted-foreground bg-muted/40 border-b border-border">
            {g.domain}
          </div>
          {g.questions.map((q) => {
            const status = statusForQuestion(q);
            const selected = q.id === selectedId;
            return (
              <button
                key={q.id}
                type="button"
                data-testid="question-row"
                data-question-id={q.id}
                onClick={() => onSelect(q.id)}
                className={`w-full text-left block px-3 py-2.5 border-b border-border hover:bg-muted/40 transition-colors ${
                  selected ? "bg-primary/5 border-l-2 border-l-primary" : ""
                }`}
              >
                <div className="flex items-start justify-between gap-2">
                  <span className="font-mono text-[11px] text-muted-foreground">
                    {q.code}
                  </span>
                  <StatusChip status={status} />
                </div>
                <div className="text-sm text-foreground mt-0.5 line-clamp-2">
                  {q.text}
                </div>
                <div className="flex items-center gap-2 mt-1 text-[11px] text-muted-foreground">
                  {q.scf_anchor_id ? (
                    <span className="font-mono">SCF:{q.scf_anchor_id}</span>
                  ) : (
                    <span className="text-amber-700 dark:text-amber-500">
                      needs mapping
                    </span>
                  )}
                </div>
              </button>
            );
          })}
        </div>
      ))}
    </div>
  );
}

function StatusChip({ status }: { status: AnswerStatus }) {
  const cls =
    status === "Final"
      ? "bg-emerald-50 text-emerald-700 dark:bg-emerald-950/40 dark:text-emerald-300"
      : status === "Draft"
        ? "bg-amber-50 text-amber-700 dark:bg-amber-950/40 dark:text-amber-300"
        : "bg-muted text-muted-foreground";
  return (
    <span
      data-testid="question-status-chip"
      className={`inline-flex items-center px-1.5 py-0.5 font-mono text-[9px] font-semibold rounded shrink-0 ${cls}`}
    >
      {status}
    </span>
  );
}
