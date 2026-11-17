// session security: sign-in through the oauth
// fixture with pkce s256, state and no scope; cookie flags; state-changing
// requests without the custom header or with a foreign origin are refused,
// also when a real foreign page tries them; sign-out ends the session.

import { APP, EVIL_ORIGIN, OAUTH } from "../fixture/env.ts";
import {
  api,
  fixtureRequests,
  newUser,
  resetFixture,
  setForeignPage,
  signIn,
  type Tenant,
  termsVersion,
} from "./helpers.ts";
import { expect, test } from "./test.ts";

const CALLBACK = `${APP}/api/auth/github/callback`;

test("sign-in sends pkce s256, state and no scope; cookies carry the documented flags", async ({
  page,
  context,
}) => {
  await resetFixture();
  const user = newUser("sess");
  await page.goto(`${APP}/sign-in`);
  await page.getByRole("link", { name: "Continue with GitHub" }).click();
  await page.waitForURL(`${OAUTH}/login/oauth/authorize?**`);

  // while the browser is at github, only the sealed oauth cookie exists. its
  // __Host- name makes the browser keep it host-only with path /
  const during = await context.cookies(`${APP}/api/auth/github/callback`);
  const oauth = during.find((c) => c.name === "__Host-sa_oauth");
  expect(oauth, "__Host-sa_oauth cookie during sign-in").toBeTruthy();
  expect(oauth).toMatchObject({
    httpOnly: true,
    secure: true,
    sameSite: "Lax",
    path: "/",
    domain: new URL(APP).hostname,
  });
  const ttl = (oauth?.expires ?? 0) - Date.now() / 1000;
  expect(ttl).toBeGreaterThan(500);
  expect(ttl).toBeLessThanOrEqual(600);
  expect((await context.cookies(APP)).find((c) => c.name === "__Host-sa_session")).toBeUndefined();

  const authorize = new URL(page.url());
  const q = authorize.searchParams;
  expect(q.has("scope"), "no scope parameter").toBe(false);
  expect(q.get("code_challenge_method")).toBe("S256");
  expect(q.get("code_challenge")).toMatch(/^[A-Za-z0-9_-]{43}$/);
  expect(q.get("redirect_uri")).toBe(CALLBACK);
  expect(q.get("allow_signup")).toBe("true");
  // 32 random bytes, base64url
  expect(q.get("state")).toMatch(/^[A-Za-z0-9_-]{43}$/);
  expect(page.url()).not.toContain("__Host-sa_oauth");

  await page.fill("#fixture-user-id", String(user.id));
  await page.fill("#fixture-login", user.login);
  await page.click("#fixture-authorize");
  await page.waitForURL(`${APP}/`);

  const cookies = await context.cookies(APP);
  expect(
    cookies.find((c) => c.name === "__Host-sa_oauth"),
    "__Host-sa_oauth deleted at the callback",
  ).toBeUndefined();
  const session = cookies.find((c) => c.name === "__Host-sa_session");
  expect(session).toMatchObject({
    httpOnly: true,
    secure: true,
    sameSite: "Lax",
    path: "/",
    // host-only: no Domain attribute, so a sibling host such as in. never receives it
    domain: new URL(APP).hostname,
  });
  expect(session?.value).toMatch(/^[A-Za-z0-9_-]{43}$/);
  const life = (session?.expires ?? 0) - Date.now() / 1000;
  // persistent; the absolute lifetime is 7 days, the idle timeout 24 hours,
  // and scs sets the cookie expiry to the earlier of the two
  expect(life).toBeGreaterThan(23 * 3600);
  expect(life).toBeLessThanOrEqual(7 * 24 * 3600);

  const me = await api<{ user: { github_id: number; github_login: string } }>(
    context,
    "GET",
    "/api/me",
  );
  expect(me.status).toBe(200);
  expect(me.body.user).toMatchObject({ github_id: user.id, github_login: user.login });
  expect(me.headers["cache-control"]).toBe("no-store");

  // what the control plane sent to the fixture's token and user endpoints
  const rec = await fixtureRequests();
  expect(rec.authorize).toHaveLength(1);
  expect(rec.authorize[0]?.has_scope).toBe(false);
  expect(rec.token).toHaveLength(1);
  expect(rec.token[0]).toMatchObject({ result: "ok", basic_auth: false });
  expect(rec.token[0]?.form).toMatchObject({
    client_secret: "<set>",
    code_verifier: "<set>",
    redirect_uri: CALLBACK,
  });
  expect(rec.user).toHaveLength(1);
  expect(rec.user[0]).toMatchObject({
    status: 200,
    accept: "application/vnd.github+json",
    api_version: "2022-11-28",
    scheme: "bearer",
  });
});

