// the console's one way to use a passkey. passkeys are soropass passkeys
// (@soropass/core): an es256 webauthn credential whose key is the founding
// signer of a soropass smart account on stellar, a contract account
// (C-address). the control plane derives that address from the credential
// id and the key and signs the person in as that account (contract 4.1.9).
//
// two ceremonies, each started by a click, because safari runs webauthn
// only inside a user activation and a create consumes it:
//   - createPasskey registers a new passkey with soropass's registerPasskey
//     (es256 only, user verification required, a discoverable credential)
//     and returns its credential id and sec-1 public key. registration
//     answers are not signed, so this proves nothing yet.
//   - answerChallenge signs the control plane's challenge with a passkey
//     through soropass's browser webauthn client. for a passkey created just
//     now it names that credential and sends its public key; otherwise the
//     browser offers the passkeys it holds for this console and the control
//     plane checks the answer with the key it stored.
//
// the challenge is fetched before the click (prepare), so the click goes
// straight to the browser's passkey prompt.
//
// every ceremony runs in the page: navigator.credentials with the console
// host as relying party. no popup, frame or foreign origin is involved, so
// the console's csp (connect-src 'self', frame-src 'none') and its
// cross-origin-opener-policy hold unchanged.

import { browserWebAuthnClient, registerPasskey } from "@soropass/core/create";
import { isKitError } from "@soropass/core/types";
import type { PasskeyAnswer } from "@/api/endpoints";
import type { PasskeyChallenge } from "@/api/schemas";
import { PasskeyError } from "./errors";

export { PasskeyError, type PasskeyErrorCode } from "./errors";

// the name a browser shows for the relying party in its passkey prompt
export const PASSKEY_RP_NAME = "Veristar";

function asPasskeyError(e: unknown): PasskeyError {
  if (e instanceof PasskeyError) return e;
  if (isKitError(e)) {
    if (e.code === "USER_CANCELLED") return new PasskeyError("cancelled", e.message);
    if (e.code === "ES256_NOT_SUPPORTED" || e.code === "UNSUPPORTED_AUTHENTICATOR")
      return new PasskeyError("unsupported", e.message);
  }
  return new PasskeyError("failed", e instanceof Error ? e.message : String(e));
}

// base64url without padding, the encoding of every byte field the control
// plane exchanges. written here rather than taken from @soropass/core/sign,
// whose entry pulls the stellar sdk into the bundle.
export function toBase64Url(bytes: Uint8Array): string {
  let bin = "";
  for (const b of bytes) bin += String.fromCharCode(b);
  return btoa(bin).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
}

export function fromBase64Url(s: string): Uint8Array {
  const b64 = s.replace(/-/g, "+").replace(/_/g, "/");
  const bin = atob(b64 + "=".repeat((4 - (b64.length % 4)) % 4));
  return Uint8Array.from(bin, (c) => c.charCodeAt(0));
}

// passkeysAvailable reports whether this browser has the webauthn api.
export function passkeysAvailable(): boolean {
  return typeof window !== "undefined" && typeof window.PublicKeyCredential === "function";
}

export interface NewPasskey {
  credentialId: string;
  // uncompressed sec-1 p-256 key, 65 bytes
  publicKey: Uint8Array;
}

// createPasskey registers a passkey for this console. userName is what the
// browser's passkey list shows for it.
export async function createPasskey(rpId: string, userName: string): Promise<NewPasskey> {
  try {
    const p = await registerPasskey({
      rpId,
      rpName: PASSKEY_RP_NAME,
      userName,
      // an opaque handle: the console never reads it back
      userId: crypto.getRandomValues(new Uint8Array(16)),
      residentKey: "required",
      userVerification: "required",
    });
    return { credentialId: p.credentialId, publicKey: p.publicKey };
  } catch (e) {
    throw asPasskeyError(e);
  }
}

// answerChallenge signs challenge with a passkey: the one just created when
// created is given, else any passkey the browser holds for this console.
export async function answerChallenge(
  challenge: PasskeyChallenge,
  created?: NewPasskey,
): Promise<PasskeyAnswer> {
  try {
    const a = await browserWebAuthnClient().get({
      rpId: challenge.rp_id,
      challenge: fromBase64Url(challenge.challenge),
      allowCredentials: created ? [created.credentialId] : [],
      userVerification: "required",
    });
    const answer: PasskeyAnswer = {
      credential_id: a.id,
      authenticator_data: toBase64Url(a.authenticatorData),
      client_data_json: toBase64Url(a.clientDataJSON),
      signature: toBase64Url(a.signature),
    };
    if (created) answer.public_key = toBase64Url(created.publicKey);
    return answer;
  } catch (e) {
    throw asPasskeyError(e);
  }
}
