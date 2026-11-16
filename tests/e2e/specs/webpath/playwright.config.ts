import { defineConfig, devices } from "@playwright/test";
import { PORTS } from "../../sites.ts";

// browser half of the web path suite (tests/webpath, make webpath).
// the go test starts the fixture in its real-gateway mode and the example
// servers once for the whole run, then calls playwright many times with this
// config, so there is no webServer here. the scenario files end in
// .webpath.ts, which the browser matrix config (testMatch *.spec.ts) never picks up.

const proxy = { server: `http://127.0.0.1:${PORTS.proxy}` };
const scenarios = /webpath\/scenarios\.webpath\.ts$/;
const signals = /webpath\/signals\.webpath\.ts$/;

export default defineConfig({
  // the parent directory, so the chromium project can also re-run the matrix's
  // custom event spec through the real gateway
  testDir: "..",
  workers: 1,
  fullyParallel: false,
  forbidOnly: !!process.env.CI,
  retries: 0,
  timeout: 300_000,
  reporter: [["list"]],
  use: { ignoreHTTPSErrors: true, trace: "off" },
  projects: [
    {
      name: "chromium",
      use: { ...devices["Desktop Chrome"], proxy },
      testMatch: [scenarios, /specs\/events\.spec\.ts$/],
    },
    { name: "firefox", use: { ...devices["Desktop Firefox"], proxy }, testMatch: scenarios },
    { name: "webkit", use: { ...devices["Desktop Safari"], proxy }, testMatch: scenarios },
    // firefox has no mobile emulation in playwright, so mobile runs in the
    // other two engines
    { name: "chromium-mobile", use: { ...devices["Pixel 7"], proxy }, testMatch: scenarios },
    { name: "webkit-mobile", use: { ...devices["iPhone 15"], proxy }, testMatch: scenarios },
    // tracker 1.3.0 counting rules and page signals (tests/webpath
    // signals_test.go). the back/forward cache is on, which playwright turns
    // off by default: chromium in its full build (the headless shell has no
    // cache) without the switch that disables it, firefox with the cache
    // kept in the parent process
    {
      name: "signals-chromium",
      use: {
        ...devices["Desktop Chrome"],
        proxy,
        channel: "chromium",
        launchOptions: { ignoreDefaultArgs: ["--disable-back-forward-cache"] },
      },
      testMatch: signals,
    },
    {
      name: "signals-firefox",
      use: {
        ...devices["Desktop Firefox"],
        proxy,
        launchOptions: {
          firefoxUserPrefs: {
            "browser.sessionhistory.max_total_viewers": 8,
            "fission.bfcacheInParent": true,
          },
        },
      },
      testMatch: signals,
    },
    { name: "signals-webkit", use: { ...devices["Desktop Safari"], proxy }, testMatch: signals },
    // the wallets kit example (tests/webpath wallets_test.go)
    {
      name: "wallets",
      use: { ...devices["Desktop Chrome"], proxy },
      testMatch: /webpath\/wallets\.webpath\.ts$/,
      grepInvert: /@wallets-ui/,
    },
    {
      name: "wallets-ui",
      use: { ...devices["Desktop Chrome"] },
      testMatch: /webpath\/wallets\.webpath\.ts$/,
      grep: /@wallets-ui/,
    },
    {
      // the umami dashboard is opened directly on its loopback port, without
      // the fixture proxy, so every request it makes is visible to the page
      name: "umami-ui",
      use: { ...devices["Desktop Chrome"] },
      testMatch: /webpath\/ui\.webpath\.ts$/,
    },
  ],
});
