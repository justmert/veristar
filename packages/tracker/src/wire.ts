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

// the gateway accepts one proof for PROOF_REPLAY_LIMIT events per node (50 at
// the starting values). a single-page app keeps one page load for as long as
// the tab is open, so the tracker fetches a new challenge after this many
// events, below the limit
export const PROOF_MAX_USES = 40;

// 403 reasons after which a new challenge can succeed: the proof was used up
// on that node or its challenge is no longer valid there. the event is sent
// once more with a fresh proof
export const STALE_PROOF_REASONS: readonly string[] = [
  "rejected_replay",
  "rejected_challenge_expired",
  "rejected_challenge_tag",
];

// pv pageview, ev custom event, nv never visible: the page was hidden from
// load until it was left, so no pageview was counted. an nv notice carries no
// url and is never stored as an event; the gateway only counts it per site
// and day for the integrity grade.
export type EventType = "pv" | "ev" | "nv";

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
  // tracker release that sent the event, so the gateway can refuse events of
  // a revoked release. optional: 1.0.0 and 1.1.0 do not send it
  sv?: string;
  w: string;
  t: EventType;
  h: string;
  // u and r are absent on an nv notice only
  u?: string;
  r?: string;
  // title, screen and language are sent from 1.3.0 only when the site asks
  // for them (data-send-title, or a before-send hook that sets them)
  ti?: string;
  s?: string;
  l?: string;
  // true when the page runs inside a frame (from 1.3.0); absent otherwise
  fr?: true;
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

// umami refuses event names starting with a spreadsheet formula trigger
// (src/app/api/send/request-schema.ts, FORMULA_TRIGGER_RE)
const FORMULA_TRIGGER = /^[=+\-@\t\r]/;

// an event name is checked like a data key: control characters are refused,
// and so are names umami would refuse
export function checkName(name: string): string | undefined {
  if (CONTROL.test(name)) return "event name contains control characters";
  if (FORMULA_TRIGGER.test(name)) return "event name must not start with =, +, -, @, tab or return";
  return undefined;
}

// what umami 3.5.0 can store (test/vectors/storable-v1.json, mirrored by
// internal/events/storable.go). numbers go to a numeric(19,4) column and umami
// refuses 1e15 and more; revenue with a currency is refused from 1e14; a
// string in umami's date format goes to a timestamp column, which refuses
// years before 1, so 0000 and all of 0001-01-01 are refused
export const MAX_DATA_NUMBER = 1e15;
export const MAX_REVENUE = 1e14;
const UMAMI_DATE =
  /^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(\.[0-9]{3})?(Z|\+[0-9]{2}:[0-9]{2})?$/;

function dateBeforeYearTwo(value: string): boolean {
  return UMAMI_DATE.test(value) && (value.startsWith("0000-") || value.startsWith("0001-01-01T"));
}

// mirrors umami's isRevenueOutOfRange
function revenueOutOfRange(data: WireData): boolean {
  const { revenue, currency } = data;
  if (typeof currency !== "string" || currency.trim() === "") return false;
  if (typeof revenue !== "number" && typeof revenue !== "string") return false;
  const n = Number(revenue);
  return Number.isFinite(n) && n >= MAX_REVENUE;
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
      if (dateBeforeYearTwo(value)) return `event data date for "${key}" is before year 1`;
      out[key] = clip(value, LIMITS.dataValueLength);
    } else if (typeof value === "boolean") out[key] = value;
    else if (typeof value === "number" && Number.isFinite(value)) {
      if (Math.abs(value) >= MAX_DATA_NUMBER)
        return `event data number for "${key}" must be below 1e15 in absolute value`;
      out[key] = value;
    } else return `event data value for "${key}" must be a string, finite number or boolean`;
  }
  if (revenueOutOfRange(out)) return "event data revenue must be below 1e14 when a currency is set";
  return out;
}

export function encodeEvent(event: WireEvent): string {
  return JSON.stringify(event);
}

export function byteLength(text: string): number {
  return new TextEncoder().encode(text).length;
}
