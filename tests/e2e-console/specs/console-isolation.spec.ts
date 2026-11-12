// the design verification, "isolation, console api" and "isolation, umami":
// a second user in a second tenant gets 404 for every tenant-scoped route of
// the first tenant, also with guessed ids; removing a member takes access
// away on the next request; after sso as user b, umami returns no data of
// tenant a, and a removed member's umami token stops working after
// reconciliation.
import { randomUUID } from "node:crypto";
import type { BrowserContext } from "@playwright/test";
import { APP, WEB } from "../fixture/env.ts";
import {
  addSiteApi,
  api,
  captureSsoToken,
  enrollApi,
  type GitHubUser,
  newUser,
  type Site,
  signIn,
  ssoFromConsole,
  type Tenant,
  termsVersion,
  umamiGet,
  uniqueHost,
  waitProvisioned,
} from "./helpers.ts";
import { expect, test } from "./test.ts";

test.describe.configure({ mode: "serial" });

interface Party {
  ctx: BrowserContext;
  user: GitHubUser;
  userId: string;
  tenant: Tenant;
  site: Site;
}

let a: Party;
let b: Party;

async function party(ctx: BrowserContext, prefix: string): Promise<Party> {
  const page = await ctx.newPage();
  const user = newUser(prefix);
  await signIn(page, user);
  const me = await api<{ user: { id: string } }>(ctx, "GET", "/api/me");
  const tenant = await enrollApi(ctx, `iso ${prefix} ${user.login}`);
  const site = await addSiteApi(ctx, tenant.id, uniqueHost(prefix));
  await waitProvisioned(ctx, tenant.id);
  await page.close();
  return { ctx, user, userId: me.body.user.id, tenant, site };
}

// every tenant-scoped route of contract section 4, with valid bodies, so a
// 404 can only come from the membership check
function tenantRoutes(t: Tenant, s: Site, memberId: string, terms: string) {
  const T = `/api/tenants/${t.id}`;
  return [
    ["GET", T, undefined],
    ["PATCH", T, { name: "taken over" }],
    ["GET", `${T}/members`, undefined],
    ["POST", `${T}/members`, { github_login: "someone-else", role: "member" }],
    ["PATCH", `${T}/members/${memberId}`, { role: "member" }],
    ["DELETE", `${T}/members/${memberId}`, undefined],
    ["DELETE", `${T}/invites/${randomUUID()}`, undefined],
    ["POST", `${T}/terms`, { version: terms }],
    ["GET", `${T}/sites`, undefined],
    ["GET", `${T}/sites?include_removed=true`, undefined],
    [
      "POST",
      `${T}/sites`,
      { name: "x", domain: "x.sa-tenant.example", origins: ["https://x.sa-tenant.example"] },
    ],
    ["GET", `${T}/sites/${s.id}`, undefined],
    ["PATCH", `${T}/sites/${s.id}`, { name: "renamed" }],
    ["DELETE", `${T}/sites/${s.id}`, undefined],
    ["GET", `/api/sites/${s.id}/snippet`, undefined],
    ["GET", `/api/sites/${s.id}/ingest`, undefined],
  ] as const;
}

test.beforeAll(async ({ browser }) => {
  a = await party(await browser.newContext(), "isoa");
  b = await party(await browser.newContext(), "isob");
});

test.afterAll(async () => {
  await a?.ctx.close();
  await b?.ctx.close();
});

test("user b gets 404 on every route of tenant a, also with guessed ids", async () => {
  const terms = await termsVersion(b.ctx);
  const failures: string[] = [];
  const check = async (method: string, path: string, body: unknown) => {
    const r = await api(b.ctx, method, path, body);
    const code = (r.body as { error?: { code?: string } })?.error?.code;
    if (r.status !== 404 || code !== "not_found")
      failures.push(`${method} ${path}: ${r.status} ${code}`);
  };
  for (const [m, p, body] of tenantRoutes(a.tenant, a.site, a.userId, terms))
    await check(m, p, body);

  // guessed and malformed ids answer exactly like a foreign one
  const guessed: Tenant = { ...a.tenant, id: randomUUID() };
  const guessedSite: Site = { ...a.site, id: randomUUID() };
  for (const [m, p, body] of tenantRoutes(guessed, guessedSite, randomUUID(), terms))
    await check(m, p, body);
  for (const id of ["not-a-uuid", a.tenant.id.toUpperCase(), `${a.tenant.id}x`]) {
    await check("GET", `/api/tenants/${id}`, undefined);
    await check("GET", `/api/tenants/${id}/sites`, undefined);
    await check("GET", `/api/sites/${id}/snippet`, undefined);
  }
  // b's own tenant with a's site id does not reach a's site either
  await check("GET", `/api/tenants/${b.tenant.id}/sites/${a.site.id}`, undefined);
  await check("PATCH", `/api/tenants/${b.tenant.id}/sites/${a.site.id}`, { name: "x" });
  await check("DELETE", `/api/tenants/${b.tenant.id}/sites/${a.site.id}`, undefined);
  expect(failures, "routes that did not answer 404 not_found").toEqual([]);

  // lists show only b's own tenant
  const list = await api<{ items: Tenant[] }>(b.ctx, "GET", "/api/tenants");
  expect(list.body.items.map((t) => t.id)).toEqual([b.tenant.id]);
  const me = await api<{ tenants: Tenant[] }>(b.ctx, "GET", "/api/me");
  expect(me.body.tenants.map((t) => t.id)).toEqual([b.tenant.id]);

  // a is untouched by b's attempts
  const tA = await api<Tenant>(a.ctx, "GET", `/api/tenants/${a.tenant.id}`);
  expect(tA.body.name).toBe(a.tenant.name);
  const sA = await api<Site>(a.ctx, "GET", `/api/tenants/${a.tenant.id}/sites/${a.site.id}`);
  expect(sA.body).toMatchObject({ name: a.site.name, status: "active" });
});

