// a small real http server standing in for the gateway in unit tests. it issues
// v1 challenges with a fixture key and records every request it receives, so
// tests assert on the bytes the tracker actually put on the network.

import { createHmac, randomBytes } from "node:crypto";
import { createServer, type IncomingHttpHeaders, type Server } from "node:http";
import type { AddressInfo } from "node:net";
import { type Challenge, tagInput } from "../src/wire.ts";

export const FIXTURE_PROOF_KEY = Buffer.from(
  "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f",
  "hex",
);

export type Recorded = {
  method: string;
  path: string;
  headers: IncomingHttpHeaders;
  body: string;
  // the status the fixture answered with
  status?: number;
};

export type Gateway = {
  origin: string;
  requests: Recorded[];
  events: () => Recorded[];
  challenges: () => Recorded[];
  bits: number;
  // events one proof may carry, as PROOF_REPLAY_LIMIT on one gateway node.
  // past it the fixture answers 403 rejected_replay like the gateway
  replayLimit: number;
  // while true, challenge requests get no answer until releaseChallenges(),
  // like a gateway that is slow or unreachable
  holdChallenges: boolean;
  releaseChallenges: () => void;
  // extra routes, for example the sdk.js file or an html page
  files: Map<string, { type: string; body: string | Buffer }>;
  close: () => Promise<void>;
};

export function tagFor(w: string, iat: number, salt: string, bits: number): string {
  return createHmac("sha256", FIXTURE_PROOF_KEY)
    .update(tagInput(w, iat, salt, bits))
    .digest("hex");
}

export async function startGateway(bits = 8): Promise<Gateway> {
  const requests: Recorded[] = [];
  const files = new Map<string, { type: string; body: string | Buffer }>();
  const gw: Partial<Gateway> = {
    requests,
    files,
    bits,
    replayLimit: Number.POSITIVE_INFINITY,
    holdChallenges: false,
  };
  const held: (() => void)[] = [];
  gw.releaseChallenges = () => {
    for (const answer of held.splice(0)) answer();
  };
  const uses = new Map<string, number>();
  const server: Server = createServer((req, res) => {
    const chunks: Buffer[] = [];
    req.on("data", (c: Buffer) => chunks.push(c));
    req.on("end", () => {
      const url = new URL(req.url ?? "/", "http://fixture");
      const recorded: Recorded = {
        method: req.method ?? "",
        path: `${url.pathname}${url.search}`,
        headers: req.headers,
        body: Buffer.concat(chunks).toString("utf8"),
      };
      requests.push(recorded);
      const cors = { "access-control-allow-origin": "*" };
      if (req.method === "GET" && url.pathname === "/v1/c") {
        const answer = () => {
          const w = url.searchParams.get("w") ?? "";
          const iat = Math.floor(Date.now() / 1000);
          const salt = randomBytes(16).toString("hex");
          const b = gw.bits as number;
          const challenge: Challenge = { w, iat, salt, bits: b, tag: tagFor(w, iat, salt, b) };
          res.writeHead(200, { ...cors, "content-type": "application/json" });
          res.end(JSON.stringify(challenge));
        };
        if (gw.holdChallenges) held.push(answer);
        else answer();
        return;
      }
      if (req.method === "POST" && url.pathname === "/v1/e") {
        // the tag identifies the challenge, as in the gateway's replay table
        let tag = "";
        try {
          tag = (JSON.parse(recorded.body) as { p?: { tag?: string } }).p?.tag ?? "";
        } catch {
          // counted under the empty tag
        }
        const n = (uses.get(tag) ?? 0) + 1;
        if (n > (gw.replayLimit as number)) {
          recorded.status = 403;
          res.writeHead(403, { ...cors, "content-type": "text/plain; charset=utf-8" });
          res.end("rejected_replay\n");
          return;
        }
        uses.set(tag, n);
        recorded.status = 202;
        res.writeHead(202, cors);
        res.end();
        return;
      }
      const file = files.get(url.pathname);
      if (req.method === "GET" && file) {
        res.writeHead(200, { "content-type": file.type });
        res.end(file.body);
        return;
      }
      res.writeHead(404);
      res.end();
    });
  });
  await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
  const { port } = server.address() as AddressInfo;
  gw.origin = `http://127.0.0.1:${port}`;
  gw.events = () => requests.filter((r) => r.path === "/v1/e");
  gw.challenges = () => requests.filter((r) => r.path.startsWith("/v1/c"));
  gw.close = () =>
    new Promise<void>((resolve, reject) => {
      server.close((err) => (err ? reject(err) : resolve()));
      // fetch keeps idle connections open, which would hold close() for seconds
      server.closeAllConnections();
    });
  return gw as Gateway;
}

// waits until the predicate holds or fails the test after the timeout
export async function waitFor(check: () => boolean, timeoutMs = 5000): Promise<void> {
  const start = Date.now();
  while (!check()) {
    if (Date.now() - start > timeoutMs) throw new Error("condition not met before timeout");
    await new Promise((r) => setTimeout(r, 20));
  }
}

export const pause = (ms: number) => new Promise((r) => setTimeout(r, ms));
