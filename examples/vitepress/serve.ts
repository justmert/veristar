// serves the built site (.vitepress/dist) for the browser tests the way a
// static host does with clean urls: /pricing is pricing.html, / is
// index.html, anything else is 404.html. vitepress preview listens on every
// interface and has no option to change that; on a busy machine the wildcard
// port can clash with an outgoing connection's local port, so the tests use
// this server on the loopback address instead.
import { readFile, stat } from "node:fs/promises";
import { createServer } from "node:http";
import { extname, join, normalize } from "node:path";

const root = join(import.meta.dirname, ".vitepress", "dist");
const port = Number(process.env.PORT ?? 4173);
const types: Record<string, string> = {
  ".html": "text/html; charset=utf-8",
  ".js": "text/javascript; charset=utf-8",
  ".css": "text/css; charset=utf-8",
  ".json": "application/json",
  ".svg": "image/svg+xml",
  ".png": "image/png",
  ".woff2": "font/woff2",
  ".txt": "text/plain; charset=utf-8",
  ".xml": "application/xml",
};

async function file(path: string): Promise<string | null> {
  try {
    return (await stat(path)).isFile() ? path : null;
  } catch {
    return null;
  }
}

async function resolve(urlPath: string): Promise<string | null> {
  const rel = normalize(decodeURIComponent(urlPath)).replace(/^(\.\.[/\\])+/, "");
  const base = join(root, rel);
  if (!base.startsWith(root)) return null;
  if (urlPath.endsWith("/")) return file(join(base, "index.html"));
  return (await file(base)) ?? (await file(`${base}.html`));
}

createServer(async (req, res) => {
  const path = new URL(req.url ?? "/", "http://localhost").pathname;
  const found = await resolve(path);
  const target = found ?? join(root, "404.html");
  res.writeHead(found ? 200 : 404, {
    "content-type": types[extname(target)] ?? "application/octet-stream",
  });
  res.end(await readFile(target));
}).listen(port, "127.0.0.1");
