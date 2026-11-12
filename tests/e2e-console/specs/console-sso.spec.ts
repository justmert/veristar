import { randomUUID } from "node:crypto";
// the design verification, "sso hardening" and "token life" (two tabs):
// /sso on web.<BASE_DOMAIN> is refused cross-site and for a url outside
// /websites/, its responses carry Cache-Control: no-store and
// Referrer-Policy: no-referrer; the console's sso route refuses a cross-site
// navigation; opening the web dashboard in two tabs keeps both logged in.
import type { Page, Response } from "@playwright/test";
import { APP, EVIL_ORIGIN, WEB } from "../fixture/env.ts";
import {
  addSiteApi,
  api,
  captureSsoToken,
  enrollApi,
  newUser,
  openDashboard,
  type Site,
  setForeignPage,
  signIn,
  type Tenant,
  umamiGet,
  uniqueHost,
  waitProvisioned,
} from "./helpers.ts";
import { expect, test } from "./test.ts";

let tenant: Tenant;
let site: Site;

test.beforeEach(async ({ page, context }) => {
  await signIn(page, newUser("sso"));
  tenant = await enrollApi(context, `sso hardening ${randomUUID().slice(0, 8)}`);
  site = await addSiteApi(context, tenant.id, uniqueHost("sso"));
  await waitProvisioned(context, tenant.id);
});

// navigates from the current page by script, so fetch metadata describes
// the page's own site, and returns the response of the first request to url
async function scriptNavigate(
  page: Page,
  url: string,
  match: (r: Response) => boolean,
): Promise<Response> {
  const resp = page.waitForResponse(match);
  await page.evaluate((u) => {
    location.href = u;
  }, url);
  const r = await resp;
  // let the navigation commit before the caller starts another one
  await page.waitForURL((u) => u.href === r.url());
  return r;
}

const isSso = (r: Response) => {
  const u = new URL(r.url());
  return u.origin === WEB && u.pathname.startsWith("/sso");
};

test("web /sso refuses cross-site and foreign targets, and is never cached", async ({
  page,
  context,
}) => {
  // a real token from a real sso, so only the request context decides
  const tokenP = captureSsoToken(page);
  const ssoResp = page.waitForResponse(isSso);
  const hop = page.waitForResponse((r) => r.url().startsWith(`${APP}/api/sso/umami`));
  await openDashboard(page, tenant.id, site);
  const token = await tokenP;
  const ok = await ssoResp;
  expect(ok.status()).toBe(200);
  expect.soft(ok.headers()["cache-control"]).toBe("no-store");
  expect.soft(ok.headers()["referrer-policy"]).toBe("no-referrer");
  const h = await hop;
  expect(h.status()).toBe(302);
  expect.soft(h.headers()["cache-control"]).toBe("no-store");
  expect.soft(h.headers()["referrer-policy"]).toBe("no-referrer");
  await page.waitForURL(`${WEB}/websites/${site.id}**`);

  const sso = (target: string) =>
    `${WEB}/sso?token=${encodeURIComponent(token)}&url=${encodeURIComponent(target)}`;

  // cross-site: a link on a foreign site
  await setForeignPage(
    `<!doctype html><title>foreign</title><a id="go" href="${sso(`/websites/${site.id}`)}">x</a>`,
  );
  await page.goto(`${EVIL_ORIGIN}/`);
  const cross = page.waitForResponse(isSso);
  await page.click("#go");
  const cr = await cross;
  expect(cr.status()).toBe(403);
  expect.soft(cr.headers()["cache-control"]).toBe("no-store");
  expect.soft(cr.headers()["referrer-policy"]).toBe("no-referrer");

  // typed into the address bar (Sec-Fetch-Site: none)
  const typed = await page.goto(sso(`/websites/${site.id}`));
  expect(typed?.status()).toBe(403);

  // same-site (from a console page), but a target outside /websites/
  for (const target of [
    "/settings/profile",
    "/websites/../settings",
    "//evil.sa-attacker.example/websites/x",
    `https://evil.sa-attacker.example/websites/${site.id}`,
    `/websites/${site.id}?x=1`,
    "/teams/x/websites",
  ]) {
    await page.goto(`${APP}/`);
    const r = await scriptNavigate(page, sso(target), isSso);
    expect(r.status(), `url=${target}`).toBe(403);
    expect.soft(r.headers()["cache-control"]).toBe("no-store");
  }
  // the same page with a /websites/ target is accepted, so the refusals
  // above came from the target and not from the token or the context
  await page.goto(`${APP}/`);
  const good = await scriptNavigate(page, sso(`/websites/${site.id}`), isSso);
  expect(good.status()).toBe(200);
  await page.waitForURL(`${WEB}/websites/${site.id}**`);
  expect((await umamiGet(context, token, `/api/websites/${site.id}`)).status).toBe(200);
});

