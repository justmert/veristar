// test fixture only, not product code. research harness for the redirect path of
// a domain handover. it starts
// the same proxy and tls pattern as tests/e2e/fixture, with these names:
//   OLD_HOST       answers 308 to NEW_HOST for every path, with
//                  access-control-allow-origin: * on the redirect itself
//   OLD_BARE_HOST  the same 308 without any cors header
//   NEW_HOST       the fixture gateway (tests/e2e/fixture/gateway.ts): released
//                  sdk.js bytes with the production headers, the challenge and the
//                  event checks, plus the go gateway's origin rule on /v1/c
//   TENANT_HOST    pages carrying the exact snippet the control plane builds
// the control port lists every request each host saw, with headers and body.

import { readFileSync } from "node:fs";
import { createServer as createHttpServer, type IncomingMessage } from "node:http";
import { createServer as createHttpsServer } from "node:https";
import { connect, type Socket } from "node:net";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { snippet } from "../../../packages/tracker/src/snippet.ts";
import { createCert } from "../fixture/certs.ts";
import {
  checkVectors,
  FixtureGateway,
  flatHeaders,
  type ProofVector,
  readBody,
} from "../fixture/gateway.ts";
import {
  NEW_HOST,
  OLD_BARE_HOST,
  OLD_HOST,
  REDIRECT_PORTS,
  SITE_ID,
  TENANT_HOST,
  TENANT_ORIGIN,
} from "./hosts.ts";

const here = dirname(fileURLToPath(import.meta.url));
const releasesDir = resolve(here, "../../../packages/tracker/releases");
const manifest = JSON.parse(readFileSync(resolve(releasesDir, "manifest.json"), "utf8")) as {
  versions: { version: string; integrity: string }[];
};

const vectorsFile = resolve(here, "../../../packages/tracker/test/vectors/proof-v1.json");
const { vectors } = JSON.parse(readFileSync(vectorsFile, "utf8")) as { vectors: ProofVector[] };
checkVectors(vectors);
const proofKeyHex = vectors[0]?.proofKeyHex;
if (!proofKeyHex) throw new Error(`${vectorsFile} has no proof key`);

const gateway = new FixtureGateway({
  releasesDir,
  proofKey: Buffer.from(proofKeyHex, "hex"),
  // low, so the research spends its time on redirects rather than hashing
  proofBits: 8,
  replayLimit: 100,
  sites: [{ siteId: SITE_ID, origins: [TENANT_ORIGIN], status: "active" }],
});

export interface SeenRequest {
  host: string;
  method: string;
  path: string;
  headers: Record<string, string>;
  body: string;
  status: number;
  reason: string;
}

// requests at the old hosts and refusals at the new host made here; everything
// the fixture gateway answered is read from gateway.recorded
const seen: SeenRequest[] = [];
const refused: string[] = [];

const ALL_HOSTS = [OLD_HOST, OLD_BARE_HOST, NEW_HOST, TENANT_HOST];
const cert = createCert(ALL_HOSTS);

function integrityOf(version: string): string {
  const v = manifest.versions.find((e) => e.version === version);
  if (!v) throw new Error(`release ${version} is not in the manifest`);
  return v.integrity;
}

// the page carries the snippet byte for byte as the control plane builds it,
// pointed at the origin under test. ?integrity=<version> swaps in the published
// integrity of another release for the negative control.
function tenantPage(url: URL): string {
  const origin = url.searchParams.get("origin") ?? "";
  const version = url.searchParams.get("version") ?? "";
  let tag = snippet({ siteId: SITE_ID, version, origin });
  const other = url.searchParams.get("integrity");
  if (other) tag = tag.replace(integrityOf(version), integrityOf(other));
  return `<!doctype html><html lang="en"><head><meta charset="utf-8"><title>redirect research</title>${tag}</head><body><a id="away" href="/blank">away</a></body></html>`;
}

// mirrors CheckOrigin in internal/gateway/origin.go, which the go gateway applies
// to the challenge as well as to events; the fixture gateway checks events only
function challengeOriginRefused(req: IncomingMessage): boolean {
  return req.headers.origin !== TENANT_ORIGIN;
}

function redirect(req: IncomingMessage, res: import("node:http").ServerResponse, cors: boolean) {
  const host = (req.headers.host ?? "").replace(/:\d+$/, "");
  readBody(req, 64 * 1024).then(
    (body) => {
      const headers: Record<string, string> = {
        location: `https://${NEW_HOST}${req.url ?? "/"}`,
        // no-store keeps one case's redirect out of the next case's cache; a
        // production redirect would be cacheable, which changes nothing here
        "cache-control": "no-store",
        "content-length": "0",
      };
      if (cors) headers["access-control-allow-origin"] = "*";
      res.writeHead(308, headers);
      res.end();
      seen.push({
        host,
        method: req.method ?? "",
        path: new URL(req.url ?? "/", "https://fixture.invalid").pathname,
        headers: flatHeaders(req),
        body: body.toString("utf8"),
        status: 308,
        reason: cors ? "redirect with cors" : "redirect without cors",
      });
    },
    (err: unknown) => {
      res.writeHead(500);
      res.end(String(err));
    },
  );
}

