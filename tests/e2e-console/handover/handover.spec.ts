// handover rehearsal (docs/handover/rebuild.md), browser half (tests/handover runs it).
//   @populate  environment a: two github users sign in, enroll a project
//              each, add sites, paste the console's snippet into the tenant
//              site and load pages through the real gateway, and send api
//              traffic from the go example app with a token from the console
//   @history   environment b after the migration: the same github users sign
//              in and find their projects, sites, api requests and umami
//              pageviews
//   @cutover   environment b behind the old names: the snippet pasted before
//              the migration still delivers a pageview, in every engine
import { readFileSync, writeFileSync } from "node:fs";
import type { BrowserContext, Page } from "@playwright/test";
import { APP, APPS_CTL, INGEST, WEB } from "../fixture/env.ts";
import {
  addSiteApi,
  api,
  captureSsoToken,
  enrollApi,
  type GitHubUser,
  newUser,
  openDashboard,
  pasteSnippet,
  type Site,
  signIn,
  type Tenant,
  umamiGet,
  uniqueHost,
  waitProvisioned,
  waitRegistered,
} from "../specs/helpers.ts";
import { expect, test } from "../specs/test.ts";

function statePath(): string {
  const p = process.env.HO_STATE;
  if (!p) throw new Error("missing env HO_STATE; run through make handover-it");
  return p;
}

interface SiteState {
  id: string;
  name: string;
  host: string;
  tag: string;
  pageviews: number;
}
interface TenantState {
  id: string;
  requests: number;
  user: GitHubUser;
  sites: SiteState[];
}
interface State {
  day: string;
  users: GitHubUser[];
  tenants: TenantState[];
}

const PAGES = ["/", "/about.html", "/"];

test.use({ timezoneId: "UTC" });

// loads pages of a tenant site and waits until the gateway accepted one
// pageview per page
async function visit(ctx: BrowserContext, host: string, pages: string[]): Promise<void> {
  const page = await ctx.newPage();
  let accepted = 0;
  page.on("response", (r) => {
    if (r.request().method() !== "POST" || r.url() !== `${INGEST}/v1/e`) return;
    const body = JSON.parse(r.request().postData() ?? "{}") as { t?: string };
    if (body.t === "pv" && r.status() === 202) accepted++;
  });
  for (const p of pages) {
    const want = accepted + 1;
    await page.goto(`https://${host}${p}`);
    await expect.poll(() => accepted, { timeout: 30_000 }).toBe(want);
  }
  await page.close();
}

// requests to the example app; each one is a request the api path counts
async function drive(base: string, n: number): Promise<number> {
  let sent = 0;
  for (let i = 0; i < n; i++) {
    const r = await fetch(`${base}/accounts`, {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ name: `h${i}` }),
    });
    expect(r.status).toBe(201);
    const { id } = (await r.json()) as { id: string };
    sent++;
    expect((await fetch(`${base}/accounts/${id}`)).status).toBe(200);
    sent++;
    expect((await fetch(`${base}/accounts/${9000 + i}`)).status).toBe(404);
    sent++;
  }
  return sent;
}

