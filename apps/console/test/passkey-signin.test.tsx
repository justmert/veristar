import { decodeChallenge, encodeChallenge, verifyAssertionSignature } from "@soropass/core/sign";
import { mockAuthenticator } from "@soropass/core/testing";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { members as membersSchema, me as meSchema } from "@/api/schemas";
import { AccountPage, confirmationText } from "@/pages/home/account";
import { memberIdentity, validateAddMember } from "@/pages/project/members";
import { SignInPage } from "@/pages/public/sign-in";
import * as f from "./fixtures/contract";
import { byRole, click, renderRoutes, stubFetch, waitFor } from "./helpers";

// a soropass passkey account address (C...), the identity of contract 4.1.9
const C_ACCOUNT = "CBH2J5X4WBV2KT6NSUE47DRFBUHTVJOBJTBGGNELJ2DZXTDB3AYRAXAO";
const RP_ID = "app.veristar.test";

const challenge = {
  challenge: encodeChallenge(new Uint8Array(32).fill(7)),
  rp_id: RP_ID,
  timeout_ms: 120000,
  expires_at: "2026-11-16T10:05:00Z",
};

// the browser's webauthn api, backed by soropass's deterministic test
// authenticator, so lib/passkey runs soropass's real registration and
// assertion code (registerPasskey, browserWebAuthnClient) against it
const authn = mockAuthenticator({ rpId: RP_ID, origin: `https://${RP_ID}` });
const seen = {
  create: [] as CredentialCreationOptions[],
  get: [] as CredentialRequestOptions[],
  refuse: false,
};

function buffer(b: Uint8Array): ArrayBuffer {
  return b.slice().buffer;
}

beforeEach(() => {
  Object.defineProperty(window, "PublicKeyCredential", {
    value: function PublicKeyCredential() {},
    configurable: true,
  });
  Object.defineProperty(navigator, "credentials", {
    configurable: true,
    value: {
      async create(o: CredentialCreationOptions) {
        seen.create.push(o);
        if (seen.refuse) throw new DOMException("dismissed", "NotAllowedError");
        const r = await authn.create(undefined as never);
        return {
          id: r.id,
          rawId: buffer(r.rawId),
          response: { attestationObject: buffer(r.attestationObject) },
        };
      },
      async get(o: CredentialRequestOptions) {
        seen.get.push(o);
        if (seen.refuse) throw new DOMException("dismissed", "NotAllowedError");
        const challengeBytes = new Uint8Array(o.publicKey?.challenge as ArrayBuffer);
        const r = await authn.get({ rpId: RP_ID, challenge: challengeBytes });
        return {
          id: r.id,
          rawId: buffer(r.rawId),
          response: {
            authenticatorData: buffer(r.authenticatorData),
            clientDataJSON: buffer(r.clientDataJSON),
            signature: buffer(r.signature),
          },
        };
      },
    },
  });
});

afterEach(() => {
  seen.create = [];
  seen.get = [];
  seen.refuse = false;
});

function dialog(): HTMLElement {
  const d = document.body.querySelector<HTMLElement>("dialog[open]");
  if (!d) throw new Error("no open dialog");
  return d;
}

type Answer = Record<string, string>;

// the answer the console sent verifies against the passkey's key the way
// the control plane and the soroban account contract check it
function expectValidAnswer(body: Answer) {
  const bytes = (k: string) => decodeChallenge(body[k] as string);
  expect(body.credential_id).toBe(authn.credentialId);
  expect(
    verifyAssertionSignature({
      publicKey: authn.publicKey,
      authenticatorData: bytes("authenticator_data"),
      clientDataJSON: bytes("client_data_json"),
      signature: bytes("signature"),
    }),
  ).toBe(true);
  const cd = JSON.parse(new TextDecoder().decode(bytes("client_data_json"))) as {
    challenge: string;
  };
  expect(cd.challenge).toBe(challenge.challenge);
}

