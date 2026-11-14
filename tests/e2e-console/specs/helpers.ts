// shared steps of the this stage flows. everything goes through the real
// console, control plane, oauth fixture and caddies; nothing here answers on
// behalf of a product service.
import { randomInt } from "node:crypto";
import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { type APIResponse, type BrowserContext, expect, type Page } from "@playwright/test";
import {
  APP,
  INGEST,
  OAUTH,
  OAUTH_CTL,
  SITE_CTL_PORT,
  TENANT_DOMAIN,
  WEB,
} from "../fixture/env.ts";

const here = dirname(fileURLToPath(import.meta.url));

export interface GitHubUser {
  id: number;
  login: string;
}

// a fresh github identity per call; ids stay clear of OPERATOR_GITHUB_IDS (1)
export function newUser(prefix: string): GitHubUser {
  return {
    id: randomInt(10_000_000, 2_000_000_000),
    login: `${prefix}-${randomInt(100_000, 999_999)}`,
  };
}

export interface Release {
  version: string;
  integrity: string;
}

export function manifest(): Release[] {
  const path = resolve(here, "../../../packages/tracker/releases/manifest.json");
  return (JSON.parse(readFileSync(path, "utf8")) as { versions: Release[] }).versions;
}

// signs in through the console's button and the fixture's consent page, and
// returns once the browser is back on a console page
export async function signIn(page: Page, user: GitHubUser, returnTo = "/"): Promise<void> {
  await page.goto(`${APP}/sign-in?return_to=${encodeURIComponent(returnTo)}`);
  await page.getByRole("link", { name: "Continue with GitHub" }).click();
  await page.waitForURL(`${OAUTH}/login/oauth/authorize?**`);
  await expect(page.locator("#fixture-banner")).toContainText("TEST-ONLY");
  await page.fill("#fixture-user-id", String(user.id));
  await page.fill("#fixture-login", user.login);
  await page.click("#fixture-authorize");
  await page.waitForURL(
    (u) => u.origin === APP && !u.pathname.startsWith("/api/") && u.pathname !== "/sign-in",
  );
}

export interface ApiResult<T = unknown> {
  status: number;
  body: T;
  headers: Record<string, string>;
}

async function result<T>(r: APIResponse): Promise<ApiResult<T>> {
  const text = await r.text();
  let body: unknown = text;
  if (text && (r.headers()["content-type"] ?? "").includes("json")) body = JSON.parse(text);
  return { status: r.status(), body: body as T, headers: r.headers() };
}

// calls the control plane as the console does: same origin, the session
// cookie of the context, X-Requested-With and Origin on state changes
export async function api<T = unknown>(
  ctx: BrowserContext,
  method: string,
  path: string,
  body?: unknown,
  headers: Record<string, string> = {},
): Promise<ApiResult<T>> {
  const h: Record<string, string> = { accept: "application/json", ...headers };
  if (method !== "GET" && method !== "HEAD") {
    if (!("x-requested-with" in lower(headers))) h["X-Requested-With"] = "sa-console";
    if (!("origin" in lower(headers))) h.Origin = APP;
  }
  const opts: Parameters<BrowserContext["request"]["fetch"]>[1] = {
    method,
    headers: h,
    maxRedirects: 0,
  };
  if (body !== undefined) opts.data = body;
  return result<T>(await ctx.request.fetch(`${APP}${path}`, opts));
}

function lower(h: Record<string, string>): Record<string, string> {
  return Object.fromEntries(Object.entries(h).map(([k, v]) => [k.toLowerCase(), v]));
}

export interface Tenant {
  id: string;
  slug: string;
  name: string;
  status: string;
  provisioning: string;
  role: string;
}

export interface Site {
  id: string;
  tenant_id: string;
  name: string;
  domain: string;
  origins: string[];
  quarantine_website_id: string;
  status: string;
}

export async function termsVersion(ctx: BrowserContext): Promise<string> {
  const r = await api<{ version: string }>(ctx, "GET", "/api/terms");
  expect(r.status).toBe(200);
  return r.body.version;
}

// enrollment through the api, for flows whose subject is not the form
export async function enrollApi(ctx: BrowserContext, name: string): Promise<Tenant> {
  const r = await api<Tenant>(ctx, "POST", "/api/tenants", {
    name,
    description: "this stage e2e",
    enrollment: { website: "https://example.org", stellar_usage: "Soroban contracts on testnet" },
    accepted_terms_version: await termsVersion(ctx),
  });
  expect(r.status, JSON.stringify(r.body)).toBe(201);
  return r.body;
}

export async function addSiteApi(
  ctx: BrowserContext,
  tenantId: string,
  host: string,
): Promise<Site> {
  const r = await api<Site>(ctx, "POST", `/api/tenants/${tenantId}/sites`, {
    name: host,
    domain: host,
    origins: [`https://${host}`],
  });
  expect(r.status, JSON.stringify(r.body)).toBe(201);
  return r.body;
}

