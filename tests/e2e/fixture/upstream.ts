// test fixture only. the real-gateway mode of the browser matrix: instead of
// answering gateway requests itself, the tls front forwards them over https to
// the edge caddy (deploy/caddy/edge.Caddyfile), which proxies to the real
// gateway binary in docker. the fixture still records every request and the
// real response status, so the specs run unchanged in both modes, and it
// appends every event the gateway answered to a jsonl log the go test
// (tests/gateway) compares with what reached WEB_EVENTS.
import { appendFileSync } from "node:fs";
import type { IncomingMessage, ServerResponse } from "node:http";
import { request as httpsRequest } from "node:https";
import {
  flatHeaders,
  MAX_BODY_BYTES,
  type RecordedRequest,
  readBody,
  type Verdict,
} from "./gateway.ts";

export interface UpstreamOptions {
  // host:port of caddy's https listener as seen from this process
  upstream: string;
  // the gateway host name caddy serves, sent as sni and host
  host: string;
  // jsonl file every answered event is appended to
  eventLog: string;
}

// reasons the specs look for, the same words the fixture gateway records
function reasonFor(method: string, path: string, status: number, tampered: boolean): string {
  if (method === "POST" && path === "/v1/e")
    return status === 202 ? "accepted" : `status ${status}`;
  if (method === "GET" && path === "/v1/c")
    return status === 200 ? "challenge issued" : `status ${status}`;
  if (status === 200) return tampered ? "served tampered" : "served";
  return `status ${status}`;
}

export class UpstreamGateway {
  readonly recorded: RecordedRequest[] = [];
  private seq = 0;
  private readonly tampered = new Set<string>();
  private readonly opts: UpstreamOptions;

  constructor(opts: UpstreamOptions) {
    this.opts = opts;
  }

  reset(): void {
    this.recorded.length = 0;
    this.tampered.clear();
  }

  setTampered(version: string, on: boolean): void {
    if (on) this.tampered.add(version);
    else this.tampered.delete(version);
  }

  async handle(req: IncomingMessage, res: ServerResponse): Promise<void> {
    const url = new URL(req.url ?? "/", "https://gateway.invalid");
    // one byte over the gateway's limit, so an oversized body still reaches
    // it as oversized
    const body = await readBody(req, MAX_BODY_BYTES + 1);
    let json: unknown = null;
    if (url.pathname === "/v1/e") {
      try {
        json = JSON.parse(body.toString("utf8"));
      } catch {
        json = null;
      }
    }
    const rec: RecordedRequest = {
      seq: ++this.seq,
      at: Date.now(),
      method: req.method ?? "",
      path: url.pathname,
      query: Object.fromEntries(url.searchParams),
      headers: flatHeaders(req),
      body: body.toString("utf8"),
      json,
      verdict: { status: 0, reason: "" },
    };
    this.recorded.push(rec);
    const sdk = /^\/v\/([^/]+)\/sdk\.js$/.exec(url.pathname);
    const tamper = req.method === "GET" && sdk !== null && this.tampered.has(sdk[1] ?? "");
    const up = await this.forward(req, body);
    let payload = up.body;
    const headers = { ...up.headers };
    delete headers["content-length"];
    delete headers["transfer-encoding"];
    if (tamper && up.status === 200) {
      // a working payload prepended to the real file, kept out of the cache so
      // the control reload gets the real bytes
      payload = Buffer.concat([Buffer.from("window.__saTampered=true;\n"), payload]);
      headers["cache-control"] = "no-store";
    }
    headers["content-length"] = String(payload.length);
    res.writeHead(up.status, headers);
    res.end(payload);
    const verdict: Verdict = {
      status: up.status,
      reason: reasonFor(rec.method, rec.path, up.status, tamper),
    };
    rec.verdict = verdict;
    if (rec.method === "POST" && rec.path === "/v1/e") {
      appendFileSync(
        this.opts.eventLog,
        `${JSON.stringify({ status: up.status, origin: rec.headers.origin ?? "", body: json })}\n`,
      );
    }
  }

  private forward(
    req: IncomingMessage,
    body: Buffer,
  ): Promise<{ status: number; headers: Record<string, string | string[]>; body: Buffer }> {
    const [host = "", port = ""] = this.opts.upstream.split(":");
    const headers: Record<string, string | string[] | undefined> = {
      ...req.headers,
      host: this.opts.host,
    };
    // hop-by-hop headers of the browser connection do not go upstream
    delete headers.connection;
    delete headers["keep-alive"];
    headers["content-length"] = String(body.length);
    return new Promise((resolve, reject) => {
      const up = httpsRequest(
        {
          host,
          port: Number(port),
          method: req.method,
          path: req.url,
          headers,
          servername: this.opts.host,
          // caddy serves a certificate from its internal ca in this setup
          rejectUnauthorized: false,
        },
        (r) => {
          const chunks: Buffer[] = [];
          r.on("data", (c: Buffer) => chunks.push(c));
          r.on("end", () => {
            const out: Record<string, string | string[]> = {};
            for (const [k, v] of Object.entries(r.headers)) if (v !== undefined) out[k] = v;
            resolve({ status: r.statusCode ?? 502, headers: out, body: Buffer.concat(chunks) });
          });
          r.on("error", reject);
        },
      );
      up.on("error", reject);
      up.end(body);
    });
  }
}
