// test fixture only, not product code. the real gateway is the go service built
// from cmd/gateway and internal/gateway. this module implements the gateway side
// of the web event contract (docs/reference/wire-v1.md) so the browser matrix can
// run against real browsers without that service: it serves the immutable
// tracker files, answers the challenge, checks events in the documented order
// and records every request it sees for the tests to inspect.
//
// what it deliberately leaves out, because the matrix does not exercise it:
// the registry lookup over nats (sites come from a fixed table), the edge_secrets
// kv (the proof key comes from the shared test vectors), country lookup, visitor
// tokens, same-ip skepticism, redaction, rate limits and the jetstream publish.
import { createHash, createHmac, randomBytes, timingSafeEqual } from "node:crypto";
import { existsSync, readFileSync } from "node:fs";
import type { IncomingMessage, ServerResponse } from "node:http";
import { join } from "node:path";

export const MAX_BODY_BYTES = 8 * 1024;
const CHALLENGE_MAX_AGE_S = 6 * 60 * 60;

export interface RegisteredSite {
  siteId: string;
  origins: string[];
  status: "active" | "suspended";
}

export interface GatewayOptions {
  releasesDir: string;
  proofKey: Buffer;
  sites: RegisteredSite[];
  // difficulty handed out in challenges. in production this is the server-side
  // PROOF_BITS setting; the fixture takes it explicitly so tests stay fast and
  // the value used is visible in the config instead of hidden in here.
  proofBits: number;
  // per-proof replay limit (section 4.4 item 6). a proof covers every event of
  // one page load, so the limit must exceed the events a test page produces.
  replayLimit: number;
}

export interface Verdict {
  status: number;
  reason: string;
}

export interface RecordedRequest {
  seq: number;
  at: number;
  method: string;
  path: string;
  query: Record<string, string>;
  headers: Record<string, string>;
  body: string;
  // parsed json body for /v1/e, null when it did not parse
  json: unknown;
  verdict: Verdict;
}

export interface Challenge {
  w: string;
  iat: number;
  salt: string;
  bits: number;
  tag: string;
}

export interface Proof {
  iat: number;
  salt: string;
  bits: number;
  tag: string;
  nonce: number;
}

const SITE_ID_RE = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;
const VERSION_RE = /^\d+\.\d+\.\d+$/;

export interface ProofVector {
  proofKeyHex: string;
  w: string;
  iat: number;
  salt: string;
  bits: number;
  tag: string;
  nonce: number;
  digest: string;
}

// fails loudly when this fixture's tag or work check disagrees with the shared
// vectors in packages/tracker/test/vectors/proof-v1.json, so the matrix can
// never pass against a fixture that verifies something other than the tracker
export function checkVectors(vectors: ProofVector[]): void {
  if (vectors.length === 0) throw new Error("no proof vectors");
  for (const v of vectors) {
    const tag = createHmac("sha256", Buffer.from(v.proofKeyHex, "hex"))
      .update(`${v.w}|${v.iat}|${v.salt}|${v.bits}`)
      .digest("hex");
    if (tag !== v.tag) throw new Error(`vector tag mismatch for bits ${v.bits}`);
    const digest = createHash("sha256").update(`${v.w}|${v.iat}|${v.salt}|${v.nonce}`).digest();
    if (digest.toString("hex") !== v.digest)
      throw new Error(`vector digest mismatch for bits ${v.bits}`);
    if (leadingZeroBits(digest) < v.bits) throw new Error(`vector work below bits ${v.bits}`);
  }
}

export function leadingZeroBits(digest: Uint8Array): number {
  let n = 0;
  for (const byte of digest) {
    if (byte === 0) {
      n += 8;
      continue;
    }
    return n + Math.clz32(byte) - 24;
  }
  return n;
}

export class FixtureGateway {
  readonly recorded: RecordedRequest[] = [];
  private seq = 0;
  private readonly replayCounts = new Map<string, number>();
  private readonly sites = new Map<string, RegisteredSite>();
  // versions whose sdk.js is served with one byte changed, for the sri test
  private readonly tampered = new Set<string>();

