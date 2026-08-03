// Slice 757 — pure view-model logic for the batch review queue.
//
// Node-testable (no JSX, no DOM) per the repo's vitest tier convention: the
// constitutional behaviors live here so they are unit-tested rather than
// buried in the component —
//
//   * the queue admits ONLY unapproved AI drafts, and a draft without at
//     least one citation is NOT approvable (P0-757-2 mirror of P0-441-4);
//   * approval requests carry EXACTLY ONE answer id — there is no bulk
//     request builder anywhere in this module (AC-10, P0-757-1);
//   * suppressed rows surface a FIXED reason label only — no model/backend
//     detail ever reaches the copy (P0-757-5, slice-367 leak discipline);
//   * cloud-routing is derived from the draft's recorded model_provider (the
//     FE mirror of qaisuggest.isCloudProvider) so a cloud-routed draft
//     renders its banner even if the tenant config view is stale (AC-4).

import type { Question } from "@/components/questionnaire/types";

// ----- slice-756 run contract (mirrors internal/questionnaire JSON) -----

export interface AnswerRun {
  id: string;
  questionnaire_id: string;
  status: string;
  started_by: string;
  row_cap: number;
  total_count: number;
  drafted_count: number;
  insufficient_count: number;
  suppressed_count: number;
  skipped_count: number;
  error_count: number;
  failure_reason?: string;
}

export interface AnswerRunItem {
  id: string;
  run_id: string;
  questionnaire_id: string;
  question_id: string;
  sort_order: number;
  outcome: string;
  reason_code?: string;
  answer_id?: string;
}

export interface AnswerRunDetail {
  run: AnswerRun;
  items: AnswerRunItem[];
}

// ----- queue view-model -----

export interface CitationLink {
  label: string;
  href: string;
}

export interface ReviewDraft {
  questionId: string;
  questionCode: string;
  questionText: string;
  answerId: string;
  narrative: string;
  citations: CitationLink[];
  // A draft with zero resolved citations must never be approvable
  // (P0-757-2). The server never persists one; this is defense-in-depth.
  canApprove: boolean;
  cloudRouted: boolean;
  modelLabel: string;
  promptVersion: string;
}

// isCloudProvider mirrors internal/qaisuggest.isCloudProvider: anything that
// is not the local Ollama family (or the CI stub) is a cloud provider and
// must render the routing banner.
export function isCloudProvider(provider: string | undefined): boolean {
  switch ((provider ?? "").toLowerCase()) {
    case "":
    case "ollama":
    case "ollama-local":
    case "local":
    case "stub":
      return false;
    default:
      return true;
  }
}

// citationLinks normalizes the two citation shapes that reach
// questionnaire_answers.citations — the AI-draft shape
// `{kind: "policy"|"evidence", id}` (slice 441) and the manual editor shape
// `{type: "controls"|"evidence", id, title}` (slice 268) — into rendered
// links. Policies link to their detail page; evidence and controls link to
// their list surfaces (no evidence detail route exists yet). Unknown shapes
// are dropped rather than rendered as dead links (P0-757-2).
export function citationLinks(citations: unknown): CitationLink[] {
  if (!Array.isArray(citations)) return [];
  const out: CitationLink[] = [];
  for (const raw of citations) {
    if (!raw || typeof raw !== "object") continue;
    const c = raw as { kind?: string; type?: string; id?: string };
    if (typeof c.id !== "string" || c.id.length === 0) continue;
    const kind = c.kind ?? c.type ?? "";
    switch (kind) {
      case "policy":
        out.push({
          label: `policy:${c.id.slice(0, 8)}`,
          href: `/policies/${c.id}`,
        });
        break;
      case "evidence":
        out.push({ label: `evidence:${c.id.slice(0, 8)}`, href: `/evidence` });
        break;
      case "controls":
        out.push({
          label: `control:${c.id.slice(0, 8)}`,
          href: `/controls/${c.id}`,
        });
        break;
      default:
        // Unknown citation shape — drop it (never render a dead link).
        break;
    }
  }
  return out;
}

// modelLabel renders the provenance line: name, version, provider.
export function modelLabel(
  name?: string,
  version?: string,
  provider?: string,
): string {
  if (!name) return "";
  const v = version ? ` v${version}` : "";
  const p = provider ? ` (${provider})` : "";
  return `${name}${v}${p}`;
}

