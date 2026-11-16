import { existsSync, readFileSync } from "node:fs";
import { extname, join, resolve } from "node:path";
import { expect, type Page, test } from "@playwright/test";

// does passkey sign-in (contract 4.1.9, @soropass/core) work under the
// console's security headers? the console's production build is served with
// the exact content-security-policy, cross-origin-opener-policy and
// permissions-policy of deploy/caddy/app.Caddyfile, and every violation the
// page reports is collected. the passkey ceremonies run in the page through
// navigator.credentials, so the policy needs no popup, frame or foreign
// origin for them.
//
// chromium drives the whole sign-in against a virtual authenticator (the
// devtools webauthn domain): create a passkey, confirm it, and the control
// plane stub receives a verifiable answer. firefox and webkit have no
// virtual authenticator in playwright; there the passkey dialog loads,
// fetches its challenge and renders under the policy. playwright's linux
// webkit build has no webauthn api at all, so there the dialog says that
// the browser does not support passkeys, also without a violation.

const root = resolve(import.meta.dirname, "../../..");
const origin = "https://app.csp.test";
const rpId = "app.csp.test";
const dist = join(root, "apps/console/dist");

function caddyHeader(name: string): string {
  const caddy = readFileSync(join(root, "deploy/caddy/app.Caddyfile"), "utf8");
  const m = caddy.match(new RegExp(`${name} "([^"]+)"`));
  if (!m?.[1]) throw new Error(`no ${name} in app.Caddyfile`);
  return m[1];
}

function securityHeaders(): Record<string, string> {
  return {
    "content-security-policy": caddyHeader("Content-Security-Policy"),
    "permissions-policy": caddyHeader("Permissions-Policy"),
    "cross-origin-opener-policy": "same-origin",
  };
}

const TYPES: Record<string, string> = {
  ".html": "text/html; charset=utf-8",
  ".js": "text/javascript; charset=utf-8",
  ".css": "text/css; charset=utf-8",
  ".svg": "image/svg+xml",
  ".woff2": "font/woff2",
};

type Violation = { directive: string; blocked: string };

async function watchViolations(page: Page): Promise<Violation[]> {
  const out: Violation[] = [];
  await page.exposeFunction("__cspViolation", (v: Violation) => out.push(v));
  await page.addInitScript(() => {
    document.addEventListener("securitypolicyviolation", (e) => {
      (window as unknown as { __cspViolation: (v: unknown) => void }).__cspViolation({
        directive: e.effectiveDirective,
        blocked: e.blockedURI,
      });
    });
  });
  return out;
}

const CHALLENGE = Buffer.alloc(32, 9).toString("base64url");

// the production build as the app caddy serves it, with the passkey routes
// of the control plane stubbed; verify answers are collected
async function serveConsole(page: Page): Promise<Record<string, string>[]> {
  const answers: Record<string, string>[] = [];
  const headers = securityHeaders();
  await page.route(`${origin}/**`, (route) => {
    const req = route.request();
    const path = new URL(req.url()).pathname;
    const json = (status: number, body: unknown) =>
      route.fulfill({
        status,
        body: JSON.stringify(body),
        headers: { "content-type": "application/json; charset=utf-8", ...headers },
      });
    if (path === "/api/auth/passkey/challenge") {
      return json(200, {
        challenge: CHALLENGE,
        rp_id: rpId,
        timeout_ms: 120000,
        expires_at: new Date(Date.now() + 300_000).toISOString(),
      });
    }
    if (path === "/api/auth/passkey/verify") {
      answers.push(req.postDataJSON() as Record<string, string>);
      return json(200, { return_to: "/signed-in" });
    }
    if (path.startsWith("/api/")) {
      return json(401, { error: { code: "unauthenticated", message: "sign in", request_id: "r" } });
    }
    const file = join(dist, path);
    const served = extname(path) !== "" && existsSync(file) ? file : join(dist, "index.html");
    return route.fulfill({
      body: readFileSync(served),
      headers: { "content-type": TYPES[extname(served)] ?? "application/octet-stream", ...headers },
    });
  });
  return answers;
}

test.beforeEach(() => {
  test.skip(
    !existsSync(join(dist, "index.html")),
    "build the console first (pnpm --filter @veristar/console build)",
  );
});

test("passkey sign-in runs under the console's security headers", async ({ page, browserName }) => {
  const violations = await watchViolations(page);
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  const answers = await serveConsole(page);

  let authenticator: string | undefined;
  const cdp = browserName === "chromium" ? await page.context().newCDPSession(page) : null;
  if (cdp) {
    await cdp.send("WebAuthn.enable");
    const added = await cdp.send("WebAuthn.addVirtualAuthenticator", {
      options: {
        protocol: "ctap2",
        transport: "internal",
        hasResidentKey: true,
        hasUserVerification: true,
        isUserVerified: true,
        automaticPresenceSimulation: true,
      },
    });
    authenticator = added.authenticatorId;
  }

  await page.goto(`${origin}/sign-in`);
  await page.getByRole("button", { name: "Continue with a passkey" }).click();
  const dialog = page.getByRole("dialog", { name: "Continue with a passkey" });
  await expect(dialog).toBeVisible();
  const webauthn = await page.evaluate(() => typeof window.PublicKeyCredential === "function");
  if (browserName === "chromium") expect(webauthn).toBe(true);
  if (webauthn) {
    // the challenge arrived: the buttons enable
    await expect(dialog.getByRole("button", { name: "Create a new passkey" })).toBeEnabled();
    await expect(dialog.getByRole("button", { name: "Sign in with your passkey" })).toBeEnabled();
  } else {
    await expect(dialog.getByText("This browser does not support passkeys")).toBeVisible();
  }

  if (cdp && authenticator) {
    await dialog.getByRole("button", { name: "Create a new passkey" }).click();
    await expect(dialog.getByText("Your passkey is created")).toBeVisible();
    await dialog.getByRole("button", { name: "Confirm with your passkey" }).click();
    // the console opens the return path; with no session behind the stub
    // it sends the browser back to sign-in for that path
    await expect(page).toHaveURL(/signed-in/);

    const creds = await cdp.send("WebAuthn.getCredentials", { authenticatorId: authenticator });
    expect(creds.credentials).toHaveLength(1);
    const cred = creds.credentials[0];
    expect(cred?.rpId).toBe(rpId);
    expect(cred?.isResidentCredential).toBe(true);
    expect(answers).toHaveLength(1);
    const a = answers[0] as Record<string, string>;
    expect(a.credential_id).toBe(
      Buffer.from(cred?.credentialId ?? "", "base64").toString("base64url"),
    );
    expect(Buffer.from(a.public_key ?? "", "base64url")).toHaveLength(65);
    const cd = JSON.parse(Buffer.from(a.client_data_json ?? "", "base64url").toString("utf8")) as {
      type: string;
      challenge: string;
      origin: string;
    };
    expect(cd).toMatchObject({ type: "webauthn.get", challenge: CHALLENGE, origin });
    // user present and user verified
    expect(Buffer.from(a.authenticator_data ?? "", "base64url")[32] & 0x05).toBe(0x05);
  }

  expect(errors).toEqual([]);
  expect(violations).toEqual([]);
});
