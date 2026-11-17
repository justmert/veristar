// the browser's own check of a signed usage report
// (docs/reference/usage-reports.md): the canonical json form of the report
// (rfc 8785, restricted to what a report holds), then a sep-53 signature
// check with webcrypto's ed25519. the page does not take the server's word
// that a report is valid: it recomputes the signed bytes and checks them.
// the list of veristar's keys it compares the signer with comes from the
// same console, though; a check that relies on no part of this server runs
// on the downloaded file (platformctl report verify, or any sep-53 library).

// the fixed prefix of sep-53: the signer hashes prefix and message with
// sha-256 and signs that hash, so a signed message is never a transaction
export const SEP53_PREFIX = "Stellar Signed Message:\n";

export class CanonicalError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "CanonicalError";
  }
}

// a utf-16 surrogate that is not part of a pair
const LONE_SURROGATE = /[\uD800-\uDBFF](?![\uDC00-\uDFFF])|(?<![\uD800-\uDBFF])[\uDC00-\uDFFF]/;

function str(s: string): string {
  // the cli refuses a lone surrogate too: it is no character, and json
  // readers disagree on what it becomes
  if (LONE_SURROGATE.test(s)) {
    throw new CanonicalError(
      "a string holds a lone surrogate; the canonical form holds characters only",
    );
  }
  return JSON.stringify(s);
}

// canonicalize writes the rfc 8785 form of a json value. numbers must be
// safe integers by value (12345.0 parses to 12345, as in the cli and every
// jcs implementation, which all write such an integer the same way); that
// is why a report carries ratios as decimal strings. strings are written
// exactly as ecmascript's JSON.stringify writes them, which is the string
// form rfc 8785 section 3.2.2.2 adopts. members are ordered by their utf-16
// code units, which is javascript's default string order.
export function canonicalize(v: unknown): string {
  if (v === null) return "null";
  if (typeof v === "boolean") return v ? "true" : "false";
  if (typeof v === "string") return str(v);
  if (typeof v === "number") {
    if (!Number.isSafeInteger(v)) {
      throw new CanonicalError(
        `${v} is not a safe integer; the canonical form holds integers only`,
      );
    }
    return Object.is(v, -0) ? "0" : String(v);
  }
  if (Array.isArray(v)) return `[${v.map(canonicalize).join(",")}]`;
  if (typeof v === "object") {
    const o = v as Record<string, unknown>;
    const names = Object.keys(o).sort();
    return `{${names.map((k) => `${str(k)}:${canonicalize(o[k])}`).join(",")}}`;
  }
  throw new CanonicalError(`a value of type ${typeof v} has no json form`);
}

const B32 = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567";

function base32Decode(s: string): Uint8Array<ArrayBuffer> | null {
  let bits = 0;
  let value = 0;
  const out: number[] = [];
  for (const ch of s) {
    const i = B32.indexOf(ch);
    if (i < 0) return null;
    value = (value << 5) | i;
    bits += 5;
    if (bits >= 8) {
      bits -= 8;
      out.push((value >>> bits) & 0xff);
    }
  }
  return new Uint8Array(out);
}

// crc16-xmodem, the checksum of a stellar strkey (sep-23)
export function crc16xmodem(data: Uint8Array): number {
  let crc = 0;
  for (const b of data) {
    crc ^= b << 8;
    for (let i = 0; i < 8; i++) {
      crc = crc & 0x8000 ? ((crc << 1) ^ 0x1021) & 0xffff : (crc << 1) & 0xffff;
    }
  }
  return crc;
}

// VERSION_ACCOUNT_ID of sep-23: the byte that makes a strkey start with G
const ACCOUNT_VERSION = 6 << 3;

// decodeAccountId returns the 32 byte ed25519 public key of a G address, or
// null when the address is malformed or its checksum does not match
export function decodeAccountId(address: string): Uint8Array<ArrayBuffer> | null {
  if (!/^G[A-Z2-7]{55}$/.test(address)) return null;
  const raw = base32Decode(address);
  if (raw?.length !== 35 || raw[0] !== ACCOUNT_VERSION) return null;
  const body = raw.subarray(0, 33);
  const crc = crc16xmodem(body);
  if (raw[33] !== (crc & 0xff) || raw[34] !== crc >>> 8) return null;
  return raw.slice(1, 33);
}

function base64Decode(s: string): Uint8Array<ArrayBuffer> | null {
  try {
    const bin = atob(s.trim());
    return Uint8Array.from(bin, (c) => c.charCodeAt(0));
  } catch {
    return null;
  }
}

// sep53Hash is sha-256 of the prefix and the message bytes
export async function sep53Hash(message: Uint8Array): Promise<Uint8Array<ArrayBuffer>> {
  const prefix = new TextEncoder().encode(SEP53_PREFIX);
  const all = new Uint8Array(prefix.length + message.length);
  all.set(prefix);
  all.set(message, prefix.length);
  return new Uint8Array(await crypto.subtle.digest("SHA-256", all));
}

// verifySep53 checks a base64 ed25519 signature of message by the account
export async function verifySep53(
  account: string,
  message: Uint8Array,
  signatureB64: string,
): Promise<boolean> {
  const pub = decodeAccountId(account);
  const sig = base64Decode(signatureB64);
  if (!pub || !sig || sig.length !== 64) return false;
  const key = await crypto.subtle.importKey("raw", pub, { name: "Ed25519" }, false, ["verify"]);
  return crypto.subtle.verify({ name: "Ed25519" }, key, sig, await sep53Hash(message));
}

export type Verdict =
  | { ok: true; signer: string; key: "current" | "retired" | "unknown" }
  | { ok: false; reason: string };

// verifyReport checks a signed file the way the cli and the docs do: the
// canonical bytes of the report, the sep-53 signature by the signer it names,
// the report naming that signer as its key, and, separately, whether the
// signer is one of the platform's published keys
export async function verifyReport(
  file: {
    report: Record<string, unknown>;
    signature: { scheme: string; signer: string; value: string };
  },
  keys: { current: string; retired: string[] },
): Promise<Verdict> {
  if (file.signature.scheme !== "sep-53") {
    return { ok: false, reason: `unknown signature scheme ${file.signature.scheme}` };
  }
  let bytes: Uint8Array;
  try {
    bytes = new TextEncoder().encode(canonicalize(file.report));
  } catch (e) {
    return { ok: false, reason: e instanceof Error ? e.message : "no canonical form" };
  }
  let valid = false;
  try {
    valid = await verifySep53(file.signature.signer, bytes, file.signature.value);
  } catch (e) {
    return {
      ok: false,
      reason:
        e instanceof Error ? `this browser could not check ed25519: ${e.message}` : "check failed",
    };
  }
  if (!valid) return { ok: false, reason: "the signature does not match the report" };
  const issuer = file.report.issuer as { signing_key?: unknown } | undefined;
  if (issuer?.signing_key !== file.signature.signer) {
    return {
      ok: false,
      reason: "the report names another signing key than the one that signed it",
    };
  }
  const s = file.signature.signer;
  const key = s === keys.current ? "current" : keys.retired.includes(s) ? "retired" : "unknown";
  return { ok: true, signer: s, key };
}