  private readonly opts: GatewayOptions;

  constructor(opts: GatewayOptions) {
    this.opts = opts;
    for (const s of opts.sites) this.sites.set(s.siteId, s);
  }

  reset(): void {
    this.recorded.length = 0;
    this.replayCounts.clear();
    this.tampered.clear();
  }

  setTampered(version: string, on: boolean): void {
    if (on) this.tampered.add(version);
    else this.tampered.delete(version);
  }

  tag(w: string, iat: number, salt: string, bits: number): string {
    return createHmac("sha256", this.opts.proofKey)
      .update(`${w}|${iat}|${salt}|${bits}`)
      .digest("hex");
  }

  issueChallenge(w: string): Challenge {
    const iat = Math.floor(Date.now() / 1000);
    const salt = randomBytes(16).toString("hex");
    const bits = this.opts.proofBits;
    return { w, iat, salt, bits, tag: this.tag(w, iat, salt, bits) };
  }

  // checks tag, age and work. returns null when the proof is valid.
  verifyProof(w: string, p: Proof): string | null {
    const expected = Buffer.from(this.tag(w, p.iat, p.salt, p.bits), "hex");
    const got = Buffer.from(p.tag, "hex");
    if (got.length !== expected.length || !timingSafeEqual(got, expected)) return "bad tag";
    const age = Math.floor(Date.now() / 1000) - p.iat;
    if (age < 0 || age > CHALLENGE_MAX_AGE_S) return "challenge expired";
    if (p.bits < this.opts.proofBits) return "difficulty below current";
    const digest = createHash("sha256").update(`${w}|${p.iat}|${p.salt}|${p.nonce}`).digest();
    if (leadingZeroBits(digest) < p.bits) return "insufficient work";
    return null;
  }

  async handle(req: IncomingMessage, res: ServerResponse): Promise<void> {
    const url = new URL(req.url ?? "/", "https://gateway.invalid");
    const body = await readBody(req, MAX_BODY_BYTES + 1);
    const rec: RecordedRequest = {
      seq: ++this.seq,
      at: Date.now(),
      method: req.method ?? "",
      path: url.pathname,
      query: Object.fromEntries(url.searchParams),
      headers: flatHeaders(req),
      body: body.toString("utf8"),
      json: null,
      verdict: { status: 0, reason: "" },
    };
    this.recorded.push(rec);
    const verdict = this.route(req, url, body, rec, res);
    rec.verdict = verdict;
  }

  private route(
    req: IncomingMessage,
    url: URL,
    body: Buffer,
    rec: RecordedRequest,
    res: ServerResponse,
  ): Verdict {
    const sdk = /^\/v\/([^/]+)\/sdk\.js$/.exec(url.pathname);
    if (req.method === "GET" && sdk) return this.serveSdk(sdk[1] ?? "", res);
    if (req.method === "GET" && url.pathname === "/v1/c") return this.challenge(url, res);
    if (req.method === "POST" && url.pathname === "/v1/e") return this.event(req, body, rec, res);
    // anything else, including any OPTIONS preflight, is not part of the contract
    return send(res, 405, "method or path not part of the contract");
  }

  private serveSdk(version: string, res: ServerResponse): Verdict {
    if (!VERSION_RE.test(version)) return send(res, 404, "bad version");
    const file = join(this.opts.releasesDir, version, "sdk.js");
    if (!existsSync(file)) return send(res, 404, "no such release");
    let bytes = readFileSync(file);
    if (this.tampered.has(version)) {
      // a working payload prepended to the original, so a browser that ignored
      // the integrity attribute would visibly run it
      bytes = Buffer.concat([Buffer.from("window.__saTampered=true;\n"), bytes]);
    }
    res.writeHead(200, {
      "content-type": "text/javascript; charset=utf-8",
      "content-length": String(bytes.length),
      // the tampered copy must not stay in the browser cache, or the control
      // reload in the sri test would get it again instead of the real file
      "cache-control": this.tampered.has(version)
        ? "no-store"
        : "public, max-age=31536000, immutable",
      // required for a crossorigin="anonymous" script, which sri needs
      "access-control-allow-origin": "*",
      "cross-origin-resource-policy": "cross-origin",
    });
    res.end(bytes);
    return { status: 200, reason: this.tampered.has(version) ? "served tampered" : "served" };
  }

