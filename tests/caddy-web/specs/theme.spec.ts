import { existsSync, mkdirSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import AxeBuilder from "@axe-core/playwright";
import { expect, type Page, test } from "@playwright/test";

// the veristar theme on umami's dashboard (docs/adr/0004): a view-only tenant
// opens its dashboard through the web caddy, once with the system set to dark
// and once to light. dark mode must render the brand's dark theme and light
// mode its light theme, with inter from
// the same host, no csp refusal, no request to another host and every text
// at wcag aa contrast (axe's color-contrast rule on the rendered pages).
// screenshots of both modes go to .scratch/design/umami for review.

const port = process.env.WEB_PORT ?? "";
const domain = process.env.WEB_DOMAIN ?? "";
const token = process.env.WEB_TOKEN ?? "";
const website = process.env.WEB_WEBSITE ?? "";
const team = process.env.WEB_TEAM ?? "";
const web = `https://web.${domain}:${port}`;
const app = `https://app.${domain}:${port}`;
const here = dirname(fileURLToPath(import.meta.url));
const theme = resolve(here, "../../../deploy/caddy/umami-theme/site");
// design review output, kept in the gitignored resources area
const shots = resolve(here, "../../../.scratch/design/umami");

// the brand tokens of theme.css per mode, as the browser reports them
const PALETTE = {
  dark: {
    background: "rgb(21, 21, 20)",
    foreground: "rgb(238, 237, 232)",
    muted: "rgb(35, 35, 32)",
    status: ["rgb(63, 178, 127)", "rgb(249, 112, 102)"],
  },
  light: {
    background: "rgb(246, 245, 242)",
    foreground: "rgb(26, 25, 23)",
    muted: "rgb(235, 233, 228)",
    status: ["rgb(26, 106, 66)", "rgb(171, 51, 40)"],
  },
} as const;
type Mode = keyof typeof PALETTE;
let mode: Mode = "dark";

// chromium aborts requests in flight with ERR_NETWORK_CHANGED when the host's
// interfaces change, which other docker work on a shared machine causes (the
// same handling as dashboard.spec.ts). a step that failed while such aborts
// happened is run again; any other failure fails the test.
async function step(page: Page, counter: { n: number }, fn: () => Promise<void>) {
  for (let attempt = 1; ; attempt++) {
    const before = counter.n;
    try {
      await fn();
      return;
    } catch (e) {
      if (counter.n === before || attempt >= 5) throw e;
      await page.reload();
    }
  }
}

async function expectTheme(page: Page, where: string) {
  await expect(page.locator('link[href="/_veristar/theme.css"]'), `${where}: one link`).toHaveCount(
    1,
  );
  const s = await page.evaluate(async () => {
    await document.fonts.ready;
    const body = getComputedStyle(document.body);
    return {
      background: body.backgroundColor,
      color: body.color,
      font: body.fontFamily,
      scheme: getComputedStyle(document.documentElement).colorScheme,
      inter: document.fonts.check('16px "Inter Variable"'),
      primary: getComputedStyle(document.documentElement).getPropertyValue("--zen-primary").trim(),
    };
  });
  expect(s.background, `${where}: page ground`).toBe(PALETTE[mode].background);
  expect(s.color, `${where}: ink`).toBe(PALETTE[mode].foreground);
  expect(s.font, `${where}: font`).toMatch(/^"Inter Variable"/);
  expect(s.scheme, `${where}: color-scheme`).toBe(mode);
  expect(s.inter, `${where}: inter loaded`).toBe(true);
  expect(s.primary, `${where}: primary`).toBe("#b5482e");
}

// the sidebar shows the veristar mark and wordmark in place of umami's, and
// the delta chips show their status by text and icon colour on the neutral
// muted ground, never on a tinted fill
async function expectBrand(page: Page, where: string) {
  const b = await page.evaluate(() => {
    const svg = document.querySelector('svg[viewBox="0 0 428 389.11"]');
    const icon = svg?.parentElement;
    const text = icon?.nextElementSibling;
    if (!svg || !icon || !text) return null;
    const after = getComputedStyle(text, "::after");
    return {
      svg: getComputedStyle(svg).visibility,
      mark: getComputedStyle(icon).backgroundImage,
      text: getComputedStyle(text).visibility,
      textSize: getComputedStyle(text).fontSize,
      word: after.content,
      wordVisible: after.visibility,
      wordWeight: after.fontWeight,
      wordSpacing: after.letterSpacing,
      wordColor: after.color,
    };
  });
  expect(b, `${where}: umami's logo element`).not.toBeNull();
  expect(b?.svg, `${where}: umami's mark hidden`).toBe("hidden");
  expect(b?.mark, `${where}: veristar mark`).toMatch(
    mode === "light" ? /\/_veristar\/brand\/mark-light\.svg/ : /\/_veristar\/brand\/mark\.svg/,
  );
  expect([b?.text, b?.textSize], `${where}: "umami" hidden`).toEqual(["hidden", "0px"]);
  expect(b?.word, `${where}: wordmark`).toBe('"Veristar"');
  expect(b?.wordVisible).toBe("visible");
  expect(b?.wordWeight).toBe("600");
  expect(b?.wordSpacing).toBe("-0.42px");
  expect(b?.wordColor).toBe(PALETTE[mode].foreground);
}

async function expectChips(page: Page, where: string) {
  const chips = await page
    .locator('[style*="var(--zen-status-success-bg)"], [style*="var(--zen-status-error-bg)"]')
    .evaluateAll((els) =>
      els.map((e) => [getComputedStyle(e).backgroundColor, getComputedStyle(e).color]),
    );
  expect(chips.length, `${where}: delta chips`).toBeGreaterThan(0);
  for (const [ground, colour] of chips) {
    expect(ground, `${where}: chip ground is the muted surface`).toBe(PALETTE[mode].muted);
    expect([...PALETTE[mode].status], `${where}: status colour`).toContain(colour);
  }
}

async function expectContrast(page: Page, where: string) {
  const r = await new AxeBuilder({ page }).withRules(["color-contrast"]).analyze();
  const found = r.violations.flatMap((v) =>
    v.nodes.map((n) => `${n.target.join(" ")}: ${n.failureSummary?.replace(/\s+/g, " ")}`),
  );
  expect(found, `${where}: color-contrast`).toEqual([]);
  expect(r.passes.length + r.incomplete.length, `${where}: axe checked text`).toBeGreaterThan(0);
}

for (const scheme of ["dark", "light"] as const) {
  test.describe(`system set to ${scheme}`, () => {
    test.use({ colorScheme: scheme });
    test.beforeEach(() => {
      mode = scheme;
    });

    test(`brand theme with the system in ${scheme} mode`, async ({ page, context }) => {
      mkdirSync(shots, { recursive: true });
      const shot = (name: string) =>
        page.screenshot({ path: `${shots}/${scheme}-${name}.png`, fullPage: true });
      const errors: string[] = [];
      const foreign: string[] = [];
      const network = { n: 0 };
      page.on("pageerror", (e) => errors.push(e.message));
      page.on("console", (m) => {
        if (/content security policy/i.test(m.text())) errors.push(m.text());
      });
      page.on("requestfailed", (r) => {
        if (r.failure()?.errorText.includes("ERR_NETWORK_CHANGED")) network.n++;
      });
      page.on("request", (r) => {
        const u = new URL(r.url());
        if (u.protocol.startsWith("http") && u.hostname !== `web.${domain}`) {
          if (u.hostname !== `app.${domain}`) foreign.push(r.url());
        }
      });

      // sso as in dashboard.spec.ts: a same-site page on app.<domain> links
      // to /sso
      const sso = `${web}/sso?token=${encodeURIComponent(token)}&url=${encodeURIComponent(`/websites/${website}`)}`;
      await context.route(`${app}/**`, (r) =>
        r.fulfill({
          contentType: "text/html",
          body: `<a id="go" href="${sso}">open dashboard</a>`,
        }),
      );
      await step(page, network, async () => {
        await page.goto(`${app}/console`);
        await page.click("#go");
        await page.waitForURL(`**/websites/${website}`, { timeout: 30_000 });
        await expect(page.getByText("Visitors", { exact: true }).first()).toBeVisible({
          timeout: 30_000,
        });
      });
      // zen picked the mode from the system setting, so both modes are covered
      expect(await page.evaluate(() => document.documentElement.getAttribute("data-theme"))).toBe(
        scheme,
      );
      await page.waitForLoadState("networkidle");
      await expectTheme(page, "overview");
      await expectBrand(page, "overview");
      await expectChips(page, "overview");
      await expectContrast(page, "overview");
      await shot("01-overview");

      // the date range menu: a floating layer on the popover surface
      await page
        .getByRole("combobox")
        .filter({ hasText: /last 24 hours/i })
        .click();
      await expect(page.getByRole("listbox").or(page.getByRole("menu")).first()).toBeVisible();
      // the menu fades in; the check and the shot need it settled
      await page.evaluate(() => Promise.all(document.getAnimations().map((a) => a.finished)));
      await expectContrast(page, "range menu");
      await page.screenshot({ path: `${shots}/${scheme}-02-range-menu.png` });
      await page.keyboard.press("Escape");

      // client-side navigation keeps the link: next.js swaps the page, not
      // the document
      await page.locator(`a[href*="/websites/${website}/realtime"]`).first().click();
      await page.waitForURL(`**/websites/${website}/realtime`, { timeout: 30_000 });
      await page.waitForLoadState("networkidle");
      await expectTheme(page, "realtime after client-side navigation");
      await expectContrast(page, "realtime");
      await shot("03-realtime");

      for (const [name, path] of [
        ["04-sessions", `/websites/${website}/sessions`],
        ["05-events", `/websites/${website}/events`],
        ["06-breakdown", `/websites/${website}/breakdown`],
        ["07-team-websites", `/teams/${team}/websites`],
        ["08-settings-teams", "/settings/teams"],
        ["09-preferences", "/settings/preferences"],
      ] as const) {
        await step(page, network, async () => {
          await page.goto(path);
          await page.waitForLoadState("networkidle");
          await expectTheme(page, name);
          await expectBrand(page, name);
          await expectContrast(page, name);
        });
        await shot(name);
      }

      // phone width: umami's mobile header carries the same mark and name
      await page.setViewportSize({ width: 390, height: 844 });
      await step(page, network, async () => {
        await page.goto(`/websites/${website}`);
        await page.waitForLoadState("networkidle");
        await expectBrand(page, "phone width");
        await expectContrast(page, "phone width");
      });
      await shot("10-phone");

      // every icon umami's head links is one the web caddy answers from the
      // theme; the go side (webTheme in deploy/caddy/web_live_test.go)
      // compares each of those answers with the theme's file byte for byte
      const icons = await page
        .locator(
          'link[rel="icon"], link[rel="apple-touch-icon"], link[rel="manifest"], link[rel="mask-icon"]',
        )
        .evaluateAll((ls) => ls.map((l) => l.getAttribute("href") ?? ""));
      expect(icons.length).toBeGreaterThan(0);
      for (const icon of icons) {
        expect(existsSync(`${theme}${icon}`), `${icon} is a theme file`).toBe(true);
      }

      expect(errors, "page errors and csp refusals").toEqual([]);
      expect(foreign, "requests to other hosts").toEqual([]);
    });
  });
}
