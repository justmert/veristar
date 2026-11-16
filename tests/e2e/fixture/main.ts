// test fixture only, not product code. starts three local listeners for the
// browser matrix:
//   proxy   (127.0.0.1:PORTS.proxy)   every browser is launched with this as its
//           http proxy. it accepts CONNECT only for the fake .example hostnames
//           and tunnels them to the tls front, which is how those names resolve
//           in all three engines without touching /etc/hosts.
//   tls     (127.0.0.1:PORTS.tls)     terminates https. the gateway host goes to
//           the fixture gateway, tenant hosts are reverse proxied to the example
//           servers on their local ports.
//   control (127.0.0.1:PORTS.control) json api for the test runner: recorded
//           requests, reset, tamper switch. browsers never reach it.

import { readFileSync } from "node:fs";
import {
  createServer as createHttpServer,
  request as httpRequest,
  type ServerResponse,
} from "node:http";
import { createServer as createHttpsServer } from "node:https";
import { connect, type Socket } from "node:net";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { GATEWAY_HOST, HOSTED_SITES, PORTS, TENANT_DOMAIN } from "../sites.ts";
import { createCert } from "./certs.ts";
import { checkVectors, FixtureGateway, type ProofVector } from "./gateway.ts";
import { UpstreamGateway } from "./upstream.ts";

const here = dirname(fileURLToPath(import.meta.url));
const releasesDir = resolve(here, "../../../packages/tracker/releases");

function requiredString(name: string): string {
  const raw = process.env[name];
  if (raw === undefined || raw === "") throw new Error(`missing env ${name}`);
  return raw;
}

function requiredInt(name: string): number {
  const raw = process.env[name];
  if (raw === undefined || raw === "") throw new Error(`missing env ${name}`);
  const n = Number(raw);
  if (!Number.isInteger(n) || n < 0) throw new Error(`env ${name} must be a non-negative integer`);
  return n;
}

// the challenge key and the definitions of tag and work come from the tracker's
// shared vectors; a disagreement stops the fixture before any test runs
const vectorsFile = resolve(here, "../../../packages/tracker/test/vectors/proof-v1.json");
const { vectors } = JSON.parse(readFileSync(vectorsFile, "utf8")) as { vectors: ProofVector[] };
checkVectors(vectors);
const proofKeyHex = vectors[0]?.proofKeyHex;
if (!proofKeyHex) throw new Error(`${vectorsFile} has no proof key`);

// SA_E2E_GATEWAY_UPSTREAM switches to the real-gateway mode (upstream.ts): the
// go test in tests/gateway starts caddy, the gateway image and nats, then runs
// the same specs with requests forwarded to caddy
const upstream = process.env.SA_E2E_GATEWAY_UPSTREAM ?? "";
const gateway = upstream
  ? new UpstreamGateway({
      upstream,
      host: GATEWAY_HOST,
      eventLog: requiredString("SA_E2E_EVENT_LOG"),
    })
  : new FixtureGateway({
      releasesDir,
      proofKey: Buffer.from(proofKeyHex, "hex"),
      proofBits: requiredInt("SA_E2E_PROOF_BITS"),
      replayLimit: requiredInt("SA_E2E_REPLAY_LIMIT"),
      sites: HOSTED_SITES.map((s) => ({ siteId: s.siteId, origins: [s.origin], status: "active" })),
    });

const siteByHost = new Map(HOSTED_SITES.map((s) => [s.host, s]));
// requests that reached the proxy or tls front for a host the fixture does not
// serve; a test can assert this stays empty
const refused: { at: number; kind: string; target: string }[] = [];

// a page on a tenant origin that asks chromium to prerender a page of the
// same site and navigates to it a few seconds later, which activates the
// prerendered page (tests/e2e/specs/webpath/signals.webpath.ts). served by
// the fixture, so no example site has to carry speculation rules, which
// would change what every other test sees in chromium. the target is a path
// of the same origin and nothing else.
const PRERENDER_PATH = "/__prerender";
function servePrerenderLauncher(rawUrl: string, res: ServerResponse) {
  const u = new URL(rawUrl, "https://launcher.invalid");
  const to = u.searchParams.get("to") ?? "/";
  const delay = Number(u.searchParams.get("delay") ?? "4000");
  if (!/^\/[A-Za-z0-9/_.-]*$/.test(to) || !Number.isInteger(delay) || delay < 0 || delay > 60_000) {
    res.writeHead(400);
    res.end("bad launcher parameters");
    return;
  }
  const rules = JSON.stringify({
    prerender: [{ source: "list", urls: [to], eagerness: "immediate" }],
  });
  res.writeHead(200, { "content-type": "text/html; charset=utf-8", "cache-control": "no-store" });
  res.end(
    `<!doctype html><title>prerender launcher</title><script type="speculationrules">${rules}</script>` +
      `<script>setTimeout(() => { location.href = ${JSON.stringify(to)}; }, ${delay});</script>`,
  );
}

