// Slice 471 — Playwright E2E for the role-scoped checklist generator (AC-15).
//
// Hermetic: every BFF proxy under /api/controls/checklist is page.route()-mocked
// (the slice-594 hermetic-mock discipline — the e2e tier asserts the FRONTEND
// contract; the real RLS + model-stub + cross-tenant behaviour is the Go
// integration tier's job). The flow asserted:
//
//   (a) Generate renders the role-sectioned cited draft with the non-binding
//       disclosure, and the export button is DISABLED before any approval
//       (P0-471-1).
//   (b) Approving the infra section flips it to "Approved" and enables export.
//   (c) The suppressed security section shows its honest note + no approve
//       button.
//   (d) A control with no evidence shows the "no evidence yet" marker (AC-6).

import { expect, test } from "./fixtures";

const GEN_ID = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa";
const INFRA_SEC = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb";
const CTRL_A = "cccccccc-cccc-4ccc-8ccc-cccccccccccc";
const CTRL_B = "dddddddd-dddd-4ddd-8ddd-dddddddddddd";

const draftBody = {
  generation_id: GEN_ID,
  cloud_routed: false,
  binding: false,
  disclosure:
    "AI-assisted draft — review before use. Not an audit artifact until each section is approved.",
  sections: [
    {
      section_id: INFRA_SEC,
      role: "infra",
      ai_assisted: true,
      human_approved: false,
      suppressed: false,
      model_name: "llama3.1:8b-instruct-q5",
      model_version: "1",
      model_provider: "ollama-local",
      cloud_routed: false,
      items: [
        {
          control_id: CTRL_A,
          control_title: "MFA on cloud accounts",
          task: `Enable MFA on all infra accounts (${CTRL_A}).`,
          no_evidence: false,
          citations: [{ kind: "control", id: CTRL_A, ref: CTRL_A }],
        },
        {
          control_id: CTRL_B,
          control_title: "Encrypted backups",
          task: `Establish and capture backup evidence (${CTRL_B}).`,
          no_evidence: true,
          citations: [{ kind: "control", id: CTRL_B, ref: CTRL_B }],
        },
      ],
    },
    {
      section_id: "",
      role: "security",
      ai_assisted: true,
      human_approved: false,
      suppressed: true,
      reason: "unresolved_citation",
      cloud_routed: false,
      items: [],
    },
  ],
};

function approvedBody() {
  const b = JSON.parse(JSON.stringify(draftBody));
  b.sections[0].human_approved = true;
  b.sections[0].human_approver = "key_grc_engineer";
  return b;
}

test.describe("role-scoped checklist generator", () => {
  test("generate → review → approve infra section → export enabled", async ({
    authedPage,
  }) => {
    let loadAfterApprove = false;

    await authedPage.route(
      "**/api/controls/checklist/generate",
      async (route) => {
        await route.fulfill({
          status: 201,
          contentType: "application/json",
          body: JSON.stringify(draftBody),
        });
      },
    );

    await authedPage.route(
      `**/api/controls/checklist/sections/${INFRA_SEC}/approve`,
      async (route) => {
        loadAfterApprove = true;
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({
            section_id: INFRA_SEC,
            role: "infra",
            human_approved: true,
            human_approver: "key_grc_engineer",
          }),
        });
      },
    );

    // The re-load after approval returns the approved-state body.
    await authedPage.route(
      `**/api/controls/checklist/${GEN_ID}`,
      async (route) => {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify(loadAfterApprove ? approvedBody() : draftBody),
        });
      },
    );

    await authedPage.goto("/controls/checklist");

    // Generate.
    await authedPage.getByTestId("generate-checklist").click();
    await expect(authedPage.getByTestId("checklist-body")).toBeVisible();

    // Non-binding disclosure present (label honesty, AC-12).
    await expect(
      authedPage.getByTestId("non-binding-disclosure"),
    ).toContainText("review before use");

    // Infra section renders with both items; the no-evidence item is marked.
    await expect(authedPage.getByTestId("section-infra")).toBeVisible();
    await expect(authedPage.getByTestId("no-evidence-infra-1")).toBeVisible();

    // Suppressed security section shows its honest note, no approve button.
    await expect(authedPage.getByTestId("suppressed-security")).toContainText(
      "could not be verified",
    );
    await expect(authedPage.getByTestId("approve-security")).toHaveCount(0);

    // Export is DISABLED before any approval (P0-471-1).
    await expect(authedPage.getByTestId("export-markdown")).toBeDisabled();

    // Approve the infra section.
    await authedPage.getByTestId("approve-infra").click();

    // It flips to approved + the approved badge shows.
    await expect(authedPage.getByTestId("approved-badge-infra")).toBeVisible();

    // Export is now ENABLED.
    await expect(authedPage.getByTestId("export-markdown")).toBeEnabled();
  });
});