test("@populate tenants, sites, pageviews and api traffic in environment a", async ({
  browser,
}) => {
  const state: State = { day: new Date().toISOString().slice(0, 10), users: [], tenants: [] };
  for (const [ti, siteCount] of [
    [0, 2],
    [1, 1],
  ] as const) {
    const ctx = await browser.newContext({ timezoneId: "UTC" });
    const page = await ctx.newPage();
    const user = newUser(`handover${ti}`);
    await signIn(page, user);
    const tenant = await enrollApi(ctx, `Handover project ${user.login}`);
    await waitProvisioned(ctx, tenant.id);
    const sites: SiteState[] = [];
    for (let si = 0; si < siteCount; si++) {
      const host = uniqueHost(`ho${ti}s${si}`);
      const site = await addSiteApi(ctx, tenant.id, host);
      const snip = await api<{ tag: string }>(ctx, "GET", `/api/sites/${site.id}/snippet`);
      expect(snip.status).toBe(200);
      await pasteSnippet(host, snip.body.tag);
      await waitRegistered(ctx, site.id, `https://${host}`);
      await visit(ctx, host, PAGES);
      sites.push({
        id: site.id,
        name: site.name,
        host,
        tag: snip.body.tag,
        pageviews: PAGES.length,
      });
    }
    const tok = await api<{ token: string }>(ctx, "POST", `/api/tenants/${tenant.id}/otlp-tokens`, {
      name: "handover",
    });
    expect(tok.status, JSON.stringify(tok.body)).toBe(201);
    const r = await fetch(`${APPS_CTL}/apps`, {
      method: "POST",
      body: JSON.stringify({
        service: `ho-api-${ti}`,
        token: tok.body.token,
        signals: "metrics,traces",
      }),
    });
    const text = await r.text();
    expect(r.status, text).toBe(200);
    const requests = await drive((JSON.parse(text) as { url: string }).url, 4 + ti);
    state.users.push(user);
    state.tenants.push({ id: tenant.id, requests, user, sites });
    await ctx.close();
  }
  writeFileSync(statePath(), JSON.stringify(state, null, 2));
});

function readState(): State {
  return JSON.parse(readFileSync(statePath(), "utf8")) as State;
}

async function stats(
  ctx: BrowserContext,
  page: Page,
  tenantId: string,
  site: SiteState,
  day: string,
) {
  const token = captureSsoToken(page);
  await openDashboard(page, tenantId, { id: site.id, name: site.name } as Site);
  const start = Date.parse(`${day}T00:00:00Z`);
  const r = await umamiGet(
    ctx,
    await token,
    `/api/websites/${site.id}/stats?startAt=${start}&endAt=${start + 86_400_000 - 1}`,
  );
  expect(r.status, JSON.stringify(r.body)).toBe(200);
  return r.body as { pageviews: number };
}

test("@history the same users see their history in environment b", async ({ browser }) => {
  const state = readState();
  for (const t of state.tenants) {
    const ctx = await browser.newContext({ timezoneId: "UTC" });
    const page = await ctx.newPage();
    await signIn(page, t.user);
    const list = await api<{ items: Tenant[] }>(ctx, "GET", "/api/tenants");
    expect(list.status).toBe(200);
    expect(list.body.items.map((x) => x.id)).toContain(t.id);
    const sites = await api<{ items: Site[] }>(ctx, "GET", `/api/tenants/${t.id}/sites`);
    expect(sites.body.items.map((s) => s.id).sort()).toEqual(t.sites.map((s) => s.id).sort());
    const routes = await api<{ items: { requests: number }[] }>(
      ctx,
      "GET",
      `/api/tenants/${t.id}/api-metrics/routes?range=24h&limit=200`,
    );
    expect(routes.status, JSON.stringify(routes.body)).toBe(200);
    expect(routes.body.items.reduce((n, i) => n + i.requests, 0)).toBe(t.requests);
    await page.goto(`${APP}/projects/${t.id}/api/traffic`);
    await page.screenshot({
      path: `${process.env.CE2E_OUT}/handover-traffic-${t.id}.png`,
      fullPage: true,
    });
    for (const s of t.sites) {
      const st = await stats(ctx, page, t.id, s, state.day);
      expect(st.pageviews, `pageviews of ${s.host} in umami b`).toBe(s.pageviews);
      expect(new URL(page.url()).origin).toBe(WEB);
    }
    await ctx.close();
  }
});

test("@cutover the snippet from before the migration delivers into environment b", async ({
  browser,
}) => {
  const state = readState();
  const site = state.tenants[0]?.sites[0];
  if (!site) throw new Error("no site in the populate state");
  // the tenant's page is unchanged: the tag it pasted before the handover
  expect(site.tag).toContain(`src="${INGEST}/v/`);
  await pasteSnippet(site.host, site.tag);
  const ctx = await browser.newContext({ timezoneId: "UTC" });
  await visit(ctx, site.host, ["/"]);
  await ctx.close();
});
