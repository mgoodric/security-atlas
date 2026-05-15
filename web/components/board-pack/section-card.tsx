// Slice 043 — one section of a board pack.
//
// Renders the section title with a `§ NN` marker, the approval-status
// badge, the structured data (per section: posture tiles / risk table /
// coverage trend / findings / operational tiles / investment panel /
// asks list), the editable narrative textarea (draft only), the operator
// inputs (the three operator-entered sections only), and Save + Approve
// buttons. Approve + Save are role-gated (AC-3): a non-approver caller
// sees the section read-only with a "requires approver" notice.
//
// A PUBLISHED pack renders the section frozen — no textarea, no buttons,
// just the templated/override text and the structured data (AC-7).

"use client";

import { useMutation } from "@tanstack/react-query";
import { useState } from "react";

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  APIError,
  approveBoardPackSection,
  BoardPack,
  BoardPackSection,
  BoardPackSectionInputs,
  updateBoardPackSection,
} from "@/lib/api";
import { cn } from "@/lib/utils";

import { HumanAuthoredBadge, TemplatedBadge } from "./templated-badge";

type SectionCardProps = {
  index: number;
  packID: string;
  section: BoardPackSection;
  isPublished: boolean;
  canApprove: boolean;
  onMutated: (pack: BoardPack) => void;
  children?: React.ReactNode;
};

// Sections where the operator types structured numbers into the section
// inputs (slice 032 decisions D3 + D5).
const OPERATOR_INPUT_SECTIONS = new Set([
  "operational_metrics",
  "investment",
  "coverage_trend",
]);

// The "asks" section is freeform human-authored — no template, no AI
// (AC-5). It still uses the same approve gate.
const HUMAN_AUTHORED_SECTIONS = new Set(["asks"]);

export function SectionCard({
  index,
  packID,
  section,
  isPublished,
  canApprove,
  onMutated,
  children,
}: SectionCardProps) {
  const [overrideText, setOverrideText] = useState(section.override_text);
  const [inputs, setInputs] = useState<BoardPackSectionInputs>({});

  const save = useMutation({
    mutationFn: () =>
      updateBoardPackSection(packID, section.key, {
        override_text: overrideText,
        inputs: Object.keys(inputs).length > 0 ? inputs : undefined,
      }),
    onSuccess: onMutated,
  });

  const approve = useMutation({
    mutationFn: () => approveBoardPackSection(packID, section.key),
    onSuccess: onMutated,
  });

  const isHumanAuthored = HUMAN_AUTHORED_SECTIONS.has(section.key);
  const showInputs = OPERATOR_INPUT_SECTIONS.has(section.key);
  const effectiveText = section.override_text || section.templated_text;

  return (
    <section
      className="mb-6 rounded-2xl border border-slate-200 bg-white p-7"
      data-testid={`section-card-${section.key}`}
    >
      <header className="mb-5 flex items-baseline justify-between">
        <div className="flex items-baseline gap-2">
          <span className="font-mono text-xs text-slate-400">
            § {String(index).padStart(2, "0")}
          </span>
          <h2 className="text-xl font-semibold tracking-tight">
            {section.title}
          </h2>
        </div>
        <Badge
          className={cn(
            "rounded text-[10px] font-semibold uppercase tracking-wider",
            section.approved
              ? "bg-emerald-50 text-emerald-700 hover:bg-emerald-50"
              : "bg-slate-100 text-slate-600 hover:bg-slate-100",
          )}
          data-testid={`section-status-${section.key}`}
        >
          {section.approved ? "approved" : "not approved"}
        </Badge>
      </header>

      {children && <div className="mb-5">{children}</div>}

      <div className="rounded-lg border border-slate-200 bg-slate-50 p-5">
        <div className="mb-3 flex items-center justify-between">
          {isHumanAuthored ? <HumanAuthoredBadge /> : <TemplatedBadge />}
          {section.approved && (
            <span className="text-xs text-slate-500">approved</span>
          )}
        </div>
        {isPublished || !canApprove ? (
          <p className="whitespace-pre-wrap text-[15px] leading-relaxed text-slate-800">
            {effectiveText || (
              <span className="italic text-slate-400">
                No narrative recorded.
              </span>
            )}
          </p>
        ) : (
          <textarea
            className="min-h-28 w-full rounded-md border border-slate-300 bg-white p-2 text-sm"
            value={overrideText}
            placeholder={section.templated_text}
            onChange={(e) => setOverrideText(e.target.value)}
            data-testid={`section-narrative-${section.key}`}
          />
        )}
      </div>

      {!isPublished && canApprove && showInputs && (
        <OperatorInputs
          sectionKey={section.key}
          inputs={inputs}
          setInputs={setInputs}
        />
      )}

      {!isPublished && !canApprove && (
        <Alert className="mt-4" data-testid={`approver-gate-${section.key}`}>
          <AlertTitle>Read-only view</AlertTitle>
          <AlertDescription>
            Editing and approving sections requires the approver role.
          </AlertDescription>
        </Alert>
      )}

      {!isPublished && canApprove && (
        <div className="mt-4 flex gap-2 print:hidden">
          <Button
            variant="outline"
            size="sm"
            disabled={save.isPending}
            onClick={() => save.mutate()}
            data-testid={`save-section-${section.key}`}
          >
            {save.isPending ? "Saving…" : "Save section"}
          </Button>
          <Button
            size="sm"
            disabled={approve.isPending || section.approved}
            onClick={() => approve.mutate()}
            data-testid={`approve-section-${section.key}`}
          >
            {section.approved
              ? "Approved"
              : approve.isPending
                ? "Approving…"
                : "Approve section"}
          </Button>
        </div>
      )}

      {(save.isError || approve.isError) && (
        <Alert variant="destructive" className="mt-4">
          <AlertTitle>Action failed</AlertTitle>
          <AlertDescription>
            {save.error instanceof APIError
              ? save.error.message
              : approve.error instanceof APIError
                ? approve.error.message
                : "Unexpected error."}
          </AlertDescription>
        </Alert>
      )}
    </section>
  );
}

