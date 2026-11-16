import { defineConfig, devices } from "@playwright/test";

// the console's content security policy against the stellar wallets kit
// (csp/). it needs no stack: every response comes from page.route, with the
// csp header of deploy/caddy/app.Caddyfile. run it with
// `make console-csp`, which builds the console first.

export default defineConfig({
  testDir: "./csp",
  workers: 1,
  fullyParallel: false,
  forbidOnly: !!process.env.CI,
  timeout: 120_000,
  expect: { timeout: 15_000 },
  reporter: [["list"]],
  projects: [
    { name: "chromium", use: { ...devices["Desktop Chrome"] } },
    { name: "firefox", use: { ...devices["Desktop Firefox"] } },
    { name: "webkit", use: { ...devices["Desktop Safari"] } },
  ],
});
