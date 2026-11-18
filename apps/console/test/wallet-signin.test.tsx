import { afterEach, describe, expect, it, vi } from "vitest";
import { me as meSchema } from "@/api/schemas";
import type { WalletModule } from "@/lib/stellar-wallet/modules";
import * as f from "./fixtures/contract";
import { byLabel, byRole, click, renderRoutes, stubFetch, type, waitFor } from "./helpers";

// one fake extension wallet, so the pages run their real wallet code paths
const ADDRESS = "GCB634SLKV5N4COK4FICWJADNTFWIHTWWM34BFPLACL6YSQVLY5ZWOQN";
const wallet = {
  signed: [] as { xdr: string; passphrase?: string; address?: string }[],
  available: true,
};

function fakeModule(): WalletModule {
  return {
    productId: "fake",
    productName: "Fake Wallet",
    productUrl: "https://wallet.example/",
    isAvailable: async () => wallet.available,
    getAddress: async () => ({ address: ADDRESS }),
    signTransaction: async (xdr, opts) => {
      wallet.signed.push({ xdr, passphrase: opts?.networkPassphrase, address: opts?.address });
      return { signedTxXdr: `signed:${xdr}`, signerAddress: ADDRESS };
    },
    signMessage: async () => ({ signedMessage: "c2ln", signerAddress: ADDRESS }),
  };
}

vi.mock("@/lib/stellar-wallet/modules", () => ({
  loadWalletModules: async () => [fakeModule()],
}));

const { resetWalletModulesForTest } = await import("@/lib/stellar-wallet");
const { SignInPage } = await import("@/pages/public/sign-in");
const { AccountPage, confirmationText } = await import("@/pages/home/account");
const { memberIdentity, validateAddMember } = await import("@/pages/project/members");

afterEach(() => {
  resetWalletModulesForTest();
  wallet.signed = [];
  wallet.available = true;
});

const challenge = {
  transaction: "AAAAchallenge",
  network_passphrase: "Public Global Stellar Network ; September 2015",
  expires_at: "2026-11-16T10:05:00Z",
};

const walletMe = {
  ...f.meBody,
  user: {
    ...f.meBody.user,
    github_id: null,
    github_login: null,
    stellar_address: ADDRESS,
    display_name: "GCB6…WOQN",
  },
};

function dialog(): HTMLElement {
  const d = document.body.querySelector<HTMLElement>("dialog[open]");
  if (!d) throw new Error("no open dialog");
  return d;
}

describe("wallet sign-in (contract 4.1.7)", () => {
  it("signs the challenge with the chosen wallet and opens the return path", async () => {
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
    await click(byRole(host, "button", "Continue with a Stellar wallet"));
    await waitFor(() => expect(dialog().textContent).toContain("Fake Wallet"));
    await click(byRole(dialog(), "button", "Fake Wallet"));
    await waitFor(() => expect(host.textContent).toContain("projects page"));

    expect(calls[0]?.url).toBe("/api/auth/stellar/challenge");
    expect(calls[0]?.method).toBe("POST");
    expect(calls[0]?.headers.get("X-Requested-With")).toBe("sa-console");
    expect(calls[0]?.body).toEqual({ address: ADDRESS, return_to: "/projects" });
    expect(wallet.signed).toEqual([
      { xdr: "AAAAchallenge", passphrase: challenge.network_passphrase, address: ADDRESS },
    ]);
    expect(calls[1]?.url).toBe("/api/auth/stellar/verify");
    expect(calls[1]?.body).toEqual({ transaction: "signed:AAAAchallenge" });
  });

  it("shows the control plane's refusal in the picker", async () => {
    stubFetch([
      { status: 200, body: challenge },
      { status: 403, body: f.errorBody("challenge_expired") },
    ]);
    const host = await renderRoutes([{ path: "/sign-in", element: <SignInPage /> }], "/sign-in");
    await click(byRole(host, "button", "Continue with a Stellar wallet"));
    await waitFor(() => expect(dialog().textContent).toContain("Fake Wallet"));
    await click(byRole(dialog(), "button", "Fake Wallet"));
    await waitFor(() => expect(dialog().querySelector("[role=alert]")).not.toBeNull());
    expect(host.textContent).toContain("Sign in");
  });

  it("points to installing a wallet when none is present", async () => {
    wallet.available = false;
    const host = await renderRoutes([{ path: "/sign-in", element: <SignInPage /> }], "/sign-in");
    await click(byRole(host, "button", "Continue with a Stellar wallet"));
    await waitFor(() =>
      expect(dialog().textContent).toContain("No Stellar wallet extension was found"),
    );
    const link = dialog().querySelector<HTMLAnchorElement>("a[href='https://wallet.example/']");
    expect(link?.textContent).toContain("Fake Wallet");
  });
});

