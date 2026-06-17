// Slice 441 — AI-answer suggestion panel (the FIRST AI-write surface).
//
// Surface (review UI affordance, AC-10/AC-11/AC-12):
//   - "Suggest answer (AI)" button → POST .../ai-suggest
//   - On a drafted suggestion: the cited draft renders in an editable textarea,
//     its resolved citations shown as chips, with an Approve button.
//   - The Approve button is DISABLED unless the draft has a resolved citation
//     (canApprove) — the operator cannot approve a draft with an unresolved
//     citation (AC-11, P0-441-4). Approval POSTs .../ai-approve with the
//     operator's (possibly edited) text.
//   - A visible banner renders when the generation was cloud-routed
//     (CLAUDE.md inference-backend rule). In v0 inference is local Ollama only.
//   - Insufficient-evidence / suppressed outcomes render a "answer manually"
//     message and NO approvable draft (AC-5).
//
// The draft is clearly marked AI + non-binding until approved. On approve, the
// parent's onApproved callback writes the approved text into the manual editor
// so the questionnaire stores it.

"use client";

import { useState } from "react";

import { CloudRoutingBanner } from "@/components/llm/cloud-routing-banner";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import {
  type AISuggestViewModel,
  parseAISuggest,
} from "@/lib/questionnaire/ai-suggest";

interface AISuggestPanelProps {
  questionnaireID: string;
  questionID: string;
  // Called with the approved narrative once the operator approves the draft,
  // so the parent editor stores it as the question's answer.
  onApproved: (narrative: string) => void;
}

export function AISuggestPanel({
  questionnaireID,
  questionID,
  onApproved,
}: AISuggestPanelProps) {
  const [vm, setVm] = useState<AISuggestViewModel | null>(null);
  const [editDraft, setEditDraft] = useState("");
  const [loading, setLoading] = useState(false);
  const [approving, setApproving] = useState(false);
  const [approveError, setApproveError] = useState<string | null>(null);
  const [approvedAt, setApprovedAt] = useState<string | null>(null);

  async function suggest(): Promise<void> {
    setLoading(true);
    setApproveError(null);
    setApprovedAt(null);
    try {
      const res = await fetch(
        `/api/questionnaires/${questionnaireID}/answers/${questionID}/ai-suggest`,
        { method: "POST" },
      );
      let raw: unknown = null;
      try {
        raw = await res.json();
      } catch {
        raw = null;
      }
      const next = parseAISuggest(res.ok, res.status, raw);
      setVm(next);
      setEditDraft(next.draft);
    } finally {
      setLoading(false);
    }
  }

  async function approve(): Promise<void> {
    if (!vm || !vm.canApprove) return;
    setApproving(true);
    setApproveError(null);
    try {
      const res = await fetch(
        `/api/questionnaires/${questionnaireID}/answers/${questionID}/ai-approve`,
        {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({
            answer_id: vm.answerId,
            narrative: editDraft,
            answer_value: "",
          }),
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
        setApproveError(msg);
        return;
      }
      setApprovedAt(new Date().toISOString());
      onApproved(editDraft); // store the approved text in the manual editor
    } finally {
      setApproving(false);
    }
  }

  return (
    <div
      data-testid="ai-suggest-panel"
      className="rounded-md border border-border bg-muted/20 p-3 space-y-3"
    >
      <div className="flex items-center justify-between">
        <span className="text-[11px] uppercase tracking-wider font-medium text-muted-foreground">
          AI answer suggestion
        </span>
        <button
          type="button"
          data-testid="ai-suggest-button"
          disabled={loading}
          onClick={() => {
            void suggest();
          }}
          className="text-xs px-3 py-1 rounded-md border border-border bg-card hover:bg-muted/40 disabled:opacity-50"
        >
          {loading ? "Drafting…" : "Suggest answer (AI)"}
        </button>
      </div>

      {/* Slice 499: config-driven routing banner. Shown whenever the tenant is
          on a cloud provider — at draft-generation time AND draft-review time
          (AC-8), driven by the tenant routing config, not hardcoded here. The
          reusable component self-resolves via the routing config and renders
          nothing on local-ollama (AC-7). */}
      <CloudRoutingBanner />

      {/* Defense-in-depth: the per-draft cloud flag from the generation result
          (the actual provider that served THIS draft). Redundant with the
          config-driven banner above on a steady cloud config, but it catches a
          draft that was cloud-routed even if the config view is momentarily
          stale. */}
      {vm && vm.cloudRouted ? (
        <Alert data-testid="ai-suggest-cloud-banner">
          <AlertTitle>Cloud LLM routing</AlertTitle>
          <AlertDescription>
            This draft was generated by a cloud LLM, not the local model. Tenant
            data left the deployment for this request.
          </AlertDescription>
        </Alert>
      ) : null}

      {vm && (vm.status === "insufficient" || vm.status === "suppressed") ? (
        <Alert data-testid="ai-suggest-manual">
          <AlertDescription>{vm.message}</AlertDescription>
        </Alert>
      ) : null}

      {vm && vm.status === "error" ? (
        <Alert variant="destructive" data-testid="ai-suggest-error">
          <AlertDescription>{vm.message}</AlertDescription>
        </Alert>
      ) : null}

      {vm && vm.status === "drafted" ? (
        <div className="space-y-2" data-testid="ai-suggest-draft">
          <div className="flex items-center gap-2 text-[11px]">
            <span className="px-1.5 py-0.5 rounded bg-amber-500/15 text-amber-700 font-semibold">
              AI DRAFT · NOT APPROVED
            </span>
            {vm.modelLabel ? (
              <span className="text-muted-foreground font-mono">
                {vm.modelLabel}
              </span>
            ) : null}
          </div>

          <textarea
            data-testid="ai-suggest-draft-text"
            value={editDraft}
            onChange={(e) => setEditDraft(e.target.value)}
            className="w-full min-h-28 p-3 text-sm border border-border rounded-md bg-card focus:outline-none focus:ring-2 focus:ring-ring"
          />

          <div
            data-testid="ai-suggest-citations"
            className="flex flex-wrap gap-1.5"
          >
            {vm.citations.map((c) => (
              <span
                key={`${c.kind}:${c.id}`}
                className="text-[11px] font-mono px-1.5 py-0.5 rounded bg-primary/10 text-primary"
              >
                {c.kind}:{c.id.slice(0, 8)}
              </span>
            ))}
          </div>

          {approveError ? (
            <Alert variant="destructive" data-testid="ai-suggest-approve-error">
              <AlertDescription>{approveError}</AlertDescription>
            </Alert>
          ) : null}

          <div className="flex items-center justify-between">
            <button
              type="button"
              data-testid="ai-suggest-approve"
              disabled={!vm.canApprove || approving}
              onClick={() => {
                void approve();
              }}
              className="text-xs px-3 py-1 rounded-md border border-primary bg-primary/10 text-primary font-medium hover:bg-primary/20 disabled:opacity-50"
            >
              {approving ? "Approving…" : "Approve answer"}
            </button>
            <span className="text-xs text-muted-foreground">
              {approvedAt
                ? `Approved · ${new Date(approvedAt).toLocaleTimeString()}`
                : "Review before approving"}
            </span>
          </div>
        </div>
      ) : null}
    </div>
  );
}
