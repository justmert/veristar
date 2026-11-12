// @ts-check
import node from "@astrojs/node";
import { defineConfig } from "astro/config";

// every page is rendered on the server per request, by the standalone node server
export default defineConfig({
  output: "server",
  adapter: node({ mode: "standalone" }),
});
