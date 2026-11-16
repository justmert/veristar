import { describe, expect, it } from "vitest";
import { fromBase64url, toBase64url } from "@/lib/webauthn";

describe("webauthn base64url", () => {
  it("round-trips every byte value without padding", () => {
    const bytes = new Uint8Array(256);
    for (let i = 0; i < 256; i++) bytes[i] = i;
    const s = toBase64url(bytes.buffer);
    expect(s).not.toMatch(/[+/=]/);
    expect(new Uint8Array(fromBase64url(s))).toEqual(bytes);
  });

  it("reads the unpadded challenge form the control plane sends", () => {
    expect(new Uint8Array(fromBase64url("AQID"))).toEqual(new Uint8Array([1, 2, 3]));
    expect(new Uint8Array(fromBase64url("_-8"))).toEqual(new Uint8Array([255, 239]));
  });
});
