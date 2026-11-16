// @vitest-environment node
// the signature check of the public report page, against the published
// vectors of sep-53 1.0.0 and rfc 8785, and a report signed here with the
// stellar sdk the way the control plane signs one
import { Keypair } from "@stellar/stellar-sdk/base";
import { describe, expect, it } from "vitest";
import {
  canonicalize,
  decodeAccountId,
  SEP53_PREFIX,
  verifyReport,
  verifySep53,
} from "@/lib/report-verify";

const SEED = "SAKICEVQLYWGSOJS4WW7HZJWAHZVEEBS527LHK5V4MLJALYKICQCJXMW";
const ACCOUNT = "GBXFXNDLV4LSWA4VB7YIL5GBD7BVNR22SGBTDKMO2SBZZHDXSKZYCP7L";
const enc = (s: string) => new TextEncoder().encode(s);
const b64 = (s: string) => Uint8Array.from(atob(s), (c) => c.charCodeAt(0));

describe("rfc 8785 canonical form", () => {
  it("sorts members by utf-16 code units (section 3.2.3)", () => {
    const input = JSON.parse(
      '{"\\u20ac":"Euro Sign","\\r":"Carriage Return","\\ufb33":"Hebrew Letter Dalet With Dagesh","1":"One","\\ud83d\\ude00":"Emoji: Grinning Face","\\u0080":"Control","\\u00f6":"Latin Small Letter O With Diaeresis"}',
    );
    expect(canonicalize(input)).toBe(
      '{"\\r":"Carriage Return","1":"One","\u0080":"Control","ö":"Latin Small Letter O With Diaeresis","€":"Euro Sign","😀":"Emoji: Grinning Face","דּ":"Hebrew Letter Dalet With Dagesh"}',
    );
  });

  it("writes the short escapes and lowercase \\u00xx (section 3.2.2.2)", () => {
    expect(canonicalize('\b\t\n\f\r"\\\u001f\u007f/€')).toBe(
      '"\\b\\t\\n\\f\\r\\"\\\\\\u001f\u007f/€"',
    );
  });

  it("writes integers, refuses fractions and unsafe integers, nests arrays", () => {
    expect(canonicalize({ b: [3, 1, -0], a: { z: null, y: true } })).toBe(
      '{"a":{"y":true,"z":null},"b":[3,1,0]}',
    );
    expect(canonicalize(9007199254740991)).toBe("9007199254740991");
    expect(() => canonicalize(1.5)).toThrow();
    expect(() => canonicalize(2 ** 53)).toThrow();
  });
});

describe("stellar strkey", () => {
  it("decodes an account id and checks its checksum", () => {
    const raw = decodeAccountId(ACCOUNT);
    expect(raw).not.toBeNull();
    expect(Array.from(raw ?? [])).toEqual(
      Array.from(Keypair.fromPublicKey(ACCOUNT).rawPublicKey()),
    );
    const flipped = `${ACCOUNT.slice(0, 54)}${ACCOUNT[54] === "A" ? "B" : "A"}`;
    expect(decodeAccountId(flipped)).toBeNull();
    expect(decodeAccountId(SEED)).toBeNull();
    expect(decodeAccountId("CAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAHK3M")).toBeNull();
  });
});

describe("sep-53 vectors", () => {
  const vectors: [Uint8Array, string][] = [
    [
      enc("Hello, World!"),
      "fO5dbYhXUhBMhe6kId/cuVq/AfEnHRHEvsP8vXh03M1uLpi5e46yO2Q8rEBzu3feXQewcQE5GArp88u6ePK6BA==",
    ],
    [
      enc("こんにちは、世界！"),
      "CDU265Xs8y3OWbB/56H9jPgUss5G9A0qFuTqH2zs2YDgTm+++dIfmAEceFqB7bhfN3am59lCtDXrCtwH2k1GBA==",
    ],
    [
      b64("2zZDP1sa1BVBfLP7TeeMk3sUbaxAkUhBhDiNdrksaFo="),
      "VA1+7hefNwv2NKScH6n+Sljj15kLAge+M2wE7fzFOf+L0MMbssA1mwfJZRyyrhBORQRle10X1Dxpx+UOI4EbDQ==",
    ],
  ];

  it("verifies every published signature and refuses a changed message or key", async () => {
    for (const [msg, sig] of vectors) {
      expect(await verifySep53(ACCOUNT, msg, sig)).toBe(true);
      const changed = msg.slice();
      changed[0] = (changed[0] ?? 0) ^ 1;
      expect(await verifySep53(ACCOUNT, changed, sig)).toBe(false);
      expect(await verifySep53(Keypair.random().publicKey(), msg, sig)).toBe(false);
    }
    expect(await verifySep53(ACCOUNT, enc("x"), "not base64!")).toBe(false);
  });

  it("matches the stellar sdk's own sep-53 signing", async () => {
    const kp = Keypair.fromSecret(SEED);
    const sig = kp.signMessage(enc("Hello, World!"));
    expect(Buffer.from(sig).toString("base64")).toBe(vectors[0]?.[1]);
    expect(SEP53_PREFIX).toBe("Stellar Signed Message:\n");
  });
});

describe("signed report files", () => {
  const kp = Keypair.random();
  const report = {
    format: "veristar-usage-report",
    version: 1,
    issuer: { name: "Veristar", signing_key: kp.publicKey() },
    project: { name: "Ünïcode € project", slug: "p" },
    web: { visitors: 35, bounce_rate: "0.166667" },
  };
  const sign = (r: unknown) =>
    Buffer.from(kp.signMessage(new TextEncoder().encode(canonicalize(r)))).toString("base64");
  const keys = { current: kp.publicKey(), retired: [] as string[] };

  it("accepts the report as signed and names the key", async () => {
    // the member order of the received object does not matter
    const { web, ...rest } = report;
    const reordered = JSON.parse(JSON.stringify({ web, ...rest }));
    const file = {
      report: reordered,
      signature: { scheme: "sep-53", signer: kp.publicKey(), value: sign(report) },
    };
    expect(await verifyReport(file, keys)).toEqual({
      ok: true,
      signer: kp.publicKey(),
      key: "current",
    });
    expect(await verifyReport(file, { current: ACCOUNT, retired: [kp.publicKey()] })).toMatchObject(
      {
        key: "retired",
      },
    );
    expect(await verifyReport(file, { current: ACCOUNT, retired: [] })).toMatchObject({
      ok: true,
      key: "unknown",
    });
  });

  it("refuses a changed number, another signer and an unknown scheme", async () => {
    const value = sign(report);
    const tampered = { ...report, web: { ...report.web, visitors: 36 } };
    expect(
      await verifyReport(
        { report: tampered, signature: { scheme: "sep-53", signer: kp.publicKey(), value } },
        keys,
      ),
    ).toMatchObject({ ok: false });
    const other = Keypair.random();
    expect(
      await verifyReport(
        {
          report: { ...report, issuer: { name: "x", signing_key: other.publicKey() } },
          signature: { scheme: "sep-53", signer: other.publicKey(), value },
        },
        keys,
      ),
    ).toMatchObject({ ok: false });
    expect(
      await verifyReport(
        { report, signature: { scheme: "ed25519", signer: kp.publicKey(), value } },
        keys,
      ),
    ).toMatchObject({ ok: false });
  });
});
