// the playwright test object every spec uses. chromium aborts every request
// in flight with ERR_NETWORK_CHANGED whenever the host's interfaces change,
// which other docker work on a shared machine does all the time (the web
// caddy suite saw the same). that is not an answer from any service under
// test. the config retries a failed test, and this fixture records every
// failed attempt in which no such abort happened in real-failures.jsonl; the
// go test fails on any line there, so a retry can only hide a failure that
// coincided with a network change, never a real one.
import { appendFileSync } from "node:fs";
import { join } from "node:path";
import { type Browser, type BrowserContext, test as base } from "@playwright/test";
import { OUT } from "../fixture/env.ts";

export { expect } from "@playwright/test";

const aborts = { count: 0 };

function watch(ctx: BrowserContext) {
  ctx.on("requestfailed", (r) => {
    if (r.failure()?.errorText.includes("ERR_NETWORK_CHANGED")) aborts.count++;
  });
}

export const test = base.extend<{ networkWatch: undefined }, { contextWatch: undefined }>({
  // worker scoped, so contexts made in beforeAll hooks are watched as well
  contextWatch: [
    async ({ browser }: { browser: Browser }, use) => {
      const orig = browser.newContext.bind(browser);
      browser.newContext = async (...args) => {
        const c = await orig(...args);
        watch(c);
        return c;
      };
      await use(undefined);
    },
    { scope: "worker", auto: true },
  ],
  networkWatch: [
    async ({ context }, use, info) => {
      watch(context);
      const before = aborts.count;
      await use(undefined);
      // a navigation lost to the abort shows up in the error, not always as
      // a failed request
      const changed =
        aborts.count - before + (info.error?.message?.includes("ERR_NETWORK_CHANGED") ? 1 : 0);
      if (changed > 0)
        info.annotations.push({ type: "network-changed", description: `${changed} aborts` });
      if (info.status !== info.expectedStatus && changed === 0) {
        appendFileSync(
          join(OUT, "real-failures.jsonl"),
          `${JSON.stringify({ test: info.titlePath.join(" > "), project: info.project.name, retry: info.retry, error: info.error?.message?.slice(0, 500) })}\n`,
        );
      }
    },
    { auto: true },
  ],
});
