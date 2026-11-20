// otlp tokens, console part: a tenant opens "API analytics", creates an
// otlp token through the real control plane, sees the plaintext once and
// copies it, finds it listed by prefix only, and revokes it after a
// confirmation. the control plane's answers are checked through the same
// api, and the page must run without a csp violation or script error
// (deploy/caddy/app.Caddyfile allows no inline styles and no eval).
import { mkdirSync } from "node:fs";
import { join } from "node:path";
import type { BrowserContext, Page } from "@playwright/test";
import { APP, BASE_DOMAIN, OUT } from "../fixture/env.ts";
import { api, enrollApi, newUser, signIn, type Tenant } from "./helpers.ts";
import { expect, test } from "./test.ts";

test.describe.configure({ mode: "serial" });

const shots = join(OUT, "otlp-tokens");

interface Token {
  id: string;
  name: string;
  environment: string;
  prefix: string;
  status: string;
  revoked_at: string | null;
}

let ctx: BrowserContext;
let page: Page;
let tenant: Tenant;
let plain = "";
let tokenId = "";
const problems: string[] = [];

test.beforeAll(async ({ browser }) => {
  mkdirSync(shots, { recursive: true });
  ctx = await browser.newContext();
  // the copy button writes to the clipboard; the test reads it back
  await ctx.grantPermissions(["clipboard-read", "clipboard-write"], { origin: APP });
  page = await ctx.newPage();
  page.on("pageerror", (e) => problems.push(`pageerror: ${e.message}`));
  page.on("console", (m) => {
    if (m.type() === "error") problems.push(`console: ${m.text()}`);
  });
  await signIn(page, newUser("otlp"));
  tenant = await enrollApi(ctx, `OTLP tokens ${Date.now()}`);
  expect(tenant.status, "the stack enrolls in open mode").toBe("active");
});

test.afterAll(async () => {
  await ctx?.close();
});

test("the api analytics page shows the steps and one short exporter per stack", async () => {
  await page.goto(`${APP}/projects/${tenant.id}`);
  // the setup page is Connect API in the sidebar's Setup group
  await page
    .getByRole("navigation", { name: "Project" })
    .getByRole("link", { name: "Connect API" })
    .click();
  await page.waitForURL(`${APP}/projects/${tenant.id}/connect-api`);
  await expect(page.getByRole("heading", { name: "Connect API", exact: true })).toBeVisible();
  // a new project: the token is the step to do now, the rest waits
  await expect(page.getByRole("heading", { name: "Create a token", level: 3 })).toBeVisible();
  await expect(page.getByText("1 of 3", { exact: true })).toBeVisible();
  await page.screenshot({ path: join(shots, "page.png"), fullPage: true });
});

