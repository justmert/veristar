// the operator roster and the passkey step-up (contract 5.15 and 5.16), end
// to end in chromium with the devtools virtual authenticator: an operator
// admin from the deploy settings confirms the area with a security key, adds
// a second operator by github login, who registers a first passkey inside
// the enrollment window and opens the area; the admin then suspends that
// operator, whose next request is refused, and the deploy-settings row
// refuses a suspension. a tenant gets 403 on every roster route, and an
// operator session without a step-up gets 401 step_up_required, on the
// operator routes and on a change to its sign-in methods.
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
  // nor can the session alone change how the operator signs in: a passkey
  // linked from it would sign in on its own
  const link = await api<{ error: { code: string } }>(
    ctx,
    "POST",
    "/api/me/identities/passkey/challenge",
    {},
  );
  expect(link.status).toBe(401);
  expect(link.body.error.code).toBe("step_up_required");
  await page.goto(`${APP}/account`);
  await expect(
    page
      .getByText("Confirm with your passkey to change sign-in methods")
      .or(page.getByText("Add a passkey for the operator area first")),
  ).toBeVisible();
  await ctx.close();
});

test("the admin confirms with a security key and sees the roster", async () => {
  await operatorStepUp(admin.page, OPERATOR);
  await admin.page.goto(`${APP}/operator/operators`);
  await expect(admin.page.getByRole("heading", { name: "Operators", level: 1 })).toBeVisible();
  const row = admin.page.locator("tr", { hasText: "sa-operator" });
  await expect(row).toContainText("Operator admin");
  await expect(row).toContainText("Deploy settings");
  // the deploy settings row only offers a passkey reset, in the row menu
  await row.getByRole("button", { name: "Actions for sa-operator" }).click();
  await expect(admin.page.getByRole("menuitem")).toHaveText(["Reset passkeys"]);
  await admin.page.keyboard.press("Escape");
  // a person's own passkeys are on the account page, not here
  await expect(admin.page.locator("[data-passkey]")).toHaveCount(0);
  await admin.page.goto(`${APP}/account`);
  // the passkey the step-up used is listed under the admin's own passkeys
  await expect(admin.page.locator("[data-passkey]")).toHaveCount(1);
});

test("the admin adds an operator who registers a first passkey", async ({ browser }) => {
  secondUser = newUser("sa-op2");
  await registerGitHub(secondUser);
  const p = admin.page;
  await p.goto(`${APP}/operator/operators`);
  // the header button opens the form in a dialog
  await p.getByRole("button", { name: "Add operator" }).click();
  // the closed action dialog has a reason field too, so the form is scoped
  const add = p.getByRole("dialog", { name: "Add an operator" });
  await add.getByLabel("GitHub login").fill(secondUser.login);
  await add.getByLabel("Role").selectOption("operator");
  await add.getByLabel("Reason").fill("joins the review team");
  await add.getByRole("button", { name: "Add operator" }).click();
  const row = p.locator("tr", { hasText: secondUser.login });
  // the role is an inline select of the row, for an admin
  await expect(row.getByLabel(`Role of ${secondUser.login}`)).toHaveValue("operator");
  // no passkey yet: the window to register one, its exact end on hover
  await expect(row).toContainText("No passkey yet");
  await expect(row.locator('[title^="First passkey before "]')).toBeVisible();
  await expect(p.getByText(`${secondUser.login} is added.`)).toBeVisible();

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
  await expect(second.page.getByRole("heading", { name: "Review", level: 1 })).toBeVisible();
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
  // the role changes in its inline select, which offers the admin role
  await expect(
    row.getByLabel(`Role of ${secondUser.login}`).locator("option", { hasText: "Operator admin" }),
  ).toHaveCount(1);
  // one menu per row; the destructive actions come last
  await row.getByRole("button", { name: `Actions for ${secondUser.login}` }).click();
  await expect(p.getByRole("menuitem")).toHaveText([
    "Reset passkeys",
    "Suspend",
    "Remove from the roster",
  ]);
  await p.getByRole("menuitem", { name: "Suspend" }).click();
  const dialog = p.getByRole("dialog");
  await dialog.getByLabel("Reason").fill("left the review team");
  await dialog.getByRole("button", { name: "Suspend" }).click();
  await expect(p.getByText(`${secondUser.login} is suspended.`)).toBeVisible();
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
