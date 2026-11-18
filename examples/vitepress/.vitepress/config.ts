import { defineConfig } from "vitepress";
import { trackerTag } from "./tracker.ts";

const tracker = trackerTag();

export default defineConfig({
  title: "VitePress example",
  // pages live at /pricing instead of /pricing.html
  cleanUrls: true,
  // docs:start head
  // rendered into the <head> of every built page; an empty string is an
  // attribute without a value, as defer is in the console's tag
  head: [
    [
      "script",
      {
        defer: "",
        src: tracker.src,
        integrity: tracker.integrity,
        crossorigin: "anonymous",
        "data-website-id": tracker.siteId,
      },
    ],
  ],
  // docs:end head
});
