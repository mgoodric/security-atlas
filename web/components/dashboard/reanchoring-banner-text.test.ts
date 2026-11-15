import { describe, expect, test } from "vitest";

import type { ControlNeedingReanchoring } from "@/lib/api/reanchoring";

import { reanchoringBannerText } from "./reanchoring-banner-text";

function control(
  id: string,
  current_release = "2026.3",
): ControlNeedingReanchoring {
  return {
    id,
    bundle_id: `bundle-${id}`,
    title: `Control ${id}`,
    legacy_scf_id: "IAC-21",
    legacy_anchor_title: "Privileged Account Management",
    legacy_release: "2026.1",
    current_release,
    note: "IAC-21 -> IAC-10 in 2026.3 (concept)",
  };
}

describe("reanchoringBannerText", () => {
  test("no controls: no banner", () => {
    expect(reanchoringBannerText({ controls: [], count: 0 })).toBeNull();
  });

  test("one control: singular copy naming the release", () => {
    const t = reanchoringBannerText({ controls: [control("a")], count: 1 });
    expect(t?.title).toBe("1 control needs re-anchoring");
    expect(t?.description).toContain("it earns no framework credit");
    expect(t?.description).toContain("SCF 2026.3");
  });

  test("several controls: plural copy; count from the rows", () => {
    const t = reanchoringBannerText({
      controls: [control("a"), control("b"), control("c")],
      count: 3,
    });
    expect(t?.count).toBe(3);
    expect(t?.title).toBe("3 controls need re-anchoring");
    expect(t?.description).toContain("they earn");
  });

  test("mixed current releases: no single release named", () => {
    const t = reanchoringBannerText({
      controls: [control("a", "2026.3"), control("b", "2026.4")],
      count: 2,
    });
    expect(t?.description).toContain("the current SCF release");
  });
});
