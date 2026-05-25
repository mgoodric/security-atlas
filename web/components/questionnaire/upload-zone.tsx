// Slice 263 — Stage A upload zone (drag-drop + file-picker for .xlsx).
//
// Closes ISC-14..ISC-19 (slice 263):
//   - accepts drag-drop AND file-picker selection
//   - rejects files > 5MB client-side (UX courtesy; backend also caps)
//   - rejects non-.xlsx files client-side
//   - calls the parent's onUpload callback with the validated File
//   - renders a loading spinner replacing the CTA while a parent
//     upload is in flight
//
// Pure presentation — the actual two-step (create-questionnaire +
// import-excel) flow lives in the parent list page so this component
// stays reusable should an empty-state-only or modal-only variant
// land later.
//
// AI-assist boundary (P0-263-1): this surface is operator-driven
// drag-drop only. No model-driven inference, no AI suggestion of
// templates or sources.

"use client";

import { useRef, useState } from "react";

const MAX_BYTES_5MB = 5 * 1024 * 1024; // platform's questionnaire.MaxUploadBytes
const ACCEPT_EXT = ".xlsx";
const ACCEPT_MIME =
  "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet";

export type UploadValidationError =
  | { kind: "too_large"; sizeBytes: number }
  | { kind: "wrong_extension"; filename: string };

export function validateFile(file: File): UploadValidationError | null {
  if (file.size > MAX_BYTES_5MB) {
    return { kind: "too_large", sizeBytes: file.size };
  }
  // .xlsx by extension OR by mime type; many browsers omit the mime,
  // so extension is the primary check.
  const lower = file.name.toLowerCase();
  if (!lower.endsWith(ACCEPT_EXT)) {
    return { kind: "wrong_extension", filename: file.name };
  }
  return null;
}

export function formatValidationError(err: UploadValidationError): string {
  switch (err.kind) {
    case "too_large":
      return "File too large — 5MB maximum.";
    case "wrong_extension":
      return "Only .xlsx files are accepted at this time.";
  }
}

interface UploadZoneProps {
  busy: boolean;
  onFile: (file: File) => void;
  onValidationError: (msg: string) => void;
}

export function UploadZone({
  busy,
  onFile,
  onValidationError,
}: UploadZoneProps) {
  const inputRef = useRef<HTMLInputElement>(null);
  const [dragOver, setDragOver] = useState(false);

  function handleFile(file: File): void {
    const err = validateFile(file);
    if (err) {
      onValidationError(formatValidationError(err));
      return;
    }
    onFile(file);
  }

  function openPicker(): void {
    if (busy) return;
    inputRef.current?.click();
  }

  return (
    <div
      data-testid="questionnaire-upload-zone"
      onClick={openPicker}
      onKeyDown={(e) => {
        if (e.key === "Enter" || e.key === " ") {
          e.preventDefault();
          openPicker();
        }
      }}
      onDragOver={(e) => {
        e.preventDefault();
        if (!busy) setDragOver(true);
      }}
      onDragLeave={() => setDragOver(false)}
      onDrop={(e) => {
        e.preventDefault();
        setDragOver(false);
        if (busy) return;
        const file = e.dataTransfer.files?.[0];
        if (file) handleFile(file);
      }}
      role="button"
      tabIndex={0}
      aria-disabled={busy}
      aria-label="Upload Excel questionnaire"
      className={`bg-card border-2 border-dashed rounded-xl px-8 py-10 text-center cursor-pointer transition-colors ${
        busy
          ? "border-border opacity-60 pointer-events-none"
          : dragOver
            ? "border-primary bg-primary/5"
            : "border-border hover:border-primary/60 hover:bg-muted/30"
      }`}
    >
      <input
        ref={inputRef}
        type="file"
        accept={`${ACCEPT_EXT},${ACCEPT_MIME}`}
        data-testid="questionnaire-upload-input"
        onChange={(e) => {
          const file = e.target.files?.[0];
          if (file) handleFile(file);
          // Reset input so re-selecting the same file fires onChange.
          if (inputRef.current) inputRef.current.value = "";
        }}
        className="hidden"
      />
      {busy ? (
        <div
          data-testid="questionnaire-upload-spinner"
          className="flex items-center justify-center gap-2 text-sm text-muted-foreground"
        >
          <span className="inline-block w-4 h-4 rounded-full border-2 border-muted-foreground/30 border-t-foreground animate-spin" />
          Uploading…
        </div>
      ) : (
        <>
          <UploadIcon />
          <div className="text-sm font-medium text-foreground mt-2 mb-1">
            Upload your first vendor questionnaire (Excel)
          </div>
          <div className="text-xs text-muted-foreground">
            Drop an .xlsx file here, or click to browse · max 5 MB
          </div>
        </>
      )}
    </div>
  );
}

function UploadIcon() {
  return (
    <svg
      className="w-8 h-8 mx-auto text-muted-foreground"
      viewBox="0 0 20 20"
      fill="currentColor"
      aria-hidden
    >
      <path d="M5.5 17a4.5 4.5 0 01-1.44-8.765 4.5 4.5 0 018.302-3.046 3.5 3.5 0 014.504 4.272A4 4 0 0115 17H5.5zm3.75-2.75a.75.75 0 001.5 0V9.66l1.95 2.1a.75.75 0 101.1-1.02l-3.25-3.5a.75.75 0 00-1.1 0l-3.25 3.5a.75.75 0 101.1 1.02l1.95-2.1v4.59z" />
    </svg>
  );
}
