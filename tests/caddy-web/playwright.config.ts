import { defineConfig, devices } from "@playwright/test";

// a tenant's umami dashboard through the web caddy, in a real browser. the go
// test TestWebCaddyLive (make test-caddy-web) starts umami and caddy, provisions
// the tenant and runs this suite with the variables below.
for (const k of ["WEB_PORT", "WEB_DOMAIN", "WEB_TOKEN", "WEB_WEBSITE", "WEB_TEAM"]) {
  if (!process.env[k]) {
    throw new Error(`${k} must be set; run make test-caddy-web`);
  }
}
const port = process.env.WEB_PORT;
const domain = process.env.WEB_DOMAIN;

export default defineConfig({
  testDir: "./specs",
  workers: 1,
  fullyParallel: false,
  forbidOnly: !!process.env.CI,
  retries: 0,
  timeout: 180_000,
  reporter: [["list"]],
  use: {
    baseURL: `https://web.${domain}:${port}`,
    // caddy's local ca signs the certificate
    ignoreHTTPSErrors: true,
    trace: "retain-on-failure",
    launchOptions: {
      // both console and dashboard hosts resolve to the caddy container's
      // published port on loopback; caddy routes by host
      // no quic: caddy advertises h3 on udp 443, which the test container
      // does not publish, so chromium would switch to a port nothing serves
      args: [
        `--host-resolver-rules=MAP web.${domain} 127.0.0.1, MAP app.${domain} 127.0.0.1`,
        "--disable-quic",
      ],
    },
  },
  projects: [{ name: "chromium", use: { ...devices["Desktop Chrome"] } }],
});
