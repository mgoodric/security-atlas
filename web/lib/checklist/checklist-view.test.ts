import { describe, expect, it } from "vitest";

import {
  aiSectionCount,
  approvedCount,
  buildSectionState,
  canExport,
  type ChecklistResponse,
  disclosureText,
  roleLabel,
  type SectionView,
  showCloudBanner,
  suppressionNote,
} from "./checklist-view";

function section(over: Partial<SectionView> = {}): SectionView {
  return {
    section_id: "sec-1",
    role: "infra",
    ai_assisted: true,
    human_approved: false,
    suppressed: false,
    cloud_routed: false,
    items: [
      {
        control_id: "ctrl-1",
        task: "Enable MFA (ctrl-1).",
        no_evidence: false,
        citations: [{ kind: "control", id: "ctrl-1" }],
      },
    ],
    model_name: "llama3.1:8b-instruct-q5",
    model_version: "1",
    model_provider: "ollama-local",
    ...over,
  };
}

function resp(sections: SectionView[]): ChecklistResponse {
  return {
    generation_id: "gen-1",
    sections,
    cloud_routed: false,
    binding: false,
    disclosure: "AI-assisted draft — review before use.",
  };
}

describe("roleLabel", () => {
  it("maps the fixed v0 taxonomy", () => {
    expect(roleLabel("infra")).toContain("Infrastructure");
    expect(roleLabel("engineering")).toContain("Engineering");
    expect(roleLabel("security")).toContain("Security");
    expect(roleLabel("unassigned")).toContain("Unassigned");
  });
  it("passes through an unknown role rather than inventing copy", () => {
    expect(roleLabel("weird")).toBe("weird");
  });
});

describe("buildSectionState", () => {
  it("marks an unapproved AI section approvable", () => {
    const st = buildSectionState(section());
    expect(st.approvable).toBe(true);
    expect(st.approved).toBe(false);
    expect(st.modelDisclosure).toContain("review before use");
  });
  it("does NOT mark an already-approved section approvable", () => {
    const st = buildSectionState(
      section({ human_approved: true, human_approver: "key_grc" }),
    );
    expect(st.approvable).toBe(false);
    expect(st.approved).toBe(true);
    expect(st.approver).toBe("key_grc");
  });
  it("does NOT mark the unassigned bucket approvable", () => {
    const st = buildSectionState(
      section({ role: "unassigned", ai_assisted: false }),
    );
    expect(st.approvable).toBe(false);
    expect(st.modelDisclosure).toBe("");
  });
  it("does NOT mark a suppressed section approvable + surfaces the note", () => {
    const st = buildSectionState(
      section({ suppressed: true, reason: "unresolved_citation" }),
    );
    expect(st.approvable).toBe(false);
    expect(st.suppressed).toBe(true);
    expect(st.note).toContain("could not be verified");
  });
});

describe("suppressionNote", () => {
  it("maps the closed vocabulary", () => {
    expect(suppressionNote("generation_unavailable")).toContain("unavailable");
    expect(suppressionNote("unresolved_citation")).toContain("verified");
    expect(suppressionNote("no_citations")).toContain("no specific control");
    expect(suppressionNote("no_tasks")).toContain("no usable tasks");
  });
  it("falls back neutrally on an unknown reason (no raw echo)", () => {
    expect(suppressionNote("x-internal-leak")).toBe(
      "This team's draft was withheld.",
    );
  });
});

describe("canExport / approval gating (P0-471-1)", () => {
  it("is false when no section is approved (a draft cannot be exported)", () => {
    expect(canExport(resp([section(), section({ role: "security" })]))).toBe(
      false,
    );
  });
  it("is true once at least one AI section is approved", () => {
    expect(canExport(resp([section({ human_approved: true })]))).toBe(true);
  });
  it("a suppressed-but-flagged-approved section does not enable export", () => {
    // Defensive: a suppressed section can never be approved, but guard anyway.
    expect(
      canExport(resp([section({ suppressed: true, human_approved: true })])),
    ).toBe(false);
  });
  it("the unassigned bucket never enables export", () => {
    expect(
      canExport(
        resp([
          section({
            role: "unassigned",
            ai_assisted: false,
            human_approved: true,
          }),
        ]),
      ),
    ).toBe(false);
  });
});

describe("progress + banners", () => {
  it("counts approved AI sections of total AI sections", () => {
    const r = resp([
      section({ human_approved: true }),
      section({ role: "security" }),
      section({ role: "unassigned", ai_assisted: false }),
    ]);
    expect(approvedCount(r)).toBe(1);
    expect(aiSectionCount(r)).toBe(2);
  });
  it("cloud banner is false in v0", () => {
    expect(showCloudBanner(resp([section()]))).toBe(false);
  });
  it("disclosureText prefers backend, falls back locally", () => {
    expect(disclosureText(resp([section()]))).toContain("review before use");
    const blank = { ...resp([section()]), disclosure: "" };
    expect(disclosureText(blank)).toContain("Not an audit artifact");
  });
});
