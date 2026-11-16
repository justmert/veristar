// the operator roster and the passkey step-up (contract 5.15 and 5.16), end
// to end in chromium with the devtools virtual authenticator: an operator
// admin from the deploy settings confirms the area with a security key, adds
// a second operator by github login, who registers a first passkey inside
// the enrollment window and opens the area; the admin then suspends that
// operator, whose next request is refused, and the deploy-settings row
// refuses a suspension. a tenant gets 403 on every roster route, and an
// operator session without a step-up gets 401 step_up_required.
import type { BrowserContext, Page } from "@playwright/test";
import { APP } from "../fixture/env.ts";
import {
  api,
  type GitHubUser,
  newUser,
  operatorStepUp,
  registerGitHub,
  signIn,
  virtualAuthenticator,
} from "./helpers.ts";
import { expect, test } from "./test.ts";

test.describe.configure({ mode: "serial" });

const OPERATOR: GitHubUser = { id: 1, login: "sa-operator" };

let admin: { ctx: BrowserContext; page: Page };
let second: { ctx: BrowserContext; page: Page };
let secondUser: GitHubUser;

test.beforeAll(async ({ browser }) => {
  const ctx = await browser.newContext();
  admin = { ctx, page: await ctx.newPage() };
  await signIn(admin.page, OPERATOR);
});

test.afterAll(async () => {
  await admin?.ctx.close();
  await second?.ctx.close();
});

test("an operator session without a step-up is refused with step_up_required", async ({
  browser,
}) => {
  const ctx = await browser.newContext();
  const page = await ctx.newPage();
  await signIn(page, OPERATOR);
  for (const path of ["/api/operator/roster", "/api/operator/tenants", "/api/operator/audit"]) {
    const r = await api<{ error: { code: string } }>(ctx, "GET", path);
    expect(r.status, path).toBe(401);
    expect(r.body.error.code).toBe("step_up_required");
  }
  // the session itself stays: the console's own routes still answer
  expect((await api(ctx, "GET", "/api/me")).status).toBe(200);
  // and the area shows the passkey prompt, not the sign-in page
  await page.goto(`${APP}/operator`);
  await expect(
    page
      .getByRole("button", { name: "Use your passkey" })
      .or(page.getByRole("button", { name: "Register" })),
  ).toBeVisible();
  expect(new URL(page.url()).pathname).toBe("/operator");
  await ctx.close();
});

test("the admin confirms with a security key and sees the roster", async () => {
  await operatorStepUp(admin.page, OPERATOR);
  await admin.page.goto(`${APP}/operator/operators`);
  await expect(admin.page.getByRole("heading", { name: "Operators", level: 2 })).toBeVisible();
  const row = admin.page.locator("tr", { hasText: "sa-operator" });
  await expect(row).toContainText("Operator admin");
  await expect(row).toContainText("Deploy settings");
  // the passkey the step-up used is listed under the admin's own passkeys
  await expect(admin.page.locator("[data-passkey]")).toHaveCount(1);
});

test("the admin adds an operator who registers a first passkey", async ({ browser }) => {
  secondUser = newUser("sa-op2");
  await registerGitHub(secondUser);
  const p = admin.page;
  await p.getByLabel("GitHub login").fill(secondUser.login);
  await p.getByLabel("Role").selectOption("operator");
  await p.getByLabel("Reason").fill("joins the review team");
  await p.getByRole("button", { name: "Add operator" }).click();
  const row = p.locator("tr", { hasText: secondUser.login });
  await expect(row).toContainText("Operator");
  await expect(row).toContainText("First passkey before");

  const ctx = await browser.newContext();
  second = { ctx, page: await ctx.newPage() };
  await signIn(second.page, secondUser);
  const auth = await virtualAuthenticator(second.page, secondUser);
  await second.page.goto(`${APP}/operator`);
  await expect(second.page.getByText("Register a passkey", { exact: true })).toBeVisible();
  await second.page.getByLabel("Name").fill("review key");
  await second.page.getByRole("button", { name: "Register" }).click();
  const dialog = second.page.getByTestId("passkey-dialog");
  await dialog.getByRole("button", { name: "Create a passkey" }).click();
  await dialog.getByRole("button", { name: "Confirm with your passkey" }).click();
  await expect(second.page.getByRole("navigation", { name: "Operator sections" })).toBeVisible();
  await auth.save();
  // a plain operator reads the roster but cannot change it
  const r = await api<{ error: { code: string } }>(second.ctx, "POST", "/api/operator/roster", {
    github_login: "someone",
    role: "operator",
    reason: "not allowed",
  });
  expect(r.status).toBe(403);
  expect(r.body.error.code).toBe("operator_admin_required");
});

test("a suspension ends the operator's access at once", async () => {
  const p = admin.page;
  await p.reload();
  const row = p.locator("tr", { hasText: secondUser.login });
  await row.getByRole("button", { name: "Suspend" }).click();
  await p.getByLabel("Reason").fill("left the review team");
  await p.getByRole("dialog").getByRole("button", { name: "Suspend" }).click();
  await expect(row).toContainText("Suspended");
  await expect(row).toContainText("left the review team");
  // the suspended operator's session ended with the suspension
  const r = await api(second.ctx, "GET", "/api/operator/roster");
  expect(r.status).toBe(401);
});

test("the deploy-settings row cannot be suspended in the console", async () => {
  const roster = await api<{ items: { id: string; github_id: number | null }[] }>(
    admin.ctx,
    "GET",
    "/api/operator/roster",
  );
  expect(roster.status).toBe(200);
  const own = roster.body.items.find((e) => e.github_id === OPERATOR.id);
  expect(own).toBeDefined();
  const r = await api<{ error: { code: string } }>(
    admin.ctx,
    "POST",
    `/api/operator/roster/${own?.id}/suspend`,
    { reason: "should be refused" },
  );
  expect(r.status).toBe(409);
  expect(r.body.error.code).toBe("bootstrap_operator");
});

test("a tenant gets 403 on every roster and passkey route", async ({ browser }) => {
  const ctx = await browser.newContext();
  const page = await ctx.newPage();
  await signIn(page, newUser("sa-roster-tenant"));
  for (const [method, path, body] of [
    ["GET", "/api/operator/roster", undefined],
    ["POST", "/api/operator/roster", { github_login: "x", role: "operator", reason: "x" }],
    ["GET", "/api/operator/step-up", undefined],
    ["POST", "/api/operator/passkeys/challenge", {}],
  ] as const) {
    const r = await api<{ error: { code: string } }>(ctx, method, path, body);
    expect(r.status, `${method} ${path}`).toBe(403);
    expect(r.body.error.code).toBe("operator_required");
  }
  await ctx.close();
});
