// passkey sign-in (contract 4.1.9) and passkey linking (4.1.8) through the
// real console behind the app caddy and its security headers, with
// chromium's virtual authenticator (devtools webauthn domain) as the
// person's device. the console runs soropass's real ceremonies against it
// and the control plane verifies the answers. the flows: create a passkey
// account, sign in again from another browser that holds the same passkey,
// join a project by the account's C-address and open its web dashboard, and add a passkey to a github account that then signs in with it.
import type { BrowserContext, CDPSession, Page } from "@playwright/test";
import { APP } from "../fixture/env.ts";
import {
  addSiteApi,
  api,
  enrollApi,
  newUser,
  type Site,
  signIn,
  type Tenant,
  uniqueHost,
  waitProvisioned,
  waitWebReady,
} from "./helpers.ts";
import { expect, test } from "./test.ts";

test.describe.configure({ mode: "serial" });
test.skip(
  ({ browserName }) => browserName !== "chromium",
  "virtual authenticators are chromium only",
);

type Me = {
  user: {
    id: string;
    github_id: number | null;
    github_login: string | null;
    stellar_address: string | null;
    passkey_address: string | null;
    display_name: string;
  };
};

type Credential = {
  credentialId: string;
  isResidentCredential: boolean;
  rpId?: string;
  privateKey: string;
  userHandle?: string;
  signCount: number;
};

let device: BrowserContext;
let owner: BrowserContext;
let me: Me["user"];
let saved: Credential;
let shared: Tenant;
let site: Site;
const violations: string[] = [];

async function watchCSP(page: Page) {
  await page.exposeFunction("__cspViolation", (v: string) => violations.push(v));
  await page.addInitScript(() => {
    document.addEventListener("securitypolicyviolation", (e) =>
      (window as unknown as { __cspViolation: (v: string) => void }).__cspViolation(
        `${e.effectiveDirective} ${e.blockedURI}`,
      ),
    );
  });
}

// a platform authenticator with user verification, as a phone or laptop
// with face id, touch id or windows hello has
async function authenticator(page: Page): Promise<{ cdp: CDPSession; id: string }> {
  const cdp = await page.context().newCDPSession(page);
  await cdp.send("WebAuthn.enable");
  const r = await cdp.send("WebAuthn.addVirtualAuthenticator", {
    options: {
      protocol: "ctap2",
      transport: "internal",
      hasResidentKey: true,
      hasUserVerification: true,
      isUserVerified: true,
      automaticPresenceSimulation: true,
    },
  });
  return { cdp, id: r.authenticatorId };
}

function passkeyDialog(page: Page) {
  return page.getByRole("dialog", { name: "Continue with a passkey" });
}

test.beforeAll(async ({ browser }) => {
  device = await browser.newContext({ ignoreHTTPSErrors: true });
  owner = await browser.newContext({ ignoreHTTPSErrors: true });
});
test.afterAll(async () => {
  await device.close();
  await owner.close();
});

test("a person creates a passkey account", async () => {
  const page = await device.newPage();
  await watchCSP(page);
  const auth = await authenticator(page);
  await page.goto(`${APP}/sign-in?return_to=${encodeURIComponent("/projects")}`);
  await page.getByRole("button", { name: "Continue with a passkey" }).click();
  const dialog = passkeyDialog(page);
  await dialog.getByRole("button", { name: "Create a new passkey" }).click();
  await expect(dialog.getByText("Your passkey is created")).toBeVisible();
  await dialog.getByRole("button", { name: "Confirm with your passkey" }).click();
  await page.waitForURL(`${APP}/projects`);

  const r = await api<Me>(device, "GET", "/api/me");
  expect(r.status).toBe(200);
  me = r.body.user;
  expect(me.github_id).toBeNull();
  expect(me.stellar_address).toBeNull();
  expect(me.passkey_address).toMatch(/^C[A-Z2-7]{55}$/);
  const addr = me.passkey_address ?? "";
  expect(me.display_name).toBe(`${addr.slice(0, 4)}…${addr.slice(-4)}`);
  await expect(page.getByText(me.display_name).first()).toBeVisible();

  // the device holds one discoverable credential for the console host
  const creds = (await auth.cdp.send("WebAuthn.getCredentials", { authenticatorId: auth.id }))
    .credentials as Credential[];
  expect(creds).toHaveLength(1);
  saved = creds[0] as Credential;
  expect(saved.isResidentCredential).toBe(true);
  expect(saved.rpId).toBe(new URL(APP).hostname);
  await page.close();
});

