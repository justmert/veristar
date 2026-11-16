// wallet sign-in (contract 4.1.7) and linked sign-in methods (4.1.8)
// through the real console behind the app caddy and its csp. the console is
// built with VITE_STELLAR_TEST_SIGNER=1 for this suite (stack_test.go), which
// adds a deterministic test signer to the wallet picker; the production build
// is checked to hold none before that build is made. the flows: sign in with
// the wallet, join a project by address and open its web dashboard,
// approve an mcp connector on the consent page as a wallet user, and link a
// github account that then signs in to the same account.
import { createHash, randomBytes } from "node:crypto";
import type { BrowserContext, Page } from "@playwright/test";
import { APP, OAUTH } from "../fixture/env.ts";
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

const SIGNER = "Test signer";
const REDIRECT = "http://127.0.0.1:65532/callback";

type Me = {
  user: {
    id: string;
    github_id: number | null;
    github_login: string | null;
    stellar_address: string | null;
    display_name: string;
  };
};

let owner: BrowserContext;
let wallet: BrowserContext;
let shared: Tenant;
let site: Site;
let me: Me["user"];
const violations: string[] = [];

// every csp violation any wallet page reports fails the suite at the end
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

async function walletSignIn(page: Page, returnTo = "/") {
  await page.goto(`${APP}/sign-in?return_to=${encodeURIComponent(returnTo)}`);
  await page.getByRole("button", { name: "Continue with a Stellar wallet" }).click();
  const picker = page.getByRole("dialog", { name: "Continue with a Stellar wallet" });
  await picker.getByRole("button", { name: SIGNER }).click();
}

test.beforeAll(async ({ browser }) => {
  owner = await browser.newContext({ ignoreHTTPSErrors: true });
  wallet = await browser.newContext({ ignoreHTTPSErrors: true });
});
test.afterAll(async () => {
  await owner.close();
  await wallet.close();
});

test("a person signs in with a stellar wallet", async () => {
  const page = await wallet.newPage();
  await watchCSP(page);
  await walletSignIn(page, "/projects");
  await page.waitForURL(`${APP}/projects`);
  const r = await api<Me>(wallet, "GET", "/api/me");
  expect(r.status).toBe(200);
  me = r.body.user;
  expect(me.github_id).toBeNull();
  expect(me.stellar_address).toMatch(/^G[A-Z2-7]{55}$/);
  const addr = me.stellar_address ?? "";
  expect(me.display_name).toBe(`${addr.slice(0, 4)}…${addr.slice(-4)}`);
  // the sidebar names the account by its short address
  await expect(page.getByText(me.display_name).first()).toBeVisible();
  await page.close();
});

test("a wallet member joins a project by address and opens its dashboard", async () => {
  const op = await owner.newPage();
  const ownerUser = newUser("wallet-owner");
  await signIn(op, ownerUser);
  // unique per attempt: a retried serial group runs this again
  shared = await enrollApi(owner, `Wallet shared ${ownerUser.login}`);
  site = await addSiteApi(owner, shared.id, uniqueHost("wallet"));
  await waitProvisioned(owner, shared.id);
  await op.close();

  const add = await api<{ kind: string }>(owner, "POST", `/api/tenants/${shared.id}/members`, {
    stellar_address: me.stellar_address,
    role: "member",
  });
  expect(add.status, JSON.stringify(add.body)).toBe(201);
  expect(add.body.kind).toBe("member");

  // the reconciler makes the wallet account's umami user and team membership
  const page = await wallet.newPage();
  await watchCSP(page);
  await waitWebReady(wallet, shared.id, site.id);
  // and the shared project's web dashboard opens in the console
  await page.goto(`${APP}/projects/${shared.id}/web?site=${site.id}`);
  await expect(page.getByRole("heading", { name: "Web analytics" })).toBeVisible();
  await expect(page.getByLabel("Right now")).toBeVisible();

  // the owner's member list shows the short address
  const op2 = await owner.newPage();
  await op2.goto(`${APP}/projects/${shared.id}/members`);
  await expect(op2.getByText(me.display_name, { exact: true })).toBeVisible();
  await op2.close();
  await page.close();
});