test("sso for a foreign site goes to the error page, not to umami", async () => {
  const page = await b.ctx.newPage();
  let reachedWeb = false;
  page.on("request", (r) => {
    if (r.url().startsWith(WEB)) reachedWeb = true;
  });
  await ssoFromConsole(page, a.site.id);
  expect(page.url()).toContain(`${APP}/sso-error?`);
  expect(new URL(page.url()).searchParams.get("code")).toBe("not_found");
  expect(reachedWeb).toBe(false);
  await page.close();
});

test("after sso as b, umami returns no data of tenant a", async () => {
  const page = await b.ctx.newPage();
  const token = captureSsoToken(page);
  await ssoFromConsole(page, b.site.id);
  await page.waitForURL(`${WEB}/websites/${b.site.id}**`);
  const tb = await token;
  expect(tb).not.toBe("");
  // b's own website works with this token
  expect((await umamiGet(b.ctx, tb, `/api/websites/${b.site.id}`)).status).toBe(200);

  const now = Date.now();
  const range = `startAt=${now - 86_400_000}&endAt=${now}`;
  const leaks: string[] = [];
  for (const id of [a.site.id, a.site.quarantine_website_id]) {
    for (const path of [
      `/api/websites/${id}`,
      `/api/websites/${id}/stats?${range}`,
      `/api/websites/${id}/pageviews?${range}&unit=hour&timezone=UTC`,
      `/api/websites/${id}/metrics?${range}&type=url`,
      `/api/websites/${id}/metrics?${range}&type=referrer`,
      `/api/websites/${id}/metrics?${range}&type=country`,
      `/api/websites/${id}/sessions?${range}`,
      `/api/websites/${id}/events?${range}`,
      `/api/websites/${id}/active`,
      `/api/websites/${id}/daterange`,
    ]) {
      const r = await umamiGet(b.ctx, tb, path);
      if (![401, 403, 404].includes(r.status))
        leaks.push(`${path}: ${r.status} ${JSON.stringify(r.body).slice(0, 120)}`);
    }
  }
  expect(leaks, "umami routes that answered tenant a's data to user b").toEqual([]);

  // the ui shows nothing of a's website either
  await page.goto(`${WEB}/websites/${a.site.id}`);
  await page.waitForLoadState("networkidle");
  await expect(page.getByText(a.site.name, { exact: true })).toHaveCount(0);
  await page.close();
});

test("a removed member loses console access at once and umami access after reconciliation", async () => {
  // a adds b as a member: b now sees tenant a
  const add = await api<{ kind: string; member: { user_id: string } }>(
    a.ctx,
    "POST",
    `/api/tenants/${a.tenant.id}/members`,
    {
      github_login: b.user.login,
      role: "member",
    },
  );
  expect(add.status, JSON.stringify(add.body)).toBe(201);
  expect(add.body.kind).toBe("member");
  expect((await api(b.ctx, "GET", `/api/tenants/${a.tenant.id}`)).status).toBe(200);
  // members may not do owner actions
  const del = await api(b.ctx, "DELETE", `/api/tenants/${a.tenant.id}/sites/${a.site.id}`);
  expect(del.status).toBe(403);
  expect(del.body).toMatchObject({ error: { code: "owner_required" } });

  // b opens a's dashboard once the reconciler added b to a's umami team
  const page = await b.ctx.newPage();
  await page.goto(`${APP}/`);
  let tb = "";
  await expect
    .poll(
      async () => {
        const token = captureSsoToken(page);
        await ssoFromConsole(page, a.site.id);
        if (page.url().startsWith(`${APP}/sso-error`))
          return new URL(page.url()).searchParams.get("code");
        await page.waitForURL(`${WEB}/websites/${a.site.id}**`);
        tb = await token;
        return "ok";
      },
      { timeout: 90_000, intervals: [1000, 2000, 3000] },
    )
    .toBe("ok");
  expect((await umamiGet(b.ctx, tb, `/api/websites/${a.site.id}`)).status).toBe(200);

  // a removes b: the next console request is refused
  const rm = await api(a.ctx, "DELETE", `/api/tenants/${a.tenant.id}/members/${b.userId}`);
  expect(rm.status).toBe(204);
  const after = await api(b.ctx, "GET", `/api/tenants/${a.tenant.id}`);
  expect(after.status).toBe(404);
  expect((await api(b.ctx, "GET", `/api/sites/${a.site.id}/snippet`)).status).toBe(404);

  // and the token b got for a's website stops working once the reconciler
  // removed the team membership
  await expect
    .poll(async () => (await umamiGet(b.ctx, tb, `/api/websites/${a.site.id}`)).status, {
      timeout: 90_000,
      intervals: [1000, 2000, 3000],
    })
    .not.toBe(200);
  const now = Date.now();
  const stats = await umamiGet(
    b.ctx,
    tb,
    `/api/websites/${a.site.id}/stats?startAt=${now - 86_400_000}&endAt=${now}`,
  );
  expect([401, 403, 404]).toContain(stats.status);
  // b's own website still works with the same token: only a's access ended
  expect((await umamiGet(b.ctx, tb, `/api/websites/${b.site.id}`)).status).toBe(200);
  await page.close();
});