test("the passkey signs in again from another browser", async ({ browser }) => {
  // a synced passkey: the same credential on another device
  const ctx = await browser.newContext({ ignoreHTTPSErrors: true });
  const page = await ctx.newPage();
  await watchCSP(page);
  const auth = await authenticator(page);
  await auth.cdp.send("WebAuthn.addCredential", { authenticatorId: auth.id, credential: saved });
  await page.goto(`${APP}/sign-in`);
  await page.getByRole("button", { name: "Continue with a passkey" }).click();
  await passkeyDialog(page).getByRole("button", { name: "Sign in with your passkey" }).click();
  await page.waitForURL(`${APP}/`);
  const r = await api<Me>(ctx, "GET", "/api/me");
  expect(r.body.user.id).toBe(me.id);
  expect(r.body.user.passkey_address).toBe(me.passkey_address);
  await ctx.close();
});

test("a passkey member joins a project by its address and opens its dashboard", async () => {
  const op = await owner.newPage();
  const ownerUser = newUser("passkey-owner");
  await signIn(op, ownerUser);
  shared = await enrollApi(owner, `Passkey shared ${ownerUser.login}`);
  site = await addSiteApi(owner, shared.id, uniqueHost("passkey"));
  await waitProvisioned(owner, shared.id);

  // the owner's member form takes the C-address
  await op.goto(`${APP}/projects/${shared.id}/members`);
  await op.getByRole("button", { name: "Add a person" }).click();
  const add = op.getByRole("dialog", { name: "Add a person" });
  await add.getByLabel("GitHub username or Stellar address").fill(me.passkey_address ?? "");
  await add.getByRole("button", { name: "Add", exact: true }).click();
  await expect(add).toBeHidden();
  await expect(
    op.getByRole("table", { name: "Members" }).getByText(me.display_name, { exact: true }),
  ).toBeVisible();
  await op.close();

  const page = await device.newPage();
  await watchCSP(page);
  // the reconciler makes the passkey account's umami user, then the shared
  // project's web dashboard reads umami as it
  await waitWebReady(device, shared.id, site.id);
  await page.goto(`${APP}/projects/${shared.id}/web?site=${site.id}`);
  await expect(page.getByRole("heading", { name: "Web", exact: true })).toBeVisible();
  await expect(page.getByLabel("Right now")).toBeVisible();
  await page.close();
});

test("a github account adds a passkey, which then signs in to the same account", async ({
  browser,
}) => {
  const ctx = await browser.newContext({ ignoreHTTPSErrors: true });
  const page = await ctx.newPage();
  await watchCSP(page);
  const auth = await authenticator(page);
  const gh = newUser("passkey-linked");
  await signIn(page, gh);
  await page.goto(`${APP}/account`);
  const methods = page.getByRole("list", { name: "Sign-in methods" });
  await methods.getByRole("button", { name: "Add a passkey" }).click();
  const dialog = page.getByRole("dialog", { name: "Add a passkey" });
  await dialog.getByRole("button", { name: "Create a passkey" }).click();
  await expect(dialog.getByText("Your passkey is created")).toBeVisible();
  await dialog.getByRole("button", { name: "Confirm with your passkey" }).click();
  // each linked method has its row menu once another method exists
  await expect(methods.locator("[data-row-menu]")).toHaveCount(2);
  const linked = (await api<Me>(ctx, "GET", "/api/me")).body.user;
  expect(linked.github_login).toBe(gh.login);
  expect(linked.passkey_address).toMatch(/^C[A-Z2-7]{55}$/);

  const cred = (
    (await auth.cdp.send("WebAuthn.getCredentials", { authenticatorId: auth.id }))
      .credentials as Credential[]
  )[0] as Credential;
  const other = await browser.newContext({ ignoreHTTPSErrors: true });
  const p2 = await other.newPage();
  const a2 = await authenticator(p2);
  await a2.cdp.send("WebAuthn.addCredential", { authenticatorId: a2.id, credential: cred });
  await p2.goto(`${APP}/sign-in`);
  await p2.getByRole("button", { name: "Continue with a passkey" }).click();
  await passkeyDialog(p2).getByRole("button", { name: "Sign in with your passkey" }).click();
  await p2.waitForURL(`${APP}/`);
  expect((await api<Me>(other, "GET", "/api/me")).body.user.id).toBe(linked.id);

  // with github linked, the passkey can go; it no longer signs in then
  await methods.getByRole("button", { name: "Actions for Your passkey" }).click();
  await page.getByRole("menuitem", { name: "Unlink" }).click();
  await page
    .getByRole("dialog", { name: "Unlink your passkey?" })
    .getByRole("button", { name: "Unlink" })
    .click();
  await expect(methods.getByRole("button", { name: "Add a passkey" })).toBeVisible();
  await other.close();
  await ctx.close();
});

test("no passkey page reported a csp violation", () => {
  expect(violations).toEqual([]);
});
