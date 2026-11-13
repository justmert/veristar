import adapter from "@sveltejs/adapter-node";
import { sveltekit } from "@sveltejs/kit/vite";
import { defineConfig } from "vite";

// sveltekit 3 takes its options here instead of svelte.config.js. the node
// adapter's standalone server renders every page per request; later
// navigations happen on the client.
export default defineConfig({
  plugins: [sveltekit({ adapter: adapter() })],
});
