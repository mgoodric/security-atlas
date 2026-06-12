// Slice 263 — Stage C right-pane answer editor.
//
// Closes ISC-23..ISC-25 + ISC-26..ISC-30 + ISC-31..ISC-37 +
// AC-10..AC-18 (the editor + suggestions + citations + save-to-library
// + save controls all live in this component).
//
// Surface:
//   - Question header (code + text + SCF anchor link)
//   - Suggestions panel (deterministic, AI-clean)
//   - Quick yes/no/n.a. answer-value chips
//   - Narrative textarea with 500ms debounced autosave (PATCH)
//   - Citation chips + + Cite picker (slice 268 unified search)
//   - Save-to-library checkbox (default OFF; AC-18)
//   - Inline retry banner above textarea on PATCH error (AC-11)

"use client";

import { useEffect, useRef, useState } from "react";

import { AISuggestPanel } from "./ai-suggest-panel";
import { CitationChips, CitationPicker } from "./citation-picker";
import { SuggestionsPanel } from "./suggestions-panel";
import type { Citation, Question } from "./types";
import { Alert, AlertDescription } from "@/components/ui/alert";

const AUTOSAVE_DEBOUNCE_MS = 500;

interface AnswerPatchBody {
  answer_value: string;
  narrative: string;
  citations: Citation[];
  save_to_library: boolean;
  scf_anchor_id: string;
}

async function patchAnswer(
  questionnaireID: string,
  questionID: string,
  body: AnswerPatchBody,
): Promise<void> {
  const res = await fetch(
    `/api/questionnaires/${questionnaireID}/answers/${questionID}`,
    {
      method: "PATCH",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
    },
  );
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
}

function normalizeCitations(raw: unknown): Citation[] {
  if (!Array.isArray(raw)) return [];
  return raw
    .filter(
      (c): c is Citation =>
        typeof c === "object" &&
        c !== null &&
        typeof (c as Citation).id === "string" &&
        typeof (c as Citation).title === "string" &&
        ((c as Citation).type === "controls" ||
          (c as Citation).type === "evidence"),
    )
    .map((c) => ({
      id: c.id,
      type: c.type,
      title: c.title,
    }));
}

interface AnswerEditorProps {
  questionnaireID: string;
  question: Question;
}

