// the console's AI / MCP page and the oauth consent flow of the mcp server
// (contract 12): a member creates an access key, an ai client uses it on
// /mcp, the member revokes it; a connector registers, the person signs in
// through github on the consent page, approves one project and the client
// exchanges the code with pkce. every step goes through the real app
// caddy, console and control plane.
import { createHash, randomBytes } from "node:crypto";
import type { BrowserContext, Page } from "@playwright/test";
import { APP } from "../fixture/env.ts";
import { api, enrollApi, newUser, signIn } from "./helpers.ts";
import { expect, test } from "./test.ts";

const REDIRECT = "http://127.0.0.1:65531/callback";

// one json-rpc request to /mcp as a native client sends it: no Origin, no
// cookie, a bearer credential
async function mcp(ctx: BrowserContext, token: string, method: string, params: unknown = {}) {
  const r = await ctx.request.fetch(`${APP}/mcp`, {
    method: "POST",
    headers: {
      authorization: `Bearer ${token}`,
      "content-type": "application/json",
      accept: "application/json, text/event-stream",
      "mcp-protocol-version": "2025-11-25",
    },
    data: { jsonrpc: "2.0", id: 1, method, params },
    maxRedirects: 0,
  });
  const text = await r.text();
  return { status: r.status(), text, headers: r.headers() };
}

async function projectPage(page: Page, tenantId: string) {
  await page.goto(`${APP}/projects/${tenantId}/ai`);
  await expect(page.getByRole("heading", { name: "AI assistants", level: 1 })).toBeVisible();
}

test("a member makes an access key on the AI page, uses it and revokes it", async ({
  page,
  context,
}) => {
  const user = newUser("mcp-key");
  await signIn(page, user);
  // the project slug is unique across the console, so the name carries the
  // attempt's user id: a playwright retry would otherwise get slug_taken
  const tenant = await enrollApi(context, `MCP E2E Key ${user.id}`);
  await projectPage(page, tenant.id);
  await expect(page.getByRole("link", { name: "AI assistants" }).first()).toBeVisible();
  await expect(page.getByText(`${APP}/mcp`).first()).toBeVisible();

  // a key belongs to a client that takes one: Claude Code's first step is
  // the key form; field rules before anything is sent
  await page.getByRole("radio", { name: /^Claude Code/ }).click();
  const form = page.getByRole("form", { name: "Create key" });
  await form.getByRole("button", { name: "Create key" }).click();
  await expect(form.getByText("Enter a name.")).toBeVisible();

  await form.getByLabel("Name", { exact: true }).fill("e2e laptop");
  await form.getByRole("button", { name: "Create key" }).click();
  // the one-time secret replaces the form in the step, masked until it is
  // revealed
  const shown = page.getByRole("region", { name: "New secret" });
  await expect(shown).toBeVisible();
  await shown.getByRole("button", { name: "Show the secret" }).click();
  // the masked text crossfades out before the key fades in
  const secret = shown.locator("code").first();
  await expect(secret).toHaveText(/^sa_mcp_[0-9A-Za-z]{40}$/);
  const key = (await secret.textContent())?.trim() ?? "";
  expect(key).toMatch(/^sa_mcp_[0-9A-Za-z]{40}$/);
  // the commands are written for the key: on screen its start, the copy
  // holds it whole
  await expect(
    page.locator("pre").filter({ hasText: "claude mcp add --transport http veristar" }).first(),
  ).toContainText(key.slice(0, 11));
  await page.getByRole("radio", { name: /^Cursor/ }).click();
  await expect(page.locator("pre").filter({ hasText: "mcpServers" })).toContainText(
    key.slice(0, 11),
  );
  // done stays disabled until the person says the key is stored
  await expect(shown.getByRole("button", { name: "Done" })).toBeDisabled();
  await shown.getByRole("checkbox").check();
  await shown.getByRole("button", { name: "Done" }).click();
  await expect(shown).toHaveCount(0);

  // the list shows the prefix, never the key
  const row = page
    .getByRole("table", { name: "MCP keys and connections" })
    .getByRole("row")
    .filter({ hasText: "e2e laptop" });
  await expect(row).toContainText("Never");
  await expect(row).toContainText(key.slice(0, 11));
  await expect(page.locator("body")).not.toContainText(key);

  // an ai client lists the tools and reads its own project
  const tools = await mcp(context, key, "tools/list");
  expect(tools.status, tools.text).toBe(200);
  expect(tools.text).toContain("get_web_stats");
  expect(tools.text).not.toContain("list_tenants");
  const projects = await mcp(context, key, "tools/call", { name: "list_projects", arguments: {} });
  expect(projects.text).toContain(tenant.id);
  await page.reload();
  await expect(row).not.toContainText("Never");

  // revoke, from the row menu: the next request is refused
  await row.getByRole("button", { name: /^Actions for / }).click();
  await page.getByRole("menuitem", { name: "Revoke" }).click();
  await page.getByRole("dialog").getByRole("button", { name: "Revoke" }).click();
  await expect(row).toContainText("Revoked");
  await expect(row).toContainText("revoked in the console");
  const after = await mcp(context, key, "tools/list");
  expect(after.status).toBe(401);
  expect(after.headers["www-authenticate"]).toContain("resource_metadata=");
});

