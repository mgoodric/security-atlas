// Slice 178 — report writer (AC-6). Produces a deterministic JSON
// report AND a Markdown summary, both keyed off the same Finding[]
// shape from `mockup-diff.ts`. Determinism is enforced by
// `sortFindings` upstream.

import { mkdirSync, writeFileSync } from "node:fs";
import { dirname } from "node:path";

import { execSync } from "node:child_process";

import type { Finding } from "./mockup-diff";
import { summarize } from "./mockup-diff";

export type ReportContext = {
  /** ISO-8601 timestamp at the start of the audit run. */
  startedAt: string;
  /** Short git SHA so the report ties to a specific tree. */
  gitSha: string;
  /** Harness version (slice number + harness path). */
  harnessVersion: string;
  /** Base URL the harness ran against (PLATFORM_BASE_URL or default). */
  baseURL: string;
  /** True if the harness ran against a non-localhost target. */
  isRemoteRun: boolean;
};

export function readGitSha(): string {
  try {
    return execSync("git rev-parse --short HEAD", {
      encoding: "utf-8",
    }).trim();
  } catch {
    return "unknown";
  }
}

export function buildContext(baseURL: string): ReportContext {
  return {
    startedAt: new Date().toISOString(),
    gitSha: readGitSha(),
    harnessVersion: "slice-178 web/e2e-audit",
    baseURL,
    isRemoteRun: !baseURL.startsWith("http://localhost"),
  };
}

export function writeJSONReport(
  path: string,
  ctx: ReportContext,
  findings: Finding[],
): void {
  const sum = summarize(findings);
  const payload = {
    harness: {
      slice: 178,
      version: ctx.harnessVersion,
      gitSha: ctx.gitSha,
      startedAt: ctx.startedAt,
      baseURL: ctx.baseURL,
    },
    summary: sum,
    findings,
  };
  mkdirSync(dirname(path), { recursive: true });
  writeFileSync(path, JSON.stringify(payload, null, 2) + "\n", "utf-8");
}

export function writeMarkdownReport(
  path: string,
  ctx: ReportContext,
  findings: Finding[],
): void {
  const sum = summarize(findings);
  const lines: string[] = [];
  lines.push(`# UI honesty audit — ${ctx.startedAt}`);
  lines.push("");
  lines.push(
    `_Harness: ${ctx.harnessVersion} · git ${ctx.gitSha} · target ${ctx.baseURL}_`,
  );
  lines.push("");
  if (ctx.isRemoteRun) {
    lines.push(
      "**Operator-local prod run** — do NOT commit this report. Reports of " +
        "non-localhost runs stay local per P0-178-3 (the harness's " +
        "`reports/local-prod/` directory is `.gitignore`d).",
    );
    lines.push("");
  }
  lines.push("## Summary");
  lines.push("");
  lines.push("| total | HONESTY-GAP | SHIP-GAP | MOCKUP-STALE |");
  lines.push("| ----- | ----------- | -------- | ------------ |");
  lines.push(
    `| ${sum.total} | ${sum.honestyGap} | ${sum.shipGap} | ${sum.mockupStale} |`,
  );
  lines.push("");

  if (Object.keys(sum.byRoute).length > 0) {
    lines.push("## Findings per route");
    lines.push("");
    lines.push("| route | findings |");
    lines.push("| ----- | -------- |");
    for (const r of Object.keys(sum.byRoute).sort()) {
      lines.push(`| \`${r}\` | ${sum.byRoute[r]} |`);
    }
    lines.push("");
  }

  if (findings.length > 0) {
    lines.push("## Findings (sorted by category)");
    lines.push("");
    for (const f of findings) {
      lines.push(`### ${f.category} · \`${f.route}\` · \`${f.subject}\``);
      lines.push("");
      if (f.mockupPath) {
        lines.push(`- Mockup: \`Plans/_archive/mockups/${f.mockupPath}\``);
      }
      if (f.details) {
        lines.push(`- Details: ${f.details}`);
      }
      lines.push(`- Suggested action: ${f.suggestedAction}`);
      lines.push("");
    }
  } else {
    lines.push("_No findings on this run._");
    lines.push("");
  }

  mkdirSync(dirname(path), { recursive: true });
  writeFileSync(path, lines.join("\n"), "utf-8");
}
