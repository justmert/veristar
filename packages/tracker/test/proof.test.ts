import { createHash, createHmac } from "node:crypto";
import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";
import { leadingZeroBits, solve, verifyNonce } from "../src/proof.ts";
import { proofInput, tagInput } from "../src/wire.ts";

type Vector = {
  proofKeyHex: string;
  w: string;
  iat: number;
  salt: string;
  bits: number;
  tagInput: string;
  tag: string;
  nonce: number;
  proofInput: string;
  digest: string;
};

const { vectors } = JSON.parse(
  readFileSync(new URL("./vectors/proof-v1.json", import.meta.url), "utf8"),
) as { vectors: Vector[] };

describe("proof vectors", () => {
  it("has vectors", () => {
    expect(vectors.length).toBeGreaterThanOrEqual(4);
  });

  for (const v of vectors) {
    describe(`bits ${v.bits}, nonce ${v.nonce}`, () => {
      it("builds the documented input strings", () => {
        expect(tagInput(v.w, v.iat, v.salt, v.bits)).toBe(v.tagInput);
        expect(proofInput(v.w, v.iat, v.salt, v.nonce)).toBe(v.proofInput);
      });

      it("tag is hmac-sha256 of the tag input under the proof key", () => {
        const tag = createHmac("sha256", Buffer.from(v.proofKeyHex, "hex"))
          .update(v.tagInput)
          .digest("hex");
        expect(tag).toBe(v.tag);
      });

      it("digest is sha-256 of the proof input and meets the difficulty", () => {
        const digest = createHash("sha256").update(v.proofInput).digest();
        expect(digest.toString("hex")).toBe(v.digest);
        expect(leadingZeroBits(digest)).toBeGreaterThanOrEqual(v.bits);
      });

      it("solve finds the smallest nonce with any batch size", async () => {
        const challenge = { w: v.w, iat: v.iat, salt: v.salt, bits: v.bits, tag: v.tag };
        for (const batch of [1, 7, 64]) {
          const proof = await solve(crypto.subtle, challenge, batch);
          expect(proof).toEqual({
            iat: v.iat,
            salt: v.salt,
            bits: v.bits,
            tag: v.tag,
            nonce: v.nonce,
          });
        }
      });

      it("every smaller nonce fails the difficulty", () => {
        for (let n = 0; n < v.nonce; n++) {
          const d = createHash("sha256")
            .update(proofInput(v.w, v.iat, v.salt, n))
            .digest();
          expect(leadingZeroBits(d)).toBeLessThan(v.bits);
        }
      });

      it("verifyNonce accepts the vector and rejects a changed salt", async () => {
        const proof = { iat: v.iat, salt: v.salt, bits: v.bits, tag: v.tag, nonce: v.nonce };
        expect(await verifyNonce(crypto.subtle, v.w, proof)).toBe(true);
        if (v.bits >= 8) {
          const other = {
            ...proof,
            salt: `${v.salt.slice(0, 31)}${v.salt.endsWith("0") ? "1" : "0"}`,
          };
          // a different salt meets the same difficulty only by chance (2^-bits)
          expect(await verifyNonce(crypto.subtle, v.w, other)).toBe(false);
        }
      });
    });
  }
});

describe("leadingZeroBits", () => {
  it("counts bits across bytes", () => {
    expect(leadingZeroBits(new Uint8Array([0x80]))).toBe(0);
    expect(leadingZeroBits(new Uint8Array([0x01]))).toBe(7);
    expect(leadingZeroBits(new Uint8Array([0x00, 0x40]))).toBe(9);
    expect(leadingZeroBits(new Uint8Array([0x00, 0x00, 0x0f]))).toBe(20);
    expect(leadingZeroBits(new Uint8Array([0, 0]))).toBe(16);
  });
});

describe("verifyNonce", () => {
  it("refuses a negative or fractional nonce", async () => {
    const v = vectors[0] as Vector;
    const base = { iat: v.iat, salt: v.salt, bits: 0, tag: v.tag };
    expect(await verifyNonce(crypto.subtle, v.w, { ...base, nonce: -1 })).toBe(false);
    expect(await verifyNonce(crypto.subtle, v.w, { ...base, nonce: 1.5 })).toBe(false);
  });
});