test("the console's sso route refuses a cross-site navigation", async ({ page }) => {
  await setForeignPage(
    `<!doctype html><title>foreign</title><a id="go" href="${APP}/api/sso/umami?site=${site.id}">x</a>`,
  );
  await page.goto(`${EVIL_ORIGIN}/`);
  let reachedWeb = false;
  page.on("request", (r) => {
    if (r.url().startsWith(WEB)) reachedWeb = true;
  });
  await page.click("#go");
  await page.waitForURL(`${APP}/sso-error?**`);
  const u = new URL(page.url());
  expect(u.searchParams.get("code")).toBe("cross_site");
  expect(reachedWeb).toBe(false);
});

test("sso without a session, or for an unknown site, never reaches umami", async ({
  page,
  browser,
}) => {
  const anon = await browser.newContext();
  try {
    const p = await anon.newPage();
    const r = await p.request.get(`${APP}/api/sso/umami?site=${site.id}`, { maxRedirects: 0 });
    expect(r.headers().location ?? "").not.toContain(WEB);
    expect([302, 303, 401]).toContain(r.status());
  } finally {
    await anon.close();
  }
  await page.goto(`${APP}/api/sso/umami?site=00000000-0000-7000-8000-000000000000`);
  await page.waitForURL(`${APP}/sso-error?**`);
  expect(new URL(page.url()).searchParams.get("code")).toBe("not_found");
});

test("opening the web dashboard in two tabs keeps both logged in", async ({ context }) => {
  const one = await context.newPage();
  const t1 = captureSsoToken(one);
  await openDashboard(one, tenant.id, site);
  await one.waitForURL(`${WEB}/websites/${site.id}**`);
  const token1 = await t1;

  const two = await context.newPage();
  const t2 = captureSsoToken(two);
  await openDashboard(two, tenant.id, site);
  await two.waitForURL(`${WEB}/websites/${site.id}**`);
  const token2 = await t2;

  // the second sso logged in again without setting a password, so the
  // first token is still accepted
  for (const t of [token1, token2]) {
    const r = await context.request.post(`${WEB}/api/auth/verify`, {
      headers: { authorization: `Bearer ${t}` },
      maxRedirects: 0,
    });
    expect(r.status()).toBe(200);
    expect((await umamiGet(context, t, `/api/websites/${site.id}`)).status).toBe(200);
  }
  // the first tab keeps working: a reload and umami's own navigation stay
  // on the dashboard instead of going to /login (which redirects to the console)
  for (const p of [one, two]) {
    await p.reload();
    await p.waitForLoadState("networkidle");
    expect(new URL(p.url()).origin).toBe(WEB);
    expect(new URL(p.url()).pathname).toBe(`/websites/${site.id}`);
    await expect(p.getByText("Views", { exact: true }).first()).toBeVisible();
    await p.goto(`${WEB}/websites/${site.id}/sessions`);
    await p.waitForLoadState("networkidle");
    expect(new URL(p.url()).pathname).toBe(`/websites/${site.id}/sessions`);
  }
  // the session cookie of the console is never sent to web.
  const webCookies = await context.cookies(WEB);
  expect(webCookies.find((c) => c.name === "sa_session")).toBeUndefined();
  expect(await api(context, "GET", "/api/me")).toMatchObject({ status: 200 });
});
