import path from "node:path";
import tailwindcss from "@tailwindcss/vite";
import react from "@vitejs/plugin-react";
import { defineConfig } from "vite";

// the console and the control plane share one origin in every environment
// (app.<BASE_DOMAIN>), because the session cookie is host-only and state
// changes are checked against that origin. the dev server keeps that shape by
// proxying /api to a running control plane instead of calling it cross-origin.
export default defineConfig(({ command }) => {
  const upstream = process.env.CONTROLPLANE_URL;
  if (command === "serve" && !upstream) {
    throw new Error("missing env CONTROLPLANE_URL (control plane base url for the dev proxy)");
  }
  return {
    plugins: [react(), tailwindcss()],
    resolve: { alias: { "@": path.resolve(import.meta.dirname, "src") } },
    server: upstream
      ? { proxy: { "/api": { target: upstream, changeOrigin: false, xfwd: false } } }
      : undefined,
    build: {
      // no inline assets: the csp in deploy/caddy/app.Caddyfile allows only
      // same-origin files, so a data: url would be refused
      assetsInlineLimit: 0,
      sourcemap: false,
    },
  };
});