test("a connector is approved on the consent page after a github sign-in", async ({ browser }) => {
  const user = newUser("mcp-consent");
  // set up the project in one context, then start the flow signed out
  const setup = await browser.newContext({ ignoreHTTPSErrors: true });
  const setupPage = await setup.newPage();
  await signIn(setupPage, user);
  // the user id keeps the slug unique per attempt, as in the test above
  const tenant = await enrollApi(setup, `MCP E2E Consent ${user.id}`);
  await setup.close();

  const ctx = await browser.newContext({ ignoreHTTPSErrors: true });
  const page = await ctx.newPage();

  // discovery: the 401 names the protected resource document, which names
  // this console as the authorization server
  const unauth = await mcp(ctx, "", "tools/list");
  expect(unauth.status).toBe(401);
  const prm = await (
    await ctx.request.get(`${APP}/.well-known/oauth-protected-resource/mcp`)
  ).json();
  expect(prm.resource).toBe(`${APP}/mcp`);
  const asm = await (await ctx.request.get(`${APP}/.well-known/oauth-authorization-server`)).json();
  expect(asm.code_challenge_methods_supported).toEqual(["S256"]);

  // dynamic client registration
  const reg = await ctx.request.post(asm.registration_endpoint, {
    headers: { "content-type": "application/json" },
    data: {
      client_name: "E2E connector",
      redirect_uris: [REDIRECT],
      token_endpoint_auth_method: "none",
    },
  });
  expect(reg.status()).toBe(201);
  const clientId = (await reg.json()).client_id as string;

  const verifier = randomBytes(32).toString("base64url");
  const challenge = createHash("sha256").update(verifier).digest("base64url");
  const state = randomBytes(12).toString("base64url");
  const authorize = new URL(asm.authorization_endpoint);
  for (const [k, v] of Object.entries({
    response_type: "code",
    client_id: clientId,
    redirect_uri: REDIRECT,
    code_challenge: challenge,
    code_challenge_method: "S256",
    state,
    scope: "analytics:read",
    resource: `${APP}/mcp`,
  }))
    authorize.searchParams.set(k, v);

  // the browser comes back to the client's redirect uri; nothing listens
  // there, so the page answers it and keeps the url
  let callback: URL | null = null;
  await page.route(`${REDIRECT}**`, async (route) => {
    callback = new URL(route.request().url());
    await route.fulfill({ status: 200, contentType: "text/plain", body: "connected" });
  });

  // signed out: the consent page sends the person through github sign-in
  // and comes back to itself with the request kept
  await page.goto(authorize.toString());
  await page.waitForURL(`${APP}/sign-in?**`);
  await page.getByRole("link", { name: "Continue with GitHub" }).click();
  await page.fill("#fixture-user-id", String(user.id));
  await page.fill("#fixture-login", user.login);
  await page.click("#fixture-authorize");
  await page.waitForURL(`${APP}/ai/consent`);
  await expect(
    page.getByRole("heading", { name: "Let E2E connector read one project" }),
  ).toBeVisible();
  await expect(page.getByText(user.login)).toBeVisible();
  await expect(page.getByText("127.0.0.1:65531")).toBeVisible();
  const choice = page.getByRole("radio", { name: new RegExp(`MCP E2E Consent ${user.id}`) });
  await expect(choice).toBeChecked();
  await page.getByRole("button", { name: "Approve" }).click();
  await expect.poll(() => callback?.toString() ?? "").toContain("code=");
  const back = callback as unknown as URL;
  expect(back.searchParams.get("state")).toBe(state);
  expect(back.searchParams.get("iss")).toBe(APP);

  // the token request with the pkce verifier; a wrong verifier is refused
  const code = back.searchParams.get("code") ?? "";
  const wrong = await ctx.request.post(asm.token_endpoint, {
    form: {
      grant_type: "authorization_code",
      client_id: clientId,
      code,
      redirect_uri: REDIRECT,
      code_verifier: randomBytes(32).toString("base64url"),
    },
  });
  expect(wrong.status()).toBe(400);
  expect((await wrong.json()).error).toBe("invalid_grant");
  const tok = await ctx.request.post(asm.token_endpoint, {
    form: {
      grant_type: "authorization_code",
      client_id: clientId,
      code,
      redirect_uri: REDIRECT,
      code_verifier: verifier,
      resource: `${APP}/mcp`,
    },
  });
  expect(tok.status(), await tok.text()).toBe(200);
  const tokens = await tok.json();
  const projects = await mcp(ctx, tokens.access_token, "tools/call", {
    name: "list_projects",
    arguments: {},
  });
  expect(projects.status, projects.text).toBe(200);
  expect(projects.text).toContain(tenant.id);

  // the grant is listed on the project's page and can be revoked there
  await page.goto(`${APP}/projects/${tenant.id}/ai`);
  const row = page
    .getByRole("table", { name: "MCP keys and connections" })
    .getByRole("row")
    .filter({ hasText: "E2E connector" });
  await expect(row).toContainText("Active");
  await row.getByRole("button", { name: /^Actions for / }).click();
  await page.getByRole("menuitem", { name: "Revoke" }).click();
  await page.getByRole("dialog").getByRole("button", { name: "Revoke" }).click();
  await expect(row).toContainText("Revoked");
  expect((await mcp(ctx, tokens.access_token, "tools/list")).status).toBe(401);
  const refreshed = await ctx.request.post(asm.token_endpoint, {
    form: { grant_type: "refresh_token", client_id: clientId, refresh_token: tokens.refresh_token },
  });
  expect(refreshed.status()).toBe(400);

  // denying sends the client access_denied
  const r = await api(ctx, "GET", "/api/me");
  expect(r.status).toBe(200);
  callback = null;
  await page.goto(authorize.toString());
  await page.waitForURL(`${APP}/ai/consent`);
  await page.getByRole("button", { name: "Deny" }).click();
  await expect.poll(() => callback?.toString() ?? "").toContain("error=access_denied");
  await ctx.close();
});
