import { resolve } from "node:path";
import tailwindcss from "@tailwindcss/vite";
import react from "@vitejs/plugin-react";
import { defineConfig, type Plugin } from "vite";

// the public address of the site. canonical, open graph, robots.txt and the
// sitemap need an absolute url; a build for another domain (after a handover)
// sets VITE_SITE_ORIGIN. vite reads VITE_* from the environment, so the
// default set here also fills %VITE_SITE_ORIGIN% in index.html.
process.env.VITE_SITE_ORIGIN = (process.env.VITE_SITE_ORIGIN || "https://veristar.app").replace(
  /\/+$/,
  "",
);
const origin = process.env.VITE_SITE_ORIGIN;

// robots.txt and sitemap.xml, written with the origin above
function crawlerFiles(): Plugin {
  return {
    name: "veristar-crawler-files",
    apply: "build",
    generateBundle() {
      this.emitFile({
        type: "asset",
        fileName: "robots.txt",
        source: `User-agent: *\nAllow: /\n\nSitemap: ${origin}/sitemap.xml\n`,
      });
      this.emitFile({
        type: "asset",
        fileName: "sitemap.xml",
        source: `<?xml version="1.0" encoding="UTF-8"?>\n<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">\n  <url><loc>${origin}/</loc></url>\n</urlset>\n`,
      });
    },
  };
}

// a static site: two html pages, the home page and the 404 page a host
// serves for any unknown path. nothing is inlined, so a csp of
// script-src 'self' and style-src 'self' holds for every file in dist/.
export default defineConfig({
  plugins: [react(), tailwindcss(), crawlerFiles()],
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
