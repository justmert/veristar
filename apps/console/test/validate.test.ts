import { describe, expect, it } from "vitest";
import * as v from "@/lib/validate";
import { C_KEY, G_BAD_CHECKSUM, G_KEY, M_KEY } from "./fixtures/contract";

describe("validation mirrors contract 3.2", () => {
  it("accepts G, M and C strkeys and checks the checksum", () => {
    expect(v.strkey(G_KEY)).toBeNull();
    expect(v.strkey(C_KEY)).toBeNull();
    expect(v.strkey(M_KEY)).toBeNull();
    expect(v.strkey(G_BAD_CHECKSUM)).toBe("invalid_strkey");
    expect(v.strkey(G_KEY.toLowerCase())).toBe("invalid_strkey");
    // a contract payload with the account prefix: wrong version byte
    expect(v.strkey(`G${C_KEY.slice(1)}`)).toBe("invalid_strkey");
  });

  it("checks origins against the site domain", () => {
    expect(v.origin("https://example.org", "example.org")).toBeNull();
    expect(v.origin("https://www.example.org", "example.org")).toBeNull();
    expect(v.origin("https://example.org:8443", "example.org")).toBeNull();
    expect(v.origin("http://localhost:4321", "example.org")).toBeNull();
    expect(v.origin("http://127.0.0.1", "example.org")).toBeNull();
    expect(v.origin("http://example.org", "example.org")).toBe("not_bare_origin");
    expect(v.origin("https://example.org/", "example.org")).toBe("not_bare_origin");
    expect(v.origin("https://example.org/path", "example.org")).toBe("not_bare_origin");
    expect(v.origin("https://Example.org", "example.org")).toBe("not_bare_origin");
    expect(v.origin("https://badexample.org", "example.org")).toBe("origin_outside_domain");
    expect(v.originList(["https://example.org", "https://example.org"], "example.org")).toEqual({
      "origins[1]": "duplicate",
    });
    expect(v.originList([], "example.org")).toEqual({ origins: "required" });
  });

  it("checks domains, logins, slugs and urls", () => {
    expect(v.domain("example.org")).toBeNull();
    expect(v.domain("xn--bcher-kva.example")).toBeNull();
    for (const bad of [
      "example",
      "Example.org",
      "example.org.",
      "https://example.org",
      "-a.org",
      "a_b.org",
    ])
      expect(v.domain(bad), bad).toBe("invalid_domain");
    expect(v.githubLogin("octo-cat")).toBeNull();
    for (const bad of ["-octo", "octo-", "octo--cat", "a".repeat(40), "octo cat"])
      expect(v.githubLogin(bad), bad).toBe("invalid_github_login");
    expect(v.slug("stellar-dex")).toBeNull();
    expect(v.slug("Stellar")).toBe("invalid_format");
    expect(v.httpsUrl("http://example.org", true)).toBe("not_https_url");
    expect(v.httpsUrl("", false)).toBeNull();
    expect(v.httpsUrl(`https://example.org/${"a".repeat(2048)}`, true)).toBe("too_long");
    expect(v.tenantName("  ")).toBe("required");
    expect(v.tenantName("a\u0007b")).toBe("invalid_format");
    expect(v.stellarUsage("line one\nline two\tok")).toBeNull();
  });
});
