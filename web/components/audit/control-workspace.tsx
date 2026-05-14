// Slice 042 — per-control workspace (AC-3, AC-4, AC-5, AC-7).
//
// A single-page tabbed layout for one control: Sampling | Walkthrough |
// Comments. The tabs are the three first-class auditor activities on a
// control from canvas §8.3.
//
// AC-7 / P0-3 — "tab between controls without losing in-progress sample
// annotations": the annotation DRAFTS live in the AnnotationDraftProvider
// mounted at the page level (above this component), keyed by
// `${sampleId}:${recordId}`. Additionally, the three tab panels here
// toggle via CSS (`hidden` class) rather than conditional rendering — so
// the Sampling panel's React subtree, including every SampleAnnotation
// input, stays MOUNTED when the auditor flips to Walkthrough or Comments.
// Nothing is lost on an in-control tab switch, and the page-level
// provider covers cross-control navigation.

"use client";

import { useState } from "react";

import { cn } from "@/lib/utils";
import type { AuditPeriod } from "@/lib/api/audit";
import { SamplePanel } from "@/components/audit/sample-panel";
import { WalkthroughRecorder } from "@/components/audit/walkthrough-recorder";
import { CommentThread } from "@/components/audit/comment-thread";

type TabKey = "sampling" | "walkthrough" | "comments";

const TABS: { key: TabKey; label: string }[] = [
  { key: "sampling", label: "Sampling" },
  { key: "walkthrough", label: "Walkthrough" },
  { key: "comments", label: "Comments" },
];

export function ControlWorkspace({
  controlId,
  period,
  callerUserId,
}: {
  controlId: string;
  period: AuditPeriod;
  callerUserId?: string;
}) {
  const [tab, setTab] = useState<TabKey>("sampling");

  return (
    <div data-testid="control-workspace" className="grid gap-4">
      <div>
        <h1 className="text-lg font-semibold tracking-tight">
          Control workspace
        </h1>
        <code className="text-xs text-muted-foreground">{controlId}</code>
      </div>

      <div
        role="tablist"
        aria-label="Control activities"
        className="flex gap-1 border-b"
      >
        {TABS.map((t) => (
          <button
            key={t.key}
            type="button"
            role="tab"
            aria-selected={tab === t.key}
            data-testid={`tab-${t.key}`}
            onClick={() => setTab(t.key)}
            className={cn(
              "-mb-px border-b-2 px-3 py-1.5 text-sm transition-colors",
              tab === t.key
                ? "border-primary font-medium text-foreground"
                : "border-transparent text-muted-foreground hover:text-foreground",
            )}
          >
            {t.label}
          </button>
        ))}
      </div>

      {/*
        All three panels stay MOUNTED — visibility is toggled with the
        `hidden` class. This is the in-control half of the AC-7
        guarantee: flipping tabs never unmounts the SampleAnnotation
        inputs, so an in-progress annotation is never discarded.
      */}
      <div
        role="tabpanel"
        data-testid="panel-sampling"
        hidden={tab !== "sampling"}
        className={cn(tab !== "sampling" && "hidden")}
      >
        <SamplePanel controlId={controlId} />
      </div>
      <div
        role="tabpanel"
        data-testid="panel-walkthrough"
        hidden={tab !== "walkthrough"}
        className={cn(tab !== "walkthrough" && "hidden")}
      >
        <WalkthroughRecorder
          controlId={controlId}
          auditPeriodId={period.audit_period_id}
        />
      </div>
      <div
        role="tabpanel"
        data-testid="panel-comments"
        hidden={tab !== "comments"}
        className={cn(tab !== "comments" && "hidden")}
      >
        <CommentThread
          auditPeriodId={period.audit_period_id}
          controlId={controlId}
          callerUserId={callerUserId}
        />
      </div>
    </div>
  );
}