describe("passkey sign-in (contract 4.1.9)", () => {
  it("creates a soropass passkey, confirms it and opens the return path", async () => {
    const calls = stubFetch([
      { status: 200, body: challenge },
      { status: 200, body: { return_to: "/projects" } },
    ]);
    const host = await renderRoutes(
      [
        { path: "/sign-in", element: <SignInPage /> },
        { path: "/projects", element: <p>projects page</p> },
      ],
      "/sign-in?return_to=%2Fprojects",
    );
    await click(byRole(host, "button", "Continue with a passkey"));
    await waitFor(() => expect(calls.length).toBe(1));
    expect(calls[0]?.url).toBe("/api/auth/passkey/challenge");
    expect(calls[0]?.body).toEqual({ return_to: "/projects" });
    await waitFor(() =>
      expect(
        (byRole(dialog(), "button", "Create a new passkey") as HTMLButtonElement).disabled,
      ).toBe(false),
    );

    await click(byRole(dialog(), "button", "Create a new passkey"));
    await waitFor(() => expect(dialog().textContent).toContain("Your passkey is created"));
    // soropass registers es256 only, with user verification, as a
    // discoverable credential for the console host
    const pk = seen.create[0]?.publicKey;
    expect(pk?.rp).toEqual({ id: RP_ID, name: "Veristar" });
    expect(pk?.pubKeyCredParams).toEqual([{ type: "public-key", alg: -7 }]);
    expect(pk?.authenticatorSelection?.userVerification).toBe("required");
    expect(pk?.authenticatorSelection?.residentKey).toBe("required");
    expect(pk?.attestation).toBe("none");
    expect(pk?.user.name).toBe("Veristar account");

    await click(byRole(dialog(), "button", "Confirm with your passkey"));
    await waitFor(() => expect(host.textContent).toContain("projects page"));
    // the assertion is for the created credential and signs the issued
    // challenge with user verification required
    const req = seen.get[0]?.publicKey;
    expect(req?.rpId).toBe(RP_ID);
    expect(req?.userVerification).toBe("required");
    expect(encodeChallenge(new Uint8Array(req?.challenge as ArrayBuffer))).toBe(
      challenge.challenge,
    );
    expect(req?.allowCredentials?.length).toBe(1);

    expect(calls[1]?.url).toBe("/api/auth/passkey/verify");
    expect(calls[1]?.method).toBe("POST");
    const body = calls[1]?.body as Answer;
    expectValidAnswer(body);
    // a new passkey sends its public key; the control plane derives the
    // account address from it and the credential id
    expect(body.public_key).toBe(encodeChallenge(authn.publicKey));
  });

  it("signs in with a passkey the browser holds, without sending a key", async () => {
    const calls = stubFetch([
      { status: 200, body: challenge },
      { status: 200, body: { return_to: "/" } },
    ]);
    const host = await renderRoutes(
      [
        { path: "/sign-in", element: <SignInPage /> },
        { path: "/", element: <p>home page</p> },
      ],
      "/sign-in",
    );
    await click(byRole(host, "button", "Continue with a passkey"));
    await waitFor(() =>
      expect(
        (byRole(dialog(), "button", "Sign in with your passkey") as HTMLButtonElement).disabled,
      ).toBe(false),
    );
    await click(byRole(dialog(), "button", "Sign in with your passkey"));
    await waitFor(() => expect(host.textContent).toContain("home page"));
    expect(seen.create).toEqual([]);
    // discoverable: the browser offers the passkeys it holds for the host
    expect(seen.get[0]?.publicKey?.allowCredentials).toEqual([]);
    const body = calls[1]?.body as Answer;
    expectValidAnswer(body);
    expect(body.public_key).toBeUndefined();
  });

  it("shows a refusal and fetches a fresh challenge for the next try", async () => {
    const calls = stubFetch([
      { status: 200, body: challenge },
      { status: 403, body: f.errorBody("passkey_unknown") },
      { status: 200, body: challenge },
    ]);
    const host = await renderRoutes([{ path: "/sign-in", element: <SignInPage /> }], "/sign-in");
    await click(byRole(host, "button", "Continue with a passkey"));
    await waitFor(() =>
      expect(
        (byRole(dialog(), "button", "Sign in with your passkey") as HTMLButtonElement).disabled,
      ).toBe(false),
    );
    await click(byRole(dialog(), "button", "Sign in with your passkey"));
    await waitFor(() =>
      expect(dialog().textContent).toContain("No account signs in with this passkey"),
    );
    await waitFor(() => expect(calls.length).toBe(3));
    expect(calls[2]?.url).toBe("/api/auth/passkey/challenge");
  });

  it("offers another try when the challenge cannot be fetched", async () => {
    const calls = stubFetch([
      { status: 503, body: f.errorBody("unavailable") },
      { status: 200, body: challenge },
    ]);
    const host = await renderRoutes([{ path: "/sign-in", element: <SignInPage /> }], "/sign-in");
    await click(byRole(host, "button", "Continue with a passkey"));
    await waitFor(() => byRole(dialog(), "button", "Try again"), 10_000);
    expect(
      (byRole(dialog(), "button", "Sign in with your passkey") as HTMLButtonElement).disabled,
    ).toBe(true);
    await click(byRole(dialog(), "button", "Try again"));
    await waitFor(() =>
      expect(
        (byRole(dialog(), "button", "Sign in with your passkey") as HTMLButtonElement).disabled,
      ).toBe(false),
    );
    expect(calls.map((c) => c.url)).toEqual([
      "/api/auth/passkey/challenge",
      "/api/auth/passkey/challenge",
    ]);
    expect(dialog().textContent).not.toContain("Try again");
  });

  it("waits out a rate limit before the next try", async () => {
    stubFetch([
      { status: 429, body: f.errorBody("rate_limited"), headers: { "Retry-After": "30" } },
    ]);
    const host = await renderRoutes([{ path: "/sign-in", element: <SignInPage /> }], "/sign-in");
    await click(byRole(host, "button", "Continue with a passkey"));
    // a loaded host can take longer than the default two seconds here
    await waitFor(() => byRole(dialog(), "button", "Try again in 30 s"), 10_000);
    expect((byRole(dialog(), "button", "Try again in 30 s") as HTMLButtonElement).disabled).toBe(
      true,
    );
  });

  it("tells a dismissed prompt apart from a failure", async () => {
    seen.refuse = true;
    const calls = stubFetch([
      { status: 200, body: challenge },
      { status: 200, body: challenge },
    ]);
    const host = await renderRoutes([{ path: "/sign-in", element: <SignInPage /> }], "/sign-in");
    await click(byRole(host, "button", "Continue with a passkey"));
    await waitFor(() =>
      expect(
        (byRole(dialog(), "button", "Create a new passkey") as HTMLButtonElement).disabled,
      ).toBe(false),
    );
    await click(byRole(dialog(), "button", "Create a new passkey"));
    await waitFor(() => expect(dialog().textContent).toContain("The passkey prompt was closed"));
    // nothing reached the control plane but the challenge
    expect(calls.map((c) => c.url)).toEqual(["/api/auth/passkey/challenge"]);
  });
});

