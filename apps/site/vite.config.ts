import { resolve } from "node:path";
import tailwindcss from "@tailwindcss/vite";
import react from "@vitejs/plugin-react";
import { defineConfig } from "vite";

// a static site: two html pages, the home page and the 404 page a host
// serves for any unknown path. nothing is inlined, so a csp of
// script-src 'self' and style-src 'self' holds for every file in dist/.
export default defineConfig({
  plugins: [react(), tailwindcss()],
  build: {
    assetsInlineLimit: 0,
    sourcemap: false,
    // the modulepreload polyfill is a script vite would add to each page
    modulePreload: { polyfill: false },
    rollupOptions: {
      input: {
        index: resolve(import.meta.dirname, "index.html"),
        "404": resolve(import.meta.dirname, "404.html"),
      },
    },
  },
});
