import { existsSync, readdirSync, readFileSync } from "node:fs";
import { createRequire } from "node:module";
import { extname, join, resolve } from "node:path";
import { expect, type Page, test } from "@playwright/test";
import { build } from "esbuild";

// does the stellar wallets kit work under the console's csp
// (script-src 'self'; style-src 'self')? two pages answer it, both served
// with the exact header of deploy/caddy/app.Caddyfile:
//   - the kit's own auth modal (StellarWalletsKit.authModal), bundled from
//     the pinned kit as a same-origin script, so only the kit's behaviour can
//     trip the policy;
//   - the console's production build, whose sign-in page drives the kit
//     through its module api and its own picker (lib/stellar-wallet).
// every csp violation the page reports is collected.

const root = resolve(import.meta.dirname, "../../..");
const origin = "https://app.csp.test";
const dist = join(root, "apps/console/dist");

function consoleCSP(): string {
  const caddy = readFileSync(join(root, "deploy/caddy/app.Caddyfile"), "utf8");
  const m = caddy.match(/Content-Security-Policy "([^"]+)"/);
  if (!m?.[1]) throw new Error("no Content-Security-Policy in app.Caddyfile");
  return m[1];
}

const JS = "text/javascript; charset=utf-8";
const HTML = "text/html; charset=utf-8";
const TYPES: Record<string, string> = {
  ".html": "text/html; charset=utf-8",
  ".js": "text/javascript; charset=utf-8",
  ".css": "text/css; charset=utf-8",
  ".svg": "image/svg+xml",
  ".woff2": "font/woff2",
};

type Violation = { directive: string; blocked: string; sample: string };

async function watchViolations(page: Page): Promise<Violation[]> {
  const out: Violation[] = [];
  await page.exposeFunction("__cspViolation", (v: Violation) => out.push(v));
  await page.addInitScript(() => {
    document.addEventListener("securitypolicyviolation", (e) => {
      (window as unknown as { __cspViolation: (v: unknown) => void }).__cspViolation({
        directive: e.effectiveDirective,
        blocked: e.blockedURI,
        sample: e.sample,
      });
    });
  });
  return out;
}

// the kit's modal, bundled into one same-origin file
async function kitBundle(): Promise<string> {
  // resolved from the console, which depends on the pinned kit
  const req = createRequire(join(root, "apps/console/package.json"));
  const kit = req.resolve("@creit.tech/stellar-wallets-kit");
  const lobstr = req.resolve("@creit.tech/stellar-wallets-kit/modules/lobstr");
  const out = await build({
    stdin: {
      contents: `
        import { StellarWalletsKit } from ${JSON.stringify(kit)};
        import { LobstrModule } from ${JSON.stringify(lobstr)};
        StellarWalletsKit.init({ modules: [new LobstrModule()] });
        window.openKit = () => {
          StellarWalletsKit.authModal().catch((e) => { window.kitError = String(e && e.message || e); });
        };
        window.kitLoaded = true;
      `,
      resolveDir: join(root, "apps/console"),
      loader: "js",
    },
    bundle: true,
    format: "iife",
    platform: "browser",
    write: false,
    logLevel: "silent",
    define: { global: "globalThis", "process.env.NODE_ENV": '"production"' },
  });
  const file = out.outputFiles[0];
  if (!file) throw new Error("no kit bundle");
  return file.text;
}

test("the wallets kit's own modal is refused by the console csp", async ({ page }) => {
  const csp = consoleCSP();
  const js = await kitBundle();
  const violations = await watchViolations(page);
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await page.route(`${origin}/**`, (route) => {
    const path = new URL(route.request().url()).pathname;
    if (path === "/kit.js") {
      return route.fulfill({
        body: js,
        headers: { "content-type": JS, "content-security-policy": csp },
      });
    }
    return route.fulfill({
      body: '<!doctype html><html><head><script src="/kit.js"></script></head><body></body></html>',
      headers: { "content-type": HTML, "content-security-policy": csp },
    });
  });
  await page.goto(`${origin}/kit.html`);
  await page.evaluate(() => (window as unknown as { openKit: () => void }).openKit());
  await page.waitForTimeout(1500);

  // the kit adds a <style> element for its twind styles; style-src 'self'
  // refuses it, so the modal renders without its styles (or not at all)
  const styleViolations = violations.filter((v) => v.directive.startsWith("style-src"));
  console.log(`kit modal under the console csp: ${JSON.stringify({ violations, errors })}`);
  expect(styleViolations.length).toBeGreaterThan(0);
  const styled = await page.evaluate(() =>
    [...document.querySelectorAll("style")].some((s) => (s.sheet?.cssRules.length ?? 0) > 0),
  );
  expect(styled).toBe(false);
});

// serves the console's production build as the app caddy does: files as
// they are, any other path the spa's index.html, /api from stubs
async function serveConsole(page: Page, api: (path: string) => { status: number; body: unknown }) {
  const csp = consoleCSP();
  await page.route(`${origin}/**`, (route) => {
    const path = new URL(route.request().url()).pathname;
    if (path.startsWith("/api/")) {
      const r = api(path);
      return route.fulfill({
        status: r.status,
        body: JSON.stringify(r.body),
        headers: {
          "content-type": "application/json; charset=utf-8",
          "content-security-policy": csp,
        },
      });
    }
    const file = join(dist, path);
    const served = extname(path) !== "" && existsSync(file) ? file : join(dist, "index.html");
    return route.fulfill({
      body: readFileSync(served),
      headers: {
        "content-type": TYPES[extname(served)] ?? "application/octet-stream",
        "content-security-policy": csp,
      },
    });
  });
}

test("the console's wallet picker works under the csp, with no violation", async ({ page }) => {
  test.skip(
    !existsSync(join(dist, "index.html")),
    "build the console first (pnpm --filter @veristar/console build)",
  );
  const violations = await watchViolations(page);
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await serveConsole(page, () => ({
    status: 401,
    body: { error: { code: "unauthenticated", message: "sign in required", request_id: "r" } },
  }));
  await page.goto(`${origin}/sign-in`);
  await page.getByRole("button", { name: "Continue with a Stellar wallet" }).click();
  const dialog = page.getByRole("dialog", { name: "Continue with a Stellar wallet" });
  await expect(dialog).toBeVisible();
  // no extension in a test browser: every wallet is offered for install,
  // which proves the kit modules loaded and ran their detection
  await expect(dialog.getByText("No Stellar wallet extension was found")).toBeVisible();
  for (const name of ["LOBSTR", "Hana Wallet", "Rabet"]) {
    await expect(dialog.getByRole("link", { name: new RegExp(name, "i") })).toBeVisible();
  }
  // the picker is styled by the console's own stylesheet (the brand font)
  const font = await dialog.getByRole("heading").evaluate((h) => getComputedStyle(h).fontFamily);
  expect(font).toContain("Inter");
  expect(errors).toEqual([]);
  expect(violations).toEqual([]);
});

test("the production build carries no test signer", () => {
  test.skip(
    !existsSync(join(dist, "index.html")),
    "build the console first (pnpm --filter @veristar/console build)",
  );
  const files = readdirSync(join(dist, "assets")).filter((f) => f.endsWith(".js"));
  expect(files.length).toBeGreaterThan(0);
  for (const f of files) {
    const text = readFileSync(join(dist, "assets", f), "utf8");
    expect(text, f).not.toContain("veristar-test-signer");
    expect(text, f).not.toContain("veristar console e2e test signer");
  }
});
