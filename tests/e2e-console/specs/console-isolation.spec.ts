// tenant isolation in the console api and its web dashboard:
// a second user in a second tenant gets 404 for every tenant-scoped route of
// the first tenant, also with guessed ids and with the first tenant's site
// inside the second tenant's own dashboard; removing a member takes access
// away on the next request. umami's own team check is the isolation gate's
// umami suite (tests/isolation/umami).
import { randomUUID } from "node:crypto";
import type { BrowserContext } from "@playwright/test";
import { APP } from "../fixture/env.ts";
import {
  addSiteApi,
  api,
  enrollApi,
  type GitHubUser,
  NOT_FOUND,
  newUser,
  type Site,
  signIn,
  type Tenant,
  termsVersion,
  uniqueHost,
  waitProvisioned,
  waitWebReady,
  webGet,
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

test("the web dashboard of another project answers not found, page and api alike", async () => {
  const routes = [
    "summary",
    "breakdown?dimension=page",
    "breakdown?dimension=country",
    "hourly",
    "realtime",
  ];
  for (const r of routes) {
    const sep = r.includes("?") ? "&" : "?";
    // a's project, and a's site inside b's own project
    for (const path of [
      `/api/tenants/${a.tenant.id}/web/${r}`,
      `/api/tenants/${a.tenant.id}/web/${r}${sep}site=${a.site.id}`,
      `/api/tenants/${b.tenant.id}/web/${r}${sep}site=${a.site.id}`,
      `/api/tenants/${randomUUID()}/web/${r}`,
    ]) {
      const res = await api(b.ctx, "GET", path);
      expect(res.status, path).toBe(404);
      expect(res.body, path).toEqual(NOT_FOUND);
    }
  }
  // b's own dashboard answers, and lists only b's site
  await waitWebReady(b.ctx, b.tenant.id, b.site.id);
  const own = await webGet<{ sites: { id: string }[] }>(b.ctx, b.tenant.id, "summary");
  expect(own.status).toBe(200);
  expect(own.body.sites.map((x) => x.id)).toEqual([b.site.id]);
  const page = await b.ctx.newPage();
  await page.goto(`${APP}/projects/${a.tenant.id}/web`);
  await expect(page.getByText(a.site.name, { exact: true })).toHaveCount(0);
  await expect(page.getByRole("heading", { name: "Web", exact: true })).toHaveCount(0);
  await page.close();
});

test("the removed sso route answers not found and hands out no umami token", async () => {
  const res = await api(b.ctx, "GET", `/api/sso/umami?site=${b.site.id}`);
  expect(res.status).toBe(404);
  expect(res.body).toEqual(NOT_FOUND);
});

test("a removed member loses console and web dashboard access at once", async () => {
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

  // b reads a's dashboard once the reconciler added b to a's umami team
  await waitWebReady(b.ctx, a.tenant.id, a.site.id);

  // a removes b: the next console request is refused
  const rm = await api(a.ctx, "DELETE", `/api/tenants/${a.tenant.id}/members/${b.userId}`);
  expect(rm.status).toBe(204);
  const after = await api(b.ctx, "GET", `/api/tenants/${a.tenant.id}`);
  expect(after.status).toBe(404);
  expect((await api(b.ctx, "GET", `/api/sites/${a.site.id}/snippet`)).status).toBe(404);

  // and every web dashboard route of a is closed to b at once
  for (const r of ["summary", "realtime", `hourly?site=${a.site.id}`]) {
    expect((await webGet(b.ctx, a.tenant.id, r)).status, r).toBe(404);
  }
  // b's own dashboard still answers: only a's access ended
  expect((await webGet(b.ctx, b.tenant.id, "realtime")).status).toBe(200);
});