  private challenge(url: URL, res: ServerResponse): Verdict {
    const w = url.searchParams.get("w") ?? "";
    if (!SITE_ID_RE.test(w)) return send(res, 400, "bad site id", true);
    const c = this.issueChallenge(w);
    const json = JSON.stringify(c);
    res.writeHead(200, {
      "content-type": "application/json",
      "cache-control": "no-store",
      "access-control-allow-origin": "*",
    });
    res.end(json);
    return { status: 200, reason: "challenge issued" };
  }

  // checks in the order of section 4.4 item 6
  private event(
    req: IncomingMessage,
    body: Buffer,
    rec: RecordedRequest,
    res: ServerResponse,
  ): Verdict {
    if (body.length > MAX_BODY_BYTES) return send(res, 413, "body too large", true);
    let parsed: unknown;
    try {
      parsed = JSON.parse(body.toString("utf8"));
    } catch {
      return send(res, 400, "body is not json", true);
    }
    rec.json = parsed;
    const schemaErr = validateWire(parsed);
    if (schemaErr) return send(res, 400, `schema: ${schemaErr}`, true);
    const ev = parsed as WireEvent;

    const site = this.sites.get(ev.w);
    if (site?.status !== "active") return send(res, 404, "unknown or inactive site", true);

    if (req.headers["sec-gpc"] === "1") return send(res, 204, "gpc", true);

    const origin = req.headers.origin ?? "";
    if (!site.origins.includes(origin))
      return send(res, 403, `origin ${origin} not registered`, true);

    const proofErr = this.verifyProof(ev.w, ev.p);
    if (proofErr) return send(res, 403, `proof: ${proofErr}`, true);

    const replayKey = `${ev.w}|${ev.p.iat}|${ev.p.salt}|${ev.p.nonce}`;
    const count = (this.replayCounts.get(replayKey) ?? 0) + 1;
    this.replayCounts.set(replayKey, count);
    if (count > this.opts.replayLimit) return send(res, 429, "replay limit", true);

    const fm = fetchMetadataProblem(req);
    if (fm) return send(res, 403, `fetch metadata: ${fm}`, true);

    return send(res, 202, "accepted", true);
  }
}

// browsers that send fetch metadata must send these values for the tracker's
// cross-site fetch; a mismatch means the request was not made by that fetch
function fetchMetadataProblem(req: IncomingMessage): string | null {
  const site = req.headers["sec-fetch-site"];
  const mode = req.headers["sec-fetch-mode"];
  const dest = req.headers["sec-fetch-dest"];
  if (site === undefined && mode === undefined && dest === undefined) return null;
  if (site !== "cross-site" && site !== "same-site" && site !== "same-origin")
    return `site=${site}`;
  if (mode !== "cors") return `mode=${mode}`;
  if (dest !== "empty") return `dest=${dest}`;
  return null;
}

export interface WireEvent {
  v: 1;
  sv?: string;
  w: string;
  t: "pv" | "ev";
  h: string;
  u: string;
  r: string;
  ti: string;
  s: string;
  l: string;
  n?: string;
  d?: Record<string, unknown>;
  ts: number;
  p: Proof;
}

const WIRE_KEYS = new Set([
  "v",
  "sv",
  "w",
  "t",
  "h",
  "u",
  "r",
  "ti",
  "s",
  "l",
  "n",
  "d",
  "ts",
  "p",
]);
// sv is optional (trackers before 1.2.0 do not send it), at most 20
// characters of x.y.z, as internal/events/wire.go checks it
const SDK_VERSION_RE = /^(0|[1-9][0-9]{0,5})\.(0|[1-9][0-9]{0,5})\.(0|[1-9][0-9]{0,5})$/;
const PROOF_KEYS = new Set(["iat", "salt", "bits", "tag", "nonce"]);