// waits until the reconciler created the tenant's umami team
export async function waitProvisioned(ctx: BrowserContext, tenantId: string): Promise<void> {
  await expect
    .poll(
      async () => (await api<Tenant>(ctx, "GET", `/api/tenants/${tenantId}`)).body.provisioning,
      {
        timeout: 90_000,
        intervals: [500, 1000, 2000],
      },
    )
    .toBe("ready");
}

// waits until the gateway has the site in its registry: a challenge request
// from a page of the site's origin without the snippet, so no pageview is
// recorded while waiting
export async function waitRegistered(ctx: BrowserContext, siteId: string, origin: string) {
  const page = await ctx.newPage();
  try {
    await page.goto(`${origin}/probe.html`);
    await expect
      .poll(
        () =>
          page.evaluate(async (u) => {
            try {
              return (await fetch(u, { credentials: "omit" })).status;
            } catch (e) {
              return String(e);
            }
          }, `${INGEST}/v1/c?w=${siteId}`),
        { timeout: 90_000, intervals: [500, 1000, 2000] },
      )
      .toBe(200);
  } finally {
    await page.close();
  }
}

// pastes a tag into the tenant site, as a tenant edits its html
export async function pasteSnippet(host: string, tag: string): Promise<void> {
  const r = await fetch(`http://127.0.0.1:${SITE_CTL_PORT}/snippet?host=${host}`, {
    method: "POST",
    body: tag,
  });
  expect(r.status).toBe(200);
}

export async function setForeignPage(html: string): Promise<void> {
  const r = await fetch(`http://127.0.0.1:${SITE_CTL_PORT}/evil`, { method: "POST", body: html });
  expect(r.status).toBe(200);
}

export interface FixtureRequests {
  authorize: { query: Record<string, string[]>; has_scope: boolean }[];
  token: { form: Record<string, string>; basic_auth: boolean; accept: string; result: string }[];
  user: { accept: string; api_version: string; scheme: string; status: number }[];
}

export async function fixtureRequests(): Promise<FixtureRequests> {
  return (await (await fetch(`${OAUTH_CTL}/_fixture/requests`)).json()) as FixtureRequests;
}

export async function resetFixture(): Promise<void> {
  expect((await fetch(`${OAUTH_CTL}/_fixture/reset`, { method: "POST" })).status).toBe(200);
}

// the umami token from the /sso hop of an sso navigation. the caller starts
// listening before the navigation.
export function captureSsoToken(page: Page): Promise<string> {
  return new Promise((ok) => {
    const on = (req: { url(): string }) => {
      const u = new URL(req.url());
      if (u.origin === WEB && u.pathname === "/sso") {
        page.off("request", on);
        ok(u.searchParams.get("token") ?? "");
      }
    };
    page.on("request", on);
  });
}

// calls umami's read api through the web caddy with a tenant's token
export async function umamiGet(
  ctx: BrowserContext,
  token: string,
  path: string,
): Promise<ApiResult> {
  return result(
    await ctx.request.get(`${WEB}${path}`, {
      headers: { authorization: `Bearer ${token}` },
      maxRedirects: 0,
    }),
  );
}

export function uniqueHost(label: string): string {
  return `${label}-${randomInt(100_000, 999_999)}.${TENANT_DOMAIN}`;
}

export const NOT_FOUND = { error: expect.objectContaining({ code: "not_found" }) };

// opens the web dashboard of a site the way a tenant does: the project
// overview's "Open web dashboard" link for that site. the reconciler may
// still be creating the user's umami membership; the control plane then
// sends the browser to /sso-error?code=provisioning_pending (contract
// 4.4.1), and a tenant tries again a little later, as this does.
export async function openDashboard(page: Page, tenantId: string, site: Site): Promise<void> {
  const deadline = Date.now() + 120_000;
  for (;;) {
    await page.goto(`${APP}/projects/${tenantId}`);
    // each site is an article named by its heading
    const card = page.getByRole("article", { name: site.name, exact: true });
    await card.getByRole("link", { name: "Open web dashboard" }).click();
    await page.waitForURL((u) => u.origin === WEB || u.pathname === "/sso-error", {
      timeout: 30_000,
    });
    const u = new URL(page.url());
    if (u.origin === WEB) {
      await page.waitForURL(`${WEB}/websites/${site.id}**`);
      return;
    }
    const code = u.searchParams.get("code");
    if (code !== "provisioning_pending" || Date.now() > deadline) {
      throw new Error(`sso for site ${site.id} ended at ${u.pathname} with code ${code}`);
    }
    await page.waitForTimeout(2_000);
  }
}

// starts sso the way a console link does: a navigation initiated by a
// console page, so fetch metadata says same-origin and, after the redirect,
// same-site. a navigation typed into the address bar arrives at web.
// cross-site after the redirect and is refused by the web caddy, as it should.
export async function ssoFromConsole(page: Page, siteId: string, view = "primary"): Promise<void> {
  await page.goto(`${APP}/`);
  await page.evaluate((u) => {
    location.href = u;
  }, `${APP}/api/sso/umami?site=${siteId}&view=${view}`);
  await page.waitForURL((u) => u.origin === WEB || u.pathname === "/sso-error");
}