const tls = createHttpsServer({ key: cert.key, cert: cert.cert }, (req, res) => {
  const host = (req.headers.host ?? "").replace(/:\d+$/, "");
  const url = new URL(req.url ?? "/", "https://fixture.invalid");
  if (host === OLD_HOST) return redirect(req, res, true);
  if (host === OLD_BARE_HOST) return redirect(req, res, false);
  if (host === NEW_HOST) {
    if (req.method === "GET" && url.pathname === "/v1/c" && challengeOriginRefused(req)) {
      // the go gateway reflects the request's origin, including "null"
      const headers: Record<string, string> = { "cache-control": "no-store" };
      if (req.headers.origin) headers["access-control-allow-origin"] = req.headers.origin;
      res.writeHead(403, headers);
      res.end("origin not registered");
      seen.push({
        host,
        method: "GET",
        path: new URL(req.url ?? "/", "https://fixture.invalid").pathname,
        headers: flatHeaders(req),
        body: "",
        status: 403,
        reason: `challenge origin ${req.headers.origin ?? "(none)"} not registered`,
      });
      return;
    }
    gateway.handle(req, res).catch((err: unknown) => {
      res.writeHead(500);
      res.end(String(err));
    });
    return;
  }
  if (host === TENANT_HOST) {
    if (url.pathname === "/page") {
      res.writeHead(200, {
        "content-type": "text/html; charset=utf-8",
        "cache-control": "no-store",
      });
      res.end(tenantPage(url));
      return;
    }
    if (url.pathname === "/blank") {
      res.writeHead(200, {
        "content-type": "text/html; charset=utf-8",
        "cache-control": "no-store",
      });
      res.end(
        '<!doctype html><html lang="en"><head><title>blank</title></head><body></body></html>',
      );
      return;
    }
    res.writeHead(404);
    res.end("not found");
    return;
  }
  refused.push(`tls ${host}`);
  res.writeHead(421);
  res.end("unknown host");
});

const proxy = createHttpServer((req, res) => {
  refused.push(`http ${req.url ?? ""}`);
  res.writeHead(403);
  res.end("only CONNECT to fixture hosts");
});

proxy.on("connect", (req, clientSocket: Socket, head: Buffer) => {
  const [host = "", port = ""] = (req.url ?? "").split(":");
  if (!ALL_HOSTS.includes(host) || port !== "443") {
    // keeps browsers' own background traffic out of the run
    clientSocket.end("HTTP/1.1 403 Forbidden\r\n\r\n");
    return;
  }
  const upstream = connect(REDIRECT_PORTS.tls, "127.0.0.1", () => {
    clientSocket.write("HTTP/1.1 200 Connection Established\r\n\r\n");
    if (head.length) upstream.write(head);
    upstream.pipe(clientSocket);
    clientSocket.pipe(upstream);
  });
  const close = () => {
    upstream.destroy();
    clientSocket.destroy();
  };
  upstream.on("error", close);
  clientSocket.on("error", close);
});

const control = createHttpServer((req, res) => {
  const json = (status: number, body: unknown) => {
    res.writeHead(status, { "content-type": "application/json", connection: "close" });
    res.end(JSON.stringify(body));
  };
  if (req.method === "GET" && req.url === "/health") return json(200, { ok: true });
  if (req.method === "GET" && req.url === "/requests") {
    const fromGateway: SeenRequest[] = gateway.recorded.map((r) => ({
      host: NEW_HOST,
      method: r.method,
      path: r.path,
      headers: r.headers,
      body: r.body,
      status: r.verdict.status,
      reason: r.verdict.reason,
    }));
    return json(200, [...seen, ...fromGateway]);
  }
  if (req.method === "GET" && req.url === "/refused") return json(200, refused);
  if (req.method === "POST" && req.url === "/reset") {
    gateway.reset();
    seen.length = 0;
    refused.length = 0;
    return json(200, { ok: true });
  }
  return json(404, { error: "not found" });
});

tls.listen(REDIRECT_PORTS.tls, "127.0.0.1");
proxy.listen(REDIRECT_PORTS.proxy, "127.0.0.1");
control.listen(REDIRECT_PORTS.control, "127.0.0.1");
console.log(
  `redirect fixture: proxy :${REDIRECT_PORTS.proxy}, tls :${REDIRECT_PORTS.tls}, control :${REDIRECT_PORTS.control}`,
);

for (const sig of ["SIGINT", "SIGTERM"] as const) {
  process.on(sig, () => {
    tls.close();
    proxy.close();
    control.close();
    process.exit(0);
  });
}
