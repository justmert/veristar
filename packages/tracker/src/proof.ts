import { type Challenge, type Proof, proofInput } from "./wire.ts";

// digests are issued in batches because each webcrypto call is async: awaiting
// them one at a time spends most of the time in promise turns, not hashing.
// 64 was the fastest or within noise of the fastest batch size in all three
// engines, while 1 was up to 50 times slower (docs/reference/browser-proof.md).
export const SOLVE_BATCH = 64;

export function leadingZeroBits(digest: Uint8Array): number {
  let bits = 0;
  for (const byte of digest) {
    if (byte === 0) {
      bits += 8;
      continue;
    }
    return bits + Math.clz32(byte) - 24;
  }
  return bits;
}

export async function proofDigest(
  subtle: SubtleCrypto,
  w: string,
  iat: number,
  salt: string,
  nonce: number,
): Promise<Uint8Array> {
  const input = new TextEncoder().encode(proofInput(w, iat, salt, nonce));
  return new Uint8Array(await subtle.digest("SHA-256", input));
}

// finds the smallest nonce whose digest has at least bits leading zero bits.
// smallest, so that the result is deterministic and test vectors are stable.
// batch is a parameter only so bench/proof-cost.mjs can compare sizes.
export async function solve(
  subtle: SubtleCrypto,
  challenge: Challenge,
  batch: number = SOLVE_BATCH,
): Promise<Proof> {
  const { w, iat, salt, bits, tag } = challenge;
  const encoder = new TextEncoder();
  const prefix = `${w}|${iat}|${salt}|`;
  for (let start = 0; start < Number.MAX_SAFE_INTEGER; start += batch) {
    const pending: Promise<ArrayBuffer>[] = [];
    for (let i = 0; i < batch; i++) {
      pending.push(subtle.digest("SHA-256", encoder.encode(prefix + (start + i))));
    }
    const digests = await Promise.all(pending);
    for (let i = 0; i < digests.length; i++) {
      const digest = digests[i] as ArrayBuffer;
      if (leadingZeroBits(new Uint8Array(digest)) >= bits) {
        return { iat, salt, bits, tag, nonce: start + i };
      }
    }
  }
  throw new Error("no nonce found below Number.MAX_SAFE_INTEGER");
}

// checks the nonce only. the tag is an hmac under a key only the gateway holds,
// so the browser cannot verify it and does not try.
export async function verifyNonce(subtle: SubtleCrypto, w: string, proof: Proof): Promise<boolean> {
  if (!Number.isSafeInteger(proof.nonce) || proof.nonce < 0) return false;
  const digest = await proofDigest(subtle, w, proof.iat, proof.salt, proof.nonce);
  return leadingZeroBits(digest) >= proof.bits;
}
