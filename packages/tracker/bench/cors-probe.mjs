// records what each engine sends for the tracker's two cross-origin requests:
// whether a preflight (OPTIONS) happens and the sec-fetch-* and origin headers.
// the page is served from 127.0.0.1 and the gateway from localhost, two sites.

import { createServer } from "node:http";
import { chromium, firefox, webkit } from "@playwright/test";

const seen = [];
const gateway = createServer((req, res) => {
  const h = req.headers;
  seen.push({
    method: req.method,
    path: req.url.split("?")[0],
    origin: h.origin ?? null,
    contentType: h["content-type"] ?? null,
    site: h["sec-fetch-site"] ?? null,
    mode: h["sec-fetch-mode"] ?? null,
    dest: h["sec-fetch-dest"] ?? null,
    cookie: h.cookie ?? null,
  });
  req.resume();
  res.writeHead(200, {
    "access-control-allow-origin": h.origin ?? "*",
    "content-type": "application/json",
  });
  res.end("{}");
});
const page = createServer((_req, res) => {
  res.writeHead(200, { "content-type": "text/html" });
  res.end("<!doctype html><title>probe</title>");
});
await new Promise((r) => gateway.listen(0, "localhost", r));
await new Promise((r) => page.listen(0, "127.0.0.1", r));
const gw = `http://localhost:${gateway.address().port}`;

const out = {};
for (const [name, engine] of Object.entries({ chromium, firefox, webkit })) {
  seen.length = 0;
  const browser = await engine.launch();
  const p = await browser.newPage();
  await p.goto(`http://127.0.0.1:${page.address().port}/`);
  await p.evaluate(async (gw) => {
    await fetch(`${gw}/v1/c?w=x`, { method: "GET", credentials: "omit", cache: "no-store" });
    await fetch(`${gw}/v1/e`, {
      method: "POST",
      keepalive: true,
      credentials: "omit",
      headers: { "Content-Type": "text/plain;charset=UTF-8" },
      body: "{}",
    });
  }, gw);
  out[name] = { version: browser.version(), requests: [...seen] };
  await browser.close();
}
gateway.close();
page.close();
console.log(JSON.stringify(out, null, 2));