// wire format v1 (docs/reference/wire-v1.md). returns an error string or null.
export function validateWire(x: unknown): string | null {
  if (typeof x !== "object" || x === null || Array.isArray(x)) return "not an object";
  const o = x as Record<string, unknown>;
  for (const k of Object.keys(o)) if (!WIRE_KEYS.has(k)) return `unknown key ${k}`;
  if (o.v !== 1) return "v must be 1";
  if (
    o.sv !== undefined &&
    (typeof o.sv !== "string" || o.sv.length > 20 || !SDK_VERSION_RE.test(o.sv))
  )
    return "sv";
  if (typeof o.w !== "string" || !SITE_ID_RE.test(o.w)) return "w";
  if (o.t !== "pv" && o.t !== "ev") return "t";
  for (const k of ["h", "u", "r", "ti", "l"] as const) {
    if (typeof o[k] !== "string") return k;
  }
  if (typeof o.s !== "string" || !/^\d+x\d+$/.test(o.s)) return "s";
  if (typeof o.ts !== "number" || !Number.isFinite(o.ts)) return "ts";
  if (o.t === "pv") {
    if (o.n !== undefined || o.d !== undefined) return "n and d are for ev only";
  } else {
    if (typeof o.n !== "string" || o.n.length === 0) return "n";
    if (o.d !== undefined) {
      if (typeof o.d !== "object" || o.d === null || Array.isArray(o.d)) return "d";
      // event data is a flat object of scalars
      for (const [k, v] of Object.entries(o.d)) {
        const scalar =
          v === null || typeof v === "string" || typeof v === "boolean" || Number.isFinite(v);
        if (!scalar) return `d.${k}`;
      }
    }
  }
  const p = o.p;
  if (typeof p !== "object" || p === null || Array.isArray(p)) return "p";
  const pr = p as Record<string, unknown>;
  for (const k of Object.keys(pr)) if (!PROOF_KEYS.has(k)) return `unknown proof key ${k}`;
  for (const k of PROOF_KEYS) if (!(k in pr)) return `missing proof key ${k}`;
  if (!Number.isSafeInteger(pr.iat)) return "p.iat";
  if (typeof pr.salt !== "string" || !/^[0-9a-f]{32}$/.test(pr.salt)) return "p.salt";
  if (!Number.isInteger(pr.bits)) return "p.bits";
  if (typeof pr.tag !== "string" || !/^[0-9a-f]{64}$/.test(pr.tag)) return "p.tag";
  if (!Number.isSafeInteger(pr.nonce) || (pr.nonce as number) < 0) return "p.nonce";
  return null;
}

function send(res: ServerResponse, status: number, reason: string, cors = false): Verdict {
  const headers: Record<string, string> = { "content-type": "text/plain; charset=utf-8" };
  // lets the tracker's cors-mode fetch complete without a console error; it does
  // not read the body of /v1/e, but a missing header would reject the promise
  if (cors) headers["access-control-allow-origin"] = "*";
  res.writeHead(status, headers);
  res.end(status === 204 ? undefined : reason);
  return { status, reason };
}

export function flatHeaders(req: IncomingMessage): Record<string, string> {
  const out: Record<string, string> = {};
  for (const [k, v] of Object.entries(req.headers)) {
    if (v === undefined) continue;
    out[k] = Array.isArray(v) ? v.join(", ") : v;
  }
  return out;
}

export function readBody(req: IncomingMessage, limit: number): Promise<Buffer> {
  return new Promise((resolve, reject) => {
    const chunks: Buffer[] = [];
    let size = 0;
    req.on("data", (c: Buffer) => {
      // keep reading past the limit is pointless; one byte over is enough to reject
      if (size < limit) chunks.push(c);
      size += c.length;
    });
    req.on("end", () => resolve(Buffer.concat(chunks).subarray(0, limit)));
    req.on("error", reject);
  });
}
