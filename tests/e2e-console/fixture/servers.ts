// test fixture only, not product code. two listeners for the this stage browser
// suite, started by global-setup.ts:
//
//   proxy  (127.0.0.1:CE2E_PROXY_PORT)  every browser uses it as its http proxy.
//          it is how the test hostnames resolve in all three engines: CONNECT
//          to app., web. and in.<BASE_DOMAIN> is tunnelled to the published
//          tls port of the matching caddy container (the browser does tls
//          with caddy itself, so fetch metadata, cookies and redirects are the
//          real ones), plain http to the oauth fixture host is forwarded to its
//          published port, and the tenant-side hosts go to the local site
//          server below. everything else is refused, so the run stays offline.
//   sites  local https server for the tenant site (the static example's pages
//          with the snippet a test copied from the console pasted in), a
//          referring site and a foreign site for cross-site checks.
//   ctl    (127.0.0.1:CE2E_SITE_CTL_PORT)  lets a spec set the pasted snippet and
//          the foreign page. browsers never reach it.

import { readFileSync } from "node:fs";
import {
  createServer as createHttpServer,
  request as httpRequest,
  type IncomingMessage,
  type Server,
} from "node:http";
import { createServer as createHttpsServer } from "node:https";
import { type AddressInfo, connect, type Socket } from "node:net";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { createCert } from "../../e2e/fixture/certs.ts";
import {
  EVIL_HOST,
  isTenantHost,
  LOCAL_HOSTS,
  PROXY_PORT,
  REFERRER_HOST,
  routes,
  SITE_CTL_PORT,
} from "./env.ts";

const here = dirname(fileURLToPath(import.meta.url));
// the hand-written example site; a tenant pastes the snippet where the marker is
const exampleDir = resolve(here, "../../../examples/static-html/src");
const MARKER = "<!-- tracker -->";

function readBody(req: IncomingMessage): Promise<string> {
  return new Promise((ok, fail) => {
    const chunks: Buffer[] = [];
    req.on("data", (c: Buffer) => chunks.push(c));
    req.on("end", () => ok(Buffer.concat(chunks).toString("utf8")));
    req.on("error", fail);
  });
}

export interface Fixture {
  close(): Promise<void>;
}

