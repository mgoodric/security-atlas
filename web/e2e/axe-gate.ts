// #2097 — the axe gate the route sweep enforces.
//
// One definition of "an axe failure" (a serious or critical violation),
// shared by `route-sweep.spec.ts` and the self-test in
// `axe-gate.spec.ts`, so the fixture that proves the gate fails is
// checking the same code the sweep runs.

import AxeBuilder from "@axe-core/playwright";
import { expect, type Page } from "@playwright/test";

export type AxeFinding = {
  id: string;
  impact: string | null | undefined;
  help: string;
  helpUrl: string;
  nodes: { target: string; summary: string }[];
};

// Serious + critical only. Moderate and minor stay out of the gate.
export async function seriousAxeViolations(page: Page): Promise<AxeFinding[]> {
  const results = await new AxeBuilder({ page }).analyze();
  return results.violations
    .filter((v) => v.impact === "serious" || v.impact === "critical")
    .map((v) => ({
      id: v.id,
      impact: v.impact,
      help: v.help,
      helpUrl: v.helpUrl,
      nodes: v.nodes.map((n) => ({
        target: n.target.join(" "),
        summary: n.failureSummary ?? "",
      })),
    }));
}

export function describeAxe(findings: AxeFinding[]): string {
  return findings
    .map(
      (f) =>
        `  ${f.impact} ${f.id}: ${f.help} (${f.helpUrl})\n` +
        f.nodes
          .map(
            (n) => `    - ${n.target}\n      ${n.summary.replace(/\n/g, " ")}`,
          )
          .join("\n"),
    )
    .join("\n");
}

// Fails with every finding named when the list is not empty.
export function expectNoSeriousAxe(findings: AxeFinding[], label: string) {
  expect(
    findings,
    `${label}: ${
      findings.length
    } serious/critical axe violation(s)\n${describeAxe(findings)}`,
  ).toEqual([]);
}
