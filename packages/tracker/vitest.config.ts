import { defineConfig } from "vitest/config";

export default defineConfig({
  define: {
    // the release build injects the real version; tests run the source directly
    __SDK_VERSION__: JSON.stringify("0.0.0-test"),
  },
  test: {
    include: ["test/**/*.test.ts"],
    environment: "node",
    testTimeout: 30000,
  },
});
