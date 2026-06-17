// Slice 499 — unit coverage for the config-driven routing-banner view-model.
//
// AC-12 (the banner-render DECISION): the banner renders for a cloud-routed
// tenant and is absent for a local tenant. vitest is node-only here (no DOM —
// CLAUDE.md test-tier convention), so this proves the decision the component's
// `if (!vm.isCloud) return null` keys off; the rendered banner itself is the
// Playwright tier.

import { describe, expect, test } from "vitest";

import {
  bannerMessage,
  normalizeProvider,
  parseRoutingConfig,
  providerLabel,
} from "./routing";

describe("parseRoutingConfig — banner render decision (AC-12)", () => {
  test("cloud tenant => banner renders (isCloud true, message names provider)", () => {
    const vm = parseRoutingConfig(true, {
      provider: "anthropic",
      is_cloud: true,
      has_api_key: true,
      api_key: "<redacted>",
    });
    expect(vm.isCloud).toBe(true);
    expect(vm.provider).toBe("anthropic");
    expect(vm.message).toContain("Anthropic");
    expect(vm.message).toContain("your data leaves this deployment");
  });

  test("local tenant => NO banner (isCloud false, empty message)", () => {
    const vm = parseRoutingConfig(true, {
      provider: "local-ollama",
      is_cloud: false,
      has_api_key: false,
    });
    expect(vm.isCloud).toBe(false);
    expect(vm.message).toBe("");
  });

  test("each cloud provider trips the banner", () => {
    for (const p of ["openai", "bedrock", "anthropic"]) {
      const vm = parseRoutingConfig(true, { provider: p, is_cloud: true });
      expect(vm.isCloud).toBe(true);
    }
  });

  test("a non-ok fetch => local default (off-by-default, no false cloud claim)", () => {
    const vm = parseRoutingConfig(false, null);
    expect(vm.isCloud).toBe(false);
    expect(vm.provider).toBe("local-ollama");
  });

  test("an unparseable body => local default", () => {
    expect(parseRoutingConfig(true, null).isCloud).toBe(false);
    expect(parseRoutingConfig(true, "garbage").isCloud).toBe(false);
    expect(parseRoutingConfig(true, 42).isCloud).toBe(false);
  });

  test("unknown provider is NEVER treated as cloud (fail-safe)", () => {
    const vm = parseRoutingConfig(true, {
      provider: "evil-custom",
      is_cloud: true,
    });
    expect(vm.provider).toBe("local-ollama");
    expect(vm.isCloud).toBe(false);
  });

  test("is_cloud flag alone (without a known cloud provider) does not force a banner", () => {
    const vm = parseRoutingConfig(true, {
      provider: "local-ollama",
      is_cloud: true,
    });
    expect(vm.isCloud).toBe(false);
  });

  test("a known cloud provider with a missing is_cloud flag still trips (cross-check)", () => {
    const vm = parseRoutingConfig(true, { provider: "openai" });
    expect(vm.isCloud).toBe(true);
  });
});

describe("providerLabel / normalizeProvider / bannerMessage", () => {
  test("provider labels", () => {
    expect(providerLabel("anthropic")).toBe("Anthropic");
    expect(providerLabel("openai")).toBe("OpenAI");
    expect(providerLabel("bedrock")).toBe("AWS Bedrock");
    expect(providerLabel("local-ollama")).toBe("the local model");
  });

  test("normalizeProvider defaults unknown to local-ollama", () => {
    expect(normalizeProvider("ANTHROPIC")).toBe("anthropic");
    expect(normalizeProvider("openai")).toBe("openai");
    expect(normalizeProvider("bedrock")).toBe("bedrock");
    expect(normalizeProvider("whatever")).toBe("local-ollama");
    expect(normalizeProvider(undefined)).toBe("local-ollama");
  });

  test("bannerMessage is the canonical routes-to string", () => {
    expect(bannerMessage("anthropic")).toBe(
      "AI assist routes to Anthropic — your data leaves this deployment.",
    );
  });
});
