// Slice 042 — annotation draft store (AC-7 / P0-3 mechanism).
//
// AC-7: "Tab between controls without losing in-progress sample
// annotations." P0-3: "Does NOT lose in-progress sample annotations on
// tab switch."
//
// The mechanism: in-progress annotation drafts live in a React context
// provider mounted ABOVE the workspace tabs. The provider is keyed by
// `${sampleId}:${evidenceRecordId}` so every (sample, record) pair has
// its own independent draft slot. Because the provider lives above the
// tab content — and the workspace toggles tab panels via CSS visibility
// rather than unmounting them — a draft typed on Control A's sample
// survives navigating to Control B and back.
//
// This is deliberately NOT useEffect-synced and NOT persisted to
// localStorage: the drafts are session-scoped working state. Sign-out
// (AC-6) clears the session cookie and a full reload drops the provider,
// which is the correct behavior — a fresh sign-in starts with no drafts.

"use client";

import {
  createContext,
  useCallback,
  useContext,
  useMemo,
  useState,
} from "react";

import type { AnnotationResult } from "@/lib/api/audit";

export type AnnotationDraft = {
  result: AnnotationResult | "";
  notes: string;
};

const EMPTY_DRAFT: AnnotationDraft = { result: "", notes: "" };

type DraftStore = {
  getDraft: (sampleID: string, evidenceRecordID: string) => AnnotationDraft;
  setDraft: (
    sampleID: string,
    evidenceRecordID: string,
    draft: AnnotationDraft,
  ) => void;
  clearDraft: (sampleID: string, evidenceRecordID: string) => void;
  hasDraft: (sampleID: string, evidenceRecordID: string) => boolean;
};

const AnnotationDraftContext = createContext<DraftStore | null>(null);

function draftKey(sampleID: string, evidenceRecordID: string): string {
  return `${sampleID}:${evidenceRecordID}`;
}

export function AnnotationDraftProvider({
  children,
}: {
  children: React.ReactNode;
}) {
  // A flat map keyed by `${sampleId}:${recordId}`. Living here — above the
  // tab content — is what makes the drafts survive tab switches.
  const [drafts, setDrafts] = useState<Record<string, AnnotationDraft>>({});

  const getDraft = useCallback(
    (sampleID: string, evidenceRecordID: string): AnnotationDraft => {
      return drafts[draftKey(sampleID, evidenceRecordID)] ?? EMPTY_DRAFT;
    },
    [drafts],
  );

  const setDraft = useCallback(
    (
      sampleID: string,
      evidenceRecordID: string,
      draft: AnnotationDraft,
    ): void => {
      setDrafts((prev) => ({
        ...prev,
        [draftKey(sampleID, evidenceRecordID)]: draft,
      }));
    },
    [],
  );

  const clearDraft = useCallback(
    (sampleID: string, evidenceRecordID: string): void => {
      setDrafts((prev) => {
        const next = { ...prev };
        delete next[draftKey(sampleID, evidenceRecordID)];
        return next;
      });
    },
    [],
  );

  const hasDraft = useCallback(
    (sampleID: string, evidenceRecordID: string): boolean => {
      const d = drafts[draftKey(sampleID, evidenceRecordID)];
      return d !== undefined && (d.result !== "" || d.notes.trim() !== "");
    },
    [drafts],
  );

  const value = useMemo<DraftStore>(
    () => ({ getDraft, setDraft, clearDraft, hasDraft }),
    [getDraft, setDraft, clearDraft, hasDraft],
  );

  return (
    <AnnotationDraftContext.Provider value={value}>
      {children}
    </AnnotationDraftContext.Provider>
  );
}

export function useAnnotationDrafts(): DraftStore {
  const ctx = useContext(AnnotationDraftContext);
  if (!ctx) {
    throw new Error(
      "useAnnotationDrafts must be used within an AnnotationDraftProvider",
    );
  }
  return ctx;
}
