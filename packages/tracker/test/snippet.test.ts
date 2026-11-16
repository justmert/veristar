import { describe, expect, it } from "vitest";
import manifest from "../releases/manifest.json" with { type: "json" };
import { isRevoked, scriptUrl, snippet } from "../src/snippet.ts";

const SITE = "8f6b1c3e-2a4d-4e5f-9a7b-0c1d2e3f4a5b";
const entry = manifest.versions.find((v) => v.version === "1.0.0");

describe("snippet", () => {
  it("builds the exact tag for 1.0.0", () => {
    expect(entry).toBeDefined();
    expect(snippet({ siteId: SITE, version: "1.0.0", origin: "https://in.sa-test.example" })).toBe(
      `<script defer src="https://in.sa-test.example/v/1.0.0/sdk.js" integrity="${entry?.integrity}" crossorigin="anonymous" data-website-id="${SITE}"></script>`,
    );
  });

  it("builds the script url", () => {
    expect(scriptUrl("https://in.sa-test.example", "1.0.0")).toBe(
      "https://in.sa-test.example/v/1.0.0/sdk.js",
    );
  });

  it("refuses values that would need escaping or are unknown", () => {
    const ok = { siteId: SITE, version: "1.0.0", origin: "https://in.sa-test.example" };
    expect(() => snippet({ ...ok, siteId: `${SITE}"` })).toThrow(/uuid/);
    expect(() => snippet({ ...ok, siteId: SITE.toUpperCase() })).toThrow(/uuid/);
    expect(() => snippet({ ...ok, origin: "https://in.sa-test.example/" })).toThrow(/origin/);
    expect(() => snippet({ ...ok, origin: "https://in.sa-test.example/x" })).toThrow(/origin/);
    expect(() => snippet({ ...ok, origin: "javascript:alert(1)" })).toThrow(/origin/);
    expect(() => snippet({ ...ok, version: "9.9.9" })).toThrow(/manifest/);
  });

  it("adds the wallets kit opt-in only when asked, and only from 1.1.0", () => {
    const v11 = manifest.versions.find((v) => v.version === "1.1.0");
    expect(v11).toBeDefined();
    const ok = { siteId: SITE, version: "1.1.0", origin: "https://in.sa-test.example" };
    expect(snippet(ok)).toBe(
      `<script defer src="https://in.sa-test.example/v/1.1.0/sdk.js" integrity="${v11?.integrity}" crossorigin="anonymous" data-website-id="${SITE}"></script>`,
    );
    expect(snippet({ ...ok, walletsKit: true })).toBe(
      `<script defer src="https://in.sa-test.example/v/1.1.0/sdk.js" integrity="${v11?.integrity}" crossorigin="anonymous" data-website-id="${SITE}" data-wallets-kit="true"></script>`,
    );
    // 1.0.0 would ignore the attribute without a word
    expect(() => snippet({ ...ok, version: "1.0.0", walletsKit: true })).toThrow(/1\.1\.0/);
  });

  it("adds the title opt-in only when asked, and only from 1.3.0", () => {
    const v13 = manifest.versions.find((v) => v.version === "1.3.0");
    expect(v13).toBeDefined();
    const ok = { siteId: SITE, version: "1.3.0", origin: "https://in.sa-test.example" };
    expect(snippet({ ...ok, sendTitle: true, walletsKit: true })).toBe(
      `<script defer src="https://in.sa-test.example/v/1.3.0/sdk.js" integrity="${v13?.integrity}" crossorigin="anonymous" data-website-id="${SITE}" data-wallets-kit="true" data-send-title="true"></script>`,
    );
    // 1.2.0 sends the title anyway; the attribute would promise a choice it ignores
    expect(() => snippet({ ...ok, version: "1.2.0", sendTitle: true })).toThrow(/1\.3\.0/);
  });

  it("1.0.0 is not revoked", () => {
    expect(isRevoked("1.0.0")).toBe(false);
  });
});