test("state-changing requests need X-Requested-With and the console origin", async ({
  page,
  context,
}) => {
  await signIn(page, newUser("csrf"));
  const body = {
    name: "csrf probe",
    enrollment: { website: "https://example.org", stellar_usage: "testing" },
    accepted_terms_version: await termsVersion(context),
  };
  const before = await api<{ items: Tenant[] }>(context, "GET", "/api/tenants");
  expect(before.status).toBe(200);

  const noHeader = await api(context, "POST", "/api/tenants", body, { "X-Requested-With": "" });
  expect(noHeader.status).toBe(403);
  expect(noHeader.body).toMatchObject({ error: { code: "csrf_header_missing" } });

  const wrongHeader = await api(context, "POST", "/api/tenants", body, {
    "X-Requested-With": "SA-CONSOLE",
  });
  expect(wrongHeader.status).toBe(403);
  expect(wrongHeader.body).toMatchObject({ error: { code: "csrf_header_missing" } });

  const foreign = await api(context, "POST", "/api/tenants", body, { Origin: EVIL_ORIGIN });
  expect(foreign.status).toBe(403);
  expect(foreign.body).toMatchObject({ error: { code: "origin_mismatch" } });

  // a sibling host of the same registered domain is another origin too
  const sibling = await api(context, "POST", "/api/tenants", body, {
    Origin: APP.replace("app.", "in."),
  });
  expect(sibling.status).toBe(403);
  expect(sibling.body).toMatchObject({ error: { code: "origin_mismatch" } });

  const logoutNoHeader = await api(context, "POST", "/api/auth/logout", undefined, {
    "X-Requested-With": "",
  });
  expect(logoutNoHeader.status).toBe(403);

  // a real foreign page: a form post (no custom header possible) and a fetch
  // with the header (needs a preflight the control plane never grants)
  await setForeignPage(`<!doctype html><title>foreign</title>
<form id="f" method="post" action="${APP}/api/tenants" enctype="text/plain">
<input name='{"name":"x","pad":"' value='"}'></form>`);
  await page.goto(`${EVIL_ORIGIN}/`);
  const formResp = page.waitForResponse((r) => r.url() === `${APP}/api/tenants`);
  await page.evaluate(() => (document.getElementById("f") as HTMLFormElement).submit());
  const fr = await formResp;
  expect([401, 403, 415]).toContain(fr.status());
  await page.waitForURL(`${APP}/api/tenants`);
  await page.goto(`${EVIL_ORIGIN}/`);
  const fetched = await page.evaluate(
    async ([url, b]) => {
      try {
        const r = await fetch(url, {
          method: "POST",
          credentials: "include",
          headers: { "X-Requested-With": "sa-console", "Content-Type": "application/json" },
          body: b,
        });
        return `status ${r.status}`;
      } catch (e) {
        return `blocked: ${(e as Error).name}`;
      }
    },
    [`${APP}/api/tenants`, JSON.stringify(body)] as const,
  );
  expect(fetched).toMatch(/^blocked: TypeError$/);

  const after = await api<{ items: Tenant[] }>(context, "GET", "/api/tenants");
  expect(after.body.items.length, "no tenant was created by any refused request").toBe(
    before.body.items.length,
  );
});

test("sign-out ends the session for the old cookie value", async ({ page, context, browser }) => {
  await signIn(page, newUser("out"));
  const value = (await context.cookies(APP)).find((c) => c.name === "__Host-sa_session")?.value ?? "";
  expect(value).not.toBe("");
  const r = await api(context, "POST", "/api/auth/logout");
  expect(r.status).toBe(204);
  expect((await context.cookies(APP)).find((c) => c.name === "__Host-sa_session")).toBeUndefined();
  expect((await api(context, "GET", "/api/me")).status).toBe(401);

  // replaying the old value in a fresh browser does not bring it back
  const other = await browser.newContext();
  try {
    await other.addCookies([
      {
        name: "__Host-sa_session",
        value,
        // a url, not a domain: a __Host- cookie must be host-only
        url: `${APP}/`,
        secure: true,
        httpOnly: true,
        sameSite: "Lax",
      },
    ]);
    const me = await api(other, "GET", "/api/me");
    expect(me.status).toBe(401);
    expect(me.body).toMatchObject({ error: { code: "unauthenticated" } });
  } finally {
    await other.close();
  }
  // idempotent without a session
  expect((await api(context, "POST", "/api/auth/logout")).status).toBe(204);
});

test("callback refuses a foreign state and a cancelled consent", async ({ page }) => {
  await page.goto(`${APP}/api/auth/github/callback?code=forged&state=forged`);
  await page.waitForURL(`${APP}/sign-in?error=state_mismatch`);
  await expect(page.getByRole("alert")).toBeVisible();

  await page.goto(`${APP}/sign-in`);
  await page.getByRole("link", { name: "Continue with GitHub" }).click();
  await page.waitForURL(`${OAUTH}/login/oauth/authorize?**`);
  await page.click("#fixture-cancel");
  await page.waitForURL(`${APP}/sign-in?error=access_denied`);
  await expect(page.getByRole("alert")).toBeVisible();
  expect((await page.context().cookies(APP)).find((c) => c.name === "__Host-sa_session")).toBeUndefined();
});

test("a code replayed at the callback does not sign in a second browser", async ({
  page,
  browser,
}) => {
  // capture the callback url of a real sign-in, then replay it in another
  // browser: that one has no __Host-sa_oauth cookie, and the code is single use
  let callback = "";
  page.on("request", (r) => {
    if (r.url().startsWith(`${APP}/api/auth/github/callback?`)) callback = r.url();
  });
  await signIn(page, newUser("replay"));
  expect(callback).toContain("code=");
  const other = await browser.newContext();
  try {
    const p = await other.newPage();
    await p.goto(callback);
    await p.waitForURL(`${APP}/sign-in?error=state_mismatch`);
    expect((await other.cookies(APP)).find((c) => c.name === "__Host-sa_session")).toBeUndefined();
  } finally {
    await other.close();
  }
});

test("api without a session answers 401", async ({ context }) => {
  const me = await api(context, "GET", "/api/me");
  expect(me.status).toBe(401);
  expect(me.body).toMatchObject({ error: { code: "unauthenticated" } });
  expect((await api(context, "GET", "/api/tenants")).status).toBe(401);
});