export async function startServers(): Promise<Fixture> {
  const routeMap = routes();
  // the pasted tag per tenant host
  const snippets = new Map<string, string>();
  let evil = "<!doctype html><title>foreign site</title><p>nothing set</p>";
  const refused: string[] = [];

  const cert = createCert(LOCAL_HOSTS);
  const sites = createHttpsServer({ key: cert.key, cert: cert.cert }, (req, res) => {
    const host = (req.headers.host ?? "").replace(/:\d+$/, "");
    const path = new URL(req.url ?? "/", "https://x.invalid").pathname;
    const html = (status: number, body: string) => {
      res.writeHead(status, {
        "content-type": "text/html; charset=utf-8",
        "cache-control": "no-store",
      });
      res.end(body);
    };
    if (isTenantHost(host)) {
      const snippet = snippets.get(host) ?? "";
      if (path === "/site.css") {
        res.writeHead(200, { "content-type": "text/css; charset=utf-8" });
        res.end(readFileSync(resolve(exampleDir, "site.css")));
        return;
      }
      // a page of the tenant origin without the snippet, from which a test
      // asks the gateway for a challenge to learn that the site is
      // registered, without recording a pageview
      if (path === "/probe.html") return html(200, "<!doctype html><title>probe</title>");
      const page = path === "/" ? "index.html" : path === "/about.html" ? "about.html" : "";
      if (!page) return html(404, "not found");
      // no snippet means the test did not paste one yet; serving the page
      // without it would hide that mistake
      if (!snippet) return html(503, "no snippet pasted yet");
      const parts = readFileSync(resolve(exampleDir, page), "utf8").split(MARKER);
      if (parts.length !== 2) return html(500, `${page} must contain the marker once`);
      return html(200, parts.join(snippet));
    }
    if (host === REFERRER_HOST) {
      // links to the tenant page named in ?to=, which must be a tenant site
      const to = new URL(req.url ?? "/", "https://x.invalid").searchParams.get("to") ?? "";
      let target: URL;
      try {
        target = new URL(to);
      } catch {
        return html(400, "to must be a url");
      }
      if (target.protocol !== "https:" || !isTenantHost(target.hostname))
        return html(400, "to must be a tenant site");
      return html(
        200,
        `<!doctype html><title>referring site</title><a id="to-shop" href="${target.href}">visit the shop</a>`,
      );
    }
    if (host === EVIL_HOST) return html(200, evil);
    html(421, "unknown host");
  });
  await new Promise<void>((ok) => sites.listen(0, "127.0.0.1", ok));
  const sitesPort = (sites.address() as AddressInfo).port;

  // tunnelled sockets are not http connections, so closeAllConnections does
  // not see them; they are tracked here and destroyed at teardown
  const open = new Set<Socket>();
  const track = (s: Socket) => {
    open.add(s);
    s.on("close", () => open.delete(s));
  };
  const tunnel = (clientSocket: Socket, head: Buffer, host: string, port: number) => {
    track(clientSocket);
    const upstream = connect(port, host, () => {
      clientSocket.write("HTTP/1.1 200 Connection Established\r\n\r\n");
      if (head.length) upstream.write(head);
      upstream.pipe(clientSocket);
      clientSocket.pipe(upstream);
    });
    const close = () => {
      upstream.destroy();
      clientSocket.destroy();
    };
    track(upstream);
    upstream.on("error", close);
    clientSocket.on("error", close);
  };

  const proxy = createHttpServer((req, res) => {
    // absolute-form request: plain http, used only for the oauth fixture host
    let target: URL;
    try {
      target = new URL(req.url ?? "");
    } catch {
      res.writeHead(400);
      res.end("absolute url required");
      return;
    }
    const key = `${target.hostname}:${target.port || "80"}`;
    const to = target.protocol === "http:" ? routeMap[key] : undefined;
    if (!to) {
      refused.push(`http ${key}`);
      res.writeHead(403);
      res.end("not a fixture host");
      return;
    }
    const [h = "", p = ""] = to.split(":");
    const headers = { ...req.headers };
    delete headers["proxy-connection"];
    const up = httpRequest(
      {
        host: h,
        port: Number(p),
        method: req.method,
        path: target.pathname + target.search,
        headers,
      },
      (r) => {
        res.writeHead(r.statusCode ?? 502, r.headers);
        r.pipe(res);
      },
    );
    up.on("error", (e) => {
      res.writeHead(502);
      res.end(`upstream ${key}: ${e.message}`);
    });
    req.pipe(up);
  });
  proxy.on("connect", (req, clientSocket: Socket, head: Buffer) => {
    const target = req.url ?? "";
    const [host = "", port = ""] = target.split(":");
    if (port === "443" && (isTenantHost(host) || host === REFERRER_HOST || host === EVIL_HOST)) {
      tunnel(clientSocket, head, "127.0.0.1", sitesPort);
      return;
    }
    const to = routeMap[target];
    if (!to) {
      // browsers phone home (updates, safe browsing); refusing keeps the run
      // offline and deterministic
      refused.push(`connect ${target}`);
      clientSocket.end("HTTP/1.1 403 Forbidden\r\n\r\n");
      return;
    }
    const [h = "", p = ""] = to.split(":");
    tunnel(clientSocket, head, h, Number(p));
  });

  const ctl = createHttpServer(async (req, res) => {
    const url = new URL(req.url ?? "/", "http://ctl.invalid");
    const json = (status: number, body: unknown) => {
      res.writeHead(status, { "content-type": "application/json", connection: "close" });
      res.end(JSON.stringify(body));
    };
    if (req.method === "GET" && url.pathname === "/health") return json(200, { ok: true });
    if (req.method === "POST" && url.pathname === "/snippet") {
      const host = url.searchParams.get("host") ?? "";
      if (!isTenantHost(host)) return json(400, { error: "host must be a tenant host" });
      snippets.set(host, await readBody(req));
      return json(200, { ok: true });
    }
    if (req.method === "POST" && url.pathname === "/evil") {
      evil = await readBody(req);
      return json(200, { ok: true });
    }
    if (req.method === "GET" && url.pathname === "/refused") return json(200, refused);
    json(404, { error: "not found" });
  });

  const listen = (s: Server, port: number) =>
    new Promise<void>((ok, fail) => {
      s.once("error", fail);
      s.listen(port, "127.0.0.1", () => ok());
    });
  await listen(proxy, PROXY_PORT);
  await listen(ctl, SITE_CTL_PORT);

  return {
    close: async () => {
      for (const sock of open) sock.destroy();
      for (const s of [sites, proxy, ctl] as Server[]) {
        s.closeAllConnections();
        await new Promise<void>((ok) => s.close(() => ok()));
      }
    },
  };
}