describe("linked sign-in methods (contract 4.1.8)", () => {
  it("parses a wallet-only /api/me", () => {
    expect(meSchema.parse(walletMe).user.stellar_address).toBe(ADDRESS);
    expect(confirmationText(meSchema.parse(walletMe))).toBe(ADDRESS);
    expect(confirmationText(meSchema.parse(f.meBody))).toBe("octocat");
  });

  it("links a wallet to a github account", async () => {
    const linked = { ...f.meBody, user: { ...f.meBody.user, stellar_address: ADDRESS } };
    const calls = stubFetch([
      { status: 200, body: f.meBody },
      { status: 200, body: challenge },
      { status: 204 },
      { status: 200, body: linked },
    ]);
    const host = await renderRoutes([{ path: "/account", element: <AccountPage /> }], "/account");
    await waitFor(() => expect(host.textContent).toContain("Linked sign-in methods"));
    // the only method cannot be unlinked
    const unlink = byRole(host, "button", "Unlink") as HTMLButtonElement;
    expect(unlink.disabled).toBe(true);
    const gh = host.querySelector<HTMLAnchorElement>("a[href='/api/auth/github/start?link=1']");
    expect(gh).toBeNull();

    await click(byRole(host, "button", "Link a wallet"));
    await waitFor(() => expect(dialog().textContent).toContain("Fake Wallet"));
    await click(byRole(dialog(), "button", "Fake Wallet"));
    await waitFor(() => expect(host.textContent).toContain("GCB6…WOQN"));
    expect(calls[1]?.url).toBe("/api/me/identities/stellar/challenge");
    expect(calls[1]?.body).toEqual({ address: ADDRESS });
    expect(calls[2]?.url).toBe("/api/me/identities/stellar");
    expect(calls[2]?.method).toBe("POST");
    expect(calls[2]?.body).toEqual({ transaction: "signed:AAAAchallenge" });
    // with two methods both can be unlinked
    const buttons = [
      ...host.querySelectorAll<HTMLButtonElement>("ul[aria-label='Sign-in methods'] button"),
    ].filter((b) => b.textContent === "Unlink");
    expect(buttons.map((b) => b.disabled)).toEqual([false, false]);
  });

  it("offers github linking to a wallet-only account and unlinks the wallet", async () => {
    const both = {
      ...walletMe,
      user: {
        ...walletMe.user,
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
    await waitFor(() => expect(host.textContent).toContain("GCB6…WOQN"));
    const unlinks = [
      ...host.querySelectorAll<HTMLButtonElement>("ul[aria-label='Sign-in methods'] button"),
    ].filter((b) => b.textContent === "Unlink");
    await click(unlinks[1] as HTMLButtonElement);
    await waitFor(() => expect(dialog().textContent).toContain("Unlink your Stellar wallet?"));
    await click(byRole(dialog(), "button", "Unlink"));
    await waitFor(() => expect(calls[1]?.url).toBe("/api/me/identities/stellar"));
    expect(calls[1]?.method).toBe("DELETE");
  });

  it("links github through the oauth flow from a wallet-only account", async () => {
    stubFetch([{ status: 200, body: walletMe }]);
    const host = await renderRoutes([{ path: "/account", element: <AccountPage /> }], "/account");
    await waitFor(() => expect(host.textContent).toContain("Linked sign-in methods"));
    const gh = host.querySelector<HTMLAnchorElement>("a[href='/api/auth/github/start?link=1']");
    expect(gh?.textContent).toBe("Link GitHub");
  });

  it("explains a refused github link", async () => {
    stubFetch([{ status: 200, body: walletMe }]);
    const host = await renderRoutes(
      [{ path: "/account", element: <AccountPage /> }],
      "/account?link_error=identity_in_use",
    );
    await waitFor(() =>
      expect(host.textContent).toContain("That GitHub account signs in to another console account"),
    );
  });

  it("asks a wallet-only account to type its whole address before deletion", async () => {
    stubFetch([{ status: 200, body: walletMe }]);
    const host = await renderRoutes([{ path: "/account", element: <AccountPage /> }], "/account");
    await waitFor(() => expect(host.textContent).toContain("Linked sign-in methods"));
    await click(byRole(host, "button", "Delete account"));
    await waitFor(() => expect(dialog()).toBeTruthy());
    const input = byLabel(document.body, `Type ${ADDRESS} to confirm`);
    await type(input, ADDRESS);
    const confirm = byRole(dialog(), "button", "Delete account") as HTMLButtonElement;
    expect(confirm.disabled).toBe(false);
  });
});

describe("members by github login or stellar address (contract 4.2.5)", () => {
  it("tells an address from a login", () => {
    expect(memberIdentity(` ${ADDRESS} `)).toEqual({ stellar_address: ADDRESS });
    expect(memberIdentity("@octocat")).toEqual({ github_login: "octocat" });
    expect(validateAddMember({ login: ADDRESS, role: "member" })).toEqual({});
    // a G-address with a broken checksum
    expect(validateAddMember({ login: `${ADDRESS.slice(0, 55)}A`, role: "member" }).login).toBe(
      "Not a valid account (G...), muxed account (M...) or contract (C...) id.",
    );
  });
});
