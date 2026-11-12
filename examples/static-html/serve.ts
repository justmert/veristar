// minimal static file server for dist/, standing in for whatever static host a
// tenant uses. no directory listing, no fallback: unknown paths are 404.
import { existsSync, readFileSync, statSync } from "node:fs";
import { createServer } from "node:http";
import { dirname, extname, join, normalize } from "node:path";
import { fileURLToPath } from "node:url";

const port = Number(process.env.PORT);
if (!Number.isInteger(port) || port <= 0) throw new Error("missing or invalid env PORT");

const root = join(dirname(fileURLToPath(import.meta.url)), "dist");
const types: Record<string, string> = {
  ".html": "text/html; charset=utf-8",
  ".css": "text/css; charset=utf-8",
  ".js": "text/javascript; charset=utf-8",
};

createServer((req, res) => {
  const path = new URL(req.url ?? "/", "http://site.invalid").pathname;
  const rel = normalize(path === "/" ? "/index.html" : path);
  const file = join(root, rel);
  if (!file.startsWith(root) || !existsSync(file) || !statSync(file).isFile()) {
    res.writeHead(404, { "content-type": "text/plain" });
    res.end("not found");
    return;
  }
  res.writeHead(200, { "content-type": types[extname(file)] ?? "application/octet-stream" });
  res.end(readFileSync(file));
}).listen(port, "127.0.0.1", () => console.log(`static-html: http://127.0.0.1:${port}`));
