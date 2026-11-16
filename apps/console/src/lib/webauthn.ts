// the browser half of the operator step-up (contract 5.15): the control
// plane sends webauthn options with binary fields as base64url strings, the
// browser api wants ArrayBuffers, and the answer goes back as json with
// base64url strings again. this file is that translation and nothing else;
// navigator.credentials does the ceremony.

export function fromBase64url(s: string): ArrayBuffer {
  const b64 = s
    .replace(/-/g, "+")
    .replace(/_/g, "/")
    .padEnd(Math.ceil(s.length / 4) * 4, "=");
  const bin = atob(b64);
  const out = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
  return out.buffer;
}

export function toBase64url(b: ArrayBuffer): string {
  const bytes = new Uint8Array(b);
  let bin = "";
  for (const x of bytes) bin += String.fromCharCode(x);
  return btoa(bin).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
}

interface Descriptor {
  id: string;
  type: string;
  transports?: string[];
}

interface CreationJSON {
  publicKey: Record<string, unknown> & {
    challenge: string;
    user: { id: string; name: string; displayName: string };
    excludeCredentials?: Descriptor[];
  };
}

interface RequestJSON {
  publicKey: Record<string, unknown> & { challenge: string; allowCredentials?: Descriptor[] };
}

function descriptors(list: Descriptor[] | undefined): PublicKeyCredentialDescriptor[] {
  return (list ?? []).map((d) => ({
    type: "public-key",
    id: fromBase64url(d.id),
    transports: d.transports as AuthenticatorTransport[] | undefined,
  }));
}

// a browser without webauthn, or a context that is not secure
export function webauthnAvailable(): boolean {
  return typeof window !== "undefined" && typeof window.PublicKeyCredential === "function";
}

export class PasskeyCancelled extends Error {
  constructor() {
    super("the passkey prompt was closed before it finished");
    this.name = "PasskeyCancelled";
  }
}

function cancelled(err: unknown): boolean {
  return (
    err instanceof DOMException && (err.name === "NotAllowedError" || err.name === "AbortError")
  );
}

// runs navigator.credentials.create with the options of
// POST /api/operator/passkeys/options and returns the credential json the
// control plane verifies
export async function createPasskey(options: CreationJSON): Promise<Record<string, unknown>> {
  const pk = options.publicKey;
  const publicKey = {
    ...pk,
    challenge: fromBase64url(pk.challenge),
    user: { ...pk.user, id: fromBase64url(pk.user.id) },
    excludeCredentials: descriptors(pk.excludeCredentials),
  } as unknown as PublicKeyCredentialCreationOptions;
  let cred: Credential | null;
  try {
    cred = await navigator.credentials.create({ publicKey });
  } catch (err) {
    if (cancelled(err)) throw new PasskeyCancelled();
    throw err;
  }
  if (!(cred instanceof PublicKeyCredential)) throw new PasskeyCancelled();
  const r = cred.response as AuthenticatorAttestationResponse;
  return {
    id: cred.id,
    rawId: toBase64url(cred.rawId),
    type: cred.type,
    response: {
      clientDataJSON: toBase64url(r.clientDataJSON),
      attestationObject: toBase64url(r.attestationObject),
      transports: typeof r.getTransports === "function" ? r.getTransports() : [],
    },
    clientExtensionResults: cred.getClientExtensionResults(),
    authenticatorAttachment: cred.authenticatorAttachment ?? undefined,
  };
}

// runs navigator.credentials.get with the options of
// POST /api/operator/step-up/options
export async function getAssertion(options: RequestJSON): Promise<Record<string, unknown>> {
  const pk = options.publicKey;
  const publicKey = {
    ...pk,
    challenge: fromBase64url(pk.challenge),
    allowCredentials: descriptors(pk.allowCredentials),
  } as unknown as PublicKeyCredentialRequestOptions;
  let cred: Credential | null;
  try {
    cred = await navigator.credentials.get({ publicKey });
  } catch (err) {
    if (cancelled(err)) throw new PasskeyCancelled();
    throw err;
  }
  if (!(cred instanceof PublicKeyCredential)) throw new PasskeyCancelled();
  const r = cred.response as AuthenticatorAssertionResponse;
  return {
    id: cred.id,
    rawId: toBase64url(cred.rawId),
    type: cred.type,
    response: {
      clientDataJSON: toBase64url(r.clientDataJSON),
      authenticatorData: toBase64url(r.authenticatorData),
      signature: toBase64url(r.signature),
      userHandle: r.userHandle ? toBase64url(r.userHandle) : undefined,
    },
    clientExtensionResults: cred.getClientExtensionResults(),
    authenticatorAttachment: cred.authenticatorAttachment ?? undefined,
  };
}
