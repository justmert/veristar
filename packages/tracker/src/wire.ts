// wire format v1 between sdk.js and the gateway. internal/events/wire.go mirrors
// these names and limits; docs/reference/wire-v1.md is the prose version.

export const WIRE_VERSION = 1;

// paths on the gateway host. the names avoid words that filter lists penalise.
export const CHALLENGE_PATH = "/v1/c";
export const EVENT_PATH = "/v1/e";

// body is sent as text/plain so the post stays a cors simple request
export const EVENT_CONTENT_TYPE = "text/plain;charset=UTF-8";

export const LIMITS = {
  bodyBytes: 8192,
  url: 500,
  referrer: 500,
  title: 500,
  hostname: 100,
  screen: 11,
  language: 35,
  eventName: 50,
  dataKeys: 16,
  dataKeyLength: 50,
  dataValueLength: 500,
} as const;

// the tracker refuses challenges harder than this, so a misconfigured gateway
// cannot make every visitor's browser search for minutes
export const MAX_PROOF_BITS = 24;

// a challenge is valid for 6 hours of server time. the tracker fetches a new
// one after 5 hours of local monotonic time, which leaves an hour of margin.
export const CHALLENGE_REFRESH_MS = 5 * 60 * 60 * 1000;

export type EventType = "pv" | "ev";

// flat object only: nested values are refused by the gateway
export type WireScalar = string | number | boolean;
export type WireData = Record<string, WireScalar>;

// what GET /v1/c?w=<site id> returns
export type Challenge = {
  w: string;
  iat: number;
  salt: string;
  bits: number;
  tag: string;
};

export type Proof = {
  iat: number;
  salt: string;
  bits: number;
  tag: string;
  nonce: number;
};

export type WireEvent = {
  v: typeof WIRE_VERSION;
  w: string;
  t: EventType;
  h: string;
  u: string;
  r: string;
  ti: string;
  s: string;
  l: string;
  n?: string;
  d?: WireData;
  ts: number;
  p: Proof;
};

const HEX32 = /^[0-9a-f]{32}$/;
const HEX64 = /^[0-9a-f]{64}$/;

// returns the challenge when the gateway answer has the v1 shape for this site,
// otherwise an error message. the tracker never guesses missing fields.
export function parseChallenge(value: unknown, siteId: string): Challenge | string {
  if (typeof value !== "object" || value === null) return "challenge is not an object";
  const c = value as Record<string, unknown>;
  if (c.w !== siteId) return "challenge is for another site";
  if (typeof c.iat !== "number" || !Number.isSafeInteger(c.iat) || c.iat <= 0)
    return "challenge iat is not a positive integer";
  if (typeof c.salt !== "string" || !HEX32.test(c.salt)) return "challenge salt is not 32 hex";
  if (
    typeof c.bits !== "number" ||
    !Number.isInteger(c.bits) ||
    c.bits < 0 ||
    c.bits > MAX_PROOF_BITS
  )
    return `challenge bits is not an integer from 0 to ${MAX_PROOF_BITS}`;
  if (typeof c.tag !== "string" || !HEX64.test(c.tag)) return "challenge tag is not 64 hex";
  return { w: c.w, iat: c.iat, salt: c.salt, bits: c.bits, tag: c.tag };
}

// the exact strings that are hashed. "|" cannot occur inside any part: the site
// id is a uuid, iat, bits and nonce are decimal integers and salt is hex.
export function proofInput(w: string, iat: number, salt: string, nonce: number): string {
  return `${w}|${iat}|${salt}|${nonce}`;
}

export function tagInput(w: string, iat: number, salt: string, bits: number): string {
  return `${w}|${iat}|${salt}|${bits}`;
}

// cuts a string to at most max utf-16 code units without splitting a surrogate pair
export function clip(value: string, max: number): string {
  if (value.length <= max) return value;
  const end = max > 0 && isHighSurrogate(value.charCodeAt(max - 1)) ? max - 1 : max;
  return value.slice(0, end);
}

function isHighSurrogate(code: number): boolean {
  return code >= 0xd800 && code <= 0xdbff;
}

// unicode category cc, which the gateway refuses in names and keys
// biome-ignore lint/suspicious/noControlCharactersInRegex: matching them is the point
export const CONTROL = /[\u0000-\u001f\u007f-\u009f]/;

// an event name is checked like a data key: control characters are refused
export function checkName(name: string): string | undefined {
  if (CONTROL.test(name)) return "event name contains control characters";
  return undefined;
}

// a fragment is sent only when it is a hash-router route ("#/..."), because
// other fragments can carry secrets such as oauth access tokens. the query part
// inside a route fragment is dropped for the same reason.
export function routeFragment(hash: string, excludeHash: boolean): string {
  if (excludeHash || !hash.startsWith("#/")) return "";
  const q = hash.indexOf("?");
  return q === -1 ? hash : hash.slice(0, q);
}

// event data is checked rather than repaired: a nested object or too many keys
// is a programming error on the site, and silently dropping keys would hide it.
// long string values are cut to the limit, as umami does.
export function checkData(data: unknown): WireData | string {
  if (typeof data !== "object" || data === null || Array.isArray(data))
    return "event data must be a plain object";
  const out: WireData = {};
  const keys = Object.keys(data);
  if (keys.length > LIMITS.dataKeys) return `event data has more than ${LIMITS.dataKeys} keys`;
  for (const key of keys) {
    if (key.length === 0 || key.length > LIMITS.dataKeyLength || CONTROL.test(key))
      return `event data key "${clip(key, 60)}" must be 1 to ${LIMITS.dataKeyLength} characters without control characters`;
    const value = (data as Record<string, unknown>)[key];
    if (typeof value === "string") {
      if (value.includes("\u0000")) return `event data value for "${key}" contains a nul character`;
      out[key] = clip(value, LIMITS.dataValueLength);
    } else if (typeof value === "boolean") out[key] = value;
    else if (typeof value === "number" && Number.isFinite(value)) out[key] = value;
    else return `event data value for "${key}" must be a string, finite number or boolean`;
  }
  return out;
}

export function encodeEvent(event: WireEvent): string {
  return JSON.stringify(event);
}

export function byteLength(text: string): number {
  return new TextEncoder().encode(text).length;
}