test("a wallet user approves an mcp connector on the consent page", async ({ browser }) => {
  const ctx = await browser.newContext({ ignoreHTTPSErrors: true });
  const page = await ctx.newPage();
  await watchCSP(page);
  const asm = await (await ctx.request.get(`${APP}/.well-known/oauth-authorization-server`)).json();
  const reg = await ctx.request.post(asm.registration_endpoint, {
    headers: { "content-type": "application/json" },
    data: {
      client_name: "Wallet connector",
      redirect_uris: [REDIRECT],
      token_endpoint_auth_method: "none",
    },
  });
  expect(reg.status()).toBe(201);
  const clientId = (await reg.json()).client_id as string;
  const verifier = randomBytes(32).toString("base64url");
  const authorize = new URL(asm.authorization_endpoint);
  for (const [k, v] of Object.entries({
    response_type: "code",
    client_id: clientId,
    redirect_uri: REDIRECT,
    code_challenge: createHash("sha256").update(verifier).digest("base64url"),
    code_challenge_method: "S256",
    state: "wallet-state",
    scope: "analytics:read",
    resource: `${APP}/mcp`,
  }))
    authorize.searchParams.set(k, v);
  let callback: URL | null = null;
  await page.route(`${REDIRECT}**`, async (route) => {
    callback = new URL(route.request().url());
    await route.fulfill({ status: 200, contentType: "text/plain", body: "connected" });
  });

  // signed out: the consent page sends the person to sign-in, the wallet
  // signs in, and the consent page comes back with the request kept
  await page.goto(authorize.toString());
  await page.waitForURL(`${APP}/sign-in?**`);
  await page.getByRole("button", { name: "Continue with a Stellar wallet" }).click();
  await page
    .getByRole("dialog", { name: "Continue with a Stellar wallet" })
    .getByRole("button", { name: SIGNER })
    .click();
  await page.waitForURL(`${APP}/ai/consent`);
  await expect(
    page.getByRole("heading", { name: "Connect Wallet connector to Veristar" }),
  ).toBeVisible();
  await expect(page.getByText(me.display_name, { exact: true })).toBeVisible();
  await page.getByRole("radio", { name: new RegExp(shared.name) }).check();
  await page.getByRole("button", { name: "Approve" }).click();
  await expect.poll(() => callback?.toString() ?? "").toContain("code=");
  const tok = await ctx.request.post(asm.token_endpoint, {
    form: {
      grant_type: "authorization_code",
      client_id: clientId,
      code: (callback as unknown as URL).searchParams.get("code") ?? "",
      redirect_uri: REDIRECT,
      code_verifier: verifier,
      resource: `${APP}/mcp`,
    },
  });
  expect(tok.status(), await tok.text()).toBe(200);
  await ctx.close();
});

test("the wallet account links github, which then signs in to the same account", async ({
  browser,
}) => {
  const page = await wallet.newPage();
  await watchCSP(page);
  await page.goto(`${APP}/account`);
  const methods = page.getByRole("list", { name: "Sign-in methods" });
  await expect(methods).toContainText(me.display_name);
  // the only method cannot be unlinked
  await expect(methods.getByRole("button", { name: "Unlink" })).toBeDisabled();
  await methods.getByRole("link", { name: "Link GitHub" }).click();
  await page.waitForURL(`${OAUTH}/login/oauth/authorize?**`);
  const gh = newUser("wallet-linked");
  await page.fill("#fixture-user-id", String(gh.id));
  await page.fill("#fixture-login", gh.login);
  await page.click("#fixture-authorize");
  await page.waitForURL(`${APP}/account?linked=github`);
  await expect(page.getByText("GitHub is linked.")).toBeVisible();
  await expect(methods.getByRole("button", { name: "Unlink" })).toHaveCount(2);

  const other = await browser.newContext({ ignoreHTTPSErrors: true });
  const p2 = await other.newPage();
  await signIn(p2, gh);
  const r = await api<Me>(other, "GET", "/api/me");
  expect(r.body.user.id).toBe(me.id);
  expect(r.body.user.stellar_address).toBe(me.stellar_address);
  await other.close();
  await page.close();
});

test("no wallet page reported a csp violation", () => {
  expect(violations).toEqual([]);
});
