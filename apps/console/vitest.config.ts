import path from "node:path";
import { defineConfig } from "vitest/config";

export default defineConfig({
  resolve: { alias: { "@": path.resolve(import.meta.dirname, "src") } },
  test: {
    environment: "jsdom",
    include: ["test/**/*.test.{ts,tsx}"],
    // a page test that walks several filters renders and fetches once per
    // step: about a second alone, past vitest's 5 s default on a ci runner
    // while the other packages' tests run beside it
    testTimeout: 30000,
  },
});