function OperatorInputs({
  sectionKey,
  inputs,
  setInputs,
}: {
  sectionKey: string;
  inputs: BoardPackSectionInputs;
  setInputs: (i: BoardPackSectionInputs) => void;
}) {
  type InputField = { key: keyof BoardPackSectionInputs; label: string };
  const fields: InputField[] =
    sectionKey === "investment"
      ? [{ key: "spend_usd", label: "Security spend this quarter ($)" }]
      : sectionKey === "coverage_trend"
        ? [
            {
              key: "baseline_coverage_pct",
              label: "Prior-quarter coverage baseline (%)",
            },
          ]
        : [
            { key: "phishing_pass_rate_pct", label: "Phishing pass rate (%)" },
            { key: "p1_patch_median_days", label: "P1 patch median (days)" },
            { key: "incident_count", label: "Incident count" },
            { key: "vendor_reviews_on_time", label: "Vendor reviews on time" },
            { key: "vendor_reviews_total", label: "Vendor reviews total" },
          ];

  return (
    <div
      className="mt-4 space-y-2 rounded-md bg-slate-50 p-3"
      data-testid={`operator-inputs-${sectionKey}`}
    >
      <p className="text-xs font-medium text-slate-600">
        Operator-entered (no automated connector for v1)
      </p>
      <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
        {fields.map((f) => (
          <div key={f.key} className="space-y-1">
            <label className="text-xs text-slate-600">{f.label}</label>
            <Input
              type="number"
              value={inputs[f.key] ?? ""}
              onChange={(e) => {
                const raw = e.target.value;
                const next = { ...inputs };
                if (raw === "") {
                  delete next[f.key];
                } else {
                  next[f.key] = Number(raw);
                }
                setInputs(next);
              }}
              data-testid={`operator-input-${sectionKey}-${f.key}`}
            />
          </div>
        ))}
      </div>
    </div>
  );
}
