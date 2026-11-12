import { defineConfig, devices } from "@playwright/test";
import { PROXY_PORT } from "./fixture/env.ts";

// this stage end-to-end flows (the design verification): sign-in through the
// test-only github oauth fixture, enrollment, sites, snippet, traffic through
// the real gateway, the umami dashboard through sso, isolation, sso
// hardening and session security. the go test stack_test.go (make
// console-e2e) starts the real services in containers and runs this suite.

const proxy = { server: `http://127.0.0.1:${PROXY_PORT}` };

export default defineConfig({
  testDir: "./specs",
  globalSetup: "./fixture/global-setup.ts",
  // the specs share one stack and one tenant site server, so they run in
  // order; every spec signs in as its own fresh github users
  workers: 1,
  fullyParallel: false,
  forbidOnly: !!process.env.CI,
  // a failed test is retried; specs/test.ts records which failures were
  // not caused by a network change, and the go test fails on those
  retries: 2,
  timeout: 300_000,
  expect: { timeout: 20_000 },
  reporter: [["list"]],
  use: {
    proxy,
    // caddy's local ca and the fixture's self-signed certificate
    ignoreHTTPSErrors: true,
    trace: "retain-on-failure",
  },
  projects: [
    { name: "chromium", use: { ...devices["Desktop Chrome"] } },
    // sign-in and cookie flags in the other two engines as well
    {
      name: "firefox",
      use: { ...devices["Desktop Firefox"] },
      testMatch: /console-session\.spec\.ts$/,
    },
    {
      name: "webkit",
      use: { ...devices["Desktop Safari"] },
      testMatch: /console-session\.spec\.ts$/,
    },
  ],
});
