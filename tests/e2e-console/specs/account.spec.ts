// console account deletion (contract 4.1.6) through the account page:
// the only owner of a project is refused and sees the project named with a
// link to its members; once another member owns it, the typed confirmation
// deletes the account, signs the browser out, takes the person out of every
// project and ends the umami token they held for a dashboard at once.
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
  umamiGet,
  uniqueHost,
  waitProvisioned,
} from "./helpers.ts";
import { expect, test } from "./test.ts";

test.describe.configure({ mode: "serial" });

let owner: BrowserContext;
let person: BrowserContext;
let user: GitHubUser;
let shared: Tenant;
let site: Site;
let own: Tenant;
let token = "";

test.beforeAll(async ({ browser }) => {
  owner = await browser.newContext();
  person = await browser.newContext();
});
test.afterAll(async () => {
  await owner.close();
  await person.close();
});

test("a member of a project opens its web dashboard", async () => {
  const op = await owner.newPage();
  const ownerUser = newUser("acct-owner");
  await signIn(op, ownerUser);
  shared = await enrollApi(owner, `acct shared ${ownerUser.login}`);
  site = await addSiteApi(owner, shared.id, uniqueHost("acct"));
  await waitProvisioned(owner, shared.id);
  await op.close();

  const page = await person.newPage();
  user = newUser("acct-person");
  await signIn(page, user);
  const add = await api<{ kind: string }>(owner, "POST", `/api/tenants/${shared.id}/members`, {
    github_login: user.login,
    role: "member",
  });
  expect(add.status, JSON.stringify(add.body)).toBe(201);
  expect(add.body.kind).toBe("member");
  own = await enrollApi(person, `acct own ${user.login}`);

  // the reconciler adds the person to the shared project's umami team
  await expect
    .poll(
      async () => {
        const t = captureSsoToken(page);
        await ssoFromConsole(page, site.id);
        if (page.url().startsWith(`${APP}/sso-error`))
          return new URL(page.url()).searchParams.get("code");
        await page.waitForURL(`${WEB}/websites/${site.id}**`);
        token = await t;
        return "ok";
      },
      { timeout: 90_000, intervals: [1000, 2000, 3000] },
    )
    .toBe("ok");
  expect((await umamiGet(person, token, `/api/websites/${site.id}`)).status).toBe(200);
  await page.close();
});

test("the only owner of a project is refused and sees which project", async () => {
  const page = await person.newPage();
  await page.goto(`${APP}/`);
  await page.getByRole("button", { name: /account menu/ }).click();
  await page.getByRole("menuitem", { name: "Account" }).click();
  await page.waitForURL(`${APP}/account`);
  await expect(page.getByRole("heading", { name: "Account", level: 1 })).toBeVisible();
  await expect(page.locator("main").getByText(user.login, { exact: true })).toBeVisible();

  await page.getByRole("button", { name: "Delete account" }).click();
  const dialog = page.getByRole("dialog", { name: "Delete your account?" });
  await expect(dialog).toBeVisible();
  const confirm = dialog.getByRole("button", { name: "Delete account" });
  await expect(confirm).toBeDisabled();
  await dialog.getByLabel(`Type ${user.login} to confirm`).fill("not my login");
  await expect(confirm).toBeDisabled();
  await dialog.getByLabel(`Type ${user.login} to confirm`).fill(user.login);
  await expect(confirm).toBeEnabled();
  await confirm.click();

  const alert = page.getByRole("alert").filter({ hasText: "You are the only owner" });
  await expect(alert).toBeVisible();
  await expect(alert.getByRole("link", { name: own.name })).toHaveAttribute(
    "href",
    `/projects/${own.id}/members`,
  );
  await expect(dialog).toBeHidden();
  expect((await api(person, "GET", "/api/me")).status).toBe(200);
  await page.close();
});

test("with a second owner the account is deleted and signed out everywhere", async () => {
  // the shared project's owner becomes an owner of the person's project too
  const ownerMe = await api<{ user: { github_login: string } }>(owner, "GET", "/api/me");
  const add = await api(person, "POST", `/api/tenants/${own.id}/members`, {
    github_login: ownerMe.body.user.github_login,
    role: "owner",
  });
  expect(add.status, JSON.stringify(add.body)).toBe(201);

  const page = await person.newPage();
  await page.goto(`${APP}/account`);
  await page.getByRole("button", { name: "Delete account" }).click();
  const dialog = page.getByRole("dialog", { name: "Delete your account?" });
  await dialog.getByLabel(`Type ${user.login} to confirm`).fill(user.login);
  await dialog.getByRole("button", { name: "Delete account" }).click();

  await page.waitForURL(`${APP}/sign-in?deleted=1`);
  await expect(
    page.getByRole("status").filter({ hasText: "Your account was deleted" }),
  ).toBeVisible();
  expect((await person.cookies(APP)).find((c) => c.name === "sa_session")).toBeUndefined();
  expect((await api(person, "GET", "/api/me")).status).toBe(401);

  // the umami user is deleted before the answer, so the dashboard token ends now
  expect([401, 403, 404]).toContain(
    (await umamiGet(person, token, `/api/websites/${site.id}`)).status,
  );

  // the owners keep their projects, without the person among the members
  const members = await api<{ members: { github_login: string }[] }>(
    owner,
    "GET",
    `/api/tenants/${shared.id}/members`,
  );
  expect(members.status).toBe(200);
  expect(members.body.members.map((m) => m.github_login)).not.toContain(user.login);
  const kept = await api<{ members: { github_login: string; role: string }[] }>(
    owner,
    "GET",
    `/api/tenants/${own.id}/members`,
  );
  expect(kept.status).toBe(200);
  expect(kept.body.members).toEqual([
    expect.objectContaining({ github_login: ownerMe.body.user.github_login, role: "owner" }),
  ]);
  expect((await api(owner, "GET", `/api/tenants/${shared.id}/sites/${site.id}`)).status).toBe(200);
  await page.close();
});

test("signing in again starts a new, empty account", async () => {
  const page = await person.newPage();
  await signIn(page, user);
  const me = await api<{ user: { github_login: string }; tenants: unknown[] }>(
    person,
    "GET",
    "/api/me",
  );
  expect(me.status).toBe(200);
  expect(me.body.user.github_login).toBe(user.login);
  expect(me.body.tenants).toEqual([]);
  await page.close();
});
