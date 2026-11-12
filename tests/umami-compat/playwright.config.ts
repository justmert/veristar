import { defineConfig, devices } from "@playwright/test";

// the ui half of the umami upgrade gate. it drives the real umami of the dev
// stack (make umami-compat), so there is no web server to start here.
const base = process.env.UMAMI_IT_URL;
if (!base) {
  throw new Error("UMAMI_IT_URL must be set; run make umami-compat");
}

export default defineConfig({
  testDir: "./specs",
  workers: 1,
  fullyParallel: false,
  forbidOnly: !!process.env.CI,
  retries: 0,
  timeout: 120_000,
  reporter: [["list"]],
  use: { baseURL: base, trace: "retain-on-failure" },
  projects: [{ name: "chromium", use: { ...devices["Desktop Chrome"] } }],
});
