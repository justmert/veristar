import { defineConfig, devices } from "@playwright/test";
import { GATEWAY_ORIGIN, PORTS, SITES, TRACKER_VERSION } from "./sites.ts";

// difficulty the fixture hands out. it is a test setting, kept low enough that
// the matrix spends its time on navigation rather than hashing; the production
// starting value is chosen from the measurements in docs/reference/browser-proof.md.
const PROOF_BITS = 12;

const proxy = { server: `http://127.0.0.1:${PORTS.proxy}` };

function siteServer(site: (typeof SITES)[number]) {
  const env = {
    SA_SITE_ID: site.siteId,
    SA_TRACKER_VERSION: TRACKER_VERSION,
    SA_GATEWAY_ORIGIN: GATEWAY_ORIGIN,
    SA_PUBLIC_HOST: site.host,
    PORT: String(site.port),
    HOST: "127.0.0.1",
  };
  return {
    command: `pnpm --filter ${site.pkg} build && pnpm --filter ${site.pkg} start`,
    url: `http://127.0.0.1:${site.port}/`,
    env,
    timeout: 180_000,
    reuseExistingServer: false,
    stdout: "pipe" as const,
    stderr: "pipe" as const,
  };
}

export default defineConfig({
  testDir: "./specs",
  // the fixture records requests globally, so tests run one at a time and each
  // starts from an empty recorder
  workers: 1,
  fullyParallel: false,
  forbidOnly: !!process.env.CI,
  retries: 0,
  timeout: 60_000,
  reporter: [["list"]],
  use: {
    proxy,
    // the fixture certificate is self-signed and generated per run
    ignoreHTTPSErrors: true,
    trace: "retain-on-failure",
  },
  projects: [
    { name: "chromium", use: { ...devices["Desktop Chrome"] }, testIgnore: /gpc\.spec/ },
    { name: "firefox", use: { ...devices["Desktop Firefox"] }, testIgnore: /gpc\.spec/ },
    { name: "webkit", use: { ...devices["Desktop Safari"] }, testIgnore: /gpc\.spec/ },
    {
      // global privacy control is a firefox preference; chromium and webkit have
      // no built-in switch for it
      name: "firefox-gpc",
      testMatch: /gpc\.spec/,
      use: {
        ...devices["Desktop Firefox"],
        launchOptions: { firefoxUserPrefs: { "privacy.globalprivacycontrol.enabled": true } },
      },
    },
  ],
  webServer: [
    {
      command: "node fixture/main.ts",
      url: `http://127.0.0.1:${PORTS.control}/health`,
      // in the real-gateway mode (tests/gateway sets SA_E2E_GATEWAY_UPSTREAM and
      // SA_E2E_EVENT_LOG) the gateway's own settings apply and these are unused
      env: {
        SA_E2E_PROOF_BITS: String(PROOF_BITS),
        SA_E2E_REPLAY_LIMIT: "100",
        SA_E2E_GATEWAY_UPSTREAM: process.env.SA_E2E_GATEWAY_UPSTREAM ?? "",
        SA_E2E_EVENT_LOG: process.env.SA_E2E_EVENT_LOG ?? "",
      },
      timeout: 30_000,
      reuseExistingServer: false,
      stdout: "pipe",
    },
    ...SITES.map(siteServer),
  ],
});