export function AnswerEditor({ questionnaireID, question }: AnswerEditorProps) {
  // Local state — autosave PATCHes to the server on a debounce.
  const [answerValue, setAnswerValue] = useState(
    question.answer?.answer_value ?? "",
  );
  const [narrative, setNarrative] = useState(question.answer?.narrative ?? "");
  const [citations, setCitations] = useState<Citation[]>(
    normalizeCitations(question.answer?.citations ?? []),
  );
  const [saveToLibrary, setSaveToLibrary] = useState(false); // AC-18 default OFF
  const [saveError, setSaveError] = useState<string | null>(null);
  const [savedAt, setSavedAt] = useState<string | null>(null);

  // When the selected question changes, reset local state to match.
  // The set-state-in-effect rule (react-hooks/set-state-in-effect)
  // requires that synchronous setState from inside an effect body be
  // queued through an external scheduler — we use a 0-delay setTimeout
  // matching slice 223's TenantSwitcher pattern.
  useEffect(() => {
    const t = setTimeout(() => {
      setAnswerValue(question.answer?.answer_value ?? "");
      setNarrative(question.answer?.narrative ?? "");
      setCitations(normalizeCitations(question.answer?.citations ?? []));
      setSaveToLibrary(false);
      setSaveError(null);
      setSavedAt(null);
      dirtyRef.current = false;
    }, 0);
    return () => clearTimeout(t);
  }, [question.id, question.answer]);

  // Autosave: debounced PATCH on narrative/answerValue/citations changes.
  // We do NOT autosave save_to_library — that's an explicit toggle
  // intent that should ride the next save.
  const dirtyRef = useRef(false);
  useEffect(() => {
    // Skip the initial mount (state matches the seed; nothing to save).
    if (!dirtyRef.current) return;
    const t = setTimeout(() => {
      void save();
    }, AUTOSAVE_DEBOUNCE_MS);
    return () => clearTimeout(t);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [answerValue, narrative, citations, saveToLibrary]);

  async function save(): Promise<void> {
    const body: AnswerPatchBody = {
      answer_value: answerValue,
      narrative,
      citations,
      save_to_library: saveToLibrary,
      scf_anchor_id: question.scf_anchor_id,
    };
    try {
      await patchAnswer(questionnaireID, question.id, body);
      setSaveError(null);
      setSavedAt(new Date().toISOString());
    } catch (err) {
      setSaveError((err as Error).message);
    }
  }

  function markDirty(): void {
    dirtyRef.current = true;
  }

  return (
    <div
      data-testid="answer-editor"
      className="flex flex-col h-full overflow-hidden"
    >
      {/* question header */}
      <div className="border-b border-border px-7 py-5">
        <div className="flex items-center gap-2 mb-3 text-xs">
          <span className="font-mono text-muted-foreground">
            {question.code}
          </span>
          {question.scf_anchor_id ? (
            <>
              <span className="text-border">·</span>
              <span
                data-testid="answer-editor-anchor"
                className="font-mono px-1.5 py-0.5 bg-primary/10 text-primary rounded font-semibold"
              >
                SCF:{question.scf_anchor_id}
              </span>
            </>
          ) : null}
          {question.domain ? (
            <>
              <span className="text-border">·</span>
              <span className="text-muted-foreground">{question.domain}</span>
            </>
          ) : null}
        </div>
        <h2 className="text-lg font-medium text-foreground leading-snug">
          {question.text}
        </h2>
      </div>

      <div className="overflow-y-auto flex-1 px-7 py-5 space-y-5">
        {/* AC-11 — inline retry banner above the textarea */}
        {saveError ? (
          <Alert variant="destructive" data-testid="answer-editor-save-error">
            <AlertDescription className="flex items-center justify-between gap-3">
              <span>Last save failed — {saveError}</span>
              <button
                type="button"
                data-testid="answer-editor-retry"
                onClick={() => {
                  void save();
                }}
                className="text-xs underline hover:opacity-80"
              >
                Retry
              </button>
            </AlertDescription>
          </Alert>
        ) : null}

        {/* Suggestions panel — deterministic SCF-anchor priors (slice 155) */}
        <SuggestionsPanel
          questionnaireID={questionnaireID}
          anchor={question.scf_anchor_id}
          onUseAnswer={(text) => {
            markDirty();
            setNarrative(text); // AC-13 REPLACE (no append)
          }}
        />

        {/* Slice 441 — AI answer suggestion (cited draft, one-click approve).
            The draft is NOT binding until the operator approves it; on approve
            the approved text becomes the stored narrative. */}
        <AISuggestPanel
          questionnaireID={questionnaireID}
          questionID={question.id}
          onApproved={(text) => {
            markDirty();
            setNarrative(text);
          }}
        />

        {/* yes / no / n.a. quick chips */}
        {question.answer_type === "yes_no" ||
        question.answer_type === "yes_no_na" ? (
          <div
            data-testid="answer-editor-value-chips"
            className="flex items-center gap-2 text-xs"
          >
            <span className="text-muted-foreground">Answer:</span>
            {["yes", "no", "n.a."].map((v) => (
              <button
                key={v}
                type="button"
                data-testid={`answer-value-${v}`}
                onClick={() => {
                  markDirty();
                  setAnswerValue(v);
                }}
                className={`px-3 py-1 rounded-md text-sm font-medium border transition-colors ${
                  answerValue === v
                    ? "bg-primary/10 text-primary border-primary"
                    : "bg-card text-foreground border-border hover:bg-muted/40"
                }`}
              >
                {v}
              </button>
            ))}
          </div>
        ) : null}

        {/* narrative textarea */}
        <div>
          <label
            htmlFor="answer-narrative"
            className="block text-xs font-medium text-muted-foreground mb-1"
          >
            Narrative answer
          </label>
          <textarea
            id="answer-narrative"
            data-testid="answer-editor-narrative"
            value={narrative}
            onChange={(e) => {
              markDirty();
              setNarrative(e.target.value);
            }}
            placeholder="Narrative answer with citations to evidence and policies. The operator writes this directly — no AI drafting."
            className="w-full min-h-36 p-3 text-sm border border-border rounded-md bg-card focus:outline-none focus:ring-2 focus:ring-ring"
          />
        </div>

        {/* citations */}
        <div>
          <div className="flex items-center justify-between mb-1">
            <span className="text-[11px] uppercase tracking-wider font-medium text-muted-foreground">
              Citations
            </span>
            <CitationPicker
              onPick={(c) => {
                if (citations.some((x) => x.id === c.id && x.type === c.type))
                  return;
                markDirty();
                setCitations([...citations, c]);
              }}
            />
          </div>
          <CitationChips
            citations={citations}
            onRemove={(c) => {
              markDirty();
              setCitations(
                citations.filter((x) => !(x.id === c.id && x.type === c.type)),
              );
            }}
          />
        </div>

        {/* save-to-library + save status */}
        <div className="flex items-center justify-between gap-4 pt-1 border-t border-border">
          <label
            data-testid="answer-editor-save-to-library"
            className="inline-flex items-center gap-2 text-xs text-muted-foreground cursor-pointer"
          >
            <input
              type="checkbox"
              checked={saveToLibrary}
              onChange={(e) => {
                markDirty();
                setSaveToLibrary(e.target.checked);
              }}
              data-testid="answer-editor-save-to-library-checkbox"
              className="rounded border-border"
            />
            Save as canonical for SCF:{question.scf_anchor_id || "—"}
          </label>
          <span className="text-xs text-muted-foreground">
            {savedAt ? `Saved · ${new Date(savedAt).toLocaleTimeString()}` : ""}
          </span>
        </div>
      </div>
    </div>
  );
}
