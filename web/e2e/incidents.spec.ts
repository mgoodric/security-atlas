import { expect, test } from "./fixtures";
import { seedFromFixture } from "./seed";

const RESOLVED_INCIDENT = "Fixture resolved incident";
const DETECTED_INCIDENT = "Fixture detected incident";
const ALT_TENANT_CANARY = "Alt-tenant incident canary";

test.describe("incident register", () => {
  test.beforeAll(() => {
    seedFromFixture("incidents");
  });

  test("signed-in tenant user can list incidents, open detail, and close with postmortem", async ({
    authedPage: page,
  }) => {
    const listResp = page.waitForResponse(
      (r) => r.url().includes("/api/incidents") && r.status() === 200,
    );
    await page.goto("/incidents");
    await listResp;

    await expect(
      page.getByRole("heading", { name: "Incidents" }),
    ).toBeVisible();
    await expect(page.getByText(RESOLVED_INCIDENT)).toBeVisible();
    await expect(page.getByText(DETECTED_INCIDENT)).toBeVisible();
    await expect(page.getByText(ALT_TENANT_CANARY)).toHaveCount(0);
    await expect(
      page.getByTestId("incidents-row-linked-counts").first(),
    ).toBeVisible();

    await page.getByText(RESOLVED_INCIDENT).click();
    await expect(page).toHaveURL(
      /\/incidents\/63363363-6336-6336-6336-633633633001/,
    );

    await expect(page.getByTestId("incident-detail-status")).toContainText(
      "Resolved",
    );
    await expect(page.getByTestId("incident-detail-severity")).toContainText(
      "P1",
    );
    await expect(
      page.getByTestId("incident-detail-affected-systems"),
    ).toContainText("api-prod");
    await expect(page.getByTestId("incident-detail-links")).toContainText(
      "Controls",
    );
    await expect(page.getByTestId("incident-detail-links")).toContainText(
      "Risks",
    );
    await expect(page.getByTestId("incident-detail-links")).toContainText(
      "Vendors",
    );
    await expect(page.getByTestId("incident-detail-links")).toContainText(
      "Evidence",
    );

    const times = await page
      .getByTestId("incident-timeline-entry")
      .locator("time")
      .allTextContents();
    expect(times).toEqual([...times].sort());
    await expect(
      page.getByRole("button", { name: "Mark Triaged" }),
    ).toHaveCount(0);

    await page
      .getByTestId("incident-close-postmortem")
      .fill("Postmortem captured by OE-633 e2e.");
    const closeResp = page.waitForResponse(
      (r) =>
        r
          .url()
          .includes(
            "/api/incidents/63363363-6336-6336-6336-633633633001/close",
          ) && r.status() === 200,
    );
    await page.getByTestId("incident-close-submit").click();
    await closeResp;

    await expect(page.getByTestId("incident-detail-status")).toContainText(
      "Closed",
    );
    await expect(page.getByTestId("incident-detail-postmortem")).toContainText(
      "Postmortem captured by OE-633 e2e.",
    );
    await expect(page.getByTestId("incident-close-submit")).toHaveCount(0);
  });

  test("detected incident presents only the triage transition", async ({
    authedPage: page,
  }) => {
    await page.goto("/incidents/63363363-6336-6336-6336-633633633002");
    await expect(page.getByTestId("incident-detail-status")).toContainText(
      "Detected",
    );
    await expect(
      page.getByRole("button", { name: "Mark Triaged" }),
    ).toBeVisible();
    await expect(
      page.getByRole("button", { name: "Mark Resolved" }),
    ).toHaveCount(0);
  });
});
