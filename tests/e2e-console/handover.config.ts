import { defineConfig, devices } from "@playwright/test";
import { PROXY_PORT } from "./fixture/env.ts";

// the design handover rehearsal. tests/handover (make handover-it) starts
// the two environments and runs one phase at a time with --grep: populate
// environment a, then after the migration the history and cutover checks
// against environment b behind the same names.

export default defineConfig({
  testDir: "./handover",
  globalSetup: "./fixture/global-setup.ts",
  workers: 1,
  fullyParallel: false,
  forbidOnly: !!process.env.CI,
  // chromium aborts requests on host network changes, which other docker
  // work on a shared machine causes; specs/test.ts records every failed
  // attempt without such an abort and the go test fails on it
  retries: 2,
  timeout: 240_000,
  expect: { timeout: 30_000 },
  reporter: [["list"]],
  use: {
    proxy: { server: `http://127.0.0.1:${PROXY_PORT}` },
    ignoreHTTPSErrors: true,
    trace: "retain-on-failure",
  },
  projects: [
    { name: "chromium", use: { ...devices["Desktop Chrome"] } },
    { name: "firefox", use: { ...devices["Desktop Firefox"] }, grep: /@cutover/ },
    { name: "webkit", use: { ...devices["Desktop Safari"] }, grep: /@cutover/ },
  ],
});
