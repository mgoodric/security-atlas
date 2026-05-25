// Slice 263 — shared client-side types for the authoring view.
//
// These mirror the Go structs at internal/questionnaire/store.go +
// internal/questionnaire/library.go. The Suggestion shape uses
// capitalized field names because the Go struct has no JSON tags —
// encoding/json defaults to the Go field name verbatim.

export interface Questionnaire {
  id: string;
  name: string;
  source_label?: string;
  source_filename?: string;
  status: string;
  notes?: string;
  created_at?: string;
  updated_at?: string;
}

export interface Answer {
  id: string;
  answer_value: string;
  narrative: string;
  citations: Citation[];
  authored_by?: string;
}

export interface Question {
  id: string;
  code: string;
  text: string;
  domain: string;
  answer_type: string;
  scf_anchor_id: string;
  sort_order: number;
  needs_mapping: boolean;
  answer?: Answer;
}

export interface QuestionnaireDetail {
  questionnaire: Questionnaire;
  questions: Question[];
}

// Suggestion mirrors internal/questionnaire/library.go `Suggestion`.
// The Go struct has no JSON tags so fields ship with their Go names.
export interface Suggestion {
  ID: string;
  ScfAnchorID: string;
  CanonicalText: string;
  SourceLabel: string;
  UpdatedAt: string;
}

// Citation matches slice 268's unified-search hit shape (controls /
// evidence). The platform stores citations as []any so any shape that
// round-trips through JSON.parse is accepted; we standardize on this
// minimal envelope for what the FE writes.
export interface Citation {
  id: string;
  type: "controls" | "evidence";
  title: string;
}
