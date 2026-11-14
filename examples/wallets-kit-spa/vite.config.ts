import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import react from "@vitejs/plugin-react";
import { snippet } from "tracker/snippet";
import { defineConfig, type Plugin } from "vite";

// two pages, one per way a tenant turns wallet capture on:
//   index.html   the script tag from the snippet generator with
//                data-wallets-kit="true" (sdk.js watches the kit's storage)
//   module.html  the npm package: init() and attachWalletsKit(StellarWalletsKit)
//
// `vite build --mode wallettest` builds the same pages with the test wallet
// module from tests/fixtures/wallets-kit-module instead of the real wallet
// modules, for the wallets kit browser tests. a normal build never contains it.

function required(name: string): string {
  const value = process.env[name];
  if (!value) throw new Error(`missing env ${name}`);
  return value;
}

// replaces <!-- tracker --> in index.html with the exact tag a tenant pastes
function trackerSnippet(): Plugin {
  return {
    name: "tracker-snippet",
    transformIndexHtml: {
      order: "pre",
      handler(html, ctx) {
        if (!html.includes("<!-- tracker -->")) return html;
        const parts = html.split("<!-- tracker -->");
        if (parts.length !== 2) throw new Error(`${ctx.path} must contain <!-- tracker --> once`);
        const tag = snippet({
          siteId: required("SA_SITE_ID"),
          version: required("SA_TRACKER_VERSION"),
          origin: required("SA_GATEWAY_ORIGIN"),
          walletsKit: true,
        });
        return parts.join(tag);
      },
    },
  };
}

// the tracker's esm source expects its version as a build constant, which the
// release build of sdk.js injects; a bundler user gets the version of the
// tracker package it installed
const trackerPackage = resolve(import.meta.dirname, "node_modules", "tracker", "package.json");
const trackerVersion: string = JSON.parse(readFileSync(trackerPackage, "utf8")).version;

const publicHost = process.env.SA_PUBLIC_HOST;
const port = process.env.PORT ? Number(process.env.PORT) : undefined;

export default defineConfig(({ command }) => ({
  plugins: [react(), trackerSnippet()],
  // the npm page reads its site id and gateway at build time, as a bundler
  // user's config would; vite preview serves files and needs neither
  define:
    command === "build"
      ? {
          __SDK_VERSION__: JSON.stringify(trackerVersion),
          __SA_SITE_ID__: JSON.stringify(required("SA_SITE_ID")),
          __SA_GATEWAY_ORIGIN__: JSON.stringify(required("SA_GATEWAY_ORIGIN")),
        }
      : {},
  build: {
    rollupOptions: {
      input: {
        index: resolve(import.meta.dirname, "index.html"),
        module: resolve(import.meta.dirname, "module.html"),
      },
    },
  },
  preview: {
    host: "127.0.0.1",
    port,
    strictPort: true,
    allowedHosts: publicHost ? [publicHost] : [],
  },
}));