test("a token is created through the control plane and shown once", async () => {
  await page.getByRole("button", { name: "Create token" }).click();
  const form = page.getByRole("dialog", { name: "Create a token" });
  await form.getByLabel("Token name").fill("Payments API");
  await form.getByLabel("Environment").fill("production");
  const created = page.waitForResponse(
    (r) =>
      r.url() === `${APP}/api/tenants/${tenant.id}/otlp-tokens` && r.request().method() === "POST",
  );
  await form.getByRole("button", { name: "Create token" }).click();
  const res = await created;
  expect(res.status()).toBe(201);
  const body = (await res.json()) as Token & { token: string };
  plain = body.token;
  tokenId = body.id;
  expect(plain).toMatch(/^sa_otlp_[0-9A-Za-z]{40}$/);
  expect(body.prefix).toBe(plain.slice(0, 12));

  // the one-time secret is masked until it is shown on purpose
  // the same dialog now holds the token, titled for it
  const dialog = page.getByRole("dialog", { name: "Copy your new token" });
  await expect(dialog).toContainText("Payments API");
  const panel = dialog.getByRole("region", { name: "New secret" });
  await expect(panel).not.toContainText(plain);
  // the exporter of each stack is written for this token and the endpoint;
  // the code on screen shows only the token's start
  const tabs = panel.getByRole("radiogroup", { name: "Language" }).getByRole("radio");
  await expect(tabs).toHaveText(["Node.js", "Go", "Python", "Rust", "Java"]);
  const node = panel.locator("[data-snippet=node]");
  await expect(node).toContainText(`https://otlp.${BASE_DOMAIN}/v1/metrics`);
  await expect(node).toContainText(`Bearer ${plain.slice(0, 12)}...`);
  await expect(node).not.toContainText("apps/console");
  await panel.getByRole("radio", { name: "Java" }).click();
  await expect(panel.locator("[data-snippet=java]")).toContainText("otlp_http:");
  await panel.getByRole("button", { name: "Show the secret" }).click();
  await expect(panel.locator("code").first()).toHaveText(plain);
  await panel.getByRole("button", { name: "Copy", exact: true }).click();
  await expect(panel.getByRole("button", { name: "Copied" })).toBeVisible();
  expect(await page.evaluate(() => navigator.clipboard.readText())).toBe(plain);
  await page.screenshot({ path: join(shots, "created.png"), fullPage: true });

  // done stays disabled until the person confirms they stored it
  const done = dialog.getByRole("button", { name: "Done" });
  await expect(done).toBeDisabled();
  await panel.getByRole("checkbox").click();
  await done.click();

  // the table names it by prefix only; the exporter is the step now
  const list = page.getByRole("table", { name: "Tokens" });
  await expect(list.getByText("Payments API", { exact: true })).toBeVisible();
  await expect(list.getByText(`${body.prefix}...`)).toBeVisible();
  await expect(list).not.toContainText(plain);
  await expect(page.getByRole("heading", { name: "Add the exporter", level: 3 })).toBeVisible();
  await expect(page.getByText(plain)).toHaveCount(0);
  // a reload shows the token again only as its prefix
  await page.reload();
  await expect(
    page.getByRole("table", { name: "Tokens" }).getByText("Payments API", { exact: true }),
  ).toBeVisible();
  expect(await page.content()).not.toContain(plain);

  const listed = await api<{ items: Token[] }>(ctx, "GET", `/api/tenants/${tenant.id}/otlp-tokens`);
  expect(listed.status).toBe(200);
  expect(listed.body.items.map((t) => [t.id, t.status, t.environment])).toEqual([
    [tokenId, "active", "production"],
  ]);
  expect(JSON.stringify(listed.body)).not.toContain(plain);
});

test("the token is revoked after a confirmation", async () => {
  const row = page
    .getByRole("table", { name: "Tokens" })
    .getByRole("row")
    .filter({ hasText: "Payments API" });
  const menu = row.getByRole("button", { name: "Actions for Payments API" });
  await menu.click();
  await page.getByRole("menuitem", { name: "Revoke" }).click();
  const dialog = page.getByRole("dialog");
  await expect(dialog).toBeVisible();
  await expect(dialog).toContainText("Revoke the token “Payments API”?");
  // cancelling changes nothing
  await dialog.getByRole("button", { name: "Cancel" }).click();
  await expect(dialog).toBeHidden();
  let one = await api<Token>(ctx, "GET", `/api/tenants/${tenant.id}/otlp-tokens/${tokenId}`);
  expect(one.body.status).toBe("active");

  await menu.click();
  await page.getByRole("menuitem", { name: "Revoke" }).click();
  const revoked = page.waitForResponse(
    (r) =>
      r.url() === `${APP}/api/tenants/${tenant.id}/otlp-tokens/${tokenId}` &&
      r.request().method() === "DELETE",
  );
  await dialog.getByRole("button", { name: "Revoke token" }).click();
  expect((await revoked).status()).toBe(204);
  await expect(dialog).toBeHidden();
  await expect(page.getByText("Payments API is revoked.")).toBeVisible();
  await expect(page.locator("[data-tokens=none]")).toHaveText("No active tokens");

  one = await api<Token>(ctx, "GET", `/api/tenants/${tenant.id}/otlp-tokens/${tokenId}`);
  expect(one.status).toBe(200);
  expect(one.body.status).toBe("revoked");
  expect(one.body.revoked_at).not.toBeNull();

  await page.getByLabel("Show revoked").check();
  const shown = page
    .getByRole("table", { name: "Tokens" })
    .getByRole("row")
    .filter({ hasText: "Payments API" });
  await expect(shown.getByText("Revoked", { exact: true })).toBeVisible();
  await expect(shown.getByRole("button", { name: /^Actions for/ })).toHaveCount(0);
  await page.screenshot({ path: join(shots, "revoked.png"), fullPage: true });
});

test("the page ran without a csp violation or script error", () => {
  expect(problems).toEqual([]);
});
