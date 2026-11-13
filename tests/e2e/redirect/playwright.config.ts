// redirect research for the domain handover (docs/handover/redirect-research.md).
// run with `pnpm --filter @veristar/e2e redirect`. kept apart from the
// main matrix (testDir ./specs) because it answers a research question rather
// than gating a release, and it uses its own ports.
import { defineConfig, devices } from "@playwright/test";
import { REDIRECT_PORTS } from "./hosts.ts";

export default defineConfig({
  testDir: ".",
  testMatch: /\.research\.ts$/,
  // the fixture records requests globally, so one test at a time
  workers: 1,
  fullyParallel: false,
  retries: 0,
  timeout: 60_000,
  reporter: [["list"]],
  use: {
    proxy: { server: `http://127.0.0.1:${REDIRECT_PORTS.proxy}` },
    // the fixture certificate is self-signed and generated per run
    ignoreHTTPSErrors: true,
  },
  projects: [
    { name: "chromium", use: { ...devices["Desktop Chrome"] } },
    { name: "firefox", use: { ...devices["Desktop Firefox"] } },
    { name: "webkit", use: { ...devices["Desktop Safari"] } },
  ],
  webServer: [
    {
      command: "node fixture.ts",
      url: `http://127.0.0.1:${REDIRECT_PORTS.control}/health`,
      timeout: 30_000,
      reuseExistingServer: false,
      stdout: "pipe",
    },
  ],
});
