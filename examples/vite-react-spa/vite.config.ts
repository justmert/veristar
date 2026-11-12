import react from "@vitejs/plugin-react";
import { snippet } from "tracker/snippet";
import { defineConfig, type Plugin } from "vite";

function required(name: string): string {
  const value = process.env[name];
  if (!value) throw new Error(`missing env ${name}`);
  return value;
}

// puts the generated tracker snippet where index.html has <!-- tracker -->,
// so the built page carries the exact tag a tenant would paste. the values are
// read only when html is transformed, so `vite preview` needs none of them.
function trackerSnippet(): Plugin {
  return {
    name: "tracker-snippet",
    transformIndexHtml(html) {
      const tag = snippet({
        siteId: required("SA_SITE_ID"),
        version: required("SA_TRACKER_VERSION"),
        origin: required("SA_GATEWAY_ORIGIN"),
      });
      const parts = html.split("<!-- tracker -->");
      if (parts.length !== 2) throw new Error("index.html must contain <!-- tracker --> once");
      return parts.join(tag);
    },
  };
}

// the preview server sits behind the e2e tls front, which forwards the public
// host name; vite refuses unknown hosts, so the name is configured explicitly
const publicHost = process.env.SA_PUBLIC_HOST;
// unset means vite's own default preview port, which is what a developer
// running this example by hand expects
const port = process.env.PORT ? Number(process.env.PORT) : undefined;

export default defineConfig({
  plugins: [react(), trackerSnippet()],
  preview: {
    host: "127.0.0.1",
    port,
    strictPort: true,
    allowedHosts: publicHost ? [publicHost] : [],
  },
});
