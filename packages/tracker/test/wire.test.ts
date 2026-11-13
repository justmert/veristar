import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";
import {
  checkData,
  checkName,
  clip,
  LIMITS,
  MAX_PROOF_BITS,
  parseChallenge,
  routeFragment,
} from "../src/wire.ts";

const SITE = "8f6b1c3e-2a4d-4e5f-9a7b-0c1d2e3f4a5b";
const good = { w: SITE, iat: 1794528000, salt: "0".repeat(32), bits: 12, tag: "a".repeat(64) };

describe("parseChallenge", () => {
  it("accepts a v1 challenge for this site", () => {
    expect(parseChallenge(good, SITE)).toEqual(good);
  });

  it("drops unknown fields", () => {
    expect(parseChallenge({ ...good, extra: 1 }, SITE)).toEqual(good);
  });

  const bad: [string, unknown][] = [
    ["null", null],
    ["another site", { ...good, w: "other" }],
    ["string iat", { ...good, iat: "1" }],
    ["fractional iat", { ...good, iat: 1.5 }],
    ["short salt", { ...good, salt: "00" }],
    ["upper-case salt", { ...good, salt: "A".repeat(32) }],
    ["negative bits", { ...good, bits: -1 }],
    ["bits above the cap", { ...good, bits: MAX_PROOF_BITS + 1 }],
    ["short tag", { ...good, tag: "a".repeat(63) }],
  ];
  for (const [name, value] of bad) {
    it(`refuses ${name}`, () => {
      expect(typeof parseChallenge(value, SITE)).toBe("string");
    });
  }
});

describe("checkData", () => {
  it("keeps scalars and cuts long strings", () => {
    const out = checkData({ a: "x".repeat(600), b: 2, c: true });
    expect(out).toEqual({ a: "x".repeat(LIMITS.dataValueLength), b: 2, c: true });
  });

  it("refuses nested values, arrays, non-finite numbers and too many keys", () => {
    expect(typeof checkData({ a: { b: 1 } })).toBe("string");
    expect(typeof checkData({ a: [1] })).toBe("string");
    expect(typeof checkData({ a: Number.NaN })).toBe("string");
    expect(typeof checkData({ a: undefined })).toBe("string");
    // the gateway refuses null anywhere in the body
    expect(typeof checkData({ a: null })).toBe("string");
    expect(typeof checkData({ "a\u0001": 1 })).toBe("string");
    expect(typeof checkData({ a: "x\u0000" })).toBe("string");
    expect(typeof checkData([1])).toBe("string");
    const many = Object.fromEntries(Array.from({ length: 17 }, (_, i) => [`k${i}`, i]));
    expect(typeof checkData(many)).toBe("string");
    expect(typeof checkData({ ["k".repeat(51)]: 1 })).toBe("string");
  });
});

describe("clip", () => {
  it("does not split a surrogate pair", () => {
    expect(clip("ab\u{1F600}", 3)).toBe("ab");
    expect(clip("abc", 3)).toBe("abc");
    expect(clip("abcd", 3)).toBe("abc");
  });
});

describe("routeFragment", () => {
  it("keeps only #/ routes, without their query", () => {
    expect(routeFragment("#/a/b", false)).toBe("#/a/b");
    expect(routeFragment("#/a?b=c", false)).toBe("#/a");
    expect(routeFragment("#/", false)).toBe("#/");
    expect(routeFragment("#access_token=x", false)).toBe("");
    expect(routeFragment("#a/b", false)).toBe("");
    expect(routeFragment("#", false)).toBe("");
    expect(routeFragment("", false)).toBe("");
    expect(routeFragment("#/a", true)).toBe("");
  });
});

describe("checkName", () => {
  it("refuses control characters", () => {
    expect(checkName("signup")).toBeUndefined();
    expect(checkName("a\tb")).toBeTypeOf("string");
    expect(checkName("a\u0085b")).toBeTypeOf("string");
  });
});

// the same file is read by internal/events (TestStorableVectors), so tracker
// and gateway refuse exactly what umami 3.5.0 cannot store
describe("storable vectors", () => {
  const file = JSON.parse(
    readFileSync(new URL("./vectors/storable-v1.json", import.meta.url), "utf8"),
  ) as {
    data: { data: Record<string, unknown>; ok: boolean }[];
    names: { name: string; ok: boolean }[];
  };
  for (const c of file.data) {
    it(`${c.ok ? "accepts" : "refuses"} ${JSON.stringify(c.data)}`, () => {
      expect(typeof checkData(c.data) === "string").toBe(!c.ok);
    });
  }
  for (const c of file.names) {
    it(`${c.ok ? "accepts" : "refuses"} the name ${JSON.stringify(c.name)}`, () => {
      expect(checkName(c.name) === undefined).toBe(c.ok);
    });
  }
});
