// shared steps of the console flows. everything goes through the real
// console, control plane, oauth fixture and caddies; nothing here answers on
// behalf of a product service.
import { randomInt } from "node:crypto";
import { existsSync, readFileSync, writeFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { type APIResponse, type BrowserContext, expect, type Page } from "@playwright/test";
import {
  APP,
  APPS_CTL,
  INGEST,
  OAUTH,
  OAUTH_CTL,
  OUT,
  SITE_CTL_PORT,
  TENANT_DOMAIN,
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
    description: "console e2e",
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

// makes the jobs daemon run its hourly aggregate-web task now
// (stack_test.go restartJobs)
export async function restartJobs(): Promise<void> {
  const r = await fetch(`${APPS_CTL}/jobs/restart`, { method: "POST" });
  expect(r.status, await r.text()).toBe(204);
}

// a web dashboard route of a project (contract 18), as the signed-in member
export function webGet<T = unknown>(
  ctx: BrowserContext,
  tenantId: string,
  route: string,
): Promise<ApiResult<T>> {
  return api<T>(ctx, "GET", `/api/tenants/${tenantId}/web/${route}`);
}

// the engine routes answer 409 provisioning_pending until the reconciler
// has made the member's umami user and membership; a member tries again a
// little later, as this does
export async function waitWebReady(ctx: BrowserContext, tenantId: string, siteId: string) {
  await expect
    .poll(async () => (await webGet(ctx, tenantId, `realtime?site=${siteId}`)).status, {
      timeout: 120_000,
      intervals: [1000, 2000, 5000],
    })
    .toBe(200);
}

export function uniqueHost(label: string): string {
  return `${label}-${randomInt(100_000, 999_999)}.${TENANT_DOMAIN}`;
}

export const NOT_FOUND = { error: expect.objectContaining({ code: "not_found" }) };

// opens the web dashboard of a site the way a tenant does: the analytics
// link of its row in the project overview's sites table
export async function openDashboard(page: Page, tenantId: string, site: Site): Promise<void> {
  await page.goto(`${APP}/projects/${tenantId}`);
  const row = page.getByRole("row").filter({ hasText: site.domain });
  await row.getByRole("link", { name: `Web analytics of ${site.name}` }).click();
  await page.waitForURL(`${APP}/projects/${tenantId}/web?site=${site.id}`);
  await expect(page.getByRole("heading", { name: "Web analytics" })).toBeVisible();
}

// registers a login with the oauth fixture, which then answers github's
// public users api for it, as github does for every account
export async function registerGitHub(user: GitHubUser): Promise<void> {
  const r = await fetch(`${OAUTH_CTL}/_fixture/users`, {
    method: "POST",
    body: JSON.stringify({ id: user.id, login: user.login }),
  });
  expect(r.status, await r.text()).toBeLessThan(300);
}

interface VirtualCredential {
  credentialId: string;
  isResidentCredential: boolean;
  rpId?: string;
  privateKey: string;
  userHandle?: string;
  signCount: number;
}

// the virtual credential of each github id the specs step up with, kept
// across specs and retries, because the stack's control plane holds the
// public key the first spec registered
function credentialFile(user: GitHubUser): string {
  return resolve(OUT, `passkey-${user.id}.json`);
}

// gives the page a chromium virtual authenticator (devtools webauthn
// domain): a usb security key with a pin, that answers every prompt
// without a touch. the credential of an earlier spec is put back on it.
export async function virtualAuthenticator(page: Page, user: GitHubUser) {
  const cdp = await page.context().newCDPSession(page);
  await cdp.send("WebAuthn.enable", { enableUI: false });
  const { authenticatorId } = await cdp.send("WebAuthn.addVirtualAuthenticator", {
    options: {
      protocol: "ctap2",
      transport: "usb",
      hasResidentKey: true,
      hasUserVerification: true,
      isUserVerified: true,
      automaticPresenceSimulation: true,
    },
  });
  if (existsSync(credentialFile(user))) {
    const saved = JSON.parse(readFileSync(credentialFile(user), "utf8")) as VirtualCredential[];
    for (const credential of saved) {
      await cdp.send("WebAuthn.addCredential", { authenticatorId, credential });
    }
  }
  return {
    cdp,
    authenticatorId,
    // stores every credential with its current signature counter, so the
    // next spec's authenticator holds the same keys and counts on from them
    save: async () => {
      const { credentials } = (await cdp.send("WebAuthn.getCredentials", { authenticatorId })) as {
        credentials: VirtualCredential[];
      };
      if (credentials.length > 0) writeFileSync(credentialFile(user), JSON.stringify(credentials));
    },
  };
}

// steps an operator's browser up (contract 5.15): the first spec registers
// the operator's passkey inside the enrollment window the start of the
// control plane opened; every later one confirms with it
export async function operatorStepUp(page: Page, user: GitHubUser): Promise<void> {
  const auth = await virtualAuthenticator(page, user);
  await page.goto(`${APP}/operator`);
  const register = page.getByRole("button", { name: "Register" });
  const confirm = page.getByRole("button", { name: "Use your passkey" });
  await expect(register.or(confirm)).toBeVisible();
  const dialog = page.getByTestId("passkey-dialog");
  if (await register.isVisible()) {
    await page.getByLabel("Name").fill("e2e security key");
    await register.click();
    await dialog.getByRole("button", { name: "Create a passkey" }).click();
  } else {
    await confirm.click();
  }
  await dialog.getByRole("button", { name: "Confirm with your passkey" }).click();
  await expect(page.getByRole("navigation", { name: "Operator sections" })).toBeVisible();
  await auth.save();
}