describe("passkey identities (contract 4.1.8, 4.2.5)", () => {
  const passkeyMe = {
    ...f.meBody,
    user: {
      ...f.meBody.user,
      github_id: null,
      github_login: null,
      passkey_address: C_ACCOUNT,
      display_name: "CBH2…AXAO",
    },
  };

  it("parses a passkey-only /api/me and asks for the address to delete it", () => {
    const me = meSchema.parse(passkeyMe);
    expect(me.user.passkey_address).toBe(C_ACCOUNT);
    expect(confirmationText(me)).toBe(C_ACCOUNT);
  });

  it("adds a passkey to a github account", async () => {
    const linked = { ...f.meBody, user: { ...f.meBody.user, passkey_address: C_ACCOUNT } };
    const calls = stubFetch([
      { status: 200, body: f.meBody },
      { status: 200, body: challenge },
      { status: 204 },
      { status: 200, body: linked },
    ]);
    const host = await renderRoutes([{ path: "/account", element: <AccountPage /> }], "/account");
    await waitFor(() => expect(host.textContent).toContain("Sign-in methods"));
    await click(byRole(host, "button", "Add a passkey"));
    await waitFor(() => expect(calls[1]?.url).toBe("/api/me/identities/passkey/challenge"));
    // linking offers creation only: a passkey the console knows belongs to
    // another account
    await waitFor(() =>
      expect((byRole(dialog(), "button", "Create a passkey") as HTMLButtonElement).disabled).toBe(
        false,
      ),
    );
    expect(dialog().textContent).not.toContain("Sign in with your passkey");
    await click(byRole(dialog(), "button", "Create a passkey"));
    // the passkey is named after the account in the browser's list
    expect(seen.create[0]?.publicKey?.user.name).toBe("octocat");
    await waitFor(() => expect(dialog().textContent).toContain("Your passkey is created"));
    await click(byRole(dialog(), "button", "Confirm with your passkey"));
    await waitFor(() => expect(host.textContent).toContain("CBH2…AXAO"));
    expect(calls[2]?.url).toBe("/api/me/identities/passkey");
    expect(calls[2]?.method).toBe("POST");
    expectValidAnswer(calls[2]?.body as Answer);
    // with two methods both can be unlinked
    const buttons = [
      ...host.querySelectorAll<HTMLButtonElement>("ul[aria-label='Sign-in methods'] button"),
    ].filter((b) => b.textContent === "Unlink");
    expect(buttons.map((b) => b.disabled)).toEqual([false, false]);
  });

  it("keeps the only passkey and unlinks it once another method exists", async () => {
    const both = {
      ...passkeyMe,
      user: {
        ...passkeyMe.user,
        github_id: 1234567,
        github_login: "octocat",
        display_name: "octocat",
      },
    };
    const calls = stubFetch([
      { status: 200, body: both },
      { status: 204 },
      { status: 200, body: f.meBody },
    ]);
    const host = await renderRoutes([{ path: "/account", element: <AccountPage /> }], "/account");
    await waitFor(() => expect(host.textContent).toContain("CBH2…AXAO"));
    const unlinks = [
      ...host.querySelectorAll<HTMLButtonElement>("ul[aria-label='Sign-in methods'] button"),
    ].filter((b) => b.textContent === "Unlink");
    expect(unlinks.length).toBe(2);
    await click(unlinks[1] as HTMLButtonElement);
    await waitFor(() => expect(dialog().textContent).toContain("Unlink your passkey?"));
    await click(byRole(dialog(), "button", "Unlink"));
    await waitFor(() => expect(calls[1]?.url).toBe("/api/me/identities/passkey"));
    expect(calls[1]?.method).toBe("DELETE");
  });

  it("confirms with the operator passkey when the change needs a step-up, then unlinks", async () => {
    const both = {
      ...passkeyMe,
      user: {
        ...passkeyMe.user,
        github_id: 1234567,
        github_login: "octocat",
        display_name: "octocat",
      },
    };
    const stepped = {
      stepped_up: true,
      stepped_up_at: "2026-11-16T10:00:00Z",
      expires_at: "2026-11-16T11:00:00Z",
      max_age_seconds: 3600,
      passkeys: 1,
      sign_in_passkey: true,
      enrollment: "closed",
      enrollment_until: null,
    };
    const calls = stubFetch([
      { status: 200, body: both },
      { status: 401, body: f.errorBody("step_up_required") },
      { status: 200, body: challenge },
      { status: 200, body: stepped },
      { status: 204 },
      { status: 200, body: f.meBody },
    ]);
    const host = await renderRoutes([{ path: "/account", element: <AccountPage /> }], "/account");
    await waitFor(() => expect(host.textContent).toContain("CBH2…AXAO"));
    const unlinks = [
      ...host.querySelectorAll<HTMLButtonElement>("ul[aria-label='Sign-in methods'] button"),
    ].filter((b) => b.textContent === "Unlink");
    await click(unlinks[1] as HTMLButtonElement);
    await click(byRole(dialog(), "button", "Unlink"));
    const top = () => {
      const open = [...document.body.querySelectorAll<HTMLElement>("dialog[open]")];
      const d = open.find((x) => x.textContent?.includes("Confirm it is you"));
      if (!d) throw new Error("no open dialog");
      return d;
    };
    await waitFor(() => expect(top().textContent).toContain("Confirm it is you"));
    expect(calls[2]?.url).toBe("/api/operator/step-up/challenge");
    await waitFor(() =>
      expect(
        (byRole(top(), "button", "Confirm with your passkey") as HTMLButtonElement).disabled,
      ).toBe(false),
    );
    await click(byRole(top(), "button", "Confirm with your passkey"));
    await waitFor(() => expect(calls.length).toBe(6));
    expect(calls[3]?.url).toBe("/api/operator/step-up");
    // the unlink runs again once confirmed
    expect(calls[4]?.url).toBe("/api/me/identities/passkey");
    expect(calls[4]?.method).toBe("DELETE");
  });

  it("disables unlinking the only passkey", async () => {
    stubFetch([{ status: 200, body: passkeyMe }]);
    const host = await renderRoutes([{ path: "/account", element: <AccountPage /> }], "/account");
    await waitFor(() => expect(host.textContent).toContain("CBH2…AXAO"));
    const unlink = byRole(host, "button", "Unlink") as HTMLButtonElement;
    expect(unlink.disabled).toBe(true);
    // the reason is shown, not left to a hover title
    expect(host.textContent).toContain("Unlink is off while this is your only sign-in method.");
    expect(unlink.getAttribute("aria-describedby")).toBe("unlink-note");
  });

  it("invites a passkey account by its C-address", () => {
    expect(memberIdentity(` ${C_ACCOUNT} `)).toEqual({ stellar_address: C_ACCOUNT });
    expect(validateAddMember({ login: C_ACCOUNT, role: "member" })).toEqual({});
    // a broken checksum is refused before the request
    expect(
      validateAddMember({ login: `${C_ACCOUNT.slice(0, 55)}A`, role: "member" }).login,
    ).toBeDefined();
    const list = membersSchema.parse({
      members: [
        {
          ...f.memberBody,
          github_id: null,
          github_login: null,
          passkey_address: C_ACCOUNT,
          display_name: "CBH2…AXAO",
        },
      ],
      invites: [
        {
          ...f.inviteBody,
          github_id: null,
          github_login: null,
          stellar_address: C_ACCOUNT,
          display_name: "CBH2…AXAO",
        },
      ],
    });
    expect(list.members[0]?.passkey_address).toBe(C_ACCOUNT);
    expect(list.invites[0]?.stellar_address).toBe(C_ACCOUNT);
  });
});