// pendingDrafts extracts the review queue from the questionnaire detail: every
// question whose answer is an UNAPPROVED AI draft, in display order. Approved
// answers have left the queue by construction (AC-7); manual answers never
// enter it.
export function pendingDrafts(questions: Question[]): ReviewDraft[] {
  const out: ReviewDraft[] = [];
  for (const q of questions) {
    const a = q.answer;
    if (!a || a.ai_assisted !== true || a.human_approved === true) continue;
    const citations = citationLinks(a.citations);
    out.push({
      questionId: q.id,
      questionCode: q.code,
      questionText: q.text,
      answerId: a.id,
      narrative: a.narrative,
      citations,
      canApprove: citations.length > 0,
      cloudRouted: isCloudProvider(a.model_provider),
      modelLabel: modelLabel(a.model_name, a.model_version, a.model_provider),
      promptVersion: a.prompt_version ?? "",
    });
  }
  return out;
}

// ----- per-answer request builders (AC-10) -----
//
// These are the ONLY request builders the review surface uses. Each takes a
// single answer id and returns a single-answer payload; no function in this
// module (or the component that consumes it) accepts a list of answer ids.

export interface ApproveRequest {
  answer_id: string;
  narrative: string;
  answer_value: string;
}

export function approveRequestBody(
  answerId: string,
  narrative: string,
): ApproveRequest {
  return { answer_id: answerId, narrative, answer_value: "" };
}

export interface RejectRequest {
  answer_id: string;
}

export function rejectRequestBody(answerId: string): RejectRequest {
  return { answer_id: answerId };
}

// ----- non-draft outcome work lists (AC-6) -----

export type OutcomeFilter =
  | "insufficient_evidence"
  | "skipped_needs_mapping"
  | "suppressed";

export interface OutcomeRow {
  questionId: string;
  questionCode: string;
  questionText: string;
  outcome: string;
  // Fixed reason code only — NEVER model/backend detail (P0-757-5).
  reasonLabel: string;
}

// suppressedReasonLabel maps the fixed qaisuggest reason vocabulary to
// operator copy. Unknown codes render the generic withheld line — the raw
// code is never echoed, so a future backend reason cannot leak detail.
export function suppressedReasonLabel(code: string | undefined): string {
  switch (code) {
    case "unresolved_citation":
      return "Draft withheld: a citation could not be verified.";
    case "no_citations":
      return "Draft withheld: the draft had no citations.";
    case "generation_unavailable":
      return "Draft withheld: generation was unavailable.";
    default:
      return "Draft withheld.";
  }
}

// outcomeRows projects the run items with the given outcome onto their
// questions. Suppressed rows get the fixed reason label; the other lists
// carry their outcome verbatim (their affordance is rendered by the
// component: manual authoring for insufficient, the slice-755 mapping
// affordance for skipped_needs_mapping).
export function outcomeRows(
  detail: AnswerRunDetail | null,
  questions: Question[],
  outcome: OutcomeFilter,
): OutcomeRow[] {
  if (!detail) return [];
  const byId = new Map(questions.map((q) => [q.id, q]));
  const out: OutcomeRow[] = [];
  for (const item of detail.items) {
    if (item.outcome !== outcome) continue;
    const q = byId.get(item.question_id);
    out.push({
      questionId: item.question_id,
      questionCode: q?.code ?? "",
      questionText: q?.text ?? "",
      outcome: item.outcome,
      reasonLabel:
        outcome === "suppressed" ? suppressedReasonLabel(item.reason_code) : "",
    });
  }
  return out;
}

// runProgress renders the run's denormalized counts by outcome (AC-2).
export interface ProgressEntry {
  key: string;
  label: string;
  count: number;
}

export function runProgress(run: AnswerRun): ProgressEntry[] {
  return [
    { key: "drafted", label: "Drafted", count: run.drafted_count },
    {
      key: "insufficient_evidence",
      label: "Insufficient evidence",
      count: run.insufficient_count,
    },
    { key: "suppressed", label: "Suppressed", count: run.suppressed_count },
    { key: "skipped", label: "Skipped", count: run.skipped_count },
    { key: "error", label: "Errors", count: run.error_count },
  ];
}