const cert = createCert([GATEWAY_HOST, `*.${TENANT_DOMAIN}`]);

const tls = createHttpsServer({ key: cert.key, cert: cert.cert }, (req, res) => {
  const host = (req.headers.host ?? "").replace(/:\d+$/, "");
  if (host === GATEWAY_HOST) {
    gateway.handle(req, res).catch((err: unknown) => {
      res.writeHead(500);
      res.end(String(err));
    });
    return;
  }
  const site = siteByHost.get(host);
  if (site && (req.url ?? "").startsWith(PRERENDER_PATH)) {
    servePrerenderLauncher(req.url ?? "", res);
    return;
  }
  if (!site) {
    refused.push({ at: Date.now(), kind: "tls-host", target: host });
    res.writeHead(421);
    res.end("unknown host");
    return;
  }
  const upstream = httpRequest(
    {
      host: "127.0.0.1",
      port: site.port,
      method: req.method,
      path: req.url,
      headers: { ...req.headers, "x-forwarded-proto": "https", "x-forwarded-host": host },
    },
    (up) => {
      res.writeHead(up.statusCode ?? 502, up.headers);
      up.pipe(res);
    },
  );
  upstream.on("error", (err) => {
    res.writeHead(502);
    res.end(`upstream ${site.key}: ${err.message}`);
  });
  req.pipe(upstream);
});

const proxy = createHttpServer((req, res) => {
  // plain http through the proxy is never expected; every page is https
  refused.push({ at: Date.now(), kind: "proxy-http", target: req.url ?? "" });
  res.writeHead(403);
  res.end("only CONNECT to fixture hosts");
});

proxy.on("connect", (req, clientSocket: Socket, head: Buffer) => {
  const [host = "", port = ""] = (req.url ?? "").split(":");
  const known = host === GATEWAY_HOST || siteByHost.has(host);
  if (!known || port !== "443") {
    // browsers phone home (updates, safe browsing, telemetry). refusing keeps the
    // run offline and deterministic.
    refused.push({ at: Date.now(), kind: "proxy-connect", target: req.url ?? "" });
    clientSocket.end("HTTP/1.1 403 Forbidden\r\n\r\n");
    return;
  }
  const upstream = connect(PORTS.tls, "127.0.0.1", () => {
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
  const url = new URL(req.url ?? "/", "http://control.invalid");
  const json = (status: number, body: unknown) => {
    // node's fetch reuses idle sockets; closing each one avoids a reset when
    // the server's keep-alive timer fires between two test polls
    res.writeHead(status, { "content-type": "application/json", connection: "close" });
    res.end(JSON.stringify(body));
  };
  if (req.method === "GET" && url.pathname === "/health") return json(200, { ok: true });
  if (req.method === "GET" && url.pathname === "/requests") return json(200, gateway.recorded);
  if (req.method === "GET" && url.pathname === "/refused") return json(200, refused);
  if (req.method === "POST" && url.pathname === "/reset") {
    gateway.reset();
    refused.length = 0;
    return json(200, { ok: true });
  }
  if (req.method === "POST" && url.pathname === "/tamper") {
    const version = url.searchParams.get("version") ?? "";
    const on = url.searchParams.get("on") === "1";
    gateway.setTampered(version, on);
    return json(200, { ok: true, version, on });
  }
  return json(404, { error: "not found" });
});

tls.listen(PORTS.tls, "127.0.0.1");
proxy.listen(PORTS.proxy, "127.0.0.1");
control.listen(PORTS.control, "127.0.0.1");
console.log(
  `e2e fixture (${upstream ? `real gateway via ${upstream}` : "fixture gateway"}): proxy :${PORTS.proxy}, tls :${PORTS.tls}, control :${PORTS.control}, releases ${join(releasesDir)}`,
);

for (const sig of ["SIGINT", "SIGTERM"] as const) {
  process.on(sig, () => {
    tls.close();
    proxy.close();
    control.close();
    process.exit(0);
  });
}
