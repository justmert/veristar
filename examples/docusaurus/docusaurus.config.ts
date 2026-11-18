import type { Config } from "@docusaurus/types";
import { trackerTag } from "./tracker.ts";

const tracker = trackerTag();

const config: Config = {
  title: "Docusaurus example",
  url: `https://${process.env.SA_PUBLIC_HOST ?? "docs.example.org"}`,
  baseUrl: "/",
  onBrokenLinks: "throw",
  // docs:start scripts
  // every key of the object becomes an attribute of the <script> element in
  // the <head> of every page, so the tag reaches the browser as the console
  // wrote it, with its integrity value
  scripts: [
    {
      src: tracker.src,
      defer: true,
      integrity: tracker.integrity,
      crossorigin: "anonymous",
      "data-website-id": tracker.siteId,
    },
  ],
  // docs:end scripts
  presets: [
    [
      "classic",
      {
        docs: { routeBasePath: "docs" },
        blog: false,
      },
    ],
  ],
};

export default config;
